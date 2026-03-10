package debugger

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/go-dap"

	"github.com/semistrict/agent-foo/internal/protocol"
)

// Session is a connection to one debug adapter.
type Session struct {
	label string
	conn  net.Conn
	cmd   *exec.Cmd // nil if attached to existing adapter
	r     *bufio.Reader
	seq   int
	mu    sync.Mutex
	done  chan struct{}

	// Single reader goroutine dispatches to these
	responses chan dap.Message // responses go here
	events    []dap.Message   // events accumulate here
	eventsMu  sync.Mutex

	// Set after initialize handshake
	capabilities *dap.Capabilities

	// Current state: "running", "stopped", "exited"
	state           string
	stoppedThreadId int

	// Original launch params for restart
	launchParams paramMap

	// adapterAddr is the TCP address of the adapter (host:port) for creating
	// child sessions in response to startDebugging reverse requests.
	adapterAddr string

	// child holds the active child session created by startDebugging.
	// When set, all DAP commands are routed through the child.
	child *Session

	// childReady is closed when a child session is fully initialized.
	childReady chan struct{}

	// parent, if set, is the parent session. Events from child sessions
	// are forwarded to the parent's event store.
	parent *Session
}

// Handler manages debug sessions keyed by label.
type Handler struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

func NewHandler() *Handler {
	return &Handler{
		sessions: make(map[string]*Session),
	}
}

func (h *Handler) HandleRequest(req *protocol.Request) *protocol.Response {
	p := parseParams(req.Params)

	switch req.Action {
	case "launch":
		return h.doLaunch(p)
	case "attach":
		return h.doAttach(p)
	case "breakpoint":
		return h.doSetBreakpoints(p)
	case "continue":
		return h.doControl(p, "continue")
	case "next":
		return h.doControl(p, "next")
	case "stepin":
		return h.doControl(p, "stepin")
	case "stepout":
		return h.doControl(p, "stepout")
	case "pause":
		return h.doControl(p, "pause")
	case "stacktrace":
		return h.doStackTrace(p)
	case "scopes":
		return h.doScopes(p)
	case "variables":
		return h.doVariables(p)
	case "evaluate":
		return h.doEvaluate(p)
	case "threads":
		return h.doThreads(p)
	case "restart":
		return h.doRestart(p)
	case "disconnect":
		return h.doDisconnect(p)
	case "events":
		return h.doEvents(p)
	case "list":
		return h.doList()
	case "close":
		return h.doClose()
	default:
		return errResp("unknown action: %s", req.Action)
	}
}

// doLaunch starts a debug adapter process and connects to it via stdio.
func (h *Handler) doLaunch(p paramMap) *protocol.Response {
	label := p["label"]
	if label == "" {
		label = "default"
	}

	h.mu.Lock()
	if _, exists := h.sessions[label]; exists {
		h.mu.Unlock()
		return errResp("session %q already exists", label)
	}
	h.mu.Unlock()

	// adapter: either a known name (e.g. "js-debug") or a shell command (e.g. "dlv dap").
	// Auto-detected from --program extension if not specified.
	adapter := p["adapter"]
	if adapter == "" {
		adapter = adapterForProgram(p["program"])
	}
	if adapter == "" {
		ext := filepath.Ext(p["program"])
		if ext != "" {
			return errResp("no debug adapter for %s files (use --adapter to specify one)", ext)
		}
		return errResp("adapter is required (use --adapter to specify one)")
	}

	// Check for known/auto-downloadable adapters
	resolved, err := resolveAdapter(adapter)
	if err != nil {
		return errResp("%v", err)
	}

	var parts []string
	useTCP := p["port"] != ""
	if resolved != nil {
		parts = resolved.Command
		useTCP = resolved.TCP
	} else {
		parts = strings.Fields(adapter)
	}

	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Env = os.Environ()

	// Use TCP: adapter listens on a port
	if useTCP {
		port := p["port"]
		if port == "" {
			// Pick a free port
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return errResp("find free port: %v", err)
			}
			port = fmt.Sprintf("%d", l.Addr().(*net.TCPAddr).Port)
			l.Close()
		}
		// Append port and host args for resolved adapters (e.g. dapDebugServer.js <port> <host>)
		if resolved != nil {
			cmd.Args = append(cmd.Args, port, "127.0.0.1")
		}
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			return errResp("start adapter: %v", err)
		}
		// Give adapter time to start listening
		time.Sleep(500 * time.Millisecond)

		conn, err := net.Dial("tcp", "127.0.0.1:"+port)
		if err != nil {
			cmd.Process.Kill()
			return errResp("connect to adapter: %v", err)
		}

		sess := h.newSession(label, conn, cmd)
		sess.launchParams = p
		sess.adapterAddr = "127.0.0.1:" + port
		sess.childReady = make(chan struct{})
		if err := sess.initialize(); err != nil {
			sess.close()
			return errResp("initialize: %v", err)
		}

		// Merge default launch args from resolved adapter
		if resolved != nil && resolved.DefaultLaunchArgs != nil {
			mergeDefaultLaunchArgs(p, resolved.DefaultLaunchArgs)
		}

		// DAP handshake: dapDebugServer.js (js-debug) blocks the launch
		// response until configurationDone is received. So we send the
		// launch request (non-blocking), then configurationDone, then
		// read the launch response.
		if err := sess.fireLaunch(p); err != nil {
			sess.close()
			return errResp("launch: %v", err)
		}
		if err := sess.configurationDone(); err != nil {
			sess.close()
			return errResp("configurationDone: %v", err)
		}
		if resp, err := sess.receiveResponse(); err != nil {
			sess.close()
			return errResp("launch response: %v", err)
		} else {
			_ = resp
		}

		// Wait for child session to be established (startDebugging)
		select {
		case <-sess.childReady:
		case <-time.After(10 * time.Second):
			sess.close()
			return errResp("timeout waiting for child debug session")
		}

		// If stopOnEntry, wait for the debuggee to actually stop before returning.
		if p["stopOnEntry"] == "true" {
			if stopped := sess.waitForStopped(0, 10*time.Second); stopped != nil {
				h.mu.Lock()
				h.sessions[label] = sess
				h.mu.Unlock()

				result := map[string]any{
					"label":    label,
					"status":   "stopped",
					"reason":   stopped.Body.Reason,
					"threadId": stopped.Body.ThreadId,
				}
				if frames := sess.fetchStackTrace(stopped.Body.ThreadId, 5); len(frames) > 0 {
					result["stackTrace"] = frames
				}
				return dataResp(result)
			}
		}

		h.mu.Lock()
		h.sessions[label] = sess
		h.mu.Unlock()

		return dataResp(map[string]any{
			"label":  label,
			"status": "launched",
		})
	}

	// Use stdio: pipe stdin/stdout
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return errResp("create stdin pipe: %v", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return errResp("create stdout pipe: %v", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return errResp("start adapter: %v", err)
	}

	conn := &stdioConn{
		Reader: stdoutPipe,
		Writer: stdinPipe,
		closer: func() error {
			stdinPipe.Close()
			return cmd.Process.Kill()
		},
	}

	sess := h.newSession(label, conn, cmd)
	sess.launchParams = p
	if err := sess.initialize(); err != nil {
		sess.close()
		return errResp("initialize: %v", err)
	}

	// Merge default launch args from resolved adapter
	if resolved != nil && resolved.DefaultLaunchArgs != nil {
		mergeDefaultLaunchArgs(p, resolved.DefaultLaunchArgs)
	}

	if err := sess.fireLaunch(p); err != nil {
		sess.close()
		return errResp("launch: %v", err)
	}
	if err := sess.configurationDone(); err != nil {
		sess.close()
		return errResp("configurationDone: %v", err)
	}
	if resp, err := sess.receiveResponse(); err != nil {
		sess.close()
		return errResp("launch response: %v", err)
	} else if lr, ok := resp.(*dap.LaunchResponse); ok && !lr.Response.Success {
		sess.close()
		return errResp("launch failed: %s", lr.Message)
	}

	// If stopOnEntry, wait for the debuggee to actually stop before returning.
	if p["stopOnEntry"] == "true" {
		if stopped := sess.waitForStopped(0, 10*time.Second); stopped != nil {
			h.mu.Lock()
			h.sessions[label] = sess
			h.mu.Unlock()

			result := map[string]any{
				"label":    label,
				"status":   "stopped",
				"reason":   stopped.Body.Reason,
				"threadId": stopped.Body.ThreadId,
			}
			if frames := sess.fetchStackTrace(stopped.Body.ThreadId, 5); len(frames) > 0 {
				result["stackTrace"] = frames
			}
			return dataResp(result)
		}
	}

	h.mu.Lock()
	h.sessions[label] = sess
	h.mu.Unlock()

	return dataResp(map[string]any{
		"label":  label,
		"status": "launched",
	})
}

// doAttach connects to an already-running debug adapter.
func (h *Handler) doAttach(p paramMap) *protocol.Response {
	label := p["label"]
	if label == "" {
		label = "default"
	}

	h.mu.Lock()
	if _, exists := h.sessions[label]; exists {
		h.mu.Unlock()
		return errResp("session %q already exists", label)
	}
	h.mu.Unlock()

	host := p["host"]
	if host == "" {
		host = "127.0.0.1"
	}
	port := p["port"]
	if port == "" {
		return errResp("port is required")
	}

	conn, err := net.Dial("tcp", host+":"+port)
	if err != nil {
		return errResp("connect: %v", err)
	}

	sess := h.newSession(label, conn, nil)
	if err := sess.initialize(); err != nil {
		sess.close()
		return errResp("initialize: %v", err)
	}

	// DAP handshake: attach → initialized event → configurationDone
	if err := sess.sendAttach(p); err != nil {
		sess.close()
		return errResp("attach: %v", err)
	}
	if err := sess.configurationDone(); err != nil {
		sess.close()
		return errResp("configurationDone: %v", err)
	}

	h.mu.Lock()
	h.sessions[label] = sess
	h.mu.Unlock()

	return dataResp(map[string]any{
		"label":  label,
		"status": "attached",
	})
}

func (h *Handler) doSetBreakpoints(p paramMap) *protocol.Response {
	sess, err := h.getSession(p)
	if err != nil {
		return errResp("%v", err)
	}

	file := p["file"]
	if file == "" {
		return errResp("file is required")
	}

	var lines []int
	if linesStr := p["lines"]; linesStr != "" {
		for _, s := range strings.Split(linesStr, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil {
				return errResp("invalid line number: %s", s)
			}
			lines = append(lines, n)
		}
	}

	var bps []dap.SourceBreakpoint
	for _, line := range lines {
		bp := dap.SourceBreakpoint{Line: line}
		if cond := p["condition"]; cond != "" {
			bp.Condition = cond
		}
		bps = append(bps, bp)
	}

	seq := sess.nextSeq()

	req := &dap.SetBreakpointsRequest{
		Request: newRequest(seq, "setBreakpoints"),
		Arguments: dap.SetBreakpointsArguments{
			Source:      dap.Source{Path: file},
			Breakpoints: bps,
		},
	}

	resp, err := sess.sendAndReceive(req)
	if err != nil {
		return errResp("setBreakpoints: %v", err)
	}

	if bpResp, ok := resp.(*dap.SetBreakpointsResponse); ok {
		var result []map[string]any
		for _, bp := range bpResp.Body.Breakpoints {
			m := map[string]any{
				"id":       bp.Id,
				"verified": bp.Verified,
				"line":     bp.Line,
			}
			if bp.Message != "" {
				m["message"] = bp.Message
			}
			if bp.Source != nil {
				m["file"] = bp.Source.Path
			}
			result = append(result, m)
		}
		return dataResp(result)
	}
	return respToResult(resp)
}

func (h *Handler) doControl(p paramMap, action string) *protocol.Response {
	sess, err := h.getSession(p)
	if err != nil {
		return errResp("%v", err)
	}

	threadId := sess.stoppedThreadId
	if v := p["thread"]; v != "" {
		threadId, _ = strconv.Atoi(v)
	}

	seq := sess.nextSeq()

	var req dap.Message
	switch action {
	case "continue":
		req = &dap.ContinueRequest{
			Request:   newRequest(seq, "continue"),
			Arguments: dap.ContinueArguments{ThreadId: threadId},
		}
	case "next":
		req = &dap.NextRequest{
			Request:   newRequest(seq, "next"),
			Arguments: dap.NextArguments{ThreadId: threadId},
		}
	case "stepin":
		req = &dap.StepInRequest{
			Request:   newRequest(seq, "stepIn"),
			Arguments: dap.StepInArguments{ThreadId: threadId},
		}
	case "stepout":
		req = &dap.StepOutRequest{
			Request:   newRequest(seq, "stepOut"),
			Arguments: dap.StepOutArguments{ThreadId: threadId},
		}
	case "pause":
		req = &dap.PauseRequest{
			Request:   newRequest(seq, "pause"),
			Arguments: dap.PauseArguments{ThreadId: threadId},
		}
	}

	// Record event count before sending so we only look at new events
	sess.eventsMu.Lock()
	eventsBefore := len(sess.events)
	sess.eventsMu.Unlock()

	// Send the control request (routed through active/child session)
	a := sess.active()
	if err := dap.WriteProtocolMessage(a.conn, req); err != nil {
		return errResp("send %s: %v", action, err)
	}

	// Read the response from the active session
	resp, err := a.receiveResponse()
	if err != nil {
		return errResp("receive %s response: %v", action, err)
	}

	// For continue/step, wait for stopped event (only new events)
	if action != "pause" {
		stopped := sess.waitForStopped(eventsBefore, 10*time.Second)
		if stopped != nil {
			result := map[string]any{
				"status":   "stopped",
				"reason":   stopped.Body.Reason,
				"threadId": stopped.Body.ThreadId,
			}
			// Fetch a brief stack trace
			if frames := sess.fetchStackTrace(stopped.Body.ThreadId, 5); len(frames) > 0 {
				result["stackTrace"] = frames
			}
			return dataResp(result)
		}
		// Check if program exited
		sess.eventsMu.Lock()
		for i := eventsBefore; i < len(sess.events); i++ {
			if e, ok := sess.events[i].(*dap.ExitedEvent); ok {
				sess.eventsMu.Unlock()
				return dataResp(map[string]any{
					"status":   "exited",
					"exitCode": e.Body.ExitCode,
				})
			}
		}
		sess.eventsMu.Unlock()
		return respToResult(resp)
	}

	return respToResult(resp)
}

func (h *Handler) doStackTrace(p paramMap) *protocol.Response {
	sess, err := h.getSession(p)
	if err != nil {
		return errResp("%v", err)
	}

	threadId := sess.stoppedThreadId
	if v := p["thread"]; v != "" {
		threadId, _ = strconv.Atoi(v)
	}

	levels := 20
	if v := p["levels"]; v != "" {
		levels, _ = strconv.Atoi(v)
	}

	frames := sess.fetchStackTrace(threadId, levels)
	if frames == nil {
		return errResp("stackTrace: failed to fetch")
	}
	return dataResp(frames)
}

func (h *Handler) doScopes(p paramMap) *protocol.Response {
	sess, err := h.getSession(p)
	if err != nil {
		return errResp("%v", err)
	}

	frameId, _ := strconv.Atoi(p["frame"])
	if p["frame"] == "" {
		if frames := sess.fetchStackTrace(sess.stoppedThreadId, 1); len(frames) > 0 {
			if id, ok := frames[0]["id"].(int); ok {
				frameId = id
			}
		}
	}

	seq := sess.nextSeq()

	req := &dap.ScopesRequest{
		Request:   newRequest(seq, "scopes"),
		Arguments: dap.ScopesArguments{FrameId: frameId},
	}

	resp, err := sess.sendAndReceive(req)
	if err != nil {
		return errResp("scopes: %v", err)
	}

	if scResp, ok := resp.(*dap.ScopesResponse); ok {
		var scopes []map[string]any
		for _, s := range scResp.Body.Scopes {
			scopes = append(scopes, map[string]any{
				"name":               s.Name,
				"variablesReference": s.VariablesReference,
				"expensive":          s.Expensive,
			})
		}
		return dataResp(scopes)
	}
	return respToResult(resp)
}

func (h *Handler) doVariables(p paramMap) *protocol.Response {
	sess, err := h.getSession(p)
	if err != nil {
		return errResp("%v", err)
	}

	ref, _ := strconv.Atoi(p["ref"])
	if ref == 0 {
		return errResp("ref (variablesReference) is required")
	}

	seq := sess.nextSeq()

	req := &dap.VariablesRequest{
		Request:   newRequest(seq, "variables"),
		Arguments: dap.VariablesArguments{VariablesReference: ref},
	}

	resp, err := sess.sendAndReceive(req)
	if err != nil {
		return errResp("variables: %v", err)
	}

	if vResp, ok := resp.(*dap.VariablesResponse); ok {
		var vars []map[string]any
		for _, v := range vResp.Body.Variables {
			m := map[string]any{
				"name":  v.Name,
				"value": v.Value,
			}
			if v.Type != "" {
				m["type"] = v.Type
			}
			if v.VariablesReference > 0 {
				m["ref"] = v.VariablesReference
			}
			vars = append(vars, m)
		}
		return dataResp(vars)
	}
	return respToResult(resp)
}

func (h *Handler) doEvaluate(p paramMap) *protocol.Response {
	sess, err := h.getSession(p)
	if err != nil {
		return errResp("%v", err)
	}

	expr := p["expr"]
	if expr == "" {
		return errResp("expr is required")
	}

	frameId, _ := strconv.Atoi(p["frame"])
	// Auto-resolve frame ID from top of stack if not specified
	if p["frame"] == "" {
		if frames := sess.fetchStackTrace(sess.stoppedThreadId, 1); len(frames) > 0 {
			if id, ok := frames[0]["id"].(int); ok {
				frameId = id
			}
		}
	}

	context := p["context"]
	if context == "" {
		context = "repl"
	}

	seq := sess.nextSeq()

	req := &dap.EvaluateRequest{
		Request: newRequest(seq, "evaluate"),
		Arguments: dap.EvaluateArguments{
			Expression: expr,
			FrameId:    frameId,
			Context:    context,
		},
	}

	resp, err := sess.sendAndReceive(req)
	if err != nil {
		return errResp("evaluate: %v", err)
	}

	if evResp, ok := resp.(*dap.EvaluateResponse); ok {
		m := map[string]any{
			"result": evResp.Body.Result,
		}
		if evResp.Body.Type != "" {
			m["type"] = evResp.Body.Type
		}
		if evResp.Body.VariablesReference > 0 {
			m["ref"] = evResp.Body.VariablesReference
		}
		return dataResp(m)
	}
	return respToResult(resp)
}

func (h *Handler) doThreads(p paramMap) *protocol.Response {
	sess, err := h.getSession(p)
	if err != nil {
		return errResp("%v", err)
	}

	seq := sess.nextSeq()

	req := &dap.ThreadsRequest{
		Request: newRequest(seq, "threads"),
	}

	resp, err := sess.sendAndReceive(req)
	if err != nil {
		return errResp("threads: %v", err)
	}

	if tResp, ok := resp.(*dap.ThreadsResponse); ok {
		var threads []map[string]any
		for _, t := range tResp.Body.Threads {
			threads = append(threads, map[string]any{
				"id":   t.Id,
				"name": t.Name,
			})
		}
		return dataResp(threads)
	}
	return respToResult(resp)
}

func (h *Handler) doEvents(p paramMap) *protocol.Response {
	sess, err := h.getSession(p)
	if err != nil {
		return errResp("%v", err)
	}

	sess.eventsMu.Lock()
	events := make([]dap.Message, len(sess.events))
	copy(events, sess.events)
	if p["clear"] == "true" {
		sess.events = nil
	}
	sess.eventsMu.Unlock()

	var result []map[string]any
	for _, ev := range events {
		m := map[string]any{}
		switch e := ev.(type) {
		case *dap.StoppedEvent:
			m["event"] = "stopped"
			m["reason"] = e.Body.Reason
			m["threadId"] = e.Body.ThreadId
		case *dap.OutputEvent:
			m["event"] = "output"
			m["category"] = e.Body.Category
			m["output"] = e.Body.Output
		case *dap.TerminatedEvent:
			m["event"] = "terminated"
		case *dap.ExitedEvent:
			m["event"] = "exited"
			m["exitCode"] = e.Body.ExitCode
		case *dap.BreakpointEvent:
			m["event"] = "breakpoint"
			m["reason"] = e.Body.Reason
		case *dap.ThreadEvent:
			m["event"] = "thread"
			m["reason"] = e.Body.Reason
			m["threadId"] = e.Body.ThreadId
		default:
			data, _ := json.Marshal(ev)
			m["raw"] = string(data)
		}
		result = append(result, m)
	}
	return dataResp(result)
}

func (h *Handler) doRestart(p paramMap) *protocol.Response {
	sess, err := h.getSession(p)
	if err != nil {
		return errResp("%v", err)
	}

	launchParams := sess.launchParams
	if launchParams == nil {
		return errResp("session has no launch params (was it attached?)")
	}

	label := p["label"]
	if label == "" {
		label = "default"
	}

	// Disconnect the old session
	seq := sess.nextSeq()
	req := &dap.DisconnectRequest{
		Request:   newRequest(seq, "disconnect"),
		Arguments: &dap.DisconnectArguments{TerminateDebuggee: true},
	}
	sess.send(req)
	sess.close()

	h.mu.Lock()
	delete(h.sessions, label)
	h.mu.Unlock()

	// Relaunch with the same params
	return h.doLaunch(launchParams)
}

func (h *Handler) doDisconnect(p paramMap) *protocol.Response {
	sess, err := h.getSession(p)
	if err != nil {
		return errResp("%v", err)
	}

	label := p["label"]
	if label == "" {
		label = "default"
	}

	terminate := p["terminate"] != "false"

	seq := sess.nextSeq()

	req := &dap.DisconnectRequest{
		Request: newRequest(seq, "disconnect"),
		Arguments: &dap.DisconnectArguments{
			TerminateDebuggee: terminate,
		},
	}
	sess.send(req)
	sess.close()

	h.mu.Lock()
	delete(h.sessions, label)
	h.mu.Unlock()

	return okResp("Disconnected")
}

func (h *Handler) doList() *protocol.Response {
	h.mu.Lock()
	defer h.mu.Unlock()

	var items []map[string]any
	for label, sess := range h.sessions {
		item := map[string]any{"label": label}
		select {
		case <-sess.done:
			item["status"] = "closed"
		default:
			sess.eventsMu.Lock()
			state := sess.state
			sess.eventsMu.Unlock()
			if state != "" {
				item["status"] = state
			} else {
				item["status"] = "connected"
			}
		}
		items = append(items, item)
	}
	return dataResp(items)
}

func (h *Handler) doClose() *protocol.Response {
	h.mu.Lock()
	labels := make([]string, 0, len(h.sessions))
	for label := range h.sessions {
		labels = append(labels, label)
	}
	sessions := make([]*Session, len(labels))
	for i, l := range labels {
		sessions[i] = h.sessions[l]
		delete(h.sessions, l)
	}
	h.mu.Unlock()

	for _, sess := range sessions {
		sess.mu.Lock()
		sess.seq++
		seq := sess.seq
		sess.mu.Unlock()

		req := &dap.DisconnectRequest{
			Request:   newRequest(seq, "disconnect"),
			Arguments: &dap.DisconnectArguments{TerminateDebuggee: true},
		}
		sess.send(req)
		sess.close()
	}

	return dataResp([]any{})
}

// --- Session methods ---

func (h *Handler) newSession(label string, conn net.Conn, cmd *exec.Cmd) *Session {
	sess := &Session{
		label:     label,
		conn:      conn,
		cmd:       cmd,
		r:         bufio.NewReader(conn),
		done:      make(chan struct{}),
		responses: make(chan dap.Message, 16),
	}
	go sess.readLoop()
	return sess
}

func (h *Handler) getSession(p paramMap) (*Session, error) {
	label := p["label"]
	if label == "" {
		label = "default"
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if label == "default" && len(h.sessions) == 1 {
		for _, sess := range h.sessions {
			return sess, nil
		}
	}

	sess, ok := h.sessions[label]
	if !ok {
		if len(h.sessions) == 0 {
			return nil, fmt.Errorf("no debug sessions")
		}
		return nil, fmt.Errorf("no debug session %q", label)
	}
	return sess, nil
}

// initialize sends the DAP initialize request and stores capabilities.
// The full handshake is: initialize → launch/attach → initialized event → configurationDone.
func (s *Session) initialize() error {
	s.mu.Lock()
	s.seq++
	seq := s.seq
	s.mu.Unlock()

	req := &dap.InitializeRequest{
		Request: newRequest(seq, "initialize"),
		Arguments: dap.InitializeRequestArguments{
			ClientID:        "af",
			ClientName:      "agent-foo",
			AdapterID:       "af",
			LinesStartAt1:   true,
			ColumnsStartAt1: true,
			PathFormat:      "path",
		},
	}

	resp, err := s.sendAndReceive(req)
	if err != nil {
		return fmt.Errorf("initialize: %v", err)
	}

	if initResp, ok := resp.(*dap.InitializeResponse); ok {
		s.capabilities = &initResp.Body
	}
	return nil
}

// configurationDone waits for the initialized event then sends configurationDone.
func (s *Session) configurationDone() error {
	s.waitForEvent("initialized", 5*time.Second)

	s.mu.Lock()
	s.seq++
	seq := s.seq
	s.mu.Unlock()

	cdReq := &dap.ConfigurationDoneRequest{
		Request: newRequest(seq, "configurationDone"),
	}
	_, err := s.sendAndReceive(cdReq)
	return err
}

// buildLaunchRequest constructs the DAP launch request from params.
func (s *Session) buildLaunchRequest(p paramMap) *dap.LaunchRequest {
	s.mu.Lock()
	s.seq++
	seq := s.seq
	s.mu.Unlock()

	// Build launch arguments from params
	args := map[string]any{}
	if v := p["program"]; v != "" {
		args["program"] = v
	}
	if v := p["cwd"]; v != "" {
		args["cwd"] = v
	}
	if v := p["args"]; v != "" {
		var cmdArgs []string
		json.Unmarshal([]byte(v), &cmdArgs)
		args["args"] = cmdArgs
	}
	if p["stopOnEntry"] == "true" {
		args["stopOnEntry"] = true
	}
	// Pass through any additional arguments
	if v := p["launchArgs"]; v != "" {
		var extra map[string]any
		if json.Unmarshal([]byte(v), &extra) == nil {
			for k, v := range extra {
				args[k] = v
			}
		}
	}

	argsJSON, _ := json.Marshal(args)

	return &dap.LaunchRequest{
		Request:   newRequest(seq, "launch"),
		Arguments: argsJSON,
	}
}

// sendLaunch sends a launch request and waits for the response.
func (s *Session) sendLaunch(p paramMap) error {
	req := s.buildLaunchRequest(p)
	_, err := s.sendAndReceive(req)
	return err
}

// fireLaunch sends a launch request without waiting for the response.
func (s *Session) fireLaunch(p paramMap) error {
	req := s.buildLaunchRequest(p)
	return s.send(req)
}

func (s *Session) sendAttach(p paramMap) error {
	s.mu.Lock()
	s.seq++
	seq := s.seq
	s.mu.Unlock()

	args := map[string]any{}
	if v := p["pid"]; v != "" {
		pid, _ := strconv.Atoi(v)
		args["processId"] = pid
	}
	if v := p["attachArgs"]; v != "" {
		var extra map[string]any
		if json.Unmarshal([]byte(v), &extra) == nil {
			for k, v := range extra {
				args[k] = v
			}
		}
	}

	argsJSON, _ := json.Marshal(args)

	req := &dap.AttachRequest{
		Request:   newRequest(seq, "attach"),
		Arguments: argsJSON,
	}

	_, err := s.sendAndReceive(req)
	return err
}

// active returns the child session if one exists, otherwise self.
// This ensures DAP commands are routed to the actual debug session
// (not the parent orchestrator session used by dapDebugServer.js).
func (s *Session) active() *Session {
	if s.child != nil {
		return s.child
	}
	return s
}

// nextSeq increments and returns the next sequence number on the active session.
func (s *Session) nextSeq() int {
	a := s.active()
	a.mu.Lock()
	a.seq++
	seq := a.seq
	a.mu.Unlock()
	return seq
}

func (s *Session) send(msg dap.Message) error {
	return dap.WriteProtocolMessage(s.active().conn, msg)
}

func (s *Session) sendAndReceive(msg dap.Message) (dap.Message, error) {
	a := s.active()
	if err := dap.WriteProtocolMessage(a.conn, msg); err != nil {
		return nil, err
	}
	return a.receiveResponse()
}

// receiveResponse waits for a response from the read loop.
func (s *Session) receiveResponse() (dap.Message, error) {
	select {
	case msg, ok := <-s.responses:
		if !ok {
			return nil, fmt.Errorf("connection closed")
		}
		// Check for error response
		if errResp, ok := msg.(*dap.ErrorResponse); ok {
			errMsg := errResp.Message
			if errResp.Body.Error != nil {
				errMsg = errResp.Body.Error.Format
			}
			return nil, fmt.Errorf("%s", errMsg)
		}
		return msg, nil
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("timeout waiting for response")
	case <-s.done:
		return nil, fmt.Errorf("connection closed")
	}
}

// readLoop is the single goroutine that reads all messages from the adapter.
// Events are dispatched to the event store; responses go to the responses channel.
// Reverse requests (like startDebugging) are handled inline.
func (s *Session) readLoop() {
	defer close(s.done)
	defer close(s.responses)
	for {
		msg, err := dap.ReadProtocolMessage(s.r)
		if err != nil {
			// Skip unknown events/commands (e.g. debugpy's "debugpySockets")
			var fieldErr *dap.DecodeProtocolMessageFieldError
			if errors.As(err, &fieldErr) {
				continue
			}
			return
		}
		if ev, ok := msg.(dap.EventMessage); ok {
			s.handleEvent(ev)
		} else if req, ok := msg.(*dap.StartDebuggingRequest); ok {
			s.handleStartDebugging(req)
		} else {
			s.responses <- msg
		}
	}
}

// handleStartDebugging handles the reverse startDebugging request from the
// adapter. It creates a new TCP connection to the adapter, initializes a
// child Session, and stores it so subsequent commands route through it.
func (s *Session) handleStartDebugging(req *dap.StartDebuggingRequest) {
	go func() {
		if s.adapterAddr == "" {
			s.sendReverseResponse(req.Seq, "startDebugging", false, "no adapter address for child session")
			return
		}

		conn, err := net.Dial("tcp", s.adapterAddr)
		if err != nil {
			s.sendReverseResponse(req.Seq, "startDebugging", false, fmt.Sprintf("connect child: %v", err))
			return
		}

		child := &Session{
			label:     s.label + "/child",
			conn:      conn,
			r:         bufio.NewReader(conn),
			done:      make(chan struct{}),
			responses: make(chan dap.Message, 16),
			parent:    s,
		}
		go child.readLoop()

		// Initialize child
		if err := child.initialize(); err != nil {
			child.close()
			s.sendReverseResponse(req.Seq, "startDebugging", false, fmt.Sprintf("init child: %v", err))
			return
		}

		// Send launch/attach with the configuration from startDebugging
		launchArgs, _ := json.Marshal(req.Arguments.Configuration)

		child.mu.Lock()
		child.seq++
		seq := child.seq
		child.mu.Unlock()

		if req.Arguments.Request == "attach" {
			child.send(&dap.AttachRequest{
				Request:   newRequest(seq, "attach"),
				Arguments: launchArgs,
			})
		} else {
			child.send(&dap.LaunchRequest{
				Request:   newRequest(seq, "launch"),
				Arguments: launchArgs,
			})
		}

		// configurationDone on child (waits for initialized event, then sends)
		if err := child.configurationDone(); err != nil {
			child.close()
			s.sendReverseResponse(req.Seq, "startDebugging", false, fmt.Sprintf("child configDone: %v", err))
			return
		}

		// Read the launch/attach response
		if _, err := child.receiveResponse(); err != nil {
			child.close()
			s.sendReverseResponse(req.Seq, "startDebugging", false, fmt.Sprintf("child launch: %v", err))
			return
		}

		// Store child — subsequent commands route through it
		s.child = child
		if s.childReady != nil {
			close(s.childReady)
		}

		s.sendReverseResponse(req.Seq, "startDebugging", true, "")
	}()
}

func (s *Session) sendReverseResponse(reqSeq int, command string, success bool, errMsg string) {
	s.mu.Lock()
	s.seq++
	seq := s.seq
	s.mu.Unlock()

	resp := &dap.StartDebuggingResponse{
		Response: dap.Response{
			ProtocolMessage: dap.ProtocolMessage{Seq: seq, Type: "response"},
			Command:         command,
			RequestSeq:      reqSeq,
			Success:         success,
			Message:         errMsg,
		},
	}
	s.send(resp)
}

func (s *Session) handleEvent(ev dap.EventMessage) {
	// Forward events to parent session if this is a child
	target := s
	if s.parent != nil {
		target = s.parent
	}
	target.eventsMu.Lock()
	target.events = append(target.events, ev.(dap.Message))
	switch e := ev.(type) {
	case *dap.StoppedEvent:
		target.state = "stopped"
		target.stoppedThreadId = e.Body.ThreadId
	case *dap.ContinuedEvent:
		target.state = "running"
	case *dap.TerminatedEvent, *dap.ExitedEvent:
		target.state = "exited"
	}
	target.eventsMu.Unlock()
}

// fetchStackTrace fetches the top N frames for a thread. Returns nil on error.
func (s *Session) fetchStackTrace(threadId, levels int) []map[string]any {
	s.mu.Lock()
	s.seq++
	seq := s.seq
	s.mu.Unlock()

	req := &dap.StackTraceRequest{
		Request: newRequest(seq, "stackTrace"),
		Arguments: dap.StackTraceArguments{
			ThreadId: threadId,
			Levels:   levels,
		},
	}

	resp, err := s.sendAndReceive(req)
	if err != nil {
		return nil
	}

	stResp, ok := resp.(*dap.StackTraceResponse)
	if !ok {
		return nil
	}

	var frames []map[string]any
	for _, f := range stResp.Body.StackFrames {
		m := map[string]any{
			"id":   f.Id,
			"name": f.Name,
			"line": f.Line,
		}
		if f.Source != nil {
			m["file"] = f.Source.Path
		}
		frames = append(frames, m)
	}
	return frames
}

func (s *Session) waitForStopped(startIdx int, timeout time.Duration) *dap.StoppedEvent {
	deadline := time.Now().Add(timeout)
	seen := startIdx
	for time.Now().Before(deadline) {
		s.eventsMu.Lock()
		for i := seen; i < len(s.events); i++ {
			if stopped, ok := s.events[i].(*dap.StoppedEvent); ok {
				s.eventsMu.Unlock()
				return stopped
			}
			// Program ended — no point waiting for a stop
			switch s.events[i].(type) {
			case *dap.TerminatedEvent, *dap.ExitedEvent:
				s.eventsMu.Unlock()
				return nil
			}
		}
		seen = len(s.events)
		s.eventsMu.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
	return nil
}

func (s *Session) waitForEvent(eventName string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	seen := 0
	for time.Now().Before(deadline) {
		s.eventsMu.Lock()
		for i := seen; i < len(s.events); i++ {
			if e, ok := s.events[i].(dap.EventMessage); ok && e.GetEvent().Event == eventName {
				s.eventsMu.Unlock()
				return
			}
		}
		seen = len(s.events)
		s.eventsMu.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
}

func (s *Session) close() {
	s.conn.Close()
	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
		s.cmd.Wait()
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

// mergeDefaultLaunchArgs injects default launch args into the launchArgs param.
// User-supplied keys take priority over defaults.
func mergeDefaultLaunchArgs(p paramMap, defaults map[string]any) {
	existing := map[string]any{}
	if v := p["launchArgs"]; v != "" {
		json.Unmarshal([]byte(v), &existing)
	}
	for k, v := range defaults {
		if _, ok := existing[k]; !ok {
			existing[k] = v
		}
	}
	data, _ := json.Marshal(existing)
	p["launchArgs"] = string(data)
}

func newRequest(seq int, command string) dap.Request {
	return dap.Request{
		ProtocolMessage: dap.ProtocolMessage{Seq: seq, Type: "request"},
		Command:         command,
	}
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

func respToResult(msg dap.Message) *protocol.Response {
	if r, ok := msg.(dap.ResponseMessage); ok {
		resp := r.GetResponse()
		if !resp.Success {
			return errResp("%s", resp.Message)
		}
	}
	return okResp("OK")
}
