package id

// Source-shape tests verify compile-time / code-structure invariants that were
// previously asserted by BATS acceptance tests grepping Go source files.
// These are architectural guardrails, not behaviour tests.
// See ENH-088 for the full audit and migration rationale.

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Audit #9: id.go uses crypto/rand ---

func TestSourceShape_IDUsesCryptoRand(t *testing.T) {
	src, err := os.ReadFile("id.go")
	require.NoError(t, err)
	content := string(src)

	assert.Contains(t, content, "crypto/rand",
		"id.go must import crypto/rand for cryptographic randomness")
	assert.Contains(t, content, "rand.Read",
		"id.go must call rand.Read for random byte generation")
}

// --- Audit #17: id.go has no [:7] truncation, uses make([]byte, 4) ---

func TestSourceShape_IDNoTruncation(t *testing.T) {
	src, err := os.ReadFile("id.go")
	require.NoError(t, err)
	content := string(src)

	assert.NotContains(t, content, "[:7]",
		"id.go must not truncate to 7 chars — IDs are 8 hex chars")
}

func TestSourceShape_IDAllocates4Bytes(t *testing.T) {
	src, err := os.ReadFile("id.go")
	require.NoError(t, err)
	content := string(src)

	assert.Contains(t, content, "make([]byte, 4)",
		"id.go must allocate exactly 4 random bytes (= 8 hex chars)")
}

func TestSourceShape_IDNoMathRand(t *testing.T) {
	src, err := os.ReadFile("id.go")
	require.NoError(t, err)
	content := string(src)

	assert.False(t, strings.Contains(content, "math/rand"),
		"id.go must not import math/rand — use crypto/rand only")
}
