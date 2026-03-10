package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/debugger"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/semistrict/agent-foo/internal/protocol"
)

// debugState tracks JS debugger state across pause/resume cycles.
type debugState struct {
	mu         sync.Mutex
	enabled    bool
	callFrames []*debugger.CallFrame
	reason     debugger.PausedReason
	paused     chan struct{} // closed when a pause event arrives
}

func (h *Handler) ensureDebugger() error {
	if h.debug == nil {
		h.debug = &debugState{}
	}
	if h.debug.enabled {
		return nil
	}

	h.debug.paused = make(chan struct{})

	// Enable the debugger domain and listen for pause events
	err := h.browser.Run(chromedp.ActionFunc(func(ctx context.Context) error {
		_, err := debugger.Enable().Do(ctx)
		if err != nil {
			return err
		}

		chromedp.ListenTarget(ctx, func(ev interface{}) {
			switch e := ev.(type) {
			case *debugger.EventPaused:
				h.debug.mu.Lock()
				h.debug.callFrames = e.CallFrames
				h.debug.reason = e.Reason
				// Signal waiters
				select {
				case <-h.debug.paused:
					// already closed, make a new one for next wait
				default:
				}
				close(h.debug.paused)
				h.debug.paused = make(chan struct{})
				h.debug.mu.Unlock()
			case *debugger.EventResumed:
				h.debug.mu.Lock()
				h.debug.callFrames = nil
				h.debug.mu.Unlock()
			}
		})
		return nil
	}))
	if err != nil {
		return err
	}
	h.debug.enabled = true
	return nil
}

func (h *Handler) doDebug(p paramMap) *protocol.Response {
	sub := p.get("sub")

	switch sub {
	case "breakpoint":
		return h.doDebugBreakpoint(p)
	case "remove":
		return h.doDebugRemoveBreakpoint(p)
	case "continue":
		return h.doDebugResume(p, false)
	case "next":
		return h.doDebugStep(p, "next")
	case "stepin":
		return h.doDebugStep(p, "stepin")
	case "stepout":
		return h.doDebugStep(p, "stepout")
	case "pause":
		return h.doDebugPause(p)
	case "stacktrace":
		return h.doDebugStackTrace(p)
	case "scopes":
		return h.doDebugScopes(p)
	case "eval":
		return h.doDebugEval(p)
	case "exceptions":
		return h.doDebugExceptions(p)
	default:
		return errResp("unknown debug sub-action: %s", sub)
	}
}

func (h *Handler) doDebugBreakpoint(p paramMap) *protocol.Response {
	if err := h.ensureDebugger(); err != nil {
		return errResp("enable debugger: %v", err)
	}

	lineStr := p.get("line")
	if lineStr == "" {
		return errResp("line is required")
	}
	line, err := strconv.ParseInt(lineStr, 10, 64)
	if err != nil {
		return errResp("invalid line: %s", lineStr)
	}
	// CDP uses 0-based lines
	line--

	var bpID debugger.BreakpointID
	var locations []*debugger.Location

	err = h.browser.Run(chromedp.ActionFunc(func(ctx context.Context) error {
		cmd := debugger.SetBreakpointByURL(line)
		if url := p.get("url"); url != "" {
			cmd = cmd.WithURLRegex(regexEscape(url))
		}
		if cond := p.get("condition"); cond != "" {
			cmd = cmd.WithCondition(cond)
		}
		bpID, locations, err = cmd.Do(ctx)
		return err
	}))
	if err != nil {
		return errResp("set breakpoint: %v", err)
	}

	result := map[string]any{
		"id":        string(bpID),
		"locations": len(locations),
	}
	if len(locations) > 0 {
		loc := locations[0]
		result["line"] = loc.LineNumber + 1 // back to 1-based
		result["scriptId"] = string(loc.ScriptID)
	}
	return dataResp(result)
}

func (h *Handler) doDebugRemoveBreakpoint(p paramMap) *protocol.Response {
	if err := h.ensureDebugger(); err != nil {
		return errResp("enable debugger: %v", err)
	}

	bpID := p.get("id")
	if bpID == "" {
		return errResp("id (breakpoint ID) is required")
	}

	err := h.browser.Run(chromedp.ActionFunc(func(ctx context.Context) error {
		return debugger.RemoveBreakpoint(debugger.BreakpointID(bpID)).Do(ctx)
	}))
	if err != nil {
		return errResp("remove breakpoint: %v", err)
	}
	return okResp("Removed")
}

func (h *Handler) doDebugResume(p paramMap, waitForPause bool) *protocol.Response {
	if err := h.ensureDebugger(); err != nil {
		return errResp("enable debugger: %v", err)
	}

	// Grab the paused channel before resuming
	h.debug.mu.Lock()
	pauseCh := h.debug.paused
	h.debug.mu.Unlock()

	err := h.browser.Run(chromedp.ActionFunc(func(ctx context.Context) error {
		return debugger.Resume().Do(ctx)
	}))
	if err != nil {
		return errResp("resume: %v", err)
	}

	// Wait for next pause event (breakpoint hit, etc.)
	select {
	case <-pauseCh:
	case <-time.After(10 * time.Second):
		return okResp("Running (no breakpoint hit)")
	}

	return h.pausedResult()
}

func (h *Handler) doDebugStep(p paramMap, kind string) *protocol.Response {
	if err := h.ensureDebugger(); err != nil {
		return errResp("enable debugger: %v", err)
	}

	h.debug.mu.Lock()
	pauseCh := h.debug.paused
	h.debug.mu.Unlock()

	err := h.browser.Run(chromedp.ActionFunc(func(ctx context.Context) error {
		switch kind {
		case "next":
			return debugger.StepOver().Do(ctx)
		case "stepin":
			return debugger.StepInto().Do(ctx)
		case "stepout":
			return debugger.StepOut().Do(ctx)
		}
		return nil
	}))
	if err != nil {
		return errResp("step: %v", err)
	}

	select {
	case <-pauseCh:
	case <-time.After(10 * time.Second):
		return okResp("Running")
	}

	return h.pausedResult()
}

func (h *Handler) doDebugPause(p paramMap) *protocol.Response {
	if err := h.ensureDebugger(); err != nil {
		return errResp("enable debugger: %v", err)
	}

	h.debug.mu.Lock()
	pauseCh := h.debug.paused
	h.debug.mu.Unlock()

	err := h.browser.Run(chromedp.ActionFunc(func(ctx context.Context) error {
		return debugger.Pause().Do(ctx)
	}))
	if err != nil {
		return errResp("pause: %v", err)
	}

	select {
	case <-pauseCh:
	case <-time.After(5 * time.Second):
		return errResp("pause: timeout waiting for paused event")
	}

	return h.pausedResult()
}

func (h *Handler) doDebugStackTrace(p paramMap) *protocol.Response {
	if h.debug == nil {
		return errResp("debugger not enabled")
	}

	h.debug.mu.Lock()
	frames := h.debug.callFrames
	h.debug.mu.Unlock()

	if frames == nil {
		return errResp("not paused")
	}

	return dataResp(formatCallFrames(frames))
}

func (h *Handler) doDebugScopes(p paramMap) *protocol.Response {
	if h.debug == nil {
		return errResp("debugger not enabled")
	}

	h.debug.mu.Lock()
	frames := h.debug.callFrames
	h.debug.mu.Unlock()

	if frames == nil {
		return errResp("not paused")
	}

	frameIdx, _ := strconv.Atoi(p.get("frame"))
	if frameIdx >= len(frames) {
		return errResp("frame %d out of range (have %d)", frameIdx, len(frames))
	}

	frame := frames[frameIdx]
	var result []map[string]any

	for _, scope := range frame.ScopeChain {
		s := map[string]any{
			"type":     string(scope.Type),
			"objectId": string(scope.Object.ObjectID),
		}
		if scope.Name != "" {
			s["name"] = scope.Name
		}

		// Fetch properties for non-global scopes
		if scope.Type != debugger.ScopeTypeGlobal {
			var vars []map[string]any
			h.browser.Run(chromedp.ActionFunc(func(ctx context.Context) error {
				props, _, _, _, err := cdpruntime.GetProperties(scope.Object.ObjectID).Do(ctx)
				if err != nil {
					return err
				}
				for _, prop := range props {
					v := map[string]any{"name": prop.Name}
					if prop.Value != nil {
						v["value"] = formatRemoteObject(prop.Value)
						if prop.Value.Type != "" {
							v["type"] = string(prop.Value.Type)
						}
					}
					vars = append(vars, v)
				}
				return nil
			}))
			s["variables"] = vars
		}

		result = append(result, s)
	}

	return dataResp(result)
}

func (h *Handler) doDebugEval(p paramMap) *protocol.Response {
	if h.debug == nil {
		return errResp("debugger not enabled")
	}

	expr := p.get("expr")
	if expr == "" {
		return errResp("expr is required")
	}

	h.debug.mu.Lock()
	frames := h.debug.callFrames
	h.debug.mu.Unlock()

	if frames == nil {
		return errResp("not paused")
	}

	frameIdx, _ := strconv.Atoi(p.get("frame"))
	if frameIdx >= len(frames) {
		return errResp("frame %d out of range", frameIdx)
	}

	var result *cdpruntime.RemoteObject
	var exceptionDetails *cdpruntime.ExceptionDetails

	err := h.browser.Run(chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		result, exceptionDetails, err = debugger.EvaluateOnCallFrame(frames[frameIdx].CallFrameID, expr).Do(ctx)
		return err
	}))
	if err != nil {
		return errResp("eval: %v", err)
	}

	if exceptionDetails != nil {
		msg := exceptionDetails.Text
		if exceptionDetails.Exception != nil {
			msg = formatRemoteObject(exceptionDetails.Exception)
		}
		return errResp("eval error: %s", msg)
	}

	m := map[string]any{
		"result": formatRemoteObject(result),
		"type":   string(result.Type),
	}
	if result.ObjectID != "" {
		m["objectId"] = string(result.ObjectID)
	}
	return dataResp(m)
}

func (h *Handler) doDebugExceptions(p paramMap) *protocol.Response {
	if err := h.ensureDebugger(); err != nil {
		return errResp("enable debugger: %v", err)
	}

	state := p.get("state")
	if state == "" {
		state = "uncaught"
	}

	var es debugger.ExceptionsState
	switch state {
	case "none":
		es = debugger.ExceptionsStateNone
	case "uncaught":
		es = debugger.ExceptionsStateUncaught
	case "all":
		es = debugger.ExceptionsStateAll
	default:
		return errResp("state must be none, uncaught, or all")
	}

	err := h.browser.Run(chromedp.ActionFunc(func(ctx context.Context) error {
		return debugger.SetPauseOnExceptions(es).Do(ctx)
	}))
	if err != nil {
		return errResp("set exceptions: %v", err)
	}
	return okResp(fmt.Sprintf("Pause on exceptions: %s", state))
}

// --- helpers ---

func (h *Handler) pausedResult() *protocol.Response {
	h.debug.mu.Lock()
	frames := h.debug.callFrames
	reason := h.debug.reason
	h.debug.mu.Unlock()

	result := map[string]any{
		"status": "paused",
		"reason": string(reason),
	}
	if len(frames) > 0 {
		// Include top 5 frames
		limit := 5
		if len(frames) < limit {
			limit = len(frames)
		}
		result["stackTrace"] = formatCallFrames(frames[:limit])
	}
	return dataResp(result)
}

func formatCallFrames(frames []*debugger.CallFrame) []map[string]any {
	var result []map[string]any
	for _, f := range frames {
		m := map[string]any{
			"id":       string(f.CallFrameID),
			"function": f.FunctionName,
		}
		if f.Location != nil {
			m["line"] = f.Location.LineNumber + 1 // 1-based
			m["column"] = f.Location.ColumnNumber + 1
			m["scriptId"] = string(f.Location.ScriptID)
		}
		if f.FunctionName == "" {
			m["function"] = "(anonymous)"
		}
		result = append(result, m)
	}
	return result
}

func formatRemoteObject(obj *cdpruntime.RemoteObject) string {
	if obj == nil {
		return "undefined"
	}
	if obj.Value != nil {
		var v any
		if json.Unmarshal(obj.Value, &v) == nil {
			switch val := v.(type) {
			case string:
				return val
			default:
				return fmt.Sprintf("%v", val)
			}
		}
	}
	if obj.Description != "" {
		return obj.Description
	}
	return string(obj.Type)
}

func regexEscape(s string) string {
	special := `\.+*?^${}()|[]`
	var b strings.Builder
	for _, c := range s {
		if strings.ContainsRune(special, c) {
			b.WriteByte('\\')
		}
		b.WriteRune(c)
	}
	return b.String()
}
