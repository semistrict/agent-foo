package integration_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var (
	testServer  *httptest.Server
	afBin       string
	testSession string
)

func TestMain(m *testing.M) {
	build := exec.Command("go", "build", "-o", "bin/af", "./cmd/af")
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic("failed to build: " + err.Error())
	}

	cwd, _ := os.Getwd()
	afBin = cwd + "/bin/af"
	testSession = fmt.Sprintf("test-%d", time.Now().UnixNano())

	testServer = httptest.NewServer(http.FileServer(http.Dir("testdata")))

	// Clean up any leftover test sessions from interrupted runs
	cleanStaleTestSessions()

	code := m.Run()

	af("stop", "--session", testSession)
	testServer.Close()

	os.Exit(code)
}

func af(args ...string) (string, error) {
	hasSession := false
	for _, a := range args {
		if a == "--session" {
			hasSession = true
		}
	}

	var fullArgs []string
	if hasSession {
		fullArgs = args
	} else {
		// Find the subcommand and inject session info
		for i, a := range args {
			switch a {
			case "browser":
				// Insert --headless --session after "browser"
				fullArgs = append(fullArgs, args[:i+1]...)
				fullArgs = append(fullArgs, "--headless", "--session", testSession)
				fullArgs = append(fullArgs, args[i+1:]...)
			case "term":
				// Insert --session after "term"
				fullArgs = append(fullArgs, args[:i+1]...)
				fullArgs = append(fullArgs, "--session", testSession)
				fullArgs = append(fullArgs, args[i+1:]...)
			case "run":
				// run parses its own flags; insert --session before the command name
				fullArgs = append(fullArgs, args[:i+1]...)
				fullArgs = append(fullArgs, "--session", testSession)
				fullArgs = append(fullArgs, args[i+1:]...)
			case "debug":
				// Insert --session after "debug"
				fullArgs = append(fullArgs, args[:i+1]...)
				fullArgs = append(fullArgs, "--session", testSession)
				fullArgs = append(fullArgs, args[i+1:]...)
			case "stop":
				// stop has its own --session flag
				fullArgs = args
			}
			if fullArgs != nil {
				break
			}
		}
		if fullArgs == nil {
			fullArgs = args
		}
	}

	cmd := exec.Command(afBin, fullArgs...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func cleanStaleTestSessions() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".af")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "test-") {
			continue
		}
		// Try to stop running daemons via their socket
		if strings.HasSuffix(name, ".sock") {
			sess := strings.TrimSuffix(name, ".sock")
			af("stop", "--session", sess)
		}
		// Remove any leftover files (pid, log, stale sock)
		os.Remove(filepath.Join(dir, name))
	}
}

func mustAf(t *testing.T, args ...string) string {
	t.Helper()
	out, err := af(args...)
	if err != nil {
		t.Fatalf("af %s failed: %v\noutput: %s", strings.Join(args, " "), err, out)
	}
	return out
}

func TestOpenAndGetTitle(t *testing.T) {
	out := mustAf(t, "browser", "open", testServer.URL+"/basic.html")
	if !strings.Contains(out, "Basic Test Page") {
		t.Fatalf("expected title in output, got: %s", out)
	}

	title := mustAf(t, "browser", "eval", "document.title")
	if title != "Basic Test Page" {
		t.Fatalf("expected 'Basic Test Page', got: %s", title)
	}
}

func TestGetURL(t *testing.T) {
	mustAf(t, "browser", "open", testServer.URL+"/basic.html")

	url := mustAf(t, "browser", "eval", "location.href")
	if !strings.Contains(url, "/basic.html") {
		t.Fatalf("expected URL containing /basic.html, got: %s", url)
	}
}

func TestGetText(t *testing.T) {
	mustAf(t, "browser", "open", testServer.URL+"/basic.html")

	text := mustAf(t, "browser", "eval", `document.querySelector('#greeting').textContent`)
	if text != "Welcome to the test page" {
		t.Fatalf("expected 'Welcome to the test page', got: %s", text)
	}
}

func TestClickButton(t *testing.T) {
	mustAf(t, "browser", "open", testServer.URL+"/basic.html")

	count := mustAf(t, "browser", "eval", `document.querySelector('#count').textContent`)
	if count != "0" {
		t.Fatalf("expected count '0', got: %s", count)
	}

	mustAf(t, "browser", "click", "#counter-btn")

	count = mustAf(t, "browser", "eval", `document.querySelector('#count').textContent`)
	if count != "1" {
		t.Fatalf("expected count '1' after click, got: %s", count)
	}

	mustAf(t, "browser", "click", "#counter-btn")
	mustAf(t, "browser", "click", "#counter-btn")

	count = mustAf(t, "browser", "eval", `document.querySelector('#count').textContent`)
	if count != "3" {
		t.Fatalf("expected count '3' after 3 clicks, got: %s", count)
	}
}

func TestSnapshotAndClickByRef(t *testing.T) {
	mustAf(t, "browser", "open", testServer.URL+"/basic.html")

	snap := mustAf(t, "browser", "snapshot", "-i")
	if !strings.Contains(snap, "link") {
		t.Fatalf("expected snapshot to contain a link, got:\n%s", snap)
	}
	if !strings.Contains(snap, "button") {
		t.Fatalf("expected snapshot to contain a button, got:\n%s", snap)
	}

	var linkRef string
	for _, line := range strings.Split(snap, "\n") {
		if strings.Contains(line, "link") && strings.Contains(line, "links page") {
			linkRef = strings.Fields(line)[0]
			break
		}
	}
	if linkRef == "" {
		t.Fatalf("could not find link ref in snapshot:\n%s", snap)
	}

	mustAf(t, "browser", "click", linkRef)

	title := mustAf(t, "browser", "eval", "document.title")
	if title != "Links Test Page" {
		t.Fatalf("expected 'Links Test Page' after clicking link, got: %s", title)
	}
}

func TestEvalWithRef(t *testing.T) {
	mustAf(t, "browser", "open", testServer.URL+"/basic.html")
	mustAf(t, "browser", "snapshot")

	out := mustAf(t, "browser", "eval", "_REF.e1.textContent")
	if !strings.Contains(out, "Hello World") {
		t.Fatalf("expected _REF.e1 to resolve to heading, got: %s", out)
	}
}

func TestEvalWithRefClick(t *testing.T) {
	mustAf(t, "browser", "open", testServer.URL+"/basic.html")

	snap := mustAf(t, "browser", "snapshot", "-i")
	var btnRef string
	for _, line := range strings.Split(snap, "\n") {
		if strings.Contains(line, "button") {
			btnRef = strings.TrimPrefix(strings.Fields(line)[0], "@")
			break
		}
	}
	if btnRef == "" {
		t.Fatalf("no button ref in snapshot:\n%s", snap)
	}

	mustAf(t, "browser", "eval", fmt.Sprintf("_REF.%s.click()", btnRef))

	count := mustAf(t, "browser", "eval", `document.querySelector('#count').textContent`)
	if count != "1" {
		t.Fatalf("expected count '1' after _REF click, got: %s", count)
	}
}

func TestFormFillAndSubmit(t *testing.T) {
	mustAf(t, "browser", "open", testServer.URL+"/form.html")

	mustAf(t, "browser", "fill", "#name-input", "Alice")

	val := mustAf(t, "browser", "eval", `document.querySelector('#name-input').value`)
	if val != "Alice" {
		t.Fatalf("expected input value 'Alice', got: %s", val)
	}

	mustAf(t, "browser", "fill", "#email-input", "alice@example.com")

	val = mustAf(t, "browser", "eval", `document.querySelector('#email-input').value`)
	if val != "alice@example.com" {
		t.Fatalf("expected email 'alice@example.com', got: %s", val)
	}

	mustAf(t, "browser", "click", "#submit-btn")

	result := mustAf(t, "browser", "eval", `document.querySelector('#result').textContent`)
	if result != "Submitted: Alice" {
		t.Fatalf("expected 'Submitted: Alice', got: %s", result)
	}
}

func TestEvalReturnsValue(t *testing.T) {
	mustAf(t, "browser", "open", testServer.URL+"/basic.html")

	out := mustAf(t, "browser", "eval", "document.title")
	if !strings.Contains(out, "Basic Test Page") {
		t.Fatalf("expected eval to return title, got: %s", out)
	}
}

func TestEvalCount(t *testing.T) {
	mustAf(t, "browser", "open", testServer.URL+"/links.html")

	count := mustAf(t, "browser", "eval", `document.querySelectorAll('a').length`)
	if count != "4" {
		t.Fatalf("expected 4 links, got: %s", count)
	}
}

func TestClickBadSelectorFailsFast(t *testing.T) {
	mustAf(t, "browser", "open", testServer.URL+"/basic.html")

	out, err := af("browser", "--timeout", "1", "click", "#does-not-exist")
	if err == nil {
		t.Fatal("expected error for bad selector, got nil")
	}
	if !strings.Contains(out, "timed out") {
		t.Fatalf("expected timeout error, got: %s", out)
	}
}

func TestJSONFlag(t *testing.T) {
	out := mustAf(t, "--json", "browser", "open", testServer.URL+"/basic.html")
	var m map[string]string
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("expected valid JSON with --json flag, got: %s", out)
	}
	if m["title"] != "Basic Test Page" {
		t.Fatalf("expected title 'Basic Test Page' in JSON, got: %v", m)
	}
}

func TestHumanReadableOpen(t *testing.T) {
	out := mustAf(t, "browser", "open", testServer.URL+"/basic.html")
	// Human-readable format: "Title — URL"
	if !strings.Contains(out, "Basic Test Page") || !strings.Contains(out, "—") {
		t.Fatalf("expected human-readable open output, got: %s", out)
	}
}

func TestEvents(t *testing.T) {
	mustAf(t, "browser", "open", testServer.URL+"/basic.html")

	// Query events (should have network/page events from the open)
	out := mustAf(t, "browser", "events", "--category", "network", "--last", "5")
	if !strings.Contains(out, "network/") {
		t.Fatalf("expected network events, got: %s", out)
	}

	// Stats
	stats := mustAf(t, "browser", "events", "stats")
	if !strings.Contains(stats, "Events:") {
		t.Fatalf("expected human-readable stats, got: %s", stats)
	}

	// JSON events
	jsonOut := mustAf(t, "--json", "browser", "events", "--category", "network", "--last", "2")
	if !strings.HasPrefix(jsonOut, "[") {
		t.Fatalf("expected JSON array with --json flag, got: %s", jsonOut)
	}
}

func TestScreenshot(t *testing.T) {
	mustAf(t, "browser", "open", testServer.URL+"/basic.html")

	path := t.TempDir() + "/test.png"
	out := mustAf(t, "browser", "screenshot", path)
	if !strings.Contains(out, "test.png") {
		t.Fatalf("expected screenshot output to mention path, got: %s", out)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("screenshot file not found: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("screenshot file is empty")
	}
}

// --- Terminal / Run tests ---

func TestRunEcho(t *testing.T) {
	out := mustAf(t, "run", "echo", "hello world")
	if !strings.Contains(out, "hello world") {
		t.Fatalf("expected 'hello world', got: %s", out)
	}
}

func TestRunExitCode(t *testing.T) {
	out, err := af("run", "false")
	if err != nil {
		// af itself doesn't fail, it prints exit code to stderr
		// but CombinedOutput captures both
	}
	_ = err
	if !strings.Contains(out, "exit code") {
		t.Fatalf("expected exit code in output, got: %s", out)
	}
}

func TestRunMultiline(t *testing.T) {
	out := mustAf(t, "run", "printf", "line1\\nline2\\nline3")
	if !strings.Contains(out, "line1") || !strings.Contains(out, "line3") {
		t.Fatalf("expected multiline output, got: %s", out)
	}
}

func TestRunStillRunning(t *testing.T) {
	out := mustAf(t, "run", "--timeout", "1", "--label", "long-sleep", "sleep", "60")
	if !strings.Contains(out, "still running as") {
		t.Fatalf("expected 'still running', got: %s", out)
	}

	// Clean up
	mustAf(t, "term", "kill", "long-sleep")
}

func TestTermSnapshot(t *testing.T) {
	// Use a long-running command so it's still alive for snapshot
	mustAf(t, "run", "--timeout", "1", "--label", "snap-test", "bash", "-c", "echo snapshot-content; sleep 60")

	out := mustAf(t, "term", "snapshot", "--text", "--label", "snap-test")
	if !strings.Contains(out, "snapshot-content") {
		t.Fatalf("expected 'snapshot-content', got: %s", out)
	}

	mustAf(t, "term", "kill", "snap-test")
}

func TestTermList(t *testing.T) {
	mustAf(t, "run", "--timeout", "1", "--label", "list-test", "sleep", "60")

	out := mustAf(t, "term", "list")
	if !strings.Contains(out, "list-test") {
		t.Fatalf("expected 'list-test' in list, got: %s", out)
	}

	mustAf(t, "term", "kill", "list-test")
}

func TestRunJSON(t *testing.T) {
	out := mustAf(t, "run", "--json", "--label", "json-echo", "echo", "json-test")
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("expected valid JSON, got: %s", out)
	}
	screen, _ := m["screen"].(string)
	if !strings.Contains(screen, "json-test") {
		t.Fatalf("expected 'json-test' in screen, got: %v", m)
	}
}
