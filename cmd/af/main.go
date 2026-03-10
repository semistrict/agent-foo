package main

import (
	"encoding/json"
	"log"
	"os"

	"github.com/semistrict/agent-foo/cmd"
	"github.com/semistrict/agent-foo/internal/browser"
	"github.com/semistrict/agent-foo/internal/daemon"
	"github.com/semistrict/agent-foo/internal/debugger"
	"github.com/semistrict/agent-foo/internal/protocol"
	"github.com/semistrict/agent-foo/internal/serve"
	"github.com/semistrict/agent-foo/internal/term"
)

func main() {
	if os.Getenv("AF_DAEMON") == "1" {
		runDaemon()
		return
	}
	cmd.Execute()
}

func runDaemon() {
	session := os.Getenv("AF_SESSION")

	browserHandler := browser.NewHandler()
	termHandler := term.NewHandler()
	debugHandler := debugger.NewHandler()
	serveHandler := serve.NewHandler()

	combined := func(req *protocol.Request) *protocol.Response {
		if req.Action == "shutdown" {
			// Close all subsystems and collect results
			termResp := termHandler.HandleRequest(&protocol.Request{Action: "close"})
			browserHandler.HandleRequest(&protocol.Request{Action: "close"})
			debugHandler.HandleRequest(&protocol.Request{Action: "close"})
			serveHandler.HandleRequest(&protocol.Request{Action: "close"})

			// Forward term close results (killed processes) as shutdown data
			result := map[string]any{}
			if termResp != nil && termResp.Data != nil {
				var killed []any
				if json.Unmarshal(termResp.Data, &killed) == nil && len(killed) > 0 {
					result["term"] = killed
				}
			}
			data, _ := json.Marshal(result)
			return &protocol.Response{Success: true, Data: data}
		}

		switch req.Subsystem {
		case "browser":
			return browserHandler.HandleRequest(req)
		case "term":
			return termHandler.HandleRequest(req)
		case "debug":
			return debugHandler.HandleRequest(req)
		case "serve":
			return serveHandler.HandleRequest(req)
		default:
			return &protocol.Response{Success: false, Error: "unknown subsystem: " + req.Subsystem}
		}
	}

	if err := daemon.Serve(session, combined); err != nil {
		log.Fatalf("daemon: %v", err)
	}
}
