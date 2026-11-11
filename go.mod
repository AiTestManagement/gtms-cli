module github.com/aitestmanagement/gtms-cli

go 1.26.0

// BUG-205: pin the Windows directory-junction GODEBUG to its pre-Go-1.23 value.
// Go 1.23 changed the winsymlink default to 1, which reports a directory junction
// as an irregular file (not a symlink) and stops filepath.EvalSymlinks following
// it, so GTMS's internal/pathsafe containment checks no longer refuse a junction
// that points outside the project. Pinning winsymlink=0 restores the v0.3.3
// behaviour process-wide. An explicit GODEBUG=winsymlink=1 in the environment
// overrides this and is unsupported for junction-safe operation (BUG-205
// Decision 2). ENH-233 removes the need for this pin before Go retires the setting.
godebug (
	winsymlink=0
)

require (
	github.com/adrg/frontmatter v0.2.0
	github.com/spf13/cobra v1.10.2
	github.com/spf13/pflag v1.0.10
	github.com/stretchr/testify v1.12.1
	golang.org/x/sys v0.48.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/BurntSushi/toml v0.3.1 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	gopkg.in/yaml.v2 v2.3.0 // indirect
)
