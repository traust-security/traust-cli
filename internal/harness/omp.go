package harness

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
)

// Omp is the oh-my-pi (omp) adapter (@oh-my-pi/pi-coding-agent). omp is a
// pi-family harness: `--mode json` emits the same JSONL session events as pi
// (session/agent_start/turn_start/message_*/tool_execution_*/turn_end/
// agent_end), with two differences the parser absorbs: an extra
// advisor_cost_changed bookkeeping event, and no agent_settled terminal
// event — the run's result is the last assistant turn_end. Unlike pi, omp
// ships a `task` subagent tool (with its `wait` collector), so skills that
// fan out review subagents run at full capability instead of degraded.
//
// Discovery of user/project agent configuration (extensions, skills, rules)
// and LSP servers is disabled, and so is prewalk model-switching: headless
// skill runs get one model, the prompt the runner composed, and nothing
// picked up from the surrounding filesystem (harness rule S9).
type Omp struct{}

func (o *Omp) ID() string          { return "omp" }
func (o *Omp) DisplayName() string { return "oh-my-pi (omp)" }
func (o *Omp) Binary() string      { return binaryEnv("omp", "omp") }

func (o *Omp) Capabilities() Caps {
	return Caps{StreamJSON: true, ModelSelect: true, PermissionScoping: true, UsageReporting: true}
}

func (o *Omp) Detect(ctx context.Context) Detection { return detect(ctx, o.Binary()) }

// ompFullTools is the full-permission toolset: omp's coding essentials plus
// the subagent facility. omp's wider builtin set (browser, computer, github,
// fetch, web_search, ask, ...) deliberately stays disabled — skill runs are
// headless with no interactive counterpart for those tools, and audit skills
// are read-only-plus-report: no network egress.
const ompFullTools = "read,bash,edit,write,glob,grep,task,wait,todo"

// mapOmpTools translates Claude-style frontmatter tool names onto omp's
// builtin tools. Task maps onto omp's subagent facility (task plus wait, its
// result collector); unmappable tools (AskUserQuestion and friends) are
// dropped with a note.
func mapOmpTools(allowed []string) string {
	var out []string
	add := func(t string) {
		for _, x := range out {
			if x == t {
				return
			}
		}
		out = append(out, t)
	}
	for _, grant := range allowed {
		name := grant
		if i := strings.IndexByte(name, '('); i > 0 {
			name = name[:i]
		}
		switch name {
		case "Read":
			add("read")
		case "Bash":
			add("bash")
		case "Write":
			add("write")
		case "Edit":
			add("edit")
		case "Grep":
			add("grep")
		case "Glob":
			add("glob")
		case "Task":
			add("task")
			add("wait")
		case "WebFetch":
			add("fetch")
		case "WebSearch":
			add("web_search")
		}
	}
	if len(out) == 0 {
		return ompFullTools
	}
	return strings.Join(out, ",")
}

func (o *Omp) Command(ctx context.Context, spec *RunSpec) *exec.Cmd {
	args := []string{
		"--mode", "json",
		"--no-extensions", "--no-skills", "--no-rules", "--no-lsp", "--no-pty", "--no-prewalk",
	}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	if spec.UnsafeFullPermission {
		// Full-permission mode: auto-approve the coding + subagent toolset.
		// omp gates by tool declaration like pi, so "all tools
		// auto-approved" is that set, not omp's every builtin.
		args = append(args, "--auto-approve", "--tools", ompFullTools)
	} else {
		args = append(args, "--tools", mapOmpTools(spec.AllowedTools))
	}
	// -p is omp's explicit non-interactive mode (process prompt and exit);
	// -- guards prompts that begin with a dash.
	args = append(args, "-p", "--", spec.Prompt)
	cmd := exec.CommandContext(ctx, o.Binary(), args...)
	cmd.Dir = spec.Cwd
	cmd.Env = spec.Env
	return cmd
}

func (o *Omp) Notes() []string {
	return []string{
		"subagent fan-out is available: Task grants map onto omp's task/wait tools",
		"omp's wider builtin set (browser, github, web tools, ask) is not enabled — full-permission auto-approves the coding + subagent toolset only",
		"extension/skill/rule discovery, LSP and prewalk model-switching are disabled for deterministic headless runs",
	}
}

// ParseEvent implements omp's JSON event stream (pi-family shapes, verified
// against omp 18.12.6). Tool-call visibility comes from tool_execution_*
// events: omp's message_update toolcall fragments carry no tool name.
func (o *Omp) ParseEvent(line []byte) (Event, bool) {
	var m struct {
		Type  string `json:"type"`
		Usage *struct {
			Input  int64 `json:"input"`
			Output int64 `json:"output"`
		} `json:"usage"`
		AssistantMessageEvent *struct {
			Type     string `json:"type"`
			Delta    string `json:"delta"`
			ToolName string `json:"toolName"`
			Reason   string `json:"reason"`
			Error    string `json:"error"`
		} `json:"assistantMessageEvent"`
		Message *struct {
			Role  string `json:"role"`
			Usage *struct {
				Input  int64 `json:"input"`
				Output int64 `json:"output"`
			} `json:"usage"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
		ToolName string          `json:"toolName"`
		Error    json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(line, &m); err != nil {
		return Event{}, false
	}
	usage := func(ev Event) Event {
		if m.Usage != nil && (m.Usage.Input > 0 || m.Usage.Output > 0) {
			ev.TokensIn, ev.TokensOut = m.Usage.Input, m.Usage.Output
		}
		return ev
	}
	switch m.Type {
	case "message_update":
		if m.AssistantMessageEvent == nil {
			return Event{}, false
		}
		switch m.AssistantMessageEvent.Type {
		case "text_delta":
			return usage(Event{Kind: EventText, Text: m.AssistantMessageEvent.Delta}), true
		case "error":
			return Event{Kind: EventError, Text: m.AssistantMessageEvent.Error, IsError: true}, true
		case "thinking_start", "thinking_delta", "thinking_end",
			"text_start", "text_end", "signature_delta",
			"toolcall_start", "toolcall_delta", "toolcall_end":
			// Streaming sub-events; complete content arrives on
			// message_end/turn_end, and tool calls on tool_execution_*.
			// Recognized, ignored — they were leaking as raw JSON (dozens
			// per turn) and omp's toolcall fragments carry no tool name.
			return Event{}, true
		default:
			ev := usage(Event{})
			if ev.TokensIn == 0 && ev.TokensOut == 0 {
				return Event{}, false
			}
			ev.Kind = EventUsage
			return ev, true
		}
	case "tool_execution_start":
		return Event{Kind: EventToolCall, Tool: m.ToolName}, true
	case "tool_execution_end":
		return Event{Kind: EventToolResult, Tool: m.ToolName,
			IsError: len(m.Error) > 0 && string(m.Error) != "null"}, true
	case "message_end":
		if m.Message == nil || m.Message.Role != "assistant" {
			// System/user message bookkeeping: recognized, ignored.
			return Event{}, m.Message != nil
		}
		var sb strings.Builder
		for _, block := range m.Message.Content {
			if block.Type == "text" {
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(block.Text)
			}
		}
		if sb.Len() == 0 {
			return Event{}, false
		}
		ev := Event{Kind: EventText, Text: sb.String()}
		if m.Message.Usage != nil {
			ev.TokensIn, ev.TokensOut = m.Message.Usage.Input, m.Message.Usage.Output
		}
		return ev, true
	case "turn_end":
		// The final assistant message of a turn (content AND usage). The
		// last one before agent_end is the run's result; omp has no
		// agent_settled.
		if m.Message == nil || m.Message.Role != "assistant" {
			return Event{}, false
		}
		ev := Event{Kind: EventResult}
		var sb strings.Builder
		for _, block := range m.Message.Content {
			if block.Type == "text" {
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(block.Text)
			}
		}
		ev.Text = sb.String()
		if m.Message.Usage != nil {
			ev.TokensIn, ev.TokensOut = m.Message.Usage.Input, m.Message.Usage.Output
		} else if m.Usage != nil {
			ev.TokensIn, ev.TokensOut = m.Usage.Input, m.Usage.Output
		}
		return ev, true
	case "session", "agent_start", "agent_end", "turn_start", "message_start",
		"advisor_cost_changed":
		// Protocol bookkeeping (omp adds advisor_cost_changed to pi's set):
		// recognized, ignored — printing them as raw lines was pure noise.
		return Event{}, true
	}
	return Event{}, false
}
