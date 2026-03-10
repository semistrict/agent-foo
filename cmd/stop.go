package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/semistrict/agent-foo/internal/daemon"
	"github.com/semistrict/agent-foo/internal/protocol"
	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop daemon(s)",
	RunE: func(cmd *cobra.Command, args []string) error {
		session, _ := cmd.Flags().GetString("session")

		if session != "" {
			return stopSession(session)
		}

		// Stop all sessions
		sessions, err := daemon.ListSessions()
		if err != nil {
			return err
		}
		if len(sessions) == 0 {
			fmt.Println("No running daemons")
			return nil
		}
		for _, sess := range sessions {
			fmt.Printf("Stopping %s...\n", sess)
			stopSession(sess)
		}
		return nil
	},
}

func stopSession(session string) error {
	req := &protocol.Request{Action: "shutdown"}
	resp, err := daemon.SendCommand(session, req)
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("%s", resp.Error)
	}

	printShutdownResult(resp.Data)
	return nil
}

func printShutdownResult(data json.RawMessage) {
	var result map[string]any
	if json.Unmarshal(data, &result) != nil {
		return
	}

	termRaw, ok := result["term"]
	if !ok {
		return
	}
	b, _ := json.Marshal(termRaw)
	var killed []map[string]any
	if json.Unmarshal(b, &killed) != nil {
		return
	}
	for _, k := range killed {
		fmt.Printf("Killed %s (pid %v)\n", k["label"], k["pid"])
		if screen, ok := k["screen"].(string); ok && screen != "" {
			fmt.Println(screen)
		}
	}
}

func init() {
	stopCmd.Flags().String("session", "", "Session to stop (default: all)")
	rootCmd.AddCommand(stopCmd)
}
