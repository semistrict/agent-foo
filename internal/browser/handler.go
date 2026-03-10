package browser

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/semistrict/agent-foo/internal/protocol"
)

//go:embed js/refs_overlay.js
var refsOverlayJS string

// Handler manages a long-lived browser and processes commands.
type Handler struct {
	browser *Instance
	opts    LaunchOptions
	timeout time.Duration
	debug   *debugState
}

func NewHandler() *Handler {
	return &Handler{}
}

func (h *Handler) ensureBrowser() error {
	if h.browser != nil {
		return nil
	}
	b, err := Launch(h.opts)
	if err != nil {
		return err
	}
	h.browser = b
	return nil
}

func (h *Handler) HandleRequest(req *protocol.Request) *protocol.Response {
	if req.Action == "close" {
		if h.browser != nil {
			h.browser.Close()
			h.browser = nil
		}
		return &protocol.Response{Success: true, Data: jsonStr("Browser closed")}
	}

	p := params(req.Params)
	// Update launch options from request params (before first launch)
	if h.browser == nil {
		if p.get("__headed") == "true" {
			h.opts.Headed = true
		}
		if v := p.get("__cdp"); v != "" {
			h.opts.CDPUrl = v
		}
		if v := p.get("__profile"); v != "" {
			h.opts.Profile = v
		}
	}

	if err := h.ensureBrowser(); err != nil {
		return errResp("launch browser: %v", err)
	}

	h.timeout = defaultTimeout
	if t := p.get("__timeout"); t != "" {
		if secs, err := strconv.Atoi(t); err == nil && secs > 0 {
			h.timeout = time.Duration(secs) * time.Second
		}
	}

	return h.dispatch(req)
}

func (h *Handler) dispatch(req *protocol.Request) *protocol.Response {
	p := params(req.Params)

	switch req.Action {
	case "open":
		return h.doOpen(p)
	case "click":
		return h.doClick(p)
	case "type":
		return h.doType(p)
	case "fill":
		return h.doFill(p)
	case "press":
		return h.doPress(p)
	case "screenshot":
		return h.doScreenshot(p)
	case "pdf":
		return h.doPDF(p)
	case "snapshot":
		return h.doSnapshot(p)
	case "eval":
		return h.doEval(p)
	case "wait":
		return h.doWait(p)
	case "back":
		return h.doNav(chromedp.NavigateBack())
	case "forward":
		return h.doNav(chromedp.NavigateForward())
	case "reload":
		return h.doNav(chromedp.Reload())
	case "upload":
		return h.doUpload(p)
	case "set":
		return h.doSet(p)
	case "events":
		return h.doEvents(p)
	case "debug":
		return h.doDebug(p)
	case "render":
		return h.doRender(p)
	case "text":
		return h.doText(p)
	case "tabs":
		return h.doTabs(p)
	case "tab":
		return h.doTab(p)
	case "close-tab":
		return h.doCloseTab(p)
	default:
		return errResp("unknown action: %s", req.Action)
	}
}

// --- helpers ---

const defaultTimeout = 5 * time.Second

type paramMap map[string]string

func params(raw json.RawMessage) paramMap {
	m := make(paramMap)
	if raw != nil {
		json.Unmarshal(raw, &m)
	}
	return m
}

func (p paramMap) get(key string) string { return p[key] }
func (p paramMap) sel(key string) string { return SelectorOrRef(p[key]) }

func (h *Handler) run(actions ...chromedp.Action) *protocol.Response {
	ctx, cancel := context.WithTimeout(h.browser.Ctx, h.timeout)
	defer cancel()
	if err := chromedp.Run(ctx, actions...); err != nil {
		if ctx.Err() != nil {
			return errResp("timed out after %s waiting for element", h.timeout)
		}
		return errResp("%v", err)
	}
	return okResp("Done")
}

// runAndCheck runs actions, then waits briefly and reports any navigation.
func (h *Handler) runAndCheck(actions ...chromedp.Action) *protocol.Response {
	// Capture URL before
	var beforeURL string
	chromedp.Run(h.browser.Ctx, chromedp.Location(&beforeURL))

	ctx, cancel := context.WithTimeout(h.browser.Ctx, h.timeout)
	defer cancel()
	if err := chromedp.Run(ctx, actions...); err != nil {
		if ctx.Err() != nil {
			return errResp("timed out after %s waiting for element", h.timeout)
		}
		return errResp("%v", err)
	}

	// Wait up to 1.5s for navigation to settle
	var afterURL, afterTitle string
	for i := 0; i < 6; i++ {
		time.Sleep(250 * time.Millisecond)
		chromedp.Run(h.browser.Ctx, chromedp.Location(&afterURL))
		if afterURL != beforeURL {
			chromedp.Run(h.browser.Ctx, chromedp.Title(&afterTitle))
			return dataResp(map[string]string{
				"result": "Done",
				"navigated": afterURL,
				"title":  afterTitle,
			})
		}
	}

	return okResp("Done")
}

// doNav runs a navigation action without timeout (navigations wait for page load).
func (h *Handler) doNav(action chromedp.Action) *protocol.Response {
	if err := h.browser.Run(action); err != nil {
		return errResp("%v", err)
	}
	return okResp("Done")
}

func errResp(format string, args ...any) *protocol.Response {
	return &protocol.Response{Success: false, Error: fmt.Sprintf(format, args...)}
}

func okResp(msg string) *protocol.Response {
	return &protocol.Response{Success: true, Data: jsonStr(msg)}
}

func dataResp(v any) *protocol.Response {
	data, _ := json.Marshal(v)
	return &protocol.Response{Success: true, Data: data}
}

func jsonStr(s string) json.RawMessage {
	data, _ := json.Marshal(s)
	return data
}

// injectREF installs a global _REF proxy that resolves data-ref attributes.
const refBootstrap = `if(!window._REF){window._REF=new Proxy({},{get:(_,k)=>document.querySelector('[data-ref="'+k+'"]')})}`

// --- command implementations ---

func (h *Handler) doOpen(p paramMap) *protocol.Response {
	url := p.get("url")
	if !strings.Contains(url, "://") {
		url = "https://" + url
	}

	// Create a new tab as a sibling (from root browser context, not current tab)
	tabCtx, tabCancel := chromedp.NewContext(h.browser.BrowserCtx)

	// Navigate in the new tab (this also initializes the target)
	if err := chromedp.Run(tabCtx, chromedp.Navigate(url)); err != nil {
		tabCancel()
		return errResp("%v", err)
	}

	var title string
	chromedp.Run(tabCtx, chromedp.Title(&title))

	// Listen for events on the new tab's target
	StartListening(tabCtx, h.browser.Events)

	// Track the tab and make it active
	tab := &Tab{Ctx: tabCtx, Cancel: tabCancel, URL: url, Title: title}
	h.browser.Tabs = append(h.browser.Tabs, tab)
	h.browser.ActiveTab = len(h.browser.Tabs) - 1
	h.browser.Ctx = tabCtx

	return dataResp(map[string]string{"url": url, "title": title})
}

func (h *Handler) doTabs(p paramMap) *protocol.Response {
	// Refresh titles/URLs for all tabs
	var tabs []map[string]any
	for i, tab := range h.browser.Tabs {
		var loc, title string
		chromedp.Run(tab.Ctx, chromedp.Location(&loc))
		chromedp.Run(tab.Ctx, chromedp.Title(&title))
		tab.URL = loc
		tab.Title = title
		m := map[string]any{
			"index":  i,
			"url":    loc,
			"title":  title,
			"active": i == h.browser.ActiveTab,
		}
		tabs = append(tabs, m)
	}
	return dataResp(tabs)
}

func (h *Handler) doTab(p paramMap) *protocol.Response {
	idxStr := p.get("index")
	if idxStr == "" {
		return errResp("index is required")
	}
	idx, err := strconv.Atoi(idxStr)
	if err != nil || idx < 0 || idx >= len(h.browser.Tabs) {
		return errResp("invalid tab index: %s (have %d tabs)", idxStr, len(h.browser.Tabs))
	}
	h.browser.ActiveTab = idx
	h.browser.Ctx = h.browser.Tabs[idx].Ctx

	tab := h.browser.Tabs[idx]
	return dataResp(map[string]string{"url": tab.URL, "title": tab.Title})
}

// parseIndexes parses a string like "0,2-5,7" into a sorted, deduplicated slice of ints.
func parseIndexes(s string, max int) ([]int, error) {
	seen := make(map[int]bool)
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if strings.Contains(part, "-") {
			bounds := strings.SplitN(part, "-", 2)
			lo, err := strconv.Atoi(strings.TrimSpace(bounds[0]))
			if err != nil {
				return nil, fmt.Errorf("invalid index: %s", bounds[0])
			}
			hi, err := strconv.Atoi(strings.TrimSpace(bounds[1]))
			if err != nil {
				return nil, fmt.Errorf("invalid index: %s", bounds[1])
			}
			if lo > hi {
				lo, hi = hi, lo
			}
			for i := lo; i <= hi; i++ {
				if i < 0 || i >= max {
					return nil, fmt.Errorf("tab index out of range: %d (have %d tabs)", i, max)
				}
				seen[i] = true
			}
		} else {
			idx, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("invalid index: %s", part)
			}
			if idx < 0 || idx >= max {
				return nil, fmt.Errorf("tab index out of range: %d (have %d tabs)", idx, max)
			}
			seen[idx] = true
		}
	}
	// Sort descending so we can remove from the end first
	idxs := make([]int, 0, len(seen))
	for i := range seen {
		idxs = append(idxs, i)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(idxs)))
	return idxs, nil
}

func (h *Handler) doCloseTab(p paramMap) *protocol.Response {
	spec := p.get("indexes")
	if spec == "" {
		// Fall back to single "index" param
		spec = p.get("index")
	}
	if spec == "" {
		return errResp("indexes is required")
	}

	idxs, err := parseIndexes(spec, len(h.browser.Tabs))
	if err != nil {
		return errResp("%v", err)
	}
	if len(idxs) >= len(h.browser.Tabs) {
		return errResp("cannot close all tabs")
	}

	var closed []map[string]string
	// Indexes are sorted descending, so removing from the end keeps earlier indexes stable
	for _, idx := range idxs {
		tab := h.browser.Tabs[idx]
		closed = append(closed, map[string]string{
			"index": fmt.Sprintf("%d", idx),
			"title": tab.Title,
			"url":   tab.URL,
		})
		tab.Cancel()
		h.browser.Tabs = append(h.browser.Tabs[:idx], h.browser.Tabs[idx+1:]...)
	}

	// Adjust active tab
	if h.browser.ActiveTab >= len(h.browser.Tabs) {
		h.browser.ActiveTab = len(h.browser.Tabs) - 1
	}
	h.browser.Ctx = h.browser.Tabs[h.browser.ActiveTab].Ctx

	return dataResp(closed)
}

func (h *Handler) doClick(p paramMap) *protocol.Response {
	return h.runAndCheck(chromedp.Click(p.sel("selector"), chromedp.ByQuery))
}

func (h *Handler) doType(p paramMap) *protocol.Response {
	return h.runAndCheck(chromedp.SendKeys(p.sel("selector"), p.get("text"), chromedp.ByQuery))
}

func (h *Handler) doFill(p paramMap) *protocol.Response {
	sel := p.sel("selector")
	return h.runAndCheck(
		chromedp.Clear(sel, chromedp.ByQuery),
		chromedp.SendKeys(sel, p.get("text"), chromedp.ByQuery),
	)
}

func (h *Handler) doPress(p paramMap) *protocol.Response {
	return h.runAndCheck(parseBrowserKey(p.get("key")))
}

func (h *Handler) doScreenshot(p paramMap) *protocol.Response {
	path := p.get("path")
	if path == "" {
		path = "screenshot.png"
	}
	full := p.get("full") == "true"
	refs := p.get("refs") == "true"

	// If refs requested, run snapshot first to assign data-ref attributes, then add overlay
	if refs {
		_, err := Snapshot(h.browser.Ctx, SnapshotOptions{})
		if err != nil {
			return errResp("snapshot for refs: %v", err)
		}
		if err := h.browser.Run(chromedp.Evaluate(refsOverlayJS, nil)); err != nil {
			return errResp("inject refs overlay: %v", err)
		}
	}

	var buf []byte
	var err error
	if full {
		err = h.browser.Run(chromedp.FullScreenshot(&buf, 90))
	} else {
		err = h.browser.Run(chromedp.CaptureScreenshot(&buf))
	}

	// Remove overlay after screenshot
	if refs {
		h.browser.Run(chromedp.Evaluate(`(() => { const el = document.getElementById('__af_refs_overlay__'); if (el) el.remove(); })()`, nil))
	}

	if err != nil {
		return errResp("%v", err)
	}
	if err := os.WriteFile(path, buf, 0644); err != nil {
		return errResp("write file: %v", err)
	}
	return dataResp(map[string]any{"path": path, "bytes": len(buf)})
}

func (h *Handler) doPDF(p paramMap) *protocol.Response {
	path := p.get("path")
	var buf []byte
	err := h.browser.Run(chromedp.ActionFunc(func(ctx context.Context) error {
		result, _, err := page.PrintToPDF().Do(ctx)
		if err != nil {
			return err
		}
		buf = result
		return nil
	}))
	if err != nil {
		return errResp("%v", err)
	}
	if err := os.WriteFile(path, buf, 0644); err != nil {
		return errResp("write file: %v", err)
	}
	return dataResp(map[string]any{"path": path, "bytes": len(buf)})
}

func (h *Handler) doSnapshot(p paramMap) *protocol.Response {
	opts := SnapshotOptions{
		InteractiveOnly: p.get("interactive") == "true",
		Compact:         p.get("compact") == "true",
	}
	if d := p.get("depth"); d != "" {
		opts.Depth, _ = strconv.Atoi(d)
	}

	if p.get("allTabs") == "true" && len(h.browser.Tabs) > 0 {
		var results []map[string]any
		for i, tab := range h.browser.Tabs {
			var loc, title string
			chromedp.Run(tab.Ctx, chromedp.Location(&loc))
			chromedp.Run(tab.Ctx, chromedp.Title(&title))
			tree, err := Snapshot(tab.Ctx, opts)
			if err != nil {
				results = append(results, map[string]any{
					"tab": i, "url": loc, "title": title, "error": err.Error(),
				})
				continue
			}
			results = append(results, map[string]any{
				"tab": i, "url": loc, "title": title, "snapshot": tree,
			})
		}
		return dataResp(results)
	}

	tree, err := Snapshot(h.browser.Ctx, opts)
	if err != nil {
		return errResp("%v", err)
	}
	return dataResp(tree)
}

func (h *Handler) doEval(p paramMap) *protocol.Response {
	// Ensure _REF proxy is available
	h.browser.Run(chromedp.Evaluate(refBootstrap, nil))

	var result *runtime.RemoteObject
	if err := h.browser.Run(chromedp.Evaluate(p.get("js"), &result)); err != nil {
		return errResp("%v", err)
	}
	if result != nil && result.Value != nil {
		return &protocol.Response{Success: true, Data: json.RawMessage(result.Value)}
	}
	return okResp("")
}

func (h *Handler) doWait(p paramMap) *protocol.Response {
	sel := SelectorOrRef(p.get("target"))
	return h.run(chromedp.WaitVisible(sel, chromedp.ByQuery))
}

func (h *Handler) doUpload(p paramMap) *protocol.Response {
	sel := p.sel("selector")
	files := strings.Split(p.get("files"), ",")
	return h.run(chromedp.SetUploadFiles(sel, files, chromedp.ByQuery))
}

func (h *Handler) doSet(p paramMap) *protocol.Response {
	setting := p.get("setting")
	switch setting {
	case "viewport":
		w, _ := strconv.ParseInt(p.get("width"), 10, 64)
		h2, _ := strconv.ParseInt(p.get("height"), 10, 64)
		return h.run(chromedp.EmulateViewport(w, h2))
	case "media":
		return h.run(chromedp.ActionFunc(func(ctx context.Context) error {
			features := []*emulation.MediaFeature{}
			if v := p.get("colorScheme"); v != "" {
				features = append(features, &emulation.MediaFeature{Name: "prefers-color-scheme", Value: v})
			}
			if p.get("reducedMotion") == "true" {
				features = append(features, &emulation.MediaFeature{Name: "prefers-reduced-motion", Value: "reduce"})
			}
			return emulation.SetEmulatedMedia().WithFeatures(features).Do(ctx)
		}))
	default:
		return errResp("unknown setting: %s", setting)
	}
}

func (h *Handler) doEvents(p paramMap) *protocol.Response {
	action := p.get("action")
	if action == "clear" {
		h.browser.Events.Clear()
		return okResp("Events cleared")
	}
	if action == "stats" {
		return dataResp(h.browser.Events.Stats())
	}

	// Default: query
	f := EventFilter{}
	if v := p.get("category"); v != "" {
		f.Category = v
	}
	if v := p.get("type"); v != "" {
		f.Type = v
	}
	if v := p.get("last"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Limit = n
		}
	}
	if v := p.get("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.Since = t
		}
	}

	events := h.browser.Events.Query(f)
	return dataResp(events)
}

