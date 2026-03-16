package output

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// Spinner provides animated progress feedback on stderr during long operations.
// It writes carriage-return-based animation frames to the given writer.
// Safe for concurrent use — Start and Stop may be called from different goroutines.
// A Spinner is single-use: after Stop(), create a new Spinner rather than calling Start() again.
type Spinner struct {
	mu      sync.Mutex
	writer  io.Writer
	message string
	active  bool
	isTTY   bool
	stopCh  chan struct{}
	doneCh  chan struct{}
	start   time.Time
}

// spinner animation frames: half-circle rotation effect.
// ● and ○ are existing icon constants; ◐ and ◑ are in the same Unicode block.
var spinnerFrames = []string{"●", "◐", "○", "◑"}

// NewSpinner creates a spinner that writes to w with the given message.
// It does not start the spinner — call Start() explicitly.
func NewSpinner(w io.Writer, message string) *Spinner {
	return &Spinner{
		writer:  w,
		message: message,
		isTTY:   IsTTY(w),
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
}

// Start begins the spinner animation. If stderr is not a TTY, writes a single
// static line and returns without starting a goroutine. Idempotent.
func (s *Spinner) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.active {
		return
	}
	s.active = true
	s.start = time.Now()

	if !s.isTTY {
		fmt.Fprintf(s.writer, "  ● %s\n", s.message)
		return
	}

	go func() {
		defer close(s.doneCh)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		i := 0
		for {
			select {
			case <-s.stopCh:
				s.clearLine()
				return
			case <-ticker.C:
				frame := spinnerFrames[i%len(spinnerFrames)]
				elapsed := time.Since(s.start).Truncate(time.Second)
				fmt.Fprintf(s.writer, "\r  %s %s %s", frame, s.message, elapsed)
				i++
			}
		}
	}()
}

// Stop halts the spinner and clears the line. Blocks until the animation
// goroutine has fully exited. Idempotent.
func (s *Spinner) Stop() {
	s.mu.Lock()
	if !s.active {
		s.mu.Unlock()
		return
	}
	s.active = false

	if !s.isTTY {
		s.mu.Unlock()
		return
	}

	s.mu.Unlock()
	close(s.stopCh)
	<-s.doneCh
}

// clearLine overwrites the current line with spaces and returns cursor to start.
// Cross-platform safe — no ANSI escape codes.
func (s *Spinner) clearLine() {
	fmt.Fprintf(s.writer, "\r%-80s\r", "")
}
