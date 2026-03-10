package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	godaemon "github.com/sevlyar/go-daemon"

	"github.com/semistrict/agent-foo/internal/protocol"
)

// Handler processes a request and returns a response.
type Handler func(req *protocol.Request) *protocol.Response

// Serve starts the daemon server for a given session.
// This is called in the daemon (child) process.
func Serve(session string, handler Handler) error {
	sockPath := protocol.SocketPath(session)

	// Clean up stale socket
	if _, err := os.Stat(sockPath); err == nil {
		os.Remove(sockPath)
	}

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer ln.Close()
	defer os.Remove(sockPath)
	defer os.Remove(protocol.PidPath(session))
	defer os.Remove(protocol.LogPath(session))

	// Graceful shutdown on signal
	shutdown := make(chan struct{})
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
		case <-shutdown:
		}
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-shutdown:
				return nil
			default:
				return nil // listener closed
			}
		}
		go handleConn(conn, handler, shutdown)
	}
}

func handleConn(conn net.Conn, handler Handler, shutdown chan struct{}) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	// Allow large messages (1MB)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		var req protocol.Request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			resp := &protocol.Response{Success: false, Error: "invalid request: " + err.Error()}
			writeResponse(conn, resp)
			continue
		}

		resp := handler(&req)
		resp.ID = req.ID
		writeResponse(conn, resp)

		// "shutdown" action triggers daemon shutdown
		if req.Action == "shutdown" {
			close(shutdown)
			return
		}
	}
}

func writeResponse(conn net.Conn, resp *protocol.Response) {
	data, _ := json.Marshal(resp)
	data = append(data, '\n')
	conn.Write(data)
}

// EnsureDaemon starts the daemon if not already running.
// Returns nil when the daemon is ready to accept connections.
func EnsureDaemon(session string, extraEnv []string) error {
	sockPath := protocol.SocketPath(session)

	// Try connecting to existing daemon
	if conn, err := net.Dial("unix", sockPath); err == nil {
		conn.Close()
		return nil
	}

	// Ensure socket dir exists
	dir := protocol.SocketDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	// Spawn daemon
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("executable: %w", err)
	}

	env := append(os.Environ(),
		"AF_DAEMON=1",
		fmt.Sprintf("AF_SESSION=%s", session),
	)
	env = append(env, extraEnv...)

	ctx := &godaemon.Context{
		PidFileName: protocol.PidPath(session),
		PidFilePerm: 0644,
		LogFileName: protocol.LogPath(session),
		LogFilePerm: 0640,
		WorkDir:     "/",
		Umask:       027,
		Args:        []string{exe, "__daemon"},
		Env:         env,
	}

	child, err := ctx.Reborn()
	if err != nil {
		return fmt.Errorf("spawn daemon: %w", err)
	}
	if child != nil {
		// Parent: wait for daemon to be ready
		return waitForSocket(sockPath)
	}
	// Child: unreachable here because we use ctx.Reborn() only from CLI
	return nil
}

func waitForSocket(sockPath string) error {
	for range 50 {
		conn, err := net.Dial("unix", sockPath)
		if err == nil {
			conn.Close()
			return nil
		}
		// Small busy-wait — daemon is starting up
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not start within 5s")
}

// SendCommand sends a request to the daemon and returns the response.
func SendCommand(session string, req *protocol.Request) (*protocol.Response, error) {
	sockPath := protocol.SocketPath(session)
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("connect to daemon: %w", err)
	}
	defer conn.Close()

	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("read: %w", err)
		}
		return nil, fmt.Errorf("daemon closed connection")
	}

	var resp protocol.Response
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &resp, nil
}

// ListSessions returns the names of active sessions.
func ListSessions() ([]string, error) {
	dir := protocol.SocketDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var sessions []string
	for _, e := range entries {
		name := e.Name()
		if len(name) > 5 && name[len(name)-5:] == ".sock" {
			sessName := name[:len(name)-5]
			// Check if actually reachable
			sockPath := protocol.SocketPath(sessName)
			conn, err := net.Dial("unix", sockPath)
			if err == nil {
				conn.Close()
				sessions = append(sessions, sessName)
			} else {
				// Stale socket, clean up
				os.Remove(sockPath)
				os.Remove(protocol.PidPath(sessName))
			}
		}
	}
	return sessions, nil
}
