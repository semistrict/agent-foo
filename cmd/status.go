package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/semistrict/agent-foo/internal/daemon"
	"github.com/semistrict/agent-foo/internal/protocol"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show running sessions and their state",
	RunE: func(cmd *cobra.Command, args []string) error {
		sessions, err := daemon.ListSessions()
		if err != nil {
			return err
		}
		if len(sessions) == 0 {
			fmt.Println("No active sessions")
			return nil
		}

		for _, sess := range sessions {
			fmt.Printf("Session: %s\n", sess)
			printSubsystemStatus(sess, "browser")
			printSubsystemStatus(sess, "term")
			printSubsystemStatus(sess, "debug")
			fmt.Println()
		}
		return nil
	},
}

func printSubsystemStatus(session, subsystem string) {
	var action string
	switch subsystem {
	case "browser":
		action = "eval"
	case "term":
		action = "list"
	case "debug":
		action = "list"
	}

	params := map[string]string{}
	if subsystem == "browser" {
		params["js"] = "JSON.stringify({url: location.href, title: document.title})"
	}
	raw, _ := json.Marshal(params)
	req := &protocol.Request{
		ID:        uuid.New().String(),
		Subsystem: subsystem,
		Action:    action,
		Params:    raw,
	}

	resp, err := daemon.SendCommand(session, req)
	if err != nil {
		return
	}

	switch subsystem {
	case "browser":
		if !resp.Success {
			return
		}
		// eval returns a JSON string of {url, title}
		var jsonStr string
		if json.Unmarshal(resp.Data, &jsonStr) != nil {
			return
		}
		var info map[string]string
		if json.Unmarshal([]byte(jsonStr), &info) != nil {
			return
		}
		fmt.Printf("  browser: %s — %s\n", info["title"], info["url"])

	case "term":
		if !resp.Success {
			return
		}
		var items []map[string]any
		if json.Unmarshal(resp.Data, &items) != nil || len(items) == 0 {
			return
		}
		for _, item := range items {
			status := "running"
			if item["exited"] == true {
				status = fmt.Sprintf("exited (%v)", item["exitCode"])
			}
			fmt.Printf("  term: %-20s pid=%-8v %s\n", item["label"], item["pid"], status)
		}

	case "debug":
		if !resp.Success {
			return
		}
		var items []map[string]any
		if json.Unmarshal(resp.Data, &items) != nil || len(items) == 0 {
			return
		}
		for _, item := range items {
			fmt.Printf("  debug: %-20s %s\n", item["label"], item["status"])
		}
	}
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
