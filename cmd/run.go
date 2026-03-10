package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/semistrict/agent-foo/internal/daemon"
	"github.com/semistrict/agent-foo/internal/protocol"
	"github.com/spf13/cobra"
)

var (
	flagRunSession string
	flagLabel      string
)

// sendTermRaw sends a command to the term daemon and returns the raw response.
func sendTermRaw(action string, params map[string]string) (*protocol.Response, error) {
	session := flagRunSession
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
		Subsystem: "term",
		Action:    action,
		Params:    raw,
	}
	return daemon.SendCommand(session, req)
}

// sendTerm sends a command to the term daemon.
func sendTerm(action string, params map[string]string) error {
	session := flagRunSession
	if session == "" {
		session = protocol.DefaultSession()
	}

	if err := daemon.EnsureDaemon(session, nil); err != nil {
		return err
	}

	if params == nil {
		params = map[string]string{}
	}
	raw, _ := json.Marshal(params)
	req := &protocol.Request{
		ID:        uuid.New().String(),
		Subsystem: "term",
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
			printTermHuman(action, resp.Data)
		}
	}
	return nil
}

func printTermHuman(action string, data json.RawMessage) {
	switch action {
	case "run":
		var m map[string]any
		if json.Unmarshal(data, &m) == nil {
			fmt.Printf("Started %s (pid %v, %vx%v)\n", m["label"], m["pid"], m["cols"], m["rows"])
			return
		}
	case "snapshot":
		// Could be a string (text_only), a single object, or an array of objects
		var s string
		if json.Unmarshal(data, &s) == nil {
			fmt.Println(s)
			return
		}
		var arr []map[string]any
		if json.Unmarshal(data, &arr) == nil {
			for i, m := range arr {
				if i > 0 {
					fmt.Println()
				}
				status := "running"
				if m["exited"] == true {
					status = fmt.Sprintf("exited (code %v)", m["exitCode"])
				}
				fmt.Printf("=== %s (pid %v, %s) ===\n", m["label"], m["pid"], status)
				if screen, ok := m["screen"].(string); ok {
					fmt.Println(screen)
				}
			}
			return
		}
		var m map[string]any
		if json.Unmarshal(data, &m) == nil {
			if screen, ok := m["screen"].(string); ok {
				fmt.Println(screen)
			}
			if m["exited"] == true {
				fmt.Printf("--- exited (code %v) ---\n", m["exitCode"])
			}
			return
		}
	case "list":
		var items []map[string]any
		if json.Unmarshal(data, &items) == nil {
			if len(items) == 0 {
				fmt.Println("No running instances")
				return
			}
			for _, item := range items {
				status := "running"
				if item["exited"] == true {
					status = fmt.Sprintf("exited (%v)", item["exitCode"])
				}
				fmt.Printf("%-20s pid=%-8v %s\n", item["label"], item["pid"], status)
			}
			return
		}
	case "close":
		var killed []map[string]any
		if json.Unmarshal(data, &killed) == nil {
			if len(killed) == 0 {
				fmt.Println("No running instances")
				return
			}
			for _, k := range killed {
				fmt.Printf("Killed %s (pid %v)\n", k["label"], k["pid"])
				if screen, ok := k["screen"].(string); ok && screen != "" {
					fmt.Println(screen)
				}
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

type streamResult struct {
	exited   bool
	exitCode float64
}

// streamTermOutput polls snapshots for a term instance, printing incremental output.
// It stops when the process exits, idle timeout is reached, or Ctrl+C is pressed.
// prevScreen is the baseline screen content (output already seen).
func streamTermOutput(label, prevScreen string, idleTimeout time.Duration) streamResult {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)

	lastChange := time.Now()
	var exited bool
	var exitCode float64

	for {
		select {
		case <-sigCh:
			goto done
		default:
		}

		snapResp, err := sendTermRaw("snapshot", map[string]string{"label": label})
		if err != nil {
			break
		}
		if !snapResp.Success {
			break
		}

		var snap map[string]any
		json.Unmarshal(snapResp.Data, &snap)
		screen, _ := snap["screen"].(string)

		if screen != prevScreen {
			if strings.HasPrefix(screen, prevScreen) {
				fmt.Print(screen[len(prevScreen):])
			} else {
				fmt.Print(screen)
			}
			prevScreen = screen
			lastChange = time.Now()
		}

		if snap["exited"] == true {
			exited = true
			exitCode, _ = snap["exitCode"].(float64)
			break
		}

		if time.Since(lastChange) >= idleTimeout {
			break
		}

		time.Sleep(50 * time.Millisecond)
	}
done:
	if prevScreen != "" {
		fmt.Println()
	}
	return streamResult{exited: exited, exitCode: exitCode}
}

var runCmd = &cobra.Command{
	Use:                "run <command> [args...]",
	Short:              "Run a command in a virtual terminal",
	Long:               "Start a command in a headless virtual terminal (midterm). Use 'af term snapshot' to read output.",
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Manually extract our flags before the command
		var label string
		var rows, cols int
		var timeout int
		var remaining []string
		for i := 0; i < len(args); i++ {
			switch args[i] {
			case "--label":
				if i+1 < len(args) {
					label = args[i+1]
					i++
				}
			case "--rows":
				if i+1 < len(args) {
					fmt.Sscanf(args[i+1], "%d", &rows)
					i++
				}
			case "--cols":
				if i+1 < len(args) {
					fmt.Sscanf(args[i+1], "%d", &cols)
					i++
				}
			case "--timeout":
				if i+1 < len(args) {
					fmt.Sscanf(args[i+1], "%d", &timeout)
					i++
				}
			case "--session":
				if i+1 < len(args) {
					flagRunSession = args[i+1]
					i++
				}
			case "--json":
				flagJSON = true
			case "--":
				remaining = append(remaining, args[i+1:]...)
				i = len(args)
			default:
				remaining = append(remaining, args[i:]...)
				i = len(args)
			}
		}
		if len(remaining) == 0 {
			return fmt.Errorf("usage: af run [--label NAME] [--rows N] [--cols N] [--timeout N] <command> [args...]")
		}

		// Start the command
		argsJSON, _ := json.Marshal(remaining)
		cwd, _ := os.Getwd()
		envJSON, _ := json.Marshal(os.Environ())
		p := map[string]string{
			"args": string(argsJSON),
			"cwd":  cwd,
			"env":  string(envJSON),
		}
		if label != "" {
			p["label"] = label
		}
		if rows > 0 {
			p["rows"] = fmt.Sprintf("%d", rows)
		}
		if cols > 0 {
			p["cols"] = fmt.Sprintf("%d", cols)
		}
		runResp, err := sendTermRaw("run", p)
		if err != nil {
			return err
		}
		if !runResp.Success {
			return fmt.Errorf("%s", runResp.Error)
		}

		// Extract label from response for wait
		var info map[string]any
		json.Unmarshal(runResp.Data, &info)
		waitLabel, _ := info["label"].(string)

		// JSON mode: use blocking wait, return final result
		if flagJSON {
			wp := map[string]string{"label": waitLabel}
			if timeout > 0 {
				wp["timeout"] = fmt.Sprintf("%d", timeout)
			}
			waitResp, err := sendTermRaw("wait", wp)
			if err != nil {
				return err
			}
			if !waitResp.Success {
				return fmt.Errorf("%s", waitResp.Error)
			}
			fmt.Println(string(waitResp.Data))
			var result map[string]any
			json.Unmarshal(waitResp.Data, &result)
			if result["exited"] == true {
				sendTermRaw("kill", map[string]string{"label": waitLabel})
			}
			return nil
		}

		// Stream output by polling snapshots
		idleTimeout := 30 * time.Second
		if timeout > 0 {
			idleTimeout = time.Duration(timeout) * time.Second
		}

		result := streamTermOutput(waitLabel, "", idleTimeout)

		if result.exited {
			if result.exitCode != 0 {
				fmt.Fprintf(os.Stderr, "exit code %v\n", result.exitCode)
			}
			sendTermRaw("kill", map[string]string{"label": waitLabel})
		} else {
			fmt.Fprintf(os.Stderr, "still running as %s (pid %v)\n", waitLabel, info["pid"])
		}
		return nil
	},
}

var termCmd = &cobra.Command{
	Use:   "term",
	Short: "Virtual terminal management",
}

var termSnapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Read terminal screen content",
	RunE: func(cmd *cobra.Command, args []string) error {
		p := map[string]string{}
		if flagLabel != "" {
			p["label"] = flagLabel
		}
		if v, _ := cmd.Flags().GetBool("scrollback"); v {
			p["scrollback"] = "true"
		}
		if v, _ := cmd.Flags().GetBool("text"); v {
			p["text_only"] = "true"
		}
		return sendTerm("snapshot", p)
	},
}

var termInputCmd = &cobra.Command{
	Use:   "input <key|text> [key...]",
	Short: "Send keys/text to terminal (tmux-style: Enter, C-c, Down, etc.)",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		literal, _ := cmd.Flags().GetBool("literal")
		label := flagLabel

		// Resolve label if not specified (needed for snapshot diffing)
		if label == "" {
			listResp, err := sendTermRaw("list", nil)
			if err != nil {
				return err
			}
			if !listResp.Success {
				return fmt.Errorf("%s", listResp.Error)
			}
			var items []map[string]any
			json.Unmarshal(listResp.Data, &items)
			if len(items) == 0 {
				return fmt.Errorf("no running instances")
			}
			if len(items) > 1 {
				return fmt.Errorf("multiple instances running, specify --label")
			}
			label, _ = items[0]["label"].(string)
		}

		// Capture screen before input so we can show only new output
		prevResp, _ := sendTermRaw("snapshot", map[string]string{
			"label":     label,
			"text_only": "true",
		})
		var prevScreen string
		if prevResp != nil && prevResp.Success {
			json.Unmarshal(prevResp.Data, &prevScreen)
		}

		// Send the input
		p := map[string]string{"label": label}
		if literal {
			p["text"] = strings.Join(args, " ")
		} else {
			keysJSON, _ := json.Marshal(args)
			p["keys"] = string(keysJSON)
		}
		resp, err := sendTermRaw("input", p)
		if err != nil {
			return err
		}
		if !resp.Success {
			return fmt.Errorf("%s", resp.Error)
		}

		// Wait briefly for output to settle
		timeout, _ := cmd.Flags().GetInt("timeout")
		if timeout <= 0 {
			timeout = 2
		}
		waitResp, err := sendTermRaw("wait", map[string]string{
			"label":   label,
			"timeout": fmt.Sprintf("%d", timeout),
		})
		if err != nil {
			return err
		}
		if !waitResp.Success {
			return fmt.Errorf("%s", waitResp.Error)
		}

		// Print only what changed
		var result map[string]any
		json.Unmarshal(waitResp.Data, &result)
		screen, _ := result["screen"].(string)
		if screen != prevScreen {
			if strings.HasPrefix(screen, prevScreen) {
				// Appended output (line-based programs)
				fmt.Println(screen[len(prevScreen):])
			} else {
				// Full redraw (TUI programs) — show final state
				fmt.Println(screen)
			}
		}
		return nil
	},
}

var termSendRawCmd = &cobra.Command{
	Use:   "sendraw <text>",
	Short: "Send raw text to terminal (no key name translation)",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		p := map[string]string{"text": strings.Join(args, " ")}
		if flagLabel != "" {
			p["label"] = flagLabel
		}
		return sendTerm("input", p)
	},
}

var termResizeCmd = &cobra.Command{
	Use:   "resize <rows> <cols>",
	Short: "Resize terminal",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		p := map[string]string{
			"rows": args[0],
			"cols": args[1],
		}
		if flagLabel != "" {
			p["label"] = flagLabel
		}
		return sendTerm("resize", p)
	},
}

var termKillCmd = &cobra.Command{
	Use:   "kill [label]",
	Short: "Kill a running terminal instance",
	RunE: func(cmd *cobra.Command, args []string) error {
		if all, _ := cmd.Flags().GetBool("all"); all {
			return sendTerm("killall", nil)
		}
		if len(args) == 0 {
			return fmt.Errorf("specify a label or use --all")
		}
		return sendTerm("kill", map[string]string{"label": args[0]})
	},
}

var termListCmd = &cobra.Command{
	Use:   "list",
	Short: "List running terminal instances",
	RunE: func(cmd *cobra.Command, args []string) error {
		return sendTerm("list", nil)
	},
}

var termCloseCmd = &cobra.Command{
	Use:   "close",
	Short: "Kill all instances and stop daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		return sendTerm("close", nil)
	},
}

func init() {
	// Shared --label flag on term subcommands
	for _, cmd := range []*cobra.Command{termSnapshotCmd, termInputCmd, termSendRawCmd, termResizeCmd} {
		cmd.Flags().StringVar(&flagLabel, "label", "", "Instance label (default: command name)")
	}

	termInputCmd.Flags().BoolP("literal", "l", false, "Send as literal text (no key name translation)")
	termInputCmd.Flags().Int("timeout", 2, "Seconds to wait for output after input")
	termKillCmd.Flags().BoolP("all", "a", false, "Kill all instances")

	// Session flag on term parent (run parses its own flags)
	termCmd.PersistentFlags().StringVar(&flagRunSession, "session", "", "Session name (default: git-scoped)")

	// Snapshot flags
	termSnapshotCmd.Flags().BoolP("scrollback", "s", false, "Include scrollback history")
	termSnapshotCmd.Flags().BoolP("text", "t", false, "Text only (no cursor/size metadata)")

	termCmd.AddCommand(termSnapshotCmd, termInputCmd, termSendRawCmd, termResizeCmd, termKillCmd, termListCmd, termCloseCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(termCmd)
}
