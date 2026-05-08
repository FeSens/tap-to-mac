package config

import (
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Snapshot is an immutable container for the active config, accessed via
// atomic load/store so that the matcher can swap configs without locks.
type Snapshot struct {
	v atomic.Pointer[Config]
}

// NewSnapshot creates a snapshot wrapping an initial config.
func NewSnapshot(cfg *Config) *Snapshot {
	s := &Snapshot{}
	s.v.Store(cfg)
	return s
}

// Load returns the currently active config.
func (s *Snapshot) Load() *Config {
	return s.v.Load()
}

// Store atomically replaces the active config.
func (s *Snapshot) Store(cfg *Config) {
	s.v.Store(cfg)
}

// Watch sets up an fsnotify watch on the config file and the templates
// directory in its parent. On any change it calls reloadFn(path) and, if
// reload succeeds, atomically swaps the snapshot. onErr is called when a
// reload fails so the caller can log it; the previous config stays active.
//
// Watch returns a stop function. Closing it stops the watcher.
func Watch(path string, snap *Snapshot, onChange func(*Config), onErr func(error)) (stop func(), err error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	dir := pathDir(path)
	if err := w.Add(dir); err != nil {
		_ = w.Close()
		return nil, err
	}
	templatesDir := dir + "/templates"
	_ = w.Add(templatesDir) // best-effort; ok if missing initially

	done := make(chan struct{})
	go func() {
		var debounce *time.Timer
		fire := func() {
			cfg, err := Load(path)
			if err != nil {
				if onErr != nil {
					onErr(err)
				}
				return
			}
			snap.Store(cfg)
			if onChange != nil {
				onChange(cfg)
			}
		}
		for {
			select {
			case <-done:
				_ = w.Close()
				return
			case _, ok := <-w.Events:
				if !ok {
					return
				}
				if debounce != nil {
					debounce.Stop()
				}
				debounce = time.AfterFunc(100*time.Millisecond, fire)
			case e, ok := <-w.Errors:
				if !ok {
					return
				}
				if onErr != nil {
					onErr(e)
				}
			}
		}
	}()
	return func() { close(done) }, nil
}

func pathDir(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return "."
}
