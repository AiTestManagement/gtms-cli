package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFrameworkFlagRegistered verifies that the --framework flag is registered
// on all three visibility commands: status, gaps, and map.
func TestFrameworkFlagRegistered(t *testing.T) {
	statusCmd := newStatusCmd()
	gapsCmd := newGapsCmd()
	mapCmd := newMapCmd()

	cmds := map[string]func(string) bool{
		"status": func(name string) bool { return statusCmd.Flags().Lookup(name) != nil },
		"gaps":   func(name string) bool { return gapsCmd.Flags().Lookup(name) != nil },
		"map":    func(name string) bool { return mapCmd.Flags().Lookup(name) != nil },
	}

	for name, hasFlag := range cmds {
		t.Run(name+" has --framework flag", func(t *testing.T) {
			assert.True(t, hasFlag("framework"), "%s command should have --framework flag", name)
		})
	}

	// Verify default value is empty string
	t.Run("status --framework default is empty", func(t *testing.T) {
		f := statusCmd.Flags().Lookup("framework")
		require.NotNil(t, f)
		assert.Equal(t, "", f.DefValue)
	})

	t.Run("gaps --framework default is empty", func(t *testing.T) {
		f := gapsCmd.Flags().Lookup("framework")
		require.NotNil(t, f)
		assert.Equal(t, "", f.DefValue)
	})

	t.Run("map --framework default is empty", func(t *testing.T) {
		f := mapCmd.Flags().Lookup("framework")
		require.NotNil(t, f)
		assert.Equal(t, "", f.DefValue)
	})

	// Verify help text is consistent
	t.Run("help text consistent across commands", func(t *testing.T) {
		statusUsage := statusCmd.Flags().Lookup("framework").Usage
		gapsUsage := gapsCmd.Flags().Lookup("framework").Usage
		mapUsage := mapCmd.Flags().Lookup("framework").Usage

		assert.Equal(t, statusUsage, gapsUsage, "status and gaps should have same --framework usage")
		assert.Equal(t, statusUsage, mapUsage, "status and map should have same --framework usage")
	})
}
