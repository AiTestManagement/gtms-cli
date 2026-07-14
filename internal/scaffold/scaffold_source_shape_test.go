package scaffold

// Source-shape tests verify that shipped scaffold templates and in-tree adapter
// scripts contain only ASCII bytes (<= 0x7F). BUG-117: the project's CLAUDE.md
// typography rule bans non-ASCII punctuation (em-dashes, box-drawing, arrows)
// because downstream tooling renders them as mojibake. This test catches drift
// on every `go test` run.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestScaffoldAndAdapterSourcesASCIIOnly reads every .go file in
// internal/scaffold/ and every .sh file under adapters/ (relative to the
// project root), and fails if any byte > 0x7F is found.
//
// This is a pure unit test (no os/exec, no git, no shell) -- runs in all tiers
// including `go test -short`.
func TestScaffoldAndAdapterSourcesASCIIOnly(t *testing.T) {
	// Resolve project root: internal/scaffold/ is two levels below project root.
	projectRoot := filepath.Join("..", "..")

	var files []string

	// Collect all .go files in internal/scaffold/
	scaffoldDir := filepath.Join(projectRoot, "internal", "scaffold")
	entries, err := os.ReadDir(scaffoldDir)
	if err != nil {
		t.Fatalf("reading internal/scaffold/: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".go" {
			files = append(files, filepath.Join(scaffoldDir, e.Name()))
		}
	}

	// Collect all .sh files under adapters/ (recursive walk)
	adaptersDir := filepath.Join(projectRoot, "adapters")
	err = filepath.Walk(adaptersDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !info.IsDir() && filepath.Ext(info.Name()) == ".sh" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking adapters/: %v", err)
	}

	assert.NotEmpty(t, files, "expected at least one file to scan")

	var offenders []string
	for _, fpath := range files {
		data, readErr := os.ReadFile(fpath)
		if readErr != nil {
			t.Errorf("reading %s: %v", fpath, readErr)
			continue
		}
		lineNum := 1
		for i, b := range data {
			if b == '\n' {
				lineNum++
				continue
			}
			if b > 0x7F {
				rel, _ := filepath.Rel(projectRoot, fpath)
				offenders = append(offenders, fmt.Sprintf("%s:%d (byte offset %d, value 0x%02x)", rel, lineNum, i, b))
			}
		}
	}

	if len(offenders) > 0 {
		t.Errorf("BUG-117 AC5: shipped scaffold templates and adapter scripts must contain "+
			"only ASCII bytes (<= 0x7F). Found %d non-ASCII byte(s):\n", len(offenders))
		for _, o := range offenders {
			t.Logf("  %s", o)
		}
	}

	t.Logf("scanned %d file(s) for non-ASCII bytes", len(files))
}
