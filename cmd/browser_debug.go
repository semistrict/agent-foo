package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

var browserDebugCmd = &cobra.Command{
	Use:   "debug",
	Short: "JavaScript debugger (CDP Debugger domain)",
}

var browserDebugBreakpointCmd = &cobra.Command{
	Use:   "breakpoint <line>",
	Short: "Set a JS breakpoint",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		p := map[string]string{
			"sub":  "breakpoint",
			"line": args[0],
		}
		if v, _ := cmd.Flags().GetString("url"); v != "" {
			p["url"] = v
		}
		if v, _ := cmd.Flags().GetString("condition"); v != "" {
			p["condition"] = v
		}
		return send("debug", p)
	},
}

var browserDebugRemoveCmd = &cobra.Command{
	Use:   "remove <breakpoint-id>",
	Short: "Remove a breakpoint",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return send("debug", map[string]string{"sub": "remove", "id": args[0]})
	},
}

var browserDebugContinueCmd = &cobra.Command{
	Use:   "continue",
	Short: "Resume execution",
	RunE: func(cmd *cobra.Command, args []string) error {
		return send("debug", map[string]string{"sub": "continue"})
	},
}

var browserDebugNextCmd = &cobra.Command{
	Use:   "next",
	Short: "Step over",
	RunE: func(cmd *cobra.Command, args []string) error {
		return send("debug", map[string]string{"sub": "next"})
	},
}

var browserDebugStepInCmd = &cobra.Command{
	Use:   "stepin",
	Short: "Step into",
	RunE: func(cmd *cobra.Command, args []string) error {
		return send("debug", map[string]string{"sub": "stepin"})
	},
}

var browserDebugStepOutCmd = &cobra.Command{
	Use:   "stepout",
	Short: "Step out",
	RunE: func(cmd *cobra.Command, args []string) error {
		return send("debug", map[string]string{"sub": "stepout"})
	},
}

var browserDebugPauseCmd = &cobra.Command{
	Use:   "pause",
	Short: "Pause execution",
	RunE: func(cmd *cobra.Command, args []string) error {
		return send("debug", map[string]string{"sub": "pause"})
	},
}

var browserDebugStackTraceCmd = &cobra.Command{
	Use:   "stacktrace",
	Short: "Show call stack",
	RunE: func(cmd *cobra.Command, args []string) error {
		return send("debug", map[string]string{"sub": "stacktrace"})
	},
}

var browserDebugScopesCmd = &cobra.Command{
	Use:   "scopes [frame]",
	Short: "Show scopes and variables for a stack frame",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		p := map[string]string{"sub": "scopes"}
		if len(args) > 0 {
			p["frame"] = args[0]
		}
		return send("debug", p)
	},
}

var browserDebugEvalCmd = &cobra.Command{
	Use:   "eval <expression>",
	Short: "Evaluate expression in paused frame",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		p := map[string]string{"sub": "eval", "expr": args[0]}
		if v, _ := cmd.Flags().GetString("frame"); v != "" {
			p["frame"] = v
		}
		return send("debug", p)
	},
}

var browserDebugExceptionsCmd = &cobra.Command{
	Use:   "exceptions [none|uncaught|all]",
	Short: "Configure pause on exceptions",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		p := map[string]string{"sub": "exceptions"}
		if len(args) > 0 {
			p["state"] = args[0]
		}
		return send("debug", p)
	},
}

func printBrowserDebugHuman(data json.RawMessage) {
	var m map[string]any
	if json.Unmarshal(data, &m) == nil {
		// Breakpoint set response
		if id, ok := m["id"]; ok {
			fmt.Printf("Breakpoint %v at line %v (scriptId: %v, %v locations)\n", id, m["line"], m["scriptId"], m["locations"])
			return
		}
		// Paused response (continue/next/stepin/stepout/pause)
		if m["status"] == "paused" {
			fmt.Printf("Paused: %s\n", m["reason"])
			if frames, ok := m["stackTrace"].([]any); ok {
				for i, f := range frames {
					fm, _ := f.(map[string]any)
					if fm == nil {
						continue
					}
					fmt.Printf("  #%-2d %s at %v:%v\n", i, fm["function"], fm["scriptId"], fm["line"])
				}
			}
			return
		}
		// Eval response
		if result, ok := m["result"]; ok {
			fmt.Println(result)
			if t, ok := m["type"].(string); ok && t != "" {
				fmt.Printf("  type: %s\n", t)
			}
			return
		}
	}

	// Scopes/stacktrace array
	var arr []map[string]any
	if json.Unmarshal(data, &arr) == nil {
		for i, item := range arr {
			// Stack frame
			if fn, ok := item["function"]; ok {
				fmt.Printf("#%-3d %s at %v:%v\n", i, fn, item["scriptId"], item["line"])
				continue
			}
			// Scope
			if typ, ok := item["type"]; ok {
				name := item["name"]
				if name == nil {
					name = ""
				}
				fmt.Printf("Scope: %s %v\n", typ, name)
				if vars, ok := item["variables"].([]any); ok {
					for _, v := range vars {
						vm, _ := v.(map[string]any)
						if vm == nil {
							continue
						}
						fmt.Printf("  %-20s = %v  (%v)\n", vm["name"], vm["value"], vm["type"])
					}
				}
			}
		}
		return
	}

	// Fallback: plain string or raw JSON
	var s string
	if json.Unmarshal(data, &s) == nil {
		fmt.Println(s)
	} else {
		fmt.Println(string(data))
	}
}

func init() {
	browserDebugBreakpointCmd.Flags().String("url", "", "URL regex to match script")
	browserDebugBreakpointCmd.Flags().String("condition", "", "Conditional breakpoint expression")
	browserDebugEvalCmd.Flags().String("frame", "", "Frame index (default: 0)")

	browserDebugCmd.AddCommand(
		browserDebugBreakpointCmd, browserDebugRemoveCmd,
		browserDebugContinueCmd, browserDebugNextCmd,
		browserDebugStepInCmd, browserDebugStepOutCmd,
		browserDebugPauseCmd, browserDebugStackTraceCmd,
		browserDebugScopesCmd, browserDebugEvalCmd,
		browserDebugExceptionsCmd,
	)
	browserCmd.AddCommand(browserDebugCmd)
}
