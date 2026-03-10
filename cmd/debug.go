package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/semistrict/agent-foo/internal/daemon"
	"github.com/semistrict/agent-foo/internal/protocol"
	"github.com/spf13/cobra"
)

var flagDebugSession string

func sendDebugRaw(action string, params map[string]string) (*protocol.Response, error) {
	session := flagDebugSession
	if session == "" {
		session = protocol.DefaultSession()
	}

	if err := daemon.EnsureDaemon(session, nil); err != nil {
		return nil, err
	}

	if params == nil {
		params = map[string]string{}
	}
	raw, _ := json.Marshal(params)
	req := &protocol.Request{
		ID:        uuid.New().String(),
		Subsystem: "debug",
		Action:    action,
		Params:    raw,
	}
	return daemon.SendCommand(session, req)
}

func sendDebug(action string, params map[string]string) error {
	resp, err := sendDebugRaw(action, params)
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
			printDebugHuman(action, resp.Data)
		}
	}
	return nil
}

func printDebugHuman(action string, data json.RawMessage) {
	switch action {
	case "launch", "attach":
		var m map[string]any
		if json.Unmarshal(data, &m) == nil {
			fmt.Printf("Debug session %s: %s\n", m["label"], m["status"])
			return
		}
	case "breakpoint":
		var bps []map[string]any
		if json.Unmarshal(data, &bps) == nil {
			for _, bp := range bps {
				verified := "unverified"
				if bp["verified"] == true {
					verified = "verified"
				}
				fmt.Printf("Breakpoint %v at line %v (%s)\n", bp["id"], bp["line"], verified)
				if msg, ok := bp["message"].(string); ok && msg != "" {
					fmt.Printf("  %s\n", msg)
				}
			}
			return
		}
	case "continue", "next", "stepin", "stepout", "pause":
		var m map[string]any
		if json.Unmarshal(data, &m) == nil {
			if m["status"] == "stopped" {
				fmt.Printf("Stopped: %s (thread %v)\n", m["reason"], m["threadId"])
				if frames, ok := m["stackTrace"].([]any); ok {
					for i, f := range frames {
						fm, _ := f.(map[string]any)
						if fm == nil {
							continue
						}
						file := fm["file"]
						if file == nil {
							file = "<unknown>"
						}
						fmt.Printf("  #%-2d %s at %s:%v\n", i, fm["name"], file, fm["line"])
					}
				}
			} else {
				fmt.Println("OK")
			}
			return
		}
	case "stacktrace":
		var frames []map[string]any
		if json.Unmarshal(data, &frames) == nil {
			for i, f := range frames {
				file := f["file"]
				if file == nil {
					file = "<unknown>"
				}
				fmt.Printf("#%-3d %s at %s:%v\n", i, f["name"], file, f["line"])
			}
			return
		}
	case "scopes":
		var scopes []map[string]any
		if json.Unmarshal(data, &scopes) == nil {
			for _, s := range scopes {
				fmt.Printf("%-20s ref=%v\n", s["name"], s["variablesReference"])
			}
			return
		}
	case "variables":
		var vars []map[string]any
		if json.Unmarshal(data, &vars) == nil {
			for _, v := range vars {
				line := fmt.Sprintf("%-20s = %s", v["name"], v["value"])
				if t, ok := v["type"].(string); ok && t != "" {
					line += fmt.Sprintf("  (%s)", t)
				}
				if ref, ok := v["ref"].(float64); ok && ref > 0 {
					line += fmt.Sprintf("  [ref=%d]", int(ref))
				}
				fmt.Println(line)
			}
			return
		}
	case "evaluate":
		var m map[string]any
		if json.Unmarshal(data, &m) == nil {
			fmt.Println(m["result"])
			if t, ok := m["type"].(string); ok && t != "" {
				fmt.Printf("  type: %s\n", t)
			}
			return
		}
	case "threads":
		var threads []map[string]any
		if json.Unmarshal(data, &threads) == nil {
			for _, t := range threads {
				fmt.Printf("Thread %v: %s\n", t["id"], t["name"])
			}
			return
		}
	case "events":
		var events []map[string]any
		if json.Unmarshal(data, &events) == nil {
			if len(events) == 0 {
				fmt.Println("No events")
				return
			}
			for _, e := range events {
				parts := []string{fmt.Sprintf("[%s]", e["event"])}
				for k, v := range e {
					if k == "event" {
						continue
					}
					parts = append(parts, fmt.Sprintf("%s=%v", k, v))
				}
				fmt.Println(strings.Join(parts, " "))
			}
			return
		}
	case "list":
		var items []map[string]any
		if json.Unmarshal(data, &items) == nil {
			if len(items) == 0 {
				fmt.Println("No debug sessions")
				return
			}
			for _, item := range items {
				fmt.Printf("%-20s %s\n", item["label"], item["status"])
			}
			return
		}
	}

	var s string
	if json.Unmarshal(data, &s) == nil {
		fmt.Println(s)
	} else {
		fmt.Println(string(data))
	}
}

var debugCmd = &cobra.Command{
	Use:   "debug",
	Short: "Debug adapter protocol (DAP) sessions",
}

var debugLaunchCmd = &cobra.Command{
	Use:   "launch",
	Short: "Launch a debug adapter and start debugging",
	RunE: func(cmd *cobra.Command, args []string) error {
		p := map[string]string{}
		if v, _ := cmd.Flags().GetString("label"); v != "" {
			p["label"] = v
		}
		if v, _ := cmd.Flags().GetString("adapter"); v != "" {
			p["adapter"] = v
		}
		if v, _ := cmd.Flags().GetString("port"); v != "" {
			p["port"] = v
		}
		if v, _ := cmd.Flags().GetString("program"); v != "" {
			p["program"] = v
		}
		if v, _ := cmd.Flags().GetString("cwd"); v != "" {
			p["cwd"] = v
		}
		if v, _ := cmd.Flags().GetString("args"); v != "" {
			p["args"] = v
		}
		if v, _ := cmd.Flags().GetBool("stop-on-entry"); v {
			p["stopOnEntry"] = "true"
		}
		if v, _ := cmd.Flags().GetString("launch-args"); v != "" {
			p["launchArgs"] = v
		}
		return sendDebug("launch", p)
	},
}

var debugAttachCmd = &cobra.Command{
	Use:   "attach",
	Short: "Attach to a running debug adapter",
	RunE: func(cmd *cobra.Command, args []string) error {
		p := map[string]string{}
		if v, _ := cmd.Flags().GetString("label"); v != "" {
			p["label"] = v
		}
		if v, _ := cmd.Flags().GetString("host"); v != "" {
			p["host"] = v
		}
		if v, _ := cmd.Flags().GetString("port"); v != "" {
			p["port"] = v
		}
		if v, _ := cmd.Flags().GetString("pid"); v != "" {
			p["pid"] = v
		}
		if v, _ := cmd.Flags().GetString("attach-args"); v != "" {
			p["attachArgs"] = v
		}
		return sendDebug("attach", p)
	},
}

var debugBreakpointCmd = &cobra.Command{
	Use:   "breakpoint <file> <line>[,line...]",
	Short: "Set breakpoints in a file",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		p := map[string]string{
			"file":  args[0],
			"lines": args[1],
		}
		if v, _ := cmd.Flags().GetString("label"); v != "" {
			p["label"] = v
		}
		if v, _ := cmd.Flags().GetString("condition"); v != "" {
			p["condition"] = v
		}
		return sendDebug("breakpoint", p)
	},
}

var debugContinueCmd = &cobra.Command{
	Use:   "continue",
	Short: "Continue execution",
	RunE: func(cmd *cobra.Command, args []string) error {
		p := map[string]string{}
		if v, _ := cmd.Flags().GetString("label"); v != "" {
			p["label"] = v
		}
		if v, _ := cmd.Flags().GetString("thread"); v != "" {
			p["thread"] = v
		}
		return sendDebug("continue", p)
	},
}

var debugNextCmd = &cobra.Command{
	Use:   "next",
	Short: "Step over (next line)",
	RunE: func(cmd *cobra.Command, args []string) error {
		p := map[string]string{}
		if v, _ := cmd.Flags().GetString("label"); v != "" {
			p["label"] = v
		}
		return sendDebug("next", p)
	},
}

var debugStepInCmd = &cobra.Command{
	Use:   "stepin",
	Short: "Step into function",
	RunE: func(cmd *cobra.Command, args []string) error {
		p := map[string]string{}
		if v, _ := cmd.Flags().GetString("label"); v != "" {
			p["label"] = v
		}
		return sendDebug("stepin", p)
	},
}

var debugStepOutCmd = &cobra.Command{
	Use:   "stepout",
	Short: "Step out of function",
	RunE: func(cmd *cobra.Command, args []string) error {
		p := map[string]string{}
		if v, _ := cmd.Flags().GetString("label"); v != "" {
			p["label"] = v
		}
		return sendDebug("stepout", p)
	},
}

var debugPauseCmd = &cobra.Command{
	Use:   "pause",
	Short: "Pause execution",
	RunE: func(cmd *cobra.Command, args []string) error {
		p := map[string]string{}
		if v, _ := cmd.Flags().GetString("label"); v != "" {
			p["label"] = v
		}
		return sendDebug("pause", p)
	},
}

var debugStackTraceCmd = &cobra.Command{
	Use:   "stacktrace",
	Short: "Show call stack",
	RunE: func(cmd *cobra.Command, args []string) error {
		p := map[string]string{}
		if v, _ := cmd.Flags().GetString("label"); v != "" {
			p["label"] = v
		}
		if v, _ := cmd.Flags().GetString("thread"); v != "" {
			p["thread"] = v
		}
		if v, _ := cmd.Flags().GetString("levels"); v != "" {
			p["levels"] = v
		}
		return sendDebug("stacktrace", p)
	},
}

var debugScopesCmd = &cobra.Command{
	Use:   "scopes [frameId]",
	Short: "Show scopes for a stack frame",
	RunE: func(cmd *cobra.Command, args []string) error {
		p := map[string]string{}
		if v, _ := cmd.Flags().GetString("label"); v != "" {
			p["label"] = v
		}
		if len(args) > 0 {
			p["frame"] = args[0]
		}
		return sendDebug("scopes", p)
	},
}

var debugVariablesCmd = &cobra.Command{
	Use:   "variables <ref>",
	Short: "Show variables for a scope reference",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		p := map[string]string{"ref": args[0]}
		if v, _ := cmd.Flags().GetString("label"); v != "" {
			p["label"] = v
		}
		return sendDebug("variables", p)
	},
}

var debugEvalCmd = &cobra.Command{
	Use:   "eval <expression>",
	Short: "Evaluate expression in current frame",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		p := map[string]string{"expr": args[0]}
		if v, _ := cmd.Flags().GetString("label"); v != "" {
			p["label"] = v
		}
		if v, _ := cmd.Flags().GetString("frame"); v != "" {
			p["frame"] = v
		}
		return sendDebug("evaluate", p)
	},
}

var debugThreadsCmd = &cobra.Command{
	Use:   "threads",
	Short: "List threads",
	RunE: func(cmd *cobra.Command, args []string) error {
		p := map[string]string{}
		if v, _ := cmd.Flags().GetString("label"); v != "" {
			p["label"] = v
		}
		return sendDebug("threads", p)
	},
}

var debugEventsCmd = &cobra.Command{
	Use:   "events",
	Short: "Show debug adapter events",
	RunE: func(cmd *cobra.Command, args []string) error {
		p := map[string]string{}
		if v, _ := cmd.Flags().GetString("label"); v != "" {
			p["label"] = v
		}
		if v, _ := cmd.Flags().GetBool("clear"); v {
			p["clear"] = "true"
		}
		return sendDebug("events", p)
	},
}

var debugDisconnectCmd = &cobra.Command{
	Use:   "disconnect",
	Short: "Disconnect from debug adapter",
	RunE: func(cmd *cobra.Command, args []string) error {
		p := map[string]string{}
		if v, _ := cmd.Flags().GetString("label"); v != "" {
			p["label"] = v
		}
		if v, _ := cmd.Flags().GetBool("no-terminate"); v {
			p["terminate"] = "false"
		}
		return sendDebug("disconnect", p)
	},
}

var debugListCmd = &cobra.Command{
	Use:   "list",
	Short: "List debug sessions",
	RunE: func(cmd *cobra.Command, args []string) error {
		return sendDebug("list", nil)
	},
}

func init() {
	debugCmd.PersistentFlags().StringVar(&flagDebugSession, "session", "", "Session name")

	// Launch flags
	debugLaunchCmd.Flags().String("label", "", "Debug session label")
	debugLaunchCmd.Flags().String("adapter", "", "Debug adapter command (e.g. 'dlv dap')")
	debugLaunchCmd.Flags().String("port", "", "Adapter TCP port (if adapter uses TCP)")
	debugLaunchCmd.Flags().String("program", "", "Program to debug")
	debugLaunchCmd.Flags().String("cwd", "", "Working directory")
	debugLaunchCmd.Flags().String("args", "", "Program arguments (JSON array)")
	debugLaunchCmd.Flags().Bool("stop-on-entry", false, "Stop on entry point")
	debugLaunchCmd.Flags().String("launch-args", "", "Additional launch arguments (JSON object)")

	// Attach flags
	debugAttachCmd.Flags().String("label", "", "Debug session label")
	debugAttachCmd.Flags().String("host", "", "Adapter host (default: 127.0.0.1)")
	debugAttachCmd.Flags().String("port", "", "Adapter port")
	debugAttachCmd.Flags().String("pid", "", "Process ID to attach to")
	debugAttachCmd.Flags().String("attach-args", "", "Additional attach arguments (JSON object)")

	// Breakpoint flags
	debugBreakpointCmd.Flags().String("label", "", "Debug session label")
	debugBreakpointCmd.Flags().String("condition", "", "Breakpoint condition expression")

	// Shared --label flag for inspection commands
	for _, cmd := range []*cobra.Command{
		debugContinueCmd, debugNextCmd, debugStepInCmd, debugStepOutCmd,
		debugPauseCmd, debugStackTraceCmd, debugScopesCmd, debugVariablesCmd,
		debugEvalCmd, debugThreadsCmd, debugEventsCmd, debugDisconnectCmd,
	} {
		cmd.Flags().String("label", "", "Debug session label")
	}

	// Thread flag for control commands
	debugContinueCmd.Flags().String("thread", "", "Thread ID")
	debugStackTraceCmd.Flags().String("thread", "", "Thread ID")
	debugStackTraceCmd.Flags().String("levels", "", "Max stack frames")
	debugEvalCmd.Flags().String("frame", "", "Frame ID for evaluation context")
	debugEventsCmd.Flags().Bool("clear", false, "Clear events after reading")
	debugDisconnectCmd.Flags().Bool("no-terminate", false, "Don't terminate debuggee")

	debugCmd.AddCommand(
		debugLaunchCmd, debugAttachCmd, debugBreakpointCmd,
		debugContinueCmd, debugNextCmd, debugStepInCmd, debugStepOutCmd, debugPauseCmd,
		debugStackTraceCmd, debugScopesCmd, debugVariablesCmd, debugEvalCmd,
		debugThreadsCmd, debugEventsCmd, debugDisconnectCmd, debugListCmd,
	)
	rootCmd.AddCommand(debugCmd)
}
