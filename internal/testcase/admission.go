package testcase

import (
	"os"
	"path/filepath"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
)

// IsReservedCompanion reports whether logicalBase is an exact reserved
// testcase companion name. The comparison is deliberately case-sensitive.
func IsReservedCompanion(logicalBase string) bool {
	return logicalBase == "index.md" || logicalBase == "log.md"
}

// IsReservedCasesCompanionPath reports whether a project-relative artifact is
// an exact reserved companion at any level beneath the configured cases tree.
// Containment is decided from the resolved logical parent; a leaf symlink's
// target does not change the result.
func IsReservedCasesCompanionPath(projectRoot, artifactRel string) bool {
	if artifactRel == "" || filepath.IsAbs(artifactRel) || !IsReservedCompanion(filepath.Base(artifactRel)) {
		return false
	}

	casesPath, _, err := pathsafe.ResolveUnderRoot(projectRoot, layout.Current().TestCases)
	if err != nil {
		return false
	}
	parentPath, _, err := pathsafe.ResolveUnderRoot(projectRoot, filepath.Dir(artifactRel))
	if err != nil {
		return false
	}

	casesInfo, err := os.Stat(casesPath)
	if err != nil {
		return false
	}
	for ancestor := parentPath; ; ancestor = filepath.Dir(ancestor) {
		ancestorInfo, err := os.Stat(ancestor)
		if err != nil {
			return false
		}
		if os.SameFile(ancestorInfo, casesInfo) {
			return true
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return false
		}
	}
}
