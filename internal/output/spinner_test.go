package output

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSpinner_NonTTY_StaticLine(t *testing.T) {
	var buf bytes.Buffer
	s := NewSpinner(&buf, "Running test-adapter...")
	// IsTTY returns false for bytes.Buffer — non-TTY path
	s.Start()
	out := buf.String()

	assert.Contains(t, out, "●")
	assert.Contains(t, out, "Running test-adapter...")
	assert.True(t, strings.HasSuffix(out, "\n"), "non-TTY output should end with newline")

	// Stop should be a no-op
	s.Stop()
}

func TestSpinner_NonTTY_StopIdempotent(t *testing.T) {
	var buf bytes.Buffer
	s := NewSpinner(&buf, "Running test-adapter...")
	s.Start()
	s.Stop()
	s.Stop() // second stop — no panic
}

func TestSpinner_Animated_WritesFrames(t *testing.T) {
	var buf bytes.Buffer
	s := NewSpinner(&buf, "Running local-claude...")
	s.isTTY = true // override for testing
	s.Start()
	time.Sleep(250 * time.Millisecond)
	s.Stop()

	out := buf.String()
	assert.Contains(t, out, "Running local-claude...", "should contain the message")
	assert.Contains(t, out, "\r", "should contain carriage returns for animation")
}

func TestSpinner_Animated_StopClearsLine(t *testing.T) {
	var buf bytes.Buffer
	s := NewSpinner(&buf, "Running local-claude...")
	s.isTTY = true
	s.Start()
	time.Sleep(250 * time.Millisecond)
	s.Stop()

	out := buf.String()
	// The last write is clearLine: "\r" + 80 spaces + "\r"
	// After Stop(), the buffer should end with the clear pattern
	assert.True(t, strings.HasSuffix(out, "\r"), "output should end with carriage return from clearLine")
}

func TestSpinner_Animated_StopBlocks(t *testing.T) {
	var buf bytes.Buffer
	s := NewSpinner(&buf, "Running local-claude...")
	s.isTTY = true
	s.Start()
	time.Sleep(150 * time.Millisecond)
	s.Stop()

	// After Stop() returns, goroutine has fully exited — no more writes
	lenAfterStop := buf.Len()
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, lenAfterStop, buf.Len(), "no writes should occur after Stop() returns")
}

func TestSpinner_Animated_StartIdempotent(t *testing.T) {
	var buf bytes.Buffer
	s := NewSpinner(&buf, "Running local-claude...")
	s.isTTY = true
	s.Start()
	s.Start() // second start — should not launch another goroutine
	time.Sleep(200 * time.Millisecond)
	s.Stop()
	// No panic, no interleaved output from a double goroutine
}

func TestSpinner_Animated_StopIdempotent(t *testing.T) {
	var buf bytes.Buffer
	s := NewSpinner(&buf, "Running local-claude...")
	s.isTTY = true
	s.Start()
	time.Sleep(150 * time.Millisecond)
	s.Stop()
	s.Stop() // second stop — no panic
}

func TestSpinner_Animated_MessageContent(t *testing.T) {
	var buf bytes.Buffer
	s := NewSpinner(&buf, "Running my-adapter...")
	s.isTTY = true
	s.Start()
	time.Sleep(150 * time.Millisecond)
	s.Stop()

	assert.Contains(t, buf.String(), "Running my-adapter...")
}
