//go:build darwin

// Package hid wires up apple-silicon-accelerometer to produce a stream of
// raw vibration events (with time + amplitude). All IOKit work happens
// inside the upstream library; this is a thin adapter.
package hid

import (
	"context"
	"errors"
	"fmt"
	"time"

	upDet "github.com/taigrr/apple-silicon-accelerometer/detector"
	"github.com/taigrr/apple-silicon-accelerometer/sensor"
	"github.com/taigrr/apple-silicon-accelerometer/shm"
)

// Event is one raw vibration event from the upstream detector. It is the
// input to our package detector.Filter.
type Event struct {
	Time      time.Time
	Amplitude float64
	Severity  string // upstream label (e.g. "light", "moderate", "heavy"); diagnostic only
}

// Source is a long-lived IMU reader. Start launches the sensor goroutine
// and the polling goroutine; Events delivers raw events; Close shuts down.
type Source struct {
	PollInterval time.Duration
	MaxBatch     int

	ring     *shm.RingBuffer
	det      *upDet.Detector
	events   chan Event
	stop     chan struct{}
	stopped  chan struct{}
	sensorEr chan error

	// attached means another process owns the shm + sensor; we just read.
	// When true, Close skips Unlink to avoid breaking the writer.
	attached bool
}

const (
	defaultPoll     = 10 * time.Millisecond
	defaultMaxBatch = 200
)

// Open creates the shared-memory ring, starts the sensor in a background
// goroutine, and returns a Source ready for Start().
//
// Only one process can hold the IOKit HID handles at a time on macOS. If a
// daemon is already running, use OpenAttached instead.
func Open() (*Source, error) {
	ring, err := shm.CreateRing(shm.NameAccel)
	if err != nil {
		return nil, fmt.Errorf("hid: create shm: %w", err)
	}
	src := &Source{
		PollInterval: defaultPoll,
		MaxBatch:     defaultMaxBatch,
		ring:         ring,
		det:          upDet.New(),
		events:       make(chan Event, 256),
		stop:         make(chan struct{}),
		stopped:      make(chan struct{}),
		sensorEr:     make(chan error, 1),
	}
	go func() {
		if err := sensor.Run(sensor.Config{AccelRing: ring}); err != nil {
			src.sensorEr <- err
		}
	}()
	// Give the sensor a moment to start producing data.
	time.Sleep(100 * time.Millisecond)
	return src, nil
}

// OpenAttached opens the shm ring read-only and drives the upstream
// detector locally — without starting our own sensor. Use when another
// process (typically the LaunchDaemon) is already writing to the ring,
// so we don't fight it for IOKit HID access.
//
// The shm segment is created by the daemon under root; readers must also
// be root to open it.
func OpenAttached() (*Source, error) {
	ring, err := shm.OpenRing(shm.NameAccel)
	if err != nil {
		return nil, fmt.Errorf("hid: open shm read-only: %w", err)
	}
	return &Source{
		PollInterval: defaultPoll,
		MaxBatch:     defaultMaxBatch,
		ring:         ring,
		det:          upDet.New(),
		events:       make(chan Event, 256),
		stop:         make(chan struct{}),
		stopped:      make(chan struct{}),
		sensorEr:     make(chan error, 1),
		attached:     true,
	}, nil
}

// IsAttached reports whether the source is reading another process's shm.
func (s *Source) IsAttached() bool { return s.attached }

// Events returns the raw event channel. Closed after Close() drains.
func (s *Source) Events() <-chan Event { return s.events }

// SensorError returns a channel that delivers any error from the sensor
// worker goroutine.
func (s *Source) SensorError() <-chan error { return s.sensorEr }

// Start spins up the polling loop. ctx cancellation is honored alongside
// Close().
func (s *Source) Start(ctx context.Context) {
	go s.loop(ctx)
}

// Close stops polling and frees the shared-memory segment. Safe to call
// multiple times.
func (s *Source) Close() error {
	select {
	case <-s.stop:
		// already stopped
	default:
		close(s.stop)
	}
	<-s.stopped
	if s.ring != nil {
		_ = s.ring.Close()
		// Only unlink if we own the segment. Otherwise we'd remove the
		// daemon's shm name from under it, breaking its restart cycle.
		if !s.attached {
			_ = s.ring.Unlink()
		}
		s.ring = nil
	}
	return nil
}

func (s *Source) loop(ctx context.Context) {
	defer close(s.stopped)
	defer close(s.events)

	ticker := time.NewTicker(s.PollInterval)
	defer ticker.Stop()

	var lastTotal uint64
	lastEventTime := time.Time{}

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case <-ticker.C:
		}

		samples, total := s.ring.ReadNew(lastTotal, shm.AccelScale)
		lastTotal = total
		if len(samples) == 0 {
			continue
		}
		if len(samples) > s.MaxBatch {
			samples = samples[len(samples)-s.MaxBatch:]
		}

		now := time.Now()
		tNow := float64(now.UnixNano()) / 1e9
		n := len(samples)
		fs := float64(s.det.FS)
		for idx, sm := range samples {
			t := tNow - float64(n-idx-1)/fs
			s.det.Process(sm.X, sm.Y, sm.Z, t)
		}
		// Drain any new events the upstream detector appended.
		for _, ev := range s.det.Events {
			if !ev.Time.After(lastEventTime) {
				continue
			}
			lastEventTime = ev.Time
			select {
			case s.events <- Event{Time: ev.Time, Amplitude: ev.Amplitude, Severity: ev.Severity}:
			case <-s.stop:
				return
			case <-ctx.Done():
				return
			}
		}
	}
}

// CheckRoot returns an error if the process is not running as root.
// The IOKit HID access path requires root.
func CheckRoot(geteuid func() int) error {
	if geteuid() != 0 {
		return errors.New("tap-to-mac requires root for IOKit HID access; run with sudo")
	}
	return nil
}
