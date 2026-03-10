package term

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/vito/midterm"
)

// fakeInstance creates an Instance with no real process.
// The returned *os.File is the write end of the PTY (unused by doWait but needed by the struct).
// The done channel controls when the "process" is considered exited.
func fakeInstance(done chan struct{}) *Instance {
	r, w, _ := os.Pipe()
	term := midterm.NewTerminal(24, 80)
	return &Instance{
		Term:       term,
		Pty:        r,
		done:       done,
		lastOutput: time.Now(),
		fakePid:    42,
		fakeExit:   w, // keep reference so pipe doesn't close
	}
}

func TestWaitExitImmediate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		done := make(chan struct{})
		close(done)

		inst := fakeInstance(done)
		inst.exitCode = 0
		inst.Term.Write([]byte("hello\r\n"))

		h := NewHandler()
		h.instances["test"] = inst

		start := time.Now()
		resp := h.doWait(paramMap{"label": "test", "timeout": "30"})
		elapsed := time.Since(start)

		var m map[string]any
		json.Unmarshal(resp.Data, &m)

		if m["exited"] != true {
			t.Fatalf("expected exited=true, got %v", m)
		}
		if m["exitCode"] != float64(0) {
			t.Fatalf("expected exitCode=0, got %v", m["exitCode"])
		}
		screen, _ := m["screen"].(string)
		if !strings.Contains(screen, "hello") {
			t.Fatalf("expected 'hello' in screen, got: %s", screen)
		}
		if elapsed > 1*time.Second {
			t.Fatalf("should return immediately, took %v", elapsed)
		}
	})
}

func TestWaitExitNonZero(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		done := make(chan struct{})
		close(done)

		inst := fakeInstance(done)
		inst.exitCode = 1

		h := NewHandler()
		h.instances["test"] = inst

		resp := h.doWait(paramMap{"label": "test"})
		var m map[string]any
		json.Unmarshal(resp.Data, &m)

		if m["exited"] != true {
			t.Fatalf("expected exited=true, got %v", m)
		}
		if m["exitCode"] != float64(1) {
			t.Fatalf("expected exitCode=1, got %v", m["exitCode"])
		}
	})
}

func TestWaitIdleTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inst := fakeInstance(make(chan struct{}))
		inst.Term.Write([]byte("hello\r\n"))

		h := NewHandler()
		h.instances["test"] = inst

		start := time.Now()
		resp := h.doWait(paramMap{"label": "test", "timeout": "5"})
		elapsed := time.Since(start)

		var m map[string]any
		json.Unmarshal(resp.Data, &m)

		if m["exited"] != nil {
			t.Fatalf("expected not exited, got %v", m)
		}
		if elapsed < 5*time.Second || elapsed > 6*time.Second {
			t.Fatalf("expected ~5s idle timeout, got %v", elapsed)
		}
	})
}

func TestWaitOutputExtendsIdle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inst := fakeInstance(make(chan struct{}))
		inst.Term.Write([]byte("initial\r\n"))

		// Output arrives at t=3s, resetting the idle timer.
		// With a 5s idle timeout, wait should return at ~8s (3+5), not 5s.
		time.AfterFunc(3*time.Second, func() {
			inst.mu.Lock()
			inst.lastOutput = time.Now()
			inst.Term.Write([]byte("more output\r\n"))
			inst.mu.Unlock()
		})

		h := NewHandler()
		h.instances["test"] = inst

		start := time.Now()
		resp := h.doWait(paramMap{"label": "test", "timeout": "5"})
		elapsed := time.Since(start)

		if elapsed < 7*time.Second {
			t.Fatalf("output at t=3s should have extended wait, returned after %v", elapsed)
		}
		if elapsed > 9*time.Second {
			t.Fatalf("expected ~8s total, got %v", elapsed)
		}

		var m map[string]any
		json.Unmarshal(resp.Data, &m)

		screen, _ := m["screen"].(string)
		if !strings.Contains(screen, "more output") {
			t.Fatalf("expected 'more output' in screen, got: %s", screen)
		}
	})
}

func TestWaitRepeatedOutputKeepsWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inst := fakeInstance(make(chan struct{}))

		// Output every 2s for 10s, then stop.
		// With 5s idle timeout, should return at ~15s (10s of output + 5s idle).
		stop := make(chan struct{})
		time.AfterFunc(10*time.Second, func() { close(stop) })

		go func() {
			tick := time.NewTicker(2 * time.Second)
			defer tick.Stop()
			for {
				select {
				case <-stop:
					return
				case <-tick.C:
					inst.mu.Lock()
					inst.lastOutput = time.Now()
					inst.mu.Unlock()
				}
			}
		}()

		h := NewHandler()
		h.instances["test"] = inst

		start := time.Now()
		h.doWait(paramMap{"label": "test", "timeout": "5"})
		elapsed := time.Since(start)

		if elapsed < 14*time.Second {
			t.Fatalf("repeated output should have kept wait alive, returned after %v", elapsed)
		}
		if elapsed > 16*time.Second {
			t.Fatalf("expected ~15s, got %v", elapsed)
		}
	})
}
