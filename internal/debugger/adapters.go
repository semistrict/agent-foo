package debugger

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	jsDebugRepo       = "microsoft/vscode-js-debug"
	jsDebugEntrypoint = "js-debug/src/dapDebugServer.js"
)

// adaptersDir returns ~/.af/adapters.
func adaptersDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".af", "adapters")
}

// adapterForProgram returns the adapter name based on program file extension.
func adapterForProgram(program string) string {
	switch strings.ToLower(filepath.Ext(program)) {
	case ".js", ".ts", ".mjs", ".cjs", ".mts", ".cts":
		return "js-debug"
	case ".go":
		return "dlv dap"
	case ".py":
		return "debugpy"
	default:
		return ""
	}
}

// resolveAdapter checks if the adapter string is a known adapter name
// and returns the resolved command parts + whether it uses TCP.
// If it's not a known adapter, returns nil (caller should treat it as a raw command).
func resolveAdapter(name string) (*resolvedAdapter, error) {
	switch name {
	case "js-debug":
		return resolveJSDebug()
	case "debugpy":
		return resolveDebugpy()
	default:
		return nil, nil
	}
}

type resolvedAdapter struct {
	// Command parts to exec (e.g. ["node", "/path/to/dapDebugServer.js"])
	Command []string
	// If true, a port arg is appended and the adapter listens on TCP
	TCP bool
	// Default launch args to merge (lower priority than user-supplied launchArgs)
	DefaultLaunchArgs map[string]any
}

// resolveJSDebug ensures vscode-js-debug is downloaded and returns its command.
func resolveJSDebug() (*resolvedAdapter, error) {
	version, downloadURL, err := fetchJSDebugLatest()
	if err != nil {
		return nil, fmt.Errorf("fetch js-debug release info: %w", err)
	}

	dir := filepath.Join(adaptersDir(), "js-debug", version)
	entrypoint := filepath.Join(dir, jsDebugEntrypoint)

	defaults := map[string]any{
		"type": "pwa-node",
	}

	if _, err := os.Stat(entrypoint); err == nil {
		return &resolvedAdapter{
			Command:           []string{"node", entrypoint},
			TCP:               true,
			DefaultLaunchArgs: defaults,
		}, nil
	}

	// Download and extract
	fmt.Fprintf(os.Stderr, "Downloading js-debug %s...\n", version)
	if err := downloadAndExtract(downloadURL, dir); err != nil {
		return nil, fmt.Errorf("download js-debug: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Installed js-debug %s\n", version)

	return &resolvedAdapter{
		Command:           []string{"node", entrypoint},
		TCP:               true,
		DefaultLaunchArgs: defaults,
	}, nil
}

// resolveDebugpy returns the command to launch debugpy as a stdio DAP adapter.
func resolveDebugpy() (*resolvedAdapter, error) {
	return &resolvedAdapter{
		Command: []string{"python3", "-m", "debugpy.adapter"},
		TCP:     false,
		DefaultLaunchArgs: map[string]any{
			"type":    "python",
			"console": "internalConsole",
		},
	}, nil
}

// fetchJSDebugLatest returns the latest version tag and the download URL
// for the js-debug-dap tarball.
func fetchJSDebugLatest() (version string, url string, err error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", jsDebugRepo)
	resp, err := http.Get(apiURL)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", "", err
	}

	for _, a := range release.Assets {
		if strings.HasPrefix(a.Name, "js-debug-dap-") && strings.HasSuffix(a.Name, ".tar.gz") {
			return release.TagName, a.BrowserDownloadURL, nil
		}
	}
	return "", "", fmt.Errorf("no js-debug-dap tarball found in release %s", release.TagName)
}

// downloadAndExtract downloads a .tar.gz from url and extracts it into dir.
func downloadAndExtract(url, dir string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("download returned %d", resp.StatusCode)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(dir, hdr.Name)

		// Prevent path traversal
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(dir)+string(os.PathSeparator)) {
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			os.MkdirAll(target, 0o755)
		case tar.TypeReg:
			os.MkdirAll(filepath.Dir(target), 0o755)
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}
	return nil
}
