package integration_test

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testdataPath(name string) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "testdata", name)
}

func TestDebugNodeJS(t *testing.T) {
	program := testdataPath("debugme.js")

	// Launch — adapter auto-detected from .js extension, stop-on-entry is default
	out := mustAf(t, "debug", "launch", program)
	if !strings.Contains(out, "Stopped") {
		t.Fatalf("expected 'Stopped', got: %s", out)
	}

	// Set a breakpoint on line 3 (return result)
	out = mustAf(t, "debug", "breakpoint", program, "3")
	if !strings.Contains(out, "line 3") {
		t.Fatalf("expected breakpoint at line 3, got: %s", out)
	}

	// Continue to the breakpoint
	out = mustAf(t, "debug", "continue")
	if !strings.Contains(out, "Stopped") {
		t.Fatalf("expected 'Stopped', got: %s", out)
	}
	if !strings.Contains(out, "breakpoint") {
		t.Fatalf("expected 'breakpoint' reason, got: %s", out)
	}

	// Check stack trace — should show we're in the add function
	out = mustAf(t, "debug", "stacktrace")
	if !strings.Contains(out, "add") {
		t.Fatalf("expected 'add' in stack trace, got: %s", out)
	}

	// Evaluate expression — a + b should be 30 (10 + 20)
	out = mustAf(t, "debug", "eval", "a + b")
	if !strings.Contains(out, "30") {
		t.Fatalf("expected '30', got: %s", out)
	}

	// Disconnect
	mustAf(t, "debug", "disconnect")
}

func TestDebugPython(t *testing.T) {
	program := testdataPath("debugme.py")

	// Launch — adapter auto-detected from .py extension, stop-on-entry is default
	out := mustAf(t, "debug", "launch", program)
	if !strings.Contains(out, "Stopped") {
		t.Fatalf("expected 'Stopped', got: %s", out)
	}

	// Set a breakpoint on line 3 (return result)
	out = mustAf(t, "debug", "breakpoint", program, "3")
	if !strings.Contains(out, "line 3") {
		t.Fatalf("expected breakpoint at line 3, got: %s", out)
	}

	// Continue to the breakpoint
	out = mustAf(t, "debug", "continue")
	if !strings.Contains(out, "Stopped") {
		t.Fatalf("expected 'Stopped', got: %s", out)
	}
	if !strings.Contains(out, "breakpoint") {
		t.Fatalf("expected 'breakpoint' reason, got: %s", out)
	}

	// Check stack trace — should show we're in the add function
	out = mustAf(t, "debug", "stacktrace")
	if !strings.Contains(out, "add") {
		t.Fatalf("expected 'add' in stack trace, got: %s", out)
	}

	// Evaluate expression — a + b should be 30 (10 + 20)
	out = mustAf(t, "debug", "eval", "a + b")
	if !strings.Contains(out, "30") {
		t.Fatalf("expected '30', got: %s", out)
	}

	// Disconnect
	mustAf(t, "debug", "disconnect")
}
