package harness

import (
	"context"
	"testing"
)

// TestOmpCommand mirrors the invocation verified against omp 18.12.6:
// determinism flags, model, tool mapping, and -p -- <prompt>.
func TestOmpCommand(t *testing.T) {
	// The spec declares Read + Bash(git clone:*): mapped onto omp's read/bash.
	args := argsOf(t, &Omp{}, spec())
	for _, want := range []string{"omp", "--mode", "json", "--no-skills", "--no-prewalk",
		"--model", "model-x", "--tools", "read,bash", "-p", "--", "PROMPT"} {
		if !contains(args, want) {
			t.Errorf("omp args missing %q: %v", want, args)
		}
	}
	if cmd := (&Omp{}).Command(context.Background(), spec()); cmd.Dir != "/harness/root" {
		t.Errorf("omp cwd = %q", cmd.Dir)
	}
	// With no declared tools, the full default set applies.
	empty := spec()
	empty.AllowedTools = nil
	if args := argsOf(t, &Omp{}, empty); !contains(args, ompFullTools) {
		t.Errorf("omp default tools wrong: %v", args)
	}
	// Full-permission mode: auto-approve the coding + subagent toolset.
	unsafe := spec()
	unsafe.UnsafeFullPermission = true
	args = argsOf(t, &Omp{}, unsafe)
	if !contains(args, "--auto-approve") || !contains(args, ompFullTools) {
		t.Errorf("omp full-permission args wrong: %v", args)
	}
}

func TestMapOmpTools(t *testing.T) {
	// Task maps onto the subagent facility: task plus its wait collector
	// (first-seen order preserved).
	if got := mapOmpTools([]string{"Read", "Task", "Glob", "Grep"}); got != "read,task,wait,glob,grep" {
		t.Errorf("mapOmpTools = %q, want read,task,wait,glob,grep", got)
	}
	// AskUserQuestion is unmappable and dropped.
	if got := mapOmpTools([]string{"Read", "AskUserQuestion"}); got != "read" {
		t.Errorf("mapOmpTools = %q, want read", got)
	}
	// Bash grants with prefix scopes map onto plain bash.
	if got := mapOmpTools([]string{"Bash(git clone:*)", "Write"}); got != "bash,write" {
		t.Errorf("mapOmpTools = %q, want bash,write", got)
	}
}

// TestOmpParseEvent uses lines captured from a real omp 18.12.6 JSON-mode
// session (including a task-subagent fan-out).
func TestOmpParseEvent(t *testing.T) {
	o := &Omp{}
	// Protocol bookkeeping is recognized and ignored: ok with a zero event.
	for _, line := range []string{
		`{"cwd":"/home/gbuchana/tmp","id":"01a1ae1b","timestamp":"2026-11-05T23:46:09.769Z","type":"session","version":3}`,
		`{"type":"advisor_cost_changed"}`,
		`{"type":"agent_start"}`,
		`{"type":"turn_start"}`,
		`{"type":"message_start"}`,
		`{"isTerminal":true,"type":"agent_end"}`,
	} {
		ev, ok := o.ParseEvent([]byte(line))
		if !ok || ev != (Event{}) {
			t.Errorf("bookkeeping %q should be recognized and ignored, got %+v, %v", line, ev, ok)
		}
	}
	// Streaming sub-events are recognized and ignored (toolcall fragments
	// carry no tool name in omp).
	if _, ok := o.ParseEvent([]byte(`{"assistantMessageEvent":{"contentIndex":0,"delta":"x","type":"toolcall_delta"},"type":"message_update"}`)); !ok {
		t.Error("toolcall_delta should be recognized")
	}
	// Text deltas stream as text.
	ev, ok := o.ParseEvent([]byte(`{"assistantMessageEvent":{"contentIndex":0,"delta":"subagent-ok","type":"text_delta"},"type":"message_update"}`))
	if !ok || ev.Kind != EventText || ev.Text != "subagent-ok" {
		t.Errorf("text event = %+v, %v", ev, ok)
	}
	// Subagent fan-out: the task tool call is visible.
	ev, ok = o.ParseEvent([]byte(`{"args":{"tasks":[]},"intent":"spawn","toolCallId":"t1","toolName":"task","type":"tool_execution_start"}`))
	if !ok || ev.Kind != EventToolCall || ev.Tool != "task" {
		t.Errorf("task event = %+v, %v", ev, ok)
	}
	// Tool results report errors via the error field (null = clean).
	ev, ok = o.ParseEvent([]byte(`{"isError":false,"result":{"content":[]},"toolCallId":"t1","toolName":"task","type":"tool_execution_end"}`))
	if !ok || ev.Kind != EventToolResult || ev.Tool != "task" || ev.IsError {
		t.Errorf("tool result = %+v, %v", ev, ok)
	}
	ev, ok = o.ParseEvent([]byte(`{"error":"boom","toolCallId":"t2","toolName":"bash","type":"tool_execution_end"}`))
	if !ok || ev.Kind != EventToolResult || !ev.IsError {
		t.Errorf("failed tool result = %+v, %v", ev, ok)
	}
	// Assistant message_end carries complete text and usage.
	ev, ok = o.ParseEvent([]byte(`{"message":{"content":[{"text":"hello","type":"text"}],"role":"assistant","usage":{"input":10,"output":2}},"type":"message_end"}`))
	if !ok || ev.Kind != EventText || ev.Text != "hello" || ev.TokensIn != 10 || ev.TokensOut != 2 {
		t.Errorf("message_end = %+v, %v", ev, ok)
	}
	// User message_end is recognized and ignored.
	if ev, ok := o.ParseEvent([]byte(`{"message":{"content":[{"text":"hi","type":"text"}],"role":"user"},"type":"message_end"}`)); !ok || ev != (Event{}) {
		t.Errorf("user message_end should be recognized and ignored, got %+v, %v", ev, ok)
	}
	// turn_end is the per-turn result; the last one wins as the run result.
	ev, ok = o.ParseEvent([]byte(`{"message":{"content":[{"text":"subagent-ok","type":"text"}],"role":"assistant","usage":{"input":123,"output":6}},"type":"turn_end"}`))
	if !ok || ev.Kind != EventResult || ev.Text != "subagent-ok" || ev.TokensIn != 123 || ev.TokensOut != 6 {
		t.Errorf("turn_end = %+v, %v", ev, ok)
	}
	// Error sub-events surface as errors.
	ev, ok = o.ParseEvent([]byte(`{"assistantMessageEvent":{"error":"provider 401","type":"error"},"type":"message_update"}`))
	if !ok || ev.Kind != EventError || !ev.IsError || ev.Text != "provider 401" {
		t.Errorf("error event = %+v, %v", ev, ok)
	}
	if _, ok := o.ParseEvent([]byte(`not json`)); ok {
		t.Error("garbage should not parse")
	}
}
