package cmd

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/traust-security/traust-cli/internal/harness"
)

func TestWrapBlockStripsIndentationAndWraps(t *testing.T) {
	block := "exit status 3\n          ERROR  Agent processing failed: 401 Unauthorized invalid x-api-key      \n"
	out := wrapBlock(block, 40)

	for _, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > 40 {
			t.Errorf("line is %d cells wide (limit 40): %q", w, line)
		}
		// Harness error boxes arrive with leading decoration; the display
		// form must start at the left edge so re-wrapped lines align.
		if strings.HasPrefix(line, " ") {
			t.Errorf("line retains leading whitespace: %q", line)
		}
	}
	if !strings.Contains(out, "exit status 3") {
		t.Errorf("content lost: %q", out)
	}
	if strings.Contains(out, "  \n") || strings.HasSuffix(out, "  ") {
		t.Errorf("trailing whitespace not stripped: %q", out)
	}
}

func TestWrapBlockPreservesNewlines(t *testing.T) {
	out := wrapBlock("short one\nshort two", 40)
	if out != "short one\nshort two" {
		t.Errorf("short lines should pass through: %q", out)
	}
}

func TestPlainSinkWritesToProvidedWriter(t *testing.T) {

	var buf strings.Builder
	sink := newPlainSink(&buf)

	// Progress should write to buf, not os.Stdout.
	sink.Progress("warning: explicit model overrides budget band")
	if buf.Len() == 0 {
		t.Fatal("Progress wrote nothing to provided writer")
	}
	if !strings.Contains(buf.String(), "warning:") {
		t.Errorf("Progress output missing warning text: %q", buf.String())
	}

	buf.Reset()

	// Raw should write to buf.
	sink.Raw("  some harness stream line")
	if buf.Len() == 0 {
		t.Fatal("Raw wrote nothing to provided writer")
	}
	if !strings.Contains(buf.String(), "harness stream") {
		t.Errorf("Raw output missing content: %q", buf.String())
	}

	buf.Reset()

	// Event (via keyedRenderer) should write to buf on Flush.
	sink.Event(harness.Event{Kind: harness.EventText, Text: "hello from agent"})
	sink.events.Flush()
	if buf.Len() == 0 {
		t.Fatal("Event wrote nothing to provided writer after Flush")
	}
	if !strings.Contains(buf.String(), "hello from agent") {
		t.Errorf("Event output missing content: %q", buf.String())
	}

	// Raw with blank line should be suppressed.
	buf.Reset()
	sink.Raw("   ")
	if buf.Len() != 0 {
		t.Errorf("Raw should suppress blank lines, got: %q", buf.String())
	}
}
