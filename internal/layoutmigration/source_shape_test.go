package layoutmigration

// Source-shape tests verify compile-time / code-structure invariants for
// internal/layoutmigration/. Mirrors the pattern established by
// internal/reader/source_shape_test.go (ENH-088).
//
// This file is the only contract committed as part of ENH-164's first
// worktree commit for this package; production source lands in subsequent
// commits and must satisfy the invariants asserted here.

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSourceShape_NoOSExecImport asserts that no non-test .go file in
// internal/layoutmigration/ imports "os/exec".
//
// Why this matters: per ENH-164 § "Migration jobs" AC and § "Investigation
// Notes > Suspected Causes / Blockers", the shared filesystem-native core
// is invoked by both the dev-time dogfood content migration (job b) and the
// user-runtime migration shim (job c). The runtime shim runs on end-user
// installs where GTMS must not be invoking git on the user's working tree
// (and where git may not even be present). The shared core must therefore
// be os.Rename / os.WriteFile only -- no shell-outs to git or any other
// external command. The dev-time job (b) wrapper may add `git mv` calls
// for rename-history preservation, but the wrapper lives outside this
// package; the shared core knows nothing about git.
//
// Implementation: walks the package directory for non-test .go files,
// parses each with go/parser in ImportsOnly mode, and asserts none of the
// import paths equal "os/exec". Test files (*_test.go) are excluded so
// tests retain the freedom to use os/exec for fixture setup if needed --
// the constraint is on the shared core's production surface, not on the
// tests that verify it.
//
// During early implementation on the worktree the package may contain only
// this test file; the test then passes trivially with zero scanned files,
// which is the correct semantics -- the invariant holds vacuously until
// production code arrives.
func TestSourceShape_NoOSExecImport(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err, "reading internal/layoutmigration/ directory")

	fset := token.NewFileSet()
	var scanned int
	var offenders []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		require.NoError(t, parseErr, "parsing %s", name)
		scanned++
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if path == "os/exec" {
				offenders = append(offenders, name)
				break
			}
		}
	}

	t.Logf("scanned %d non-test .go file(s) in internal/layoutmigration/", scanned)

	if len(offenders) > 0 {
		t.Errorf("ENH-164 AC: internal/layoutmigration/ must be filesystem-native; "+
			"the shared core is consumed by the user-runtime migration shim and must not shell out to git or any other external command. "+
			"Files importing os/exec: %v", offenders)
	}
}
