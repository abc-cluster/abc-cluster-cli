package pipeline

import "os"

// readFile reads the full content of a file and returns it as a byte slice.
func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
