package browser

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/semistrict/agent-foo/internal/protocol"
)

//go:embed js/render.js
var renderJS string

type renderData struct {
	Viewport struct {
		W int `json:"w"`
		H int `json:"h"`
	} `json:"viewport"`
	Elements []renderElement `json:"elements"`
}

type renderElement struct {
	Tag         string `json:"tag"`
	Text        string `json:"text"`
	Role        string `json:"role,omitempty"`
	Ref         string `json:"ref,omitempty"`
	X           int    `json:"x"`
	Y           int    `json:"y"`
	W           int    `json:"w"`
	H           int    `json:"h"`
	Interactive bool   `json:"interactive,omitempty"`
}

func (h *Handler) doRender(p paramMap) *protocol.Response {
	cols := 120
	rows := 50
	if v := p.get("width"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cols = n
		}
	}
	if v := p.get("height"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			rows = n
		}
	}

	// Collect element data from browser
	var result *runtime.RemoteObject
	if err := h.browser.Run(chromedp.Evaluate(renderJS, &result)); err != nil {
		return errResp("render eval: %v", err)
	}
	if result == nil || result.Value == nil {
		return errResp("render: no data returned")
	}

	// result.Value is JSON-encoded string inside JSON (double-encoded)
	var raw string
	if err := json.Unmarshal(result.Value, &raw); err != nil {
		return errResp("render unmarshal string: %v", err)
	}

	var data renderData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return errResp("render unmarshal data: %v", err)
	}

	if len(data.Elements) == 0 {
		return okResp("(no visible elements)")
	}

	grid := renderGrid(data, cols, rows)
	return &protocol.Response{Success: true, Data: jsonStr(grid)}
}

func renderGrid(data renderData, cols, rows int) string {
	// Scale factors
	sx := float64(cols) / float64(data.Viewport.W)
	sy := float64(rows) / float64(data.Viewport.H)

	// Initialize grid with spaces
	grid := make([][]rune, rows)
	for i := range grid {
		grid[i] = make([]rune, cols)
		for j := range grid[i] {
			grid[i][j] = ' '
		}
	}

	// Sort elements: largest area first so smaller elements draw on top
	elems := make([]renderElement, len(data.Elements))
	copy(elems, data.Elements)
	sort.Slice(elems, func(i, j int) bool {
		return elems[i].W*elems[i].H > elems[j].W*elems[j].H
	})

	type placed struct {
		el             renderElement
		x1, y1, x2, y2 int
	}
	var boxes []placed

	// Pass 1: draw all boxes
	for _, el := range elems {
		x1 := int(float64(el.X) * sx)
		y1 := int(float64(el.Y) * sy)
		x2 := int(float64(el.X+el.W) * sx)
		y2 := int(float64(el.Y+el.H) * sy)

		if x1 < 0 {
			x1 = 0
		}
		if y1 < 0 {
			y1 = 0
		}
		if x2 >= cols {
			x2 = cols - 1
		}
		if y2 >= rows {
			y2 = rows - 1
		}
		if x1 >= x2 || y1 >= y2 {
			continue
		}

		// Skip elements too small to show even a ref
		innerW := x2 - x1 - 1
		if el.Ref != "" && innerW < len("@"+el.Ref) {
			continue
		}
		if innerW < 3 {
			continue
		}

		drawBox(grid, x1, y1, x2, y2)
		boxes = append(boxes, placed{el, x1, y1, x2, y2})
	}

	// Pass 2: draw labels largest-first, then smallest on top
	// Track which cells contain part of a ref string so we never partially overwrite one
	refCells := make([][]bool, rows)
	for i := range refCells {
		refCells[i] = make([]bool, cols)
	}

	for _, b := range boxes {
		lx := b.x1 + 1
		ly := b.y1
		if b.y2-b.y1 > 1 {
			ly = b.y1 + 1
		}
		maxLen := b.x2 - lx
		if maxLen <= 0 {
			continue
		}

		lbl := fitLabel(b.el, maxLen)
		if len(lbl) == 0 {
			continue
		}

		// Check if writing this label would partially overwrite an existing ref
		wouldBreakRef := false
		for j := range lbl {
			c := lx + j
			if c < cols && refCells[ly][c] {
				wouldBreakRef = true
				break
			}
		}
		if wouldBreakRef {
			continue
		}

		// Write label
		for j, ch := range lbl {
			if lx+j < cols {
				grid[ly][lx+j] = ch
			}
		}

		// Mark ref portion as protected
		if b.el.Ref != "" {
			refStr := "@" + b.el.Ref
			for j := 0; j < len(refStr) && lx+j < cols; j++ {
				refCells[ly][lx+j] = true
			}
		}
	}

	// Convert grid to string
	var b strings.Builder
	for _, row := range grid {
		line := strings.TrimRight(string(row), " ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func drawBox(grid [][]rune, x1, y1, x2, y2 int) {
	// Corners
	grid[y1][x1] = '┌'
	grid[y1][x2] = '┐'
	grid[y2][x1] = '└'
	grid[y2][x2] = '┘'

	// Top and bottom edges
	for x := x1 + 1; x < x2; x++ {
		grid[y1][x] = '─'
		grid[y2][x] = '─'
	}

	// Left and right edges
	for y := y1 + 1; y < y2; y++ {
		grid[y][x1] = '│'
		grid[y][x2] = '│'
	}
}

// fitLabel builds a label that fits in maxLen chars.
// Priority: ref > name > text. Ref is never partially shown.
func fitLabel(el renderElement, maxLen int) string {
	ref := ""
	if el.Ref != "" {
		ref = "@" + el.Ref
	}

	name := el.Tag
	if el.Role != "" {
		name = el.Role
	}
	switch name {
	case "button":
		name = "btn"
	case "input":
		name = "in"
	case "textarea":
		name = "txt"
	case "navigation":
		name = "nav"
	case "contentinfo":
		name = "footer"
	}
	if el.Interactive {
		name = "[" + name + "]"
	}

	text := el.Text
	if len(text) > 25 {
		text = text[:25] + "…"
	}
	if text != "" {
		text = fmt.Sprintf("%q", text)
	}

	// Try full: @ref name "text"
	parts := []string{}
	if ref != "" {
		parts = append(parts, ref)
	}
	parts = append(parts, name)
	if text != "" {
		parts = append(parts, text)
	}
	full := strings.Join(parts, " ")
	if len(full) <= maxLen {
		return full
	}

	// Drop text, try: @ref name
	parts = parts[:0]
	if ref != "" {
		parts = append(parts, ref)
	}
	parts = append(parts, name)
	short := strings.Join(parts, " ")
	if len(short) <= maxLen {
		// Fill remaining space with truncated text
		remain := maxLen - len(short) - 1 // space before text
		if text != "" && remain >= 4 {
			if len(text) > remain {
				text = text[:remain-1] + "…"
			}
			return short + " " + text
		}
		return short
	}

	// Drop name, try just: @ref
	if ref != "" && len(ref) <= maxLen {
		return ref
	}

	// If element has a ref but it doesn't fit, show nothing
	// Never show a name without its ref
	if ref != "" {
		return ""
	}

	// No ref, try just name
	if len(name) <= maxLen {
		return name
	}

	return ""
}

// Ensure embed import is used
var _ embed.FS
