package reader

// ScopeInfo describes the resolved scope for a command invocation.
type ScopeInfo struct {
	// ScanDir is the absolute path to scan for test cases.
	// For shallow: use os.ReadDir on this directory.
	// For recursive: use filepath.Walk starting from this directory.
	ScanDir string

	// RelPath is the scope directory relative to project root, using forward slashes.
	// Examples: "test-cases/", "test-cases/login/", "test-cases/login/oauth/"
	RelPath string

	// Recursive is true when the -r flag was passed.
	Recursive bool
}
