package logbuffer

import (
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

const capacity = 1000

type Entry struct {
	At      time.Time `json:"at"`
	Message string    `json:"message"`
}

type buffer struct {
	mu      sync.RWMutex
	entries []Entry
}

var defaultBuffer = &buffer{}

// Output keeps normal container stdout while retaining a bounded copy for the
// native admin log viewer.
func Output() io.Writer { return io.MultiWriter(os.Stdout, defaultBuffer) }

func (b *buffer) Write(p []byte) (int, error) {
	now := time.Now()
	lines := strings.Split(strings.TrimRight(string(p), "\r\n"), "\n")
	b.mu.Lock()
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			b.entries = append(b.entries, Entry{At: now, Message: line})
		}
	}
	if len(b.entries) > capacity {
		b.entries = append([]Entry(nil), b.entries[len(b.entries)-capacity:]...)
	}
	b.mu.Unlock()
	return len(p), nil
}

func Entries(limit int) []Entry {
	defaultBuffer.mu.RLock()
	defer defaultBuffer.mu.RUnlock()
	if limit <= 0 || limit > capacity {
		limit = capacity
	}
	start := len(defaultBuffer.entries) - limit
	if start < 0 {
		start = 0
	}
	return append([]Entry(nil), defaultBuffer.entries[start:]...)
}
