package serve

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/semistrict/agent-foo/internal/protocol"
)

type server struct {
	Label    string `json:"label"`
	Target   string `json:"target"`
	URL      string `json:"url"`
	listener net.Listener
}

type Handler struct {
	mu      sync.Mutex
	servers map[string]*server
}

func NewHandler() *Handler {
	return &Handler{servers: map[string]*server{}}
}

func (h *Handler) HandleRequest(req *protocol.Request) *protocol.Response {
	if req.Action == "close" {
		return h.doCloseAll()
	}

	var p map[string]string
	if req.Params != nil {
		json.Unmarshal(req.Params, &p)
	}
	if p == nil {
		p = map[string]string{}
	}

	switch req.Action {
	case "start":
		return h.doStart(p)
	case "stop":
		return h.doStop(p)
	case "list":
		return h.doList()
	default:
		return &protocol.Response{Success: false, Error: "unknown action: " + req.Action}
	}
}

func (h *Handler) doStart(p map[string]string) *protocol.Response {
	target := p["target"]
	if target == "" {
		return &protocol.Response{Success: false, Error: "target is required"}
	}

	abs, err := filepath.Abs(target)
	if err != nil {
		return &protocol.Response{Success: false, Error: fmt.Sprintf("resolve path: %v", err)}
	}

	info, err := os.Stat(abs)
	if err != nil {
		return &protocol.Response{Success: false, Error: fmt.Sprintf("stat: %v", err)}
	}

	addr := ":0"
	if port := p["port"]; port != "" {
		addr = ":" + port
	}

	var handler http.Handler
	if info.IsDir() {
		handler = http.FileServer(http.Dir(abs))
	} else {
		dir := filepath.Dir(abs)
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" || r.URL.Path == "/"+filepath.Base(abs) {
				http.ServeFile(w, r, abs)
				return
			}
			http.FileServer(http.Dir(dir)).ServeHTTP(w, r)
		})
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return &protocol.Response{Success: false, Error: fmt.Sprintf("listen: %v", err)}
	}

	port := ln.Addr().(*net.TCPAddr).Port
	url := fmt.Sprintf("http://localhost:%d/", port)
	if !info.IsDir() {
		url += filepath.Base(abs)
	}

	label := p["label"]
	if label == "" {
		label = filepath.Base(abs)
	}

	h.mu.Lock()
	// Stop existing server with same label
	if old, ok := h.servers[label]; ok {
		old.listener.Close()
	}
	srv := &server{
		Label:    label,
		Target:   abs,
		URL:      url,
		listener: ln,
	}
	h.servers[label] = srv
	h.mu.Unlock()

	go func() {
		http.Serve(ln, handler)
		h.mu.Lock()
		if h.servers[label] == srv {
			delete(h.servers, label)
		}
		h.mu.Unlock()
	}()

	data, _ := json.Marshal(map[string]any{
		"label": label,
		"url":   url,
		"port":  port,
	})
	return &protocol.Response{Success: true, Data: data}
}

func (h *Handler) doStop(p map[string]string) *protocol.Response {
	label := p["label"]
	if label == "" {
		return &protocol.Response{Success: false, Error: "label is required"}
	}

	h.mu.Lock()
	srv, ok := h.servers[label]
	if ok {
		srv.listener.Close()
		delete(h.servers, label)
	}
	h.mu.Unlock()

	if !ok {
		return &protocol.Response{Success: false, Error: "no server with label: " + label}
	}
	data, _ := json.Marshal("Stopped " + label)
	return &protocol.Response{Success: true, Data: data}
}

func (h *Handler) doList() *protocol.Response {
	h.mu.Lock()
	var list []map[string]string
	for _, srv := range h.servers {
		list = append(list, map[string]string{
			"label":  srv.Label,
			"target": srv.Target,
			"url":    srv.URL,
		})
	}
	h.mu.Unlock()

	data, _ := json.Marshal(list)
	return &protocol.Response{Success: true, Data: data}
}

func (h *Handler) doCloseAll() *protocol.Response {
	h.mu.Lock()
	for _, srv := range h.servers {
		srv.listener.Close()
	}
	h.servers = map[string]*server{}
	h.mu.Unlock()

	data, _ := json.Marshal("All servers stopped")
	return &protocol.Response{Success: true, Data: data}
}
