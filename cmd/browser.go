package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/google/uuid"
	"github.com/semistrict/agent-foo/internal/daemon"
	"github.com/semistrict/agent-foo/internal/protocol"
	"github.com/spf13/cobra"
)

var (
	flagSession  string
	flagHeadless bool
	flagCDP      string
	flagProfile  string
	flagFull     bool
	flagTimeout  int
)

// send ensures the daemon is running, sends a command, and prints the result.
func send(action string, params map[string]string) error {
	session := flagSession
	if session == "" {
		session = protocol.DefaultSession()
	}

	if err := daemon.EnsureDaemon(session, nil); err != nil {
		return err
	}

	if params == nil {
		params = map[string]string{}
	}
	// Pass browser config in params (used by handler on first launch)
	if !flagHeadless {
		params["__headed"] = "true"
	}
	if flagCDP != "" {
		params["__cdp"] = flagCDP
	}
	if flagProfile != "" {
		params["__profile"] = flagProfile
	}
	if flagTimeout > 0 {
		params["__timeout"] = fmt.Sprintf("%d", flagTimeout)
	}
	raw, _ := json.Marshal(params)
	req := &protocol.Request{
		ID:        uuid.New().String(),
		Subsystem: "browser",
		Action:    action,
		Params:    raw,
	}
	resp, err := daemon.SendCommand(session, req)
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("%s", resp.Error)
	}

	if resp.Data != nil {
		if flagJSON {
			fmt.Println(string(resp.Data))
		} else {
			printHuman(action, resp.Data)
		}
	}
	return nil
}

// printHuman formats response data for human consumption.
func printHuman(action string, data json.RawMessage) {
	switch action {
	case "open":
		var m map[string]string
		if json.Unmarshal(data, &m) == nil {
			fmt.Printf("%s — %s\n", m["title"], m["url"])
			return
		}
	case "click", "type", "fill", "press":
		var m map[string]string
		if json.Unmarshal(data, &m) == nil {
			if nav, ok := m["navigated"]; ok {
				fmt.Printf("Navigated: %s — %s\n", m["title"], nav)
			} else {
				fmt.Println("Done")
			}
			return
		}
	case "screenshot", "pdf":
		var m map[string]any
		if json.Unmarshal(data, &m) == nil {
			fmt.Printf("%s (%v bytes)\n", m["path"], m["bytes"])
			return
		}
	case "snapshot":
		// all-tabs returns an array
		var tabs []map[string]any
		if json.Unmarshal(data, &tabs) == nil {
			for _, t := range tabs {
				fmt.Printf("--- tab %v: %s — %s ---\n", t["tab"], t["title"], t["url"])
				if e, ok := t["error"].(string); ok {
					fmt.Printf("error: %s\n", e)
				} else if s, ok := t["snapshot"].(string); ok {
					fmt.Print(s)
				}
			}
			return
		}
	case "debug":
		printBrowserDebugHuman(data)
		return
	case "tabs":
		var tabs []map[string]any
		if json.Unmarshal(data, &tabs) == nil {
			for _, t := range tabs {
				marker := " "
				if active, ok := t["active"].(bool); ok && active {
					marker = "→"
				}
				fmt.Printf("%s %v: %s — %s\n", marker, t["index"], t["title"], t["url"])
			}
			return
		}
	case "tab":
		var m map[string]string
		if json.Unmarshal(data, &m) == nil {
			fmt.Printf("%s — %s\n", m["title"], m["url"])
			return
		}
	case "events":
		var events []map[string]any
		if json.Unmarshal(data, &events) == nil {
			for _, ev := range events {
				ts := ""
				if t, ok := ev["time"].(string); ok {
					if parsed, err := time.Parse(time.RFC3339Nano, t); err == nil {
						ts = parsed.Format("15:04:05.000")
					} else {
						ts = t
					}
				}
				cat, _ := ev["category"].(string)
				typ, _ := ev["type"].(string)

				detail := formatEventData(ev["data"])
				fmt.Printf("[%s] %s/%s  %s\n", ts, cat, typ, detail)
			}
			if len(events) == 0 {
				fmt.Println("No events")
			}
			return
		}
		// Could be stats or a string
		var m map[string]any
		if json.Unmarshal(data, &m) == nil {
			// stats response
			if _, ok := m["count"]; ok {
				fmt.Printf("Events: %.0f  Bytes: %.0f  Max: %.0f\n", m["count"], m["bytes"], m["maxBytes"])
				return
			}
		}
	}

	// Default: try as string, then raw JSON
	var s string
	if json.Unmarshal(data, &s) == nil {
		fmt.Println(s)
	} else {
		fmt.Println(string(data))
	}
}

func formatEventData(v any) string {
	raw, ok := v.(json.RawMessage)
	if !ok {
		// events come back with data already parsed as map from the outer unmarshal
		// re-marshal and unmarshal to get the inner map
		b, _ := json.Marshal(v)
		raw = b
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return string(raw)
	}

	var parts []string
	// Pick the most relevant fields by category
	for _, key := range []string{"url", "method", "status", "statusText", "level", "description", "errorText", "message", "dialogType", "title", "type", "targetId", "args"} {
		val, ok := m[key]
		if !ok {
			continue
		}
		switch v := val.(type) {
		case string:
			if v != "" {
				parts = append(parts, fmt.Sprintf("%s=%s", key, v))
			}
		case float64:
			parts = append(parts, fmt.Sprintf("%s=%g", key, v))
		case []any:
			strs := make([]string, len(v))
			for i, a := range v {
				strs[i] = fmt.Sprintf("%v", a)
			}
			parts = append(parts, fmt.Sprintf("%s=[%s]", key, strings.Join(strs, ", ")))
		default:
			parts = append(parts, fmt.Sprintf("%s=%v", key, v))
		}
	}
	return strings.Join(parts, " ")
}

var browserCmd = &cobra.Command{
	Use:   "browser",
	Short: "Browser automation using your installed Chrome",
	Long:  "Control your locally installed Chrome via CDP. No Playwright or separate browser needed.",
}

var openCmd = &cobra.Command{
	Use:   "open <url>",
	Short: "Navigate to URL",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return send("open", map[string]string{"url": args[0]})
	},
}

var clickCmd = &cobra.Command{
	Use:   "click <selector>",
	Short: "Click element (CSS selector or @ref)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return send("click", map[string]string{"selector": args[0]})
	},
}

var typeCmd = &cobra.Command{
	Use:   "type <selector> <text>",
	Short: "Type into element",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return send("type", map[string]string{"selector": args[0], "text": args[1]})
	},
}

var fillCmd = &cobra.Command{
	Use:   "fill <selector> <text>",
	Short: "Clear and fill element",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return send("fill", map[string]string{"selector": args[0], "text": args[1]})
	},
}

var pressCmd = &cobra.Command{
	Use:   "press <key>",
	Short: "Press key (Enter, Tab, Control+a, etc.)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return send("press", map[string]string{"key": args[0]})
	},
}

var screenshotCmd = &cobra.Command{
	Use:   "screenshot [path]",
	Short: "Take screenshot",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		p := map[string]string{}
		if len(args) > 0 {
			p["path"] = args[0]
		}
		if flagFull {
			p["full"] = "true"
		}
		return send("screenshot", p)
	},
}

var renderCmd = &cobra.Command{
	Use:   "render",
	Short: "Text wireframe of the page layout",
	RunE: func(cmd *cobra.Command, args []string) error {
		p := map[string]string{}
		widthSet := cmd.Flags().Changed("width")
		heightSet := cmd.Flags().Changed("height")
		if widthSet {
			v, _ := cmd.Flags().GetInt("width")
			p["width"] = fmt.Sprintf("%d", v)
		}
		if heightSet {
			v, _ := cmd.Flags().GetInt("height")
			p["height"] = fmt.Sprintf("%d", v)
		}
		// Default to terminal size
		if !widthSet || !heightSet {
			if w, h, err := term.GetSize(0); err == nil {
				if !widthSet {
					p["width"] = fmt.Sprintf("%d", w)
				}
				if !heightSet {
					p["height"] = fmt.Sprintf("%d", h)
				}
			}
		}
		return send("render", p)
	},
}

var pdfCmd = &cobra.Command{
	Use:   "pdf <path>",
	Short: "Save page as PDF",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return send("pdf", map[string]string{"path": args[0]})
	},
}

var snapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Accessibility tree with refs (for AI)",
	RunE: func(cmd *cobra.Command, args []string) error {
		p := map[string]string{}
		if v, _ := cmd.Flags().GetBool("interactive"); v {
			p["interactive"] = "true"
		}
		if v, _ := cmd.Flags().GetBool("compact"); v {
			p["compact"] = "true"
		}
		if v, _ := cmd.Flags().GetInt("depth"); v > 0 {
			p["depth"] = fmt.Sprintf("%d", v)
		}
		if v, _ := cmd.Flags().GetBool("all-tabs"); v {
			p["allTabs"] = "true"
		}
		return send("snapshot", p)
	},
}

var evalCmd = &cobra.Command{
	Use:   "eval <js>",
	Short: "Run JavaScript (_REF.e1 expands to the element with that ref)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return send("eval", map[string]string{"js": args[0]})
	},
}

var waitCmd = &cobra.Command{
	Use:   "wait <selector>",
	Short: "Wait for element to be visible",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return send("wait", map[string]string{"target": args[0]})
	},
}

var backCmd = &cobra.Command{
	Use: "back", Short: "Go back",
	RunE: func(cmd *cobra.Command, args []string) error { return send("back", nil) },
}

var forwardCmd = &cobra.Command{
	Use: "forward", Short: "Go forward",
	RunE: func(cmd *cobra.Command, args []string) error { return send("forward", nil) },
}

var reloadCmd = &cobra.Command{
	Use: "reload", Short: "Reload page",
	RunE: func(cmd *cobra.Command, args []string) error { return send("reload", nil) },
}

var uploadCmd = &cobra.Command{
	Use:   "upload <selector> <files...>",
	Short: "Upload files",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return send("upload", map[string]string{"selector": args[0], "files": strings.Join(args[1:], ",")})
	},
}

var setCmd = &cobra.Command{
	Use:   "set <setting> [values...]",
	Short: "Browser settings: viewport, media",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		p := map[string]string{"setting": args[0]}
		switch args[0] {
		case "viewport":
			if len(args) < 3 {
				return fmt.Errorf("usage: set viewport <width> <height>")
			}
			p["width"], p["height"] = args[1], args[2]
		case "media":
			for _, a := range args[1:] {
				switch a {
				case "dark", "light":
					p["colorScheme"] = a
				case "reduced-motion":
					p["reducedMotion"] = "true"
				}
			}
		}
		return send("set", p)
	},
}

var eventsCmd = &cobra.Command{
	Use:   "events [clear|stats]",
	Short: "Query recorded CDP events (console, network, page, target)",
	Long: `Query the event buffer. Flags filter results.

Examples:
  af browser events                          # all events
  af browser events --category console       # console events only
  af browser events --category network --type response --last 10
  af browser events clear                    # clear buffer
  af browser events stats                    # buffer stats`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		p := map[string]string{}
		if len(args) > 0 {
			p["action"] = args[0]
		}
		if v, _ := cmd.Flags().GetString("category"); v != "" {
			p["category"] = v
		}
		if v, _ := cmd.Flags().GetString("type"); v != "" {
			p["type"] = v
		}
		if v, _ := cmd.Flags().GetInt("last"); v > 0 {
			p["last"] = fmt.Sprintf("%d", v)
		}
		if v, _ := cmd.Flags().GetString("since"); v != "" {
			p["since"] = v
		}
		return send("events", p)
	},
}

var tabsCmd = &cobra.Command{
	Use:   "tabs",
	Short: "List open tabs",
	RunE: func(cmd *cobra.Command, args []string) error {
		return send("tabs", nil)
	},
}

var tabCmd = &cobra.Command{
	Use:   "tab <index>",
	Short: "Switch to tab by index",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return send("tab", map[string]string{"index": args[0]})
	},
}

var closeCmd = &cobra.Command{
	Use:   "close",
	Short: "Close browser and stop daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		return send("close", nil)
	},
}

var sessionCmd = &cobra.Command{
	Use:   "session [list]",
	Short: "Show or list sessions",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 && args[0] == "list" {
			sessions, err := daemon.ListSessions()
			if err != nil {
				return err
			}
			if len(sessions) == 0 {
				fmt.Println("No active sessions")
				return nil
			}
			current := flagSession
			if current == "" {
				current = protocol.DefaultSession()
			}
			for _, s := range sessions {
				if s == current {
					fmt.Printf("→ %s\n", s)
				} else {
					fmt.Printf("  %s\n", s)
				}
			}
			return nil
		}
		session := flagSession
		if session == "" {
			session = protocol.DefaultSession()
		}
		fmt.Println(session)
		return nil
	},
}

func init() {
	browserCmd.PersistentFlags().StringVar(&flagSession, "session", "", "Session name (default: git-scoped)")
	browserCmd.PersistentFlags().BoolVar(&flagHeadless, "headless", false, "Run browser without window")
	browserCmd.PersistentFlags().StringVar(&flagCDP, "cdp", "", "Connect via CDP URL")
	browserCmd.PersistentFlags().StringVar(&flagProfile, "profile", "", "Persistent browser profile path")
	browserCmd.PersistentFlags().IntVar(&flagTimeout, "timeout", 0, "Command timeout in seconds (default: 5)")

	screenshotCmd.Flags().BoolVarP(&flagFull, "full", "f", false, "Full page screenshot")
	renderCmd.Flags().IntP("width", "W", 0, "Grid width in columns (default: terminal width)")
	renderCmd.Flags().IntP("height", "H", 0, "Grid height in rows (default: terminal height)")
	eventsCmd.Flags().String("category", "", "Filter by category (console, network, page, target)")
	eventsCmd.Flags().String("type", "", "Filter by type (e.g. log, request, response, exception)")
	eventsCmd.Flags().Int("last", 0, "Return only the last N events")
	eventsCmd.Flags().String("since", "", "Only events after this time (RFC3339)")
	snapshotCmd.Flags().BoolP("interactive", "i", false, "Only interactive elements")
	snapshotCmd.Flags().BoolP("compact", "c", false, "Remove empty structural elements")
	snapshotCmd.Flags().IntP("depth", "d", 0, "Limit tree depth")
	snapshotCmd.Flags().BoolP("all-tabs", "a", false, "Snapshot all open tabs")

	browserCmd.AddCommand(
		openCmd, clickCmd, typeCmd, fillCmd, pressCmd,
		screenshotCmd, renderCmd, pdfCmd, snapshotCmd, evalCmd, waitCmd,
		backCmd, forwardCmd, reloadCmd,
		uploadCmd, setCmd, eventsCmd, tabsCmd, tabCmd, closeCmd, sessionCmd,
	)
	rootCmd.AddCommand(browserCmd)
}
