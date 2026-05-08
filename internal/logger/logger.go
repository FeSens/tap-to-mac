// Package logger writes structured JSON-lines events to a rotated log file.
package logger

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

const (
	DefaultMaxSizeMB = 10
	DefaultMaxBackup = 5
)

type Logger struct {
	mu  sync.Mutex
	out io.Writer
	cl  io.Closer
}

// NewFile opens a size-rotated JSON-lines logger writing to path.
// Path's parent directory must already exist (or be creatable by the caller).
func NewFile(path string) (*Logger, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("logger: mkdir %s: %w", filepath.Dir(path), err)
	}
	w := &lumberjack.Logger{
		Filename:   path,
		MaxSize:    DefaultMaxSizeMB,
		MaxBackups: DefaultMaxBackup,
		Compress:   true,
	}
	return &Logger{out: w, cl: w}, nil
}

// NewWriter wraps an existing writer (used in tests and for stdout).
func NewWriter(w io.Writer) *Logger {
	return &Logger{out: w}
}

// Close flushes and closes the underlying file if any.
func (l *Logger) Close() error {
	if l.cl != nil {
		return l.cl.Close()
	}
	return nil
}

// Event is a single structured log entry.
type Event struct {
	TS            time.Time `json:"ts"`
	Event         string    `json:"event"`
	BurstSize     int       `json:"burst_size,omitempty"`
	IntervalsMs   []int64   `json:"intervals_ms,omitempty"`
	Matched       string    `json:"matched,omitempty"`
	Command       string    `json:"command,omitempty"`
	ExitCode      *int      `json:"exit_code,omitempty"`
	DurationMs    int64     `json:"duration_ms,omitempty"`
	StderrExcerpt string    `json:"stderr_excerpt,omitempty"`
	Message       string    `json:"message,omitempty"`
	Error         string    `json:"error,omitempty"`
}

// Log writes one event as a JSON line.
func (l *Logger) Log(ev Event) error {
	if ev.TS.IsZero() {
		ev.TS = time.Now().UTC()
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("logger: marshal: %w", err)
	}
	b = append(b, '\n')
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err = l.out.Write(b)
	return err
}

// Info is a convenience for diagnostic messages.
func (l *Logger) Info(msg string) {
	_ = l.Log(Event{Event: "info", Message: msg})
}

// Errorf records an error event.
func (l *Logger) Errorf(format string, args ...any) {
	_ = l.Log(Event{Event: "error", Error: fmt.Sprintf(format, args...)})
}
