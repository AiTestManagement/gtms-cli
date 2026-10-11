package testcase

import (
	"testing"
)

// This file's TestExists_* suite tested testcase.Exists, the filename existence
// resolver ENH-205 deleted along with the rest of the legacy bare-ID-to-spec
// resolver surface. The behaviours it covered -- unqualified and folder-qualified
// existence, subfolder search, partial-ID non-match, multiple-file resolution --
// are covered against the snapshot by the snapshot_*_test.go suite. The shared
// `require` helper below is retained; it backs testcase_symlink_test.go.

// require is a test helper that fails the test immediately on error.
func require(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
