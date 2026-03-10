package browser

import (
	_ "embed"
	"encoding/json"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/semistrict/agent-foo/internal/protocol"
)

//go:embed js/vendor/turndown.js
var turndownJS string

//go:embed js/vendor/turndown-plugin-gfm.js
var turndownGfmJS string

//go:embed js/text.js
var textJS string

func (h *Handler) doText(p paramMap) *protocol.Response {
	// Inject Turndown library
	if err := h.browser.Run(chromedp.Evaluate(turndownJS, nil)); err != nil {
		return errResp("inject turndown: %v", err)
	}
	// Inject GFM plugin
	if err := h.browser.Run(chromedp.Evaluate(turndownGfmJS, nil)); err != nil {
		return errResp("inject turndown-gfm: %v", err)
	}

	// Run conversion
	var result *runtime.RemoteObject
	if err := h.browser.Run(chromedp.Evaluate(textJS, &result)); err != nil {
		return errResp("text eval: %v", err)
	}
	if result == nil || result.Value == nil {
		return errResp("text: no data returned")
	}

	return &protocol.Response{Success: true, Data: json.RawMessage(result.Value)}
}
