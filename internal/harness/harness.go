// Package harness adapts headless agent harness CLIs (Claude Code, Codex,
// pi, oh-my-pi, opencode, Crush, goose) behind one interface: build the
// invocation, parse the event stream. The runner composes prompts from
// SKILL.md bodies, so no adapter depends on any harness's skill-discovery
// mechanism.
package harness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// EventKind classifies a normalized stream event.
type EventKind string

const (
	// EventText is an assistant text delta or complete message.
	EventText EventKind = "text"
	// EventToolCall is a tool invocation by the agent.
	EventToolCall EventKind = "tool_call"
	// EventToolResult is a completed tool invocation's (truncated) output.
	EventToolResult EventKind = "tool_result"
	// EventThinking is a completed reasoning/thinking block.
	EventThinking EventKind = "thinking"
	// EventUsage is a cumulative token-usage update.
	EventUsage EventKind = "usage"
	// EventResult is the final result of the run.
	EventResult EventKind = "result"
	// EventError is a stream-reported error.
	EventError EventKind = "error"
	// EventNeedsInput means the agent asked the user a question or raised a
	// permission request — impossible to satisfy headlessly (exit code 2).
	EventNeedsInput EventKind = "needs_input"
)

// Event is a normalized stream event.
type Event struct {
	Kind      EventKind `json:"kind"`
	Text      string    `json:"text,omitempty"`
	Tool      string    `json:"tool,omitempty"`
	TokensIn  int64     `json:"tokens_in,omitempty"`
	TokensOut int64     `json:"tokens_out,omitempty"`
	CostUSD   float64   `json:"cost_usd,omitempty"`
	IsError   bool      `json:"is_error,omitempty"`
	// Key, when set, identifies a streaming part whose content is
	// REPLACED as it grows (crush streams message parts incrementally):
	// renderers update the line registered under the key in place instead
	// of appending. Unkeyed events append.
	Key string `json:"key,omitempty"`
}

// Caps declares what a harness can express, honestly. The CLI prints
// capability caveats before every run rather than silently degrading.
type Caps struct {
	// StreamJSON: the harness emits machine-readable JSONL on stdout.
	StreamJSON bool
	// ModelSelect: the model is selectable per invocation.
	ModelSelect bool
	// PermissionScoping: tool grants can be narrowed per invocation.
	PermissionScoping bool
	// UsageReporting: token usage is reported in the stream (spend rows).
	UsageReporting bool
}

// Detection is the result of probing for a harness binary.
type Detection struct {
	Available bool
	Binary    string
	Version   string
	Detail    string
}

// RunSpec is everything an adapter needs to build the headless invocation.
// Cwd is ALWAYS the harness root, never the audited checkout (harness
// security rule S9); the audit target travels inside the prompt as data.
type RunSpec struct {
	Prompt               string
	Model                string
	Cwd                  string
	Env                  []string
	AllowedTools         []string
	MaxTurns             int
	UnsafeFullPermission bool
	// OutDir is the run journal directory (for harnesses that write their
	// final message to a file).
	OutDir string
}

// ErrServerUnsupported is returned by a SessionRunner whose managed backend
// cannot start (missing subcommand, old version). The runner falls back to
// the one-shot process path when it sees this.
var ErrServerUnsupported = errors.New("harness server mode unsupported")

// SessionResult is the terminal outcome of a session-based run. Err and
// Cancelled describe the RUN's fate, not the transport: transport failures
// go into the error return of RunSession.
type SessionResult struct {
	Text      string
	Err       string
	Cancelled bool
}

// SessionRunner is implemented by harnesses that execute through a managed
// session backend (crush's `crush server`: workspace, SSE event stream,
// cancel endpoint) instead of a one-shot process. The runner feeds every
// normalized event to emit; RunSession must honor context cancellation by
// cancelling the remote run and returning promptly.
type SessionRunner interface {
	// RunSession executes spec.Prompt and emits normalized events until the
	// run terminates. Returns ErrServerUnsupported when the backend cannot
	// start, so the runner can fall back to the process path.
	RunSession(ctx context.Context, spec *RunSpec, emit func(Event)) (SessionResult, error)
}

// Harness is one agent harness adapter.
type Harness interface {
	ID() string
	DisplayName() string
	// Binary returns the binary name (or test override via
	// TRAUST_CLI_<ID>_BIN).
	Binary() string
	Detect(ctx context.Context) Detection
	Capabilities() Caps
	// Command builds the headless invocation. Implementations must set
	// cmd.Dir = spec.Cwd and cmd.Env = spec.Env.
	Command(ctx context.Context, spec *RunSpec) *exec.Cmd
	// ParseEvent maps one stdout line to a normalized event; false when the
	// line is not an event the adapter understands (it is still journaled).
	ParseEvent(line []byte) (Event, bool)
	// Notes are honest caveats printed before a run.
	Notes() []string
}

// ModelChecker is implemented by harnesses that can verify a model id is
// runnable before the process launches. The runner uses it to fail fast
// with actionable guidance — a registry floor model the local harness
// cannot authenticate to otherwise dies two seconds later with an opaque
// provider 401.
type ModelChecker interface {
	CheckModel(ctx context.Context, model string) error
}

// IDs returns the harness priority order.
func IDs() []string {
	return []string{"claude", "codex", "pi", "omp", "opencode", "crush", "goose"}
}

// ErrUnknownHarness is returned for unregistered ids.
var ErrUnknownHarness = errors.New("unknown harness")

// Get returns the adapter for an id. "agy" (Antigravity) has no headless
// execution path and is rejected with an explanation.
func Get(id string) (Harness, error) {
	switch strings.ToLower(id) {
	case "claude", "claude-code":
		return &Claude{}, nil
	case "codex":
		return &Codex{}, nil
	case "pi":
		return &Pi{}, nil
	case "omp", "oh-my-pi", "ohmypi":
		return &Omp{}, nil
	case "opencode":
		return &Opencode{}, nil
	case "crush":
		return &Crush{}, nil
	case "goose":
		return &Goose{}, nil
	case "agy", "antigravity":
		return nil, fmt.Errorf("antigravity (agy) has no headless execution path — the adapter is deferred pending a research spike (ACP/A2A); use claude, codex, pi, opencode, crush or goose")
	default:
		return nil, fmt.Errorf("%w: %q (supported: %s, agy pending)", ErrUnknownHarness, id, strings.Join(IDs(), ", "))
	}
}

// All returns every registered adapter in priority order.
func All() []Harness {
	var out []Harness
	for _, id := range IDs() {
		h, err := Get(id)
		if err == nil {
			out = append(out, h)
		}
	}
	return out
}

// DetectAll probes every harness and returns detections keyed by id.
func DetectAll(ctx context.Context) map[string]Detection {
	out := map[string]Detection{}
	for _, h := range All() {
		out[h.ID()] = h.Detect(ctx)
	}
	return out
}

// DetectDefault returns the first harness (in priority order) found on PATH.
func DetectDefault(ctx context.Context) (Harness, bool) {
	for _, h := range All() {
		if h.Detect(ctx).Available {
			return h, true
		}
	}
	return nil, false
}

// detect probes for the binary and captures its version banner.
func detect(ctx context.Context, bin string) Detection {
	path, err := exec.LookPath(bin)
	if err != nil {
		return Detection{Available: false, Detail: fmt.Sprintf("%s not found in PATH", bin)}
	}
	vctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(vctx, path, "--version").CombinedOutput()
	version := strings.TrimSpace(string(out))
	if len(version) > 80 {
		version = version[:80] + "…"
	}
	if err != nil && version == "" {
		version = fmt.Sprintf("version probe failed: %v", err)
	}
	return Detection{Available: true, Binary: path, Version: version}
}

// binaryEnv returns the test override path for a harness id, or the default
// binary name.
func binaryEnv(id, def string) string {
	if v := os.Getenv("TRAUST_CLI_" + strings.ToUpper(id) + "_BIN"); v != "" {
		return v
	}
	return def
}
