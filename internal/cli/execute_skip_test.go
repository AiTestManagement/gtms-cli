package cli

import (
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/stretchr/testify/assert"
)

func TestSkipIcon(t *testing.T) {
	tests := []struct {
		name     string
		reason   string
		wantIcon string
	}{
		{
			name:     "already passing shows complete icon",
			reason:   "already passing",
			wantIcon: output.IconComplete,
		},
		{
			name:     "no automation record shows warning icon",
			reason:   "no automation record",
			wantIcon: output.IconWarning,
		},
		{
			name:     "automation not ready shows warning icon",
			reason:   "automation not ready",
			wantIcon: output.IconWarning,
		},
		{
			name:     "active task exists shows warning icon",
			reason:   "active task exists",
			wantIcon: output.IconWarning,
		},
		{
			name:     "test skipped shows skipped icon",
			reason:   "test skipped",
			wantIcon: output.IconSkipped,
		},
		{
			name:     "unknown reason shows warning icon",
			reason:   "some unknown reason",
			wantIcon: output.IconWarning,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := skipIcon(tt.reason)
			assert.Equal(t, tt.wantIcon, got)
		})
	}
}
