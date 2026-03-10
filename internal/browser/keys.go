package browser

import (
	"strings"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

// browserKeys maps tmux-style key names to chromedp kb constants.
var browserKeys = map[string]string{
	"Enter":     kb.Enter,
	"Tab":       kb.Tab,
	"Escape":    kb.Escape,
	"BSpace":    kb.Backspace,
	"Backspace": kb.Backspace,
	"Space":     " ",
	"Delete":    kb.Delete,
	"DC":        kb.Delete,
	"Insert":    kb.Insert,
	"IC":        kb.Insert,
	"Up":        kb.ArrowUp,
	"Down":      kb.ArrowDown,
	"Left":      kb.ArrowLeft,
	"Right":     kb.ArrowRight,
	"Home":      kb.Home,
	"End":       kb.End,
	"PageUp":    kb.PageUp,
	"PPage":     kb.PageUp,
	"PgUp":      kb.PageUp,
	"PageDown":  kb.PageDown,
	"NPage":     kb.PageDown,
	"PgDn":      kb.PageDown,
	"F1":        kb.F1,
	"F2":        kb.F2,
	"F3":        kb.F3,
	"F4":        kb.F4,
	"F5":        kb.F5,
	"F6":        kb.F6,
	"F7":        kb.F7,
	"F8":        kb.F8,
	"F9":        kb.F9,
	"F10":       kb.F10,
	"F11":       kb.F11,
	"F12":       kb.F12,
}

// parseBrowserKey converts a tmux-style key name to a chromedp KeyEvent action.
// Supports: named keys (Enter, Tab, etc.), Ctrl (C-x, ^x, Control+x),
// and single characters.
func parseBrowserKey(key string) chromedp.KeyAction {
	// Direct named key
	if v, ok := browserKeys[key]; ok {
		return chromedp.KeyEvent(v)
	}

	// Control+x format (as shown in help text)
	if strings.HasPrefix(key, "Control+") && len(key) > 8 {
		inner := key[8:]
		k := inner
		if v, ok := browserKeys[inner]; ok {
			k = v
		}
		return chromedp.KeyEvent(k, chromedp.KeyModifiers(input.ModifierCtrl))
	}

	// C-x or ^x → Ctrl modifier
	if (strings.HasPrefix(key, "C-") || strings.HasPrefix(key, "^")) && len(key) >= 3 {
		var inner string
		if key[0] == '^' {
			inner = key[1:]
		} else {
			inner = key[2:]
		}
		// Check if inner part is a named key (e.g., C-Enter)
		if v, ok := browserKeys[inner]; ok {
			return chromedp.KeyEvent(v, chromedp.KeyModifiers(input.ModifierCtrl))
		}
		// Single char
		return chromedp.KeyEvent(strings.ToLower(inner), chromedp.KeyModifiers(input.ModifierCtrl))
	}

	// Single char or literal — pass through
	return chromedp.KeyEvent(key)
}
