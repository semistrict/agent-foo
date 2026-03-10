package protocol

import (
	"encoding/json"
	"fmt"
)

// Request is a command sent from CLI to daemon.
type Request struct {
	ID        string          `json:"id"`
	Subsystem string          `json:"subsystem,omitempty"`
	Action    string          `json:"action"`
	Params    json.RawMessage `json:"params,omitempty"`
}

// Response is a result sent from daemon to CLI.
type Response struct {
	ID      string          `json:"id"`
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// SocketDir returns the base directory for session sockets/pids.
func SocketDir() string {
	home, _ := homeDir()
	return home + "/.af"
}

// SocketPath returns the Unix socket path for a session.
func SocketPath(session string) string {
	return fmt.Sprintf("%s/%s.sock", SocketDir(), session)
}

// PidPath returns the PID file path for a session.
func PidPath(session string) string {
	return fmt.Sprintf("%s/%s.pid", SocketDir(), session)
}

// LogPath returns the log file path for a session.
func LogPath(session string) string {
	return fmt.Sprintf("%s/%s.log", SocketDir(), session)
}
