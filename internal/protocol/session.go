package protocol

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultSession returns a session name scoped to the closest enclosing
// git repository. Falls back to "default" if not inside a git repo.
func DefaultSession() string {
	dir, err := os.Getwd()
	if err != nil {
		return "default"
	}
	root := findGitRoot(dir)
	if root == "" {
		return "default"
	}
	// Use a short hash of the absolute path to keep socket names sane
	h := sha256.Sum256([]byte(root))
	return fmt.Sprintf("%x", h[:8])
}

func findGitRoot(dir string) string {
	for {
		if info, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			if info.IsDir() {
				return dir
			}
			// .git can be a file (worktree), still counts
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
