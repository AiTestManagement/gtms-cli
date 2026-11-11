package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeExecuteConfig writes a minimal valid gtms.config with an optional
// top-level execute block and returns the file path.
func writeExecuteConfig(t *testing.T, executeBlock string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "gtms.config")
	body := "project:\n  name: Test\n  repo: org/test\n"
	if executeBlock != "" {
		body += executeBlock
	}
	require.NoError(t, os.WriteFile(path, []byte(body), 0644))
	return path
}

func TestOnScriptChange_AbsentDefaultsToAccept(t *testing.T) {
	cfg, err := LoadFromFile(writeExecuteConfig(t, ""))
	require.NoError(t, err)
	assert.Nil(t, cfg.Execute, "absent execute block stays nil")
	assert.Equal(t, OnScriptChangeAccept, cfg.OnScriptChange())
}

func TestOnScriptChange_ExplicitAccept(t *testing.T) {
	cfg, err := LoadFromFile(writeExecuteConfig(t, "execute:\n  on-script-change: accept\n"))
	require.NoError(t, err)
	assert.Equal(t, OnScriptChangeAccept, cfg.OnScriptChange())
}

func TestOnScriptChange_Refuse(t *testing.T) {
	cfg, err := LoadFromFile(writeExecuteConfig(t, "execute:\n  on-script-change: refuse\n"))
	require.NoError(t, err)
	assert.Equal(t, OnScriptChangeRefuse, cfg.OnScriptChange())
}

// tc-1247a26d: any unsupported value is a hard config error that names both the
// setting and the offending value -- it must not silently fall back to accept.
func TestOnScriptChange_InvalidValuesRejected(t *testing.T) {
	for _, bad := range []string{"record", "ignore"} {
		t.Run(bad, func(t *testing.T) {
			_, err := LoadFromFile(writeExecuteConfig(t, "execute:\n  on-script-change: "+bad+"\n"))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "on-script-change", "error must name the setting")
			assert.Contains(t, err.Error(), bad, "error must name the offending value")
		})
	}
}

// A nil receiver and an empty-string value both resolve to accept.
func TestOnScriptChange_NilAndEmpty(t *testing.T) {
	var c *Config
	assert.Equal(t, OnScriptChangeAccept, c.OnScriptChange())
	c = &Config{Execute: &ExecuteConfig{OnScriptChange: ""}}
	assert.Equal(t, OnScriptChangeAccept, c.OnScriptChange())
}
