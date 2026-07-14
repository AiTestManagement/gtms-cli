package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGuideURL_RefFromVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    string
	}{
		{"dev default", "dev", "main"},
		{"tagged release", "v0.1.0", "v0.1.0"},
		{"tagged patch", "v0.2.1", "v0.2.1"},
		{"pre-release tag", "v1.0.0-rc.1", "v1.0.0-rc.1"},
		{"arbitrary non-tag", "custom-build", "main"},
		{"empty string", "", "main"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			saved := Version
			defer func() { Version = saved }()
			Version = tc.version
			assert.Equal(t, tc.want, refFromVersion())
		})
	}
}

func TestGuideURL_WithAnchor(t *testing.T) {
	saved := Version
	defer func() { Version = saved }()
	Version = "dev"

	got := guideURL("USER-GUIDE.md", "adapter-execution-model")
	assert.Equal(t,
		"https://github.com/aitestmanagement/gtms-cli/blob/main/USER-GUIDE.md#adapter-execution-model",
		got)
}

func TestGuideURL_WithoutAnchor(t *testing.T) {
	saved := Version
	defer func() { Version = saved }()
	Version = "v0.2.0"

	got := guideURL("reference/adapter-guide.md", "")
	assert.Equal(t,
		"https://github.com/aitestmanagement/gtms-cli/blob/v0.2.0/reference/adapter-guide.md",
		got)
}

func TestGuideURL_TaggedBuildFlipsRef(t *testing.T) {
	saved := Version
	defer func() { Version = saved }()
	Version = "v0.1.0"

	got := guideURL("USER-GUIDE.md", "adapter-execution-model")
	assert.Contains(t, got, "/blob/v0.1.0/",
		"tagged build should use the tag as the ref")
	assert.NotContains(t, got, "/blob/main/",
		"tagged build should not use main as the ref")
}
