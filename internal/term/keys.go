package term

import (
	"fmt"
	"strings"
)

// specialKeys maps tmux-style key names to ANSI escape sequences.
var specialKeys = map[string]string{
	"Up":       "\x1b[A",
	"Down":     "\x1b[B",
	"Right":    "\x1b[C",
	"Left":     "\x1b[D",
	"Home":     "\x1b[H",
	"End":      "\x1b[F",
	"IC":       "\x1b[2~", // Insert
	"Insert":   "\x1b[2~",
	"DC":       "\x1b[3~", // Delete
	"Delete":   "\x1b[3~",
	"PPage":    "\x1b[5~", // Page Up
	"PageUp":   "\x1b[5~",
	"PgUp":     "\x1b[5~",
	"NPage":    "\x1b[6~", // Page Down
	"PageDown": "\x1b[6~",
	"PgDn":     "\x1b[6~",
	"Enter":    "\r",
	"Escape":   "\x1b",
	"Tab":      "\t",
	"BTab":     "\x1b[Z", // Shift-Tab / Backtab
	"BSpace":   "\x7f",   // Backspace
	"Space":    " ",
	"F1":       "\x1bOP",
	"F2":       "\x1bOQ",
	"F3":       "\x1bOR",
	"F4":       "\x1bOS",
	"F5":       "\x1b[15~",
	"F6":       "\x1b[17~",
	"F7":       "\x1b[18~",
	"F8":       "\x1b[19~",
	"F9":       "\x1b[20~",
	"F10":      "\x1b[21~",
	"F11":      "\x1b[23~",
	"F12":      "\x1b[24~",
}

// TranslateKey converts a tmux-style key name to its byte sequence.
// If the key is not recognized, it is returned as-is (literal characters).
func TranslateKey(key string) string {
	// Check special key names (case-sensitive, like tmux)
	if seq, ok := specialKeys[key]; ok {
		return seq
	}

	// C-x or ^x → Ctrl key
	if (strings.HasPrefix(key, "C-") || strings.HasPrefix(key, "^")) && len(key) >= 3 {
		var ch byte
		if key[0] == '^' {
			ch = key[1]
		} else {
			ch = key[2]
		}
		// Ctrl turns a-z/A-Z into 1-26, and @[\]^_ into 0,27-31
		if ch >= 'a' && ch <= 'z' {
			return string(rune(ch - 'a' + 1))
		}
		if ch >= 'A' && ch <= 'Z' {
			return string(rune(ch - 'A' + 1))
		}
		switch ch {
		case '@':
			return "\x00"
		case '[':
			return "\x1b"
		case '\\':
			return "\x1c"
		case ']':
			return "\x1d"
		case '^':
			return "\x1e"
		case '_':
			return "\x1f"
		}
	}

	// M-x → Alt/Meta (ESC prefix)
	if strings.HasPrefix(key, "M-") && len(key) >= 3 {
		rest := key[2:]
		// M-C-x → Alt+Ctrl
		translated := TranslateKey(rest)
		return "\x1b" + translated
	}

	// S-Up etc. → shifted arrow keys
	if strings.HasPrefix(key, "S-") {
		inner := key[2:]
		switch inner {
		case "Up":
			return "\x1b[1;2A"
		case "Down":
			return "\x1b[1;2B"
		case "Right":
			return "\x1b[1;2C"
		case "Left":
			return "\x1b[1;2D"
		}
		// For other S- keys, check if inner is a special key
		if seq, ok := specialKeys[inner]; ok {
			return seq // many terminals don't distinguish shifted specials
		}
	}

	// Not recognized — return as literal text
	return key
}

// TranslateKeys converts multiple tmux-style key arguments into a single byte sequence.
func TranslateKeys(keys []string) string {
	var buf strings.Builder
	for _, k := range keys {
		buf.WriteString(TranslateKey(k))
	}
	return buf.String()
}

// FormatKeyHelp returns a help string listing supported key names.
func FormatKeyHelp() string {
	return fmt.Sprintf(`Special keys: Up, Down, Left, Right, Home, End, Enter, Escape, Tab,
  BTab (Shift-Tab), BSpace (Backspace), Space, DC/Delete, IC/Insert,
  PPage/PageUp/PgUp, NPage/PageDown/PgDn, F1-F12
Modifiers: C-x or ^x (Ctrl), M-x (Alt/Meta), S-x (Shift)
Examples: Enter, C-c, M-x, C-M-a, F5, Down`)
}
