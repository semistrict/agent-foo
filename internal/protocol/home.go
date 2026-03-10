package protocol

import "os"

func homeDir() (string, error) {
	return os.UserHomeDir()
}
