package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/semistrict/agent-foo/internal/daemon"
	"github.com/semistrict/agent-foo/internal/protocol"
	"github.com/spf13/cobra"
)

func sendServe(action string, params map[string]string) (*protocol.Response, error) {
	session := flagSession
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
		Subsystem: "serve",
		Action:    action,
		Params:    raw,
	}
	return daemon.SendCommand(session, req)
}

var serveCmd = &cobra.Command{
	Use:   "serve <file|dir> [url]",
	Short: "Serve a file or directory over HTTP",
	Long: `Start a local HTTP server for a file or directory.

Uses a random available port by default. Optionally specify a URL like
http://localhost:3000 to pick the port.

Examples:
  af serve .                             # serve current dir on random port
  af serve index.html                    # serve single file on random port
  af serve ./dist http://localhost:3000  # serve on port 3000`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		target := args[0]
		if strings.HasPrefix(target, "~/") {
			home, _ := os.UserHomeDir()
			target = filepath.Join(home, target[2:])
		}
		abs, err := filepath.Abs(target)
		if err != nil {
			return fmt.Errorf("resolve path: %v", err)
		}
		p := map[string]string{"target": abs}

		if v, _ := cmd.Flags().GetString("label"); v != "" {
			p["label"] = v
		}

		if len(args) > 1 {
			// Extract port from URL like http://localhost:3000/...
			u := args[1]
			for _, prefix := range []string{"http://", "https://"} {
				if strings.HasPrefix(u, prefix) {
					u = u[len(prefix):]
					break
				}
			}
			hostPort := u
			if idx := strings.IndexByte(u, '/'); idx >= 0 {
				hostPort = u[:idx]
			}
			if idx := strings.LastIndexByte(hostPort, ':'); idx >= 0 {
				p["port"] = hostPort[idx+1:]
			}
		}

		resp, err := sendServe("start", p)
		if err != nil {
			return err
		}
		if !resp.Success {
			return fmt.Errorf("%s", resp.Error)
		}

		var m map[string]any
		if json.Unmarshal(resp.Data, &m) == nil {
			fmt.Println(m["url"])
		}
		return nil
	},
}

var serveStopCmd = &cobra.Command{
	Use:   "stop <label>",
	Short: "Stop a running server",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := sendServe("stop", map[string]string{"label": args[0]})
		if err != nil {
			return err
		}
		if !resp.Success {
			return fmt.Errorf("%s", resp.Error)
		}
		var s string
		if json.Unmarshal(resp.Data, &s) == nil {
			fmt.Println(s)
		}
		return nil
	},
}

var serveListCmd = &cobra.Command{
	Use:   "list",
	Short: "List running servers",
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := sendServe("list", nil)
		if err != nil {
			return err
		}
		if !resp.Success {
			return fmt.Errorf("%s", resp.Error)
		}
		if flagJSON {
			fmt.Println(string(resp.Data))
			return nil
		}
		var servers []map[string]string
		if json.Unmarshal(resp.Data, &servers) == nil {
			if len(servers) == 0 {
				fmt.Println("No active servers")
				return nil
			}
			for _, s := range servers {
				fmt.Printf("%-20s %s  (%s)\n", s["label"], s["url"], s["target"])
			}
		}
		return nil
	},
}

func init() {
	serveCmd.Flags().String("label", "", "Server label (default: filename)")
	serveCmd.AddCommand(serveStopCmd, serveListCmd)
	rootCmd.AddCommand(serveCmd)
}
