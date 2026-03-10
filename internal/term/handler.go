package term

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/vito/midterm"

	"github.com/semistrict/agent-foo/internal/protocol"
)

// Instance holds a running command inside a virtual terminal.
type Instance struct {
	Term       *midterm.Terminal
	Scrollback []midterm.Line
	Cmd        *exec.Cmd
	Pty        *os.File
	done       chan struct{} // closed when process exits
	lastOutput time.Time     // last time output was received
	mu         sync.Mutex

	// For testing without real processes
	fakePid  int
	exitCode int
	fakeExit *os.File // prevent GC of pipe
}

func (inst *Instance) pid() int {
	if inst.Cmd != nil && inst.Cmd.Process != nil {
		return inst.Cmd.Process.Pid
	}
	return inst.fakePid
}

func (inst *Instance) getExitCode() int {
	if inst.Cmd != nil && inst.Cmd.ProcessState != nil {
		return inst.Cmd.ProcessState.ExitCode()
	}
	return inst.exitCode
}

// Handler manages terminal instances keyed by label.
type Handler struct {
	mu        sync.Mutex
	instances map[string]*Instance
}

func NewHandler() *Handler {
	return &Handler{
		instances: make(map[string]*Instance),
	}
}

func (h *Handler) HandleRequest(req *protocol.Request) *protocol.Response {
	p := parseParams(req.Params)

	switch req.Action {
	case "run":
		return h.doRun(p)
	case "snapshot":
		return h.doSnapshot(p)
	case "wait":
		return h.doWait(p)
	case "input":
		return h.doInput(p)
	case "resize":
		return h.doResize(p)
	case "kill":
		return h.doKill(p)
	case "killall":
		return h.doKillAll()
	case "list":
		return h.doList()
	case "close":
		return h.doClose()
	default:
		return errResp("unknown action: %s", req.Action)
	}
}

func (h *Handler) doRun(p paramMap) *protocol.Response {
	var args []string
	if err := json.Unmarshal([]byte(p["args"]), &args); err != nil || len(args) == 0 {
		return errResp("args is required (JSON string array)")
	}

	rows, cols := 24, 80
	if v := p["rows"]; v != "" {
		rows, _ = strconv.Atoi(v)
	}
	if v := p["cols"]; v != "" {
		cols, _ = strconv.Atoi(v)
	}

	label := p["label"]
	if label == "" {
		label = args[0]
	}

	h.mu.Lock()
	// If label is taken, auto-suffix with -2, -3, etc. (unless user explicitly chose it)
	if _, exists := h.instances[label]; exists {
		if existing, ok := h.instances[label]; ok {
			select {
			case <-existing.done:
				// Exited: reuse the label
				existing.Pty.Close()
				delete(h.instances, label)
			default:
				if p["label"] != "" {
					// User explicitly chose this label; don't rename
					h.mu.Unlock()
					return errResp("instance %q already running", label)
				}
				// Auto-assign a new label
				base := label
				for n := 2; ; n++ {
					candidate := fmt.Sprintf("%s-%d", base, n)
					if _, taken := h.instances[candidate]; !taken {
						label = candidate
						break
					}
				}
			}
		}
	}
	h.mu.Unlock()

	cmd := exec.Command(args[0], args[1:]...)
	if cwd := p["cwd"]; cwd != "" {
		cmd.Dir = cwd
	}
	// Use client's environment if provided, otherwise fall back to daemon's
	var environ []string
	if envRaw := p["env"]; envRaw != "" {
		json.Unmarshal([]byte(envRaw), &environ)
	}
	if len(environ) == 0 {
		environ = os.Environ()
	}
	for _, e := range environ {
		if !strings.HasPrefix(e, "CLAUDECODE=") {
			cmd.Env = append(cmd.Env, e)
		}
	}

	term := midterm.NewTerminal(rows, cols)

	var scrollback []midterm.Line
	var scrollMu sync.Mutex
	term.OnScrollback(func(line midterm.Line) {
		scrollMu.Lock()
		scrollback = append(scrollback, line)
		scrollMu.Unlock()
	})

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	})
	if err != nil {
		return errResp("start command: %v", err)
	}

	done := make(chan struct{})
	inst := &Instance{
		Term:       term,
		Scrollback: scrollback,
		Cmd:        cmd,
		Pty:        ptmx,
		done:       done,
		lastOutput: time.Now(),
	}

	h.mu.Lock()
	h.instances[label] = inst
	h.mu.Unlock()

	// Pump PTY output into the virtual terminal
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				inst.mu.Lock()
				term.Write(buf[:n])
				inst.lastOutput = time.Now()
				scrollMu.Lock()
				inst.Scrollback = scrollback
				scrollMu.Unlock()
				inst.mu.Unlock()
			}
			if err != nil {
				break
			}
		}
	}()

	// Wait for process exit in background
	go func() {
		cmd.Wait()
		close(done)
	}()

	return dataResp(map[string]any{
		"label": label,
		"pid":   cmd.Process.Pid,
		"rows":  rows,
		"cols":  cols,
	})
}

func (h *Handler) doSnapshot(p paramMap) *protocol.Response {
	// If a label is specified, snapshot just that instance
	if p["label"] != "" {
		inst, err := h.getInstance(p)
		if err != nil {
			return errResp("%v", err)
		}
		return h.snapshotOne(inst, p)
	}

	// No label: snapshot all instances
	h.mu.Lock()
	if len(h.instances) == 0 {
		h.mu.Unlock()
		return errResp("no running instances")
	}
	// Collect labels in sorted order for deterministic output
	labels := make([]string, 0, len(h.instances))
	for label := range h.instances {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	instances := make([]*Instance, len(labels))
	for i, label := range labels {
		instances[i] = h.instances[label]
	}
	h.mu.Unlock()

	// Always return array when no label specified
	var results []map[string]any
	for i, inst := range instances {
		r := h.snapshotResult(inst, p)
		r["label"] = labels[i]
		results = append(results, r)
	}
	return dataResp(results)
}

func (h *Handler) snapshotOne(inst *Instance, p paramMap) *protocol.Response {
	if p["text_only"] == "true" {
		inst.mu.Lock()
		screen := h.renderScreen(inst)
		inst.mu.Unlock()
		return dataResp(screen)
	}
	return dataResp(h.snapshotResult(inst, p))
}

func (h *Handler) snapshotResult(inst *Instance, p paramMap) map[string]any {
	inst.mu.Lock()
	screen := h.renderScreen(inst)
	cursor := map[string]int{
		"row": inst.Term.Cursor.Y,
		"col": inst.Term.Cursor.X,
	}
	rows := inst.Term.Height
	cols := inst.Term.Width
	inst.mu.Unlock()

	result := map[string]any{
		"screen": screen,
		"cursor": cursor,
		"rows":   rows,
		"cols":   cols,
		"pid":    inst.pid(),
	}

	exited := false
	exitCode := -1
	select {
	case <-inst.done:
		exited = true
		exitCode = inst.getExitCode()
	default:
	}
	if exited {
		result["exited"] = true
		result["exitCode"] = exitCode
	}
	return result
}

func (h *Handler) doWait(p paramMap) *protocol.Response {
	inst, err := h.getInstance(p)
	if err != nil {
		return errResp("%v", err)
	}

	idleTimeout := 30 * time.Second
	if v := p["timeout"]; v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			idleTimeout = time.Duration(secs) * time.Second
		}
	}

	// Wait until process exits or no output for idleTimeout
	for {
		select {
		case <-inst.done:
			// Small delay to let the PTY reader drain remaining output
			time.Sleep(50 * time.Millisecond)
			inst.mu.Lock()
			screen := h.renderScreen(inst)
			inst.mu.Unlock()
			return dataResp(map[string]any{
				"screen":   screen,
				"pid":      inst.pid(),
				"exited":   true,
				"exitCode": inst.getExitCode(),
			})
		case <-time.After(100 * time.Millisecond):
			inst.mu.Lock()
			idle := time.Since(inst.lastOutput)
			inst.mu.Unlock()
			if idle >= idleTimeout {
				inst.mu.Lock()
				screen := h.renderScreen(inst)
				inst.mu.Unlock()
				return dataResp(map[string]any{
					"screen": screen,
					"pid":    inst.pid(),
				})
			}
		}
	}
}

func (h *Handler) renderScreen(inst *Instance) string {
	var buf strings.Builder
	for _, line := range inst.Scrollback {
		buf.WriteString(trimRenderedLine(line.Display()))
		buf.WriteByte('\n')
	}
	height := inst.Term.UsedHeight()
	if height == 0 {
		height = inst.Term.Height
	}
	for y := 0; y < height; y++ {
		var line strings.Builder
		inst.Term.RenderLine(&line, y)
		buf.WriteString(trimRenderedLine(line.String()))
		buf.WriteByte('\n')
	}
	return strings.TrimRight(buf.String(), "\n")
}

// trimRenderedLine strips trailing whitespace and ANSI reset sequences from a rendered line.
func trimRenderedLine(s string) string {
	for {
		t := strings.TrimRight(s, " ")
		t = strings.TrimSuffix(t, "\x1b[0m")
		if t == s {
			return s
		}
		s = t
	}
}

func (h *Handler) doInput(p paramMap) *protocol.Response {
	inst, err := h.getInstance(p)
	if err != nil {
		return errResp("%v", err)
	}

	// Support both raw text and tmux-style key names
	var data string
	if keysRaw := p["keys"]; keysRaw != "" {
		var keys []string
		if json.Unmarshal([]byte(keysRaw), &keys) == nil {
			data = TranslateKeys(keys)
		} else {
			data = TranslateKey(keysRaw)
		}
	} else if text := p["text"]; text != "" {
		data = text
	} else {
		return errResp("text or keys is required")
	}

	_, err = io.WriteString(inst.Pty, data)
	if err != nil {
		return errResp("write to pty: %v", err)
	}
	return okResp("Done")
}

func (h *Handler) doResize(p paramMap) *protocol.Response {
	inst, err := h.getInstance(p)
	if err != nil {
		return errResp("%v", err)
	}

	rows, _ := strconv.Atoi(p["rows"])
	cols, _ := strconv.Atoi(p["cols"])
	if rows <= 0 || cols <= 0 {
		return errResp("rows and cols must be positive")
	}

	inst.mu.Lock()
	inst.Term.Resize(rows, cols)
	inst.mu.Unlock()

	pty.Setsize(inst.Pty, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	})

	return okResp("Done")
}

func (h *Handler) doKill(p paramMap) *protocol.Response {
	label := p["label"]
	if label == "" {
		return errResp("label is required")
	}

	h.mu.Lock()
	inst, ok := h.instances[label]
	if !ok {
		h.mu.Unlock()
		return errResp("no instance %q", label)
	}
	delete(h.instances, label)
	h.mu.Unlock()

	forced := gracefulKill(inst)
	if forced {
		return okResp("Killed (forced)")
	}
	return okResp("Killed")
}

func (h *Handler) doKillAll() *protocol.Response {
	h.mu.Lock()
	n := len(h.instances)
	labels := make([]string, 0, n)
	for label := range h.instances {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	insts := make([]*Instance, len(labels))
	for i, l := range labels {
		insts[i] = h.instances[l]
		delete(h.instances, l)
	}
	h.mu.Unlock()

	forced := 0
	for _, inst := range insts {
		if gracefulKill(inst) {
			forced++
		}
	}
	msg := fmt.Sprintf("Killed %d instances", n)
	if forced > 0 {
		msg += fmt.Sprintf(" (%d forced)", forced)
	}
	return okResp(msg)
}

func (h *Handler) doList() *protocol.Response {
	h.mu.Lock()
	defer h.mu.Unlock()

	var items []map[string]any
	for label, inst := range h.instances {
		exited := false
		select {
		case <-inst.done:
			exited = true
		default:
		}
		item := map[string]any{
			"label":  label,
			"pid":    inst.pid(),
			"exited": exited,
		}
		if exited {
			item["exitCode"] = inst.getExitCode()
		}
		items = append(items, item)
	}
	return dataResp(items)
}

func (h *Handler) doClose() *protocol.Response {
	h.mu.Lock()
	if len(h.instances) == 0 {
		h.mu.Unlock()
		return dataResp([]any{})
	}
	labels := make([]string, 0, len(h.instances))
	for label := range h.instances {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	instances := make([]*Instance, len(labels))
	for i, l := range labels {
		instances[i] = h.instances[l]
	}
	h.mu.Unlock()

	var results []map[string]any
	for i, inst := range instances {
		// Grab last screen before killing
		inst.mu.Lock()
		screen := h.renderScreen(inst)
		inst.mu.Unlock()

		forced := gracefulKill(inst)

		result := map[string]any{
			"label":  labels[i],
			"pid":    inst.pid(),
			"screen": screen,
		}
		if forced {
			result["forced"] = true
		}
		results = append(results, result)

		h.mu.Lock()
		delete(h.instances, labels[i])
		h.mu.Unlock()
	}
	return dataResp(results)
}

func (h *Handler) getInstance(p paramMap) (*Instance, error) {
	label := p["label"]
	if label == "" {
		// Default to first (or only) instance
		h.mu.Lock()
		defer h.mu.Unlock()
		if len(h.instances) == 0 {
			return nil, fmt.Errorf("no running instances")
		}
		if len(h.instances) == 1 {
			for _, inst := range h.instances {
				return inst, nil
			}
		}
		return nil, fmt.Errorf("multiple instances running, specify --label")
	}

	h.mu.Lock()
	inst, ok := h.instances[label]
	h.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("no instance %q", label)
	}
	return inst, nil
}

// gracefulKill sends SIGTERM, waits up to 10s for exit, then SIGKILL if needed.
// Returns true if SIGKILL was required (forced).
func gracefulKill(inst *Instance) bool {
	if inst.Cmd == nil || inst.Cmd.Process == nil {
		inst.Pty.Close()
		return false
	}

	// Already exited?
	select {
	case <-inst.done:
		inst.Pty.Close()
		return false
	default:
	}

	// Send SIGTERM
	inst.Cmd.Process.Signal(syscall.SIGTERM)

	// Wait up to 10s
	select {
	case <-inst.done:
		inst.Pty.Close()
		return false
	case <-time.After(10 * time.Second):
		// Force kill
		inst.Cmd.Process.Kill()
		<-inst.done
		inst.Pty.Close()
		return true
	}
}

// --- helpers ---

type paramMap map[string]string

func parseParams(raw json.RawMessage) paramMap {
	m := make(paramMap)
	if raw != nil {
		json.Unmarshal(raw, &m)
	}
	return m
}

func errResp(format string, args ...any) *protocol.Response {
	return &protocol.Response{Success: false, Error: fmt.Sprintf(format, args...)}
}

func okResp(msg string) *protocol.Response {
	data, _ := json.Marshal(msg)
	return &protocol.Response{Success: true, Data: data}
}

func dataResp(v any) *protocol.Response {
	data, _ := json.Marshal(v)
	return &protocol.Response{Success: true, Data: data}
}
