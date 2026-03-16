package id

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_Length(t *testing.T) {
	id := New()
	assert.Len(t, id, 7, "ID should be exactly 7 characters")
}

func TestNew_HexChars(t *testing.T) {
	id := New()
	matched, err := regexp.MatchString("^[0-9a-f]{7}$", id)
	require.NoError(t, err)
	assert.True(t, matched, "ID should contain only lowercase hex characters, got: %s", id)
}

func TestNew_Unique(t *testing.T) {
	id1 := New()
	id2 := New()
	assert.NotEqual(t, id1, id2, "Two consecutive IDs should be different")
}

func TestNew_MultipleCalls(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := New()
		assert.Len(t, id, 7)
		assert.False(t, seen[id], "Duplicate ID generated: %s", id)
		seen[id] = true
	}
}
