package reader

// BUG-210 parity guard: the reader keeps a local copy of the four reserved
// Mode 3 execute adapter names (isReservedMode3ExecuteAdapter in status.go)
// because internal/adapter imports internal/reader, so reader cannot import
// adapter without a cycle. This source-shape test asserts the reader's list
// stays identical to the authoritative list in internal/adapter/mode3.go
// (IsMode3ExecuteAdapterName), so the two cannot drift apart silently.

import (
	"os"
	"regexp"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSourceShape_ReservedMode3NamesMatchAdapterPackage(t *testing.T) {
	src, err := os.ReadFile("../adapter/mode3.go")
	require.NoError(t, err, "must be able to read the authoritative adapter/mode3.go")

	// Extract every `name == "..."` literal inside IsMode3ExecuteAdapterName.
	// The function is the first block of such comparisons in the file.
	re := regexp.MustCompile(`name == "([a-z0-9-]+)"`)
	matches := re.FindAllStringSubmatch(string(src), -1)
	require.NotEmpty(t, matches, "could not find the reserved-name comparisons in adapter/mode3.go")

	authoritative := make([]string, 0, len(matches))
	for _, m := range matches {
		authoritative = append(authoritative, m[1])
	}
	sort.Strings(authoritative)

	want := []string{"agent-execute", "agent-execute-script", "manual-execute", "manual-execute-script"}
	assert.Equal(t, want, authoritative,
		"adapter/mode3.go reserved names changed -- update reader's isReservedMode3ExecuteAdapter to match")

	// The reader's local predicate must agree with that exact set.
	for _, name := range authoritative {
		assert.True(t, isReservedMode3ExecuteAdapter(name),
			"reader isReservedMode3ExecuteAdapter must recognise %q", name)
	}
	assert.False(t, isReservedMode3ExecuteAdapter("bats-runner"),
		"reader isReservedMode3ExecuteAdapter must reject a non-reserved adapter")
}
