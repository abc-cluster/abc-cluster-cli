package utils

import (
	"os"
	"time"
)

// EnvOrDefault returns the value of the first environment variable in keys
// that is non-empty, or "" if none are set.
func EnvOrDefault(keys ...string) string {
	for _, key := range keys {
		if v := os.Getenv(key); v != "" {
			return v
		}
	}
	return ""
}

// SleepCh returns a channel that closes after n seconds. Use in select
// statements within polling loops to allow context cancellation.
func SleepCh(n int) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		time.Sleep(time.Duration(n) * time.Second)
		close(ch)
	}()
	return ch
}
