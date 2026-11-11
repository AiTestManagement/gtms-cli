package scaffold

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
	"gopkg.in/yaml.v3"
)

// GitignoreAction describes what ensureGitignore did to .gitignore.
type GitignoreAction string

const (
	// GitignoreCreated means .gitignore was absent and was created with .gtms/.
	GitignoreCreated GitignoreAction = "created"
	// GitignoreAppended means .gitignore existed but lacked .gtms/; the entry was appended.
	GitignoreAppended GitignoreAction = "appended"
	// GitignoreUnchanged means .gitignore already contained .gtms/; no modification was needed.
	GitignoreUnchanged GitignoreAction = "unchanged"
)

// Options configures the Init scaffolding operation.
type Options struct {
	ProjectRoot string // absolute path to the directory to scaffold in
	Name        string // project name for config
	Repo        string // repository path (e.g. "org/repo")
	Preset      string // adapter preset: "minimal", "claude", "github"
	Force       bool   // overwrite existing gtms.config if present
}

// Result describes what Init created.
type Result struct {
	ConfigPath      string          // path to the generated gtms.config
	DirsCreated     []string        // directories created (relative to project root)
	DirsRefused     []string        // BUG-213: directory destinations refused because they resolve outside the project
	FilesCreated    []string        // files created (relative to project root)
	FilesSkipped    []string        // files not written because they already existed
	FilesRefused    []string        // BUG-213: destinations (files or directories) refused because they resolve outside the project or are non-regular; never listed under Created
	FilesRestored   []string        // files restored/reconstructed in a pre-existing gtms/ directory
	Warnings        []string        // ENH-135: actionable warning messages (rendered with ! glyph)
	Notes           []string        // ENH-135: informational notes (rendered with "Note:" prefix, no glyph)
	GitignoreAction GitignoreAction // what happened to .gitignore (S7/S8/S9)
	ConfigBackup    string          // BUG-203: project-relative name of the gtms.config backup --force took before rewriting (empty if none)
	Parent          string          // BUG-207: the active GTMS parent folder scaffolded into ("gtms" for default layout, e.g. "testing" when renamed)
}

// Init scaffolds a GTMS project in the given directory.
// It creates directories, writes a config file, and optionally
// writes prompt templates and adapter stubs based on the preset.
func Init(opts Options) (*Result, error) {
	if !IsValidPreset(opts.Preset) {
		return nil, fmt.Errorf("unknown preset '%s'. Valid presets: %s", opts.Preset, strings.Join(ValidPresets(), ", "))
	}

	// Check for existing config. REV-209 R3-CODEX-001: use Lstat (no symlink follow) so
	// a symlinked gtms.config is detected as the symlink it is, not its target.
	configPath := filepath.Join(opts.ProjectRoot, "gtms.config")
	cfgInfo, cfgStatErr := os.Lstat(configPath)
	configPreExisted := cfgStatErr == nil
	if configPreExisted && !opts.Force {
		return nil, fmt.Errorf("gtms.config already exists in this directory")
	}
	// REV-209 R3-CODEX-001: under --force, an existing gtms.config that is a symlink (or any
	// other non-regular file) must not be backed up or rewritten -- reading would copy an
	// external target into a project-local backup, and rewriting would follow the link and
	// mutate a file outside the project. Abort BEFORE any backup or rewrite. Both the
	// --preset path and the --demo path reach this (the demo flow runs Init first).
	if configPreExisted && opts.Force && !cfgInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("gtms.config is not a regular file (%s); refusing to back up or overwrite it with --force. "+
			"Replace it with a regular file and re-run", cfgInfo.Mode().String())
	}

	// BUG-207 Owner Decision 3: resolve the active GTMS parent folder BEFORE any
	// mutation. In a project whose GTMS folder was renamed (ENH-098), this is the
	// renamed folder; invalid or unsafe states refuse here, before any directory,
	// sentinel, config backup/rewrite or .gitignore change happens.
	parent, err := resolveActiveParent(opts)
	if err != nil {
		return nil, err
	}

	result := &Result{Parent: parent}

	// BUG-207: S3 restore detection is keyed on the ACTIVE parent folder, not the
	// literal "gtms". A renamed parent always pre-exists (it holds the sentinel we
	// just discovered); only a default-layout gtms/ that exists without a sentinel
	// is the S3 restore case.
	parentDir := filepath.Join(opts.ProjectRoot, parent)
	parentPreExisted := false
	if fi, err := os.Stat(parentDir); err == nil && fi.IsDir() {
		parentPreExisted = true
	}

	// BUG-207: a renamed parent (parent != "gtms") drives the renamed-parent
	// ancillary warning below.
	renamedParentName := ""
	if parent != "gtms" {
		renamedParentName = parent
	}

	// Create directories
	dirs, err := createDirectories(opts.ProjectRoot, opts.Preset, parent, result)
	if err != nil {
		return nil, fmt.Errorf("creating directories: %w", err)
	}
	result.DirsCreated = dirs

	// Create sentinel file (.gtms-root) inside the active parent directory.
	// S5/D9: preserve existing non-empty sentinel content (reserved for future use).
	// S3: if sentinel is newly created in a pre-existing parent, track as restored.
	// BUG-213: route the sentinel write through the containment-aware create-only
	// writer. A symlinked .gtms-root is refused (also already caught by
	// ScanParents), and a never-created sentinel is reported restored or created
	// exactly as before. An existing regular sentinel is left untouched (S5).
	sentinelRel := parent + "/.gtms-root"
	sentinelOutcome, sentinelWarn, err := safeCreateOnly(opts.ProjectRoot, sentinelRel, []byte(""), 0o644)
	if err != nil {
		return nil, fmt.Errorf("creating sentinel file: %w", err)
	}
	switch sentinelOutcome {
	case writeCreated:
		if parentPreExisted {
			result.FilesRestored = append(result.FilesRestored, sentinelRel)
		} else {
			result.FilesCreated = append(result.FilesCreated, sentinelRel)
		}
	case writeRefused:
		// A refused sentinel (a symlinked .gtms-root) is reported like every
		// other refused destination (Owner Decision 1). In practice ScanParents
		// rejects a symlinked sentinel earlier, so this is defence in depth.
		result.FilesSkipped = append(result.FilesSkipped, sentinelRel)
		result.FilesRefused = append(result.FilesRefused, sentinelRel)
		result.Warnings = append(result.Warnings, sentinelWarn)
	}
	// writeSkippedExists -> sentinel already present, left untouched (S5).

	// Ensure .gtms/ is listed in .gitignore (idempotent). BUG-207: the computed
	// entry set uses the active parent for the execution rules.
	giAction, err := ensureGitignoreForParent(opts.ProjectRoot, result, parent)
	if err != nil {
		return nil, fmt.Errorf("updating .gitignore: %w", err)
	}
	result.GitignoreAction = giAction

	// BUG-203 Owner Decision 1: before --force rewrites an existing gtms.config, save
	// the user's copy to a backup that never clobbers an earlier one, and name it in the
	// output. The refresh route for a v0.3.x project is gtms init --force, which rewrites
	// gtms.config (that is the flag's main purpose); without this the user's config
	// customisations would be lost with no recovery.
	if opts.Force && configPreExisted {
		// REV-209 R3-CODEX-002: ensure the backup pattern is ignored before writing a
		// backup, even in an existing project whose .gitignore already carries the GTMS
		// sentinel (ensureGitignore leaves such a file untouched for ENH-108 idempotency).
		if err := ensureGitignoreHasConfigBak(opts.ProjectRoot); err != nil {
			return nil, fmt.Errorf("updating .gitignore for config backup: %w", err)
		}
		backupRel, bErr := backupConfigNeverOverwrite(opts.ProjectRoot)
		if bErr != nil {
			return nil, fmt.Errorf("backing up gtms.config: %w", bErr)
		}
		result.ConfigBackup = backupRel
	}

	// Write config. BUG-207 Owner Decision 2: in a renamed-parent project, path
	// values whose canonical first segment is gtms/ are remapped under the active
	// parent, with safe YAML quoting for parents like "qa #1".
	cfgPath, err := writeConfigForParent(opts.ProjectRoot, opts.Name, opts.Repo, opts.Preset, parent)
	if err != nil {
		return nil, fmt.Errorf("writing config: %w", err)
	}
	result.ConfigPath = cfgPath
	result.FilesCreated = append(result.FilesCreated, "gtms.config")

	// ENH-180: Write starter Agent Skills catalog (all presets).
	// Five spec-compliant skills covering the GTMS pipeline workflow,
	// plus a catalog README under gtms/skills/. BUG-203: WriteExampleSkills no longer
	// writes gtms/skills/gtms-tests-create/SKILL.md -- that one joins the common-asset
	// upgrade catalogue below so init --force can refresh a stale v0.3.x copy.
	if err := writeExampleSkillsForParent(opts.ProjectRoot, result, parent); err != nil {
		return nil, fmt.Errorf("writing example skills: %w", err)
	}

	// BUG-111 / ADR-022: Install preset-owned assets (framework-specific files).
	// BATS runner, TAP helper, Playwright runner etc. are no longer unconditional.
	if err := installPresetAssetsForParent(opts.ProjectRoot, opts.Preset, opts.Force, result, parent); err != nil {
		return nil, fmt.Errorf("installing preset assets: %w", err)
	}

	// BUG-203: Install the all-preset scaffolded producers (both testcase templates,
	// both result templates, the create/prime scripts, the authoring guide, the result
	// schema and the gtms-tests-create starter skill) through the shared upgrade path.
	// On a fresh init each is written (absent -> created); under --force an unedited
	// v0.3.x copy is refreshed in place, an edited copy is kept with a .gtms-new
	// companion, and a current copy is skipped. This replaces the former
	// writeFileIfNotExists / direct-write calls (which skipped these under --force, so a
	// v0.3.x project kept emitting the pre-v2 header) and the unconditional
	// WriteStarterGuides overwrite (which silently lost a customised guide).
	if err := installCommonAssets(opts.ProjectRoot, opts.Force, result, parent); err != nil {
		return nil, fmt.Errorf("installing common assets: %w", err)
	}

	// ENH-183: Write agent-instructions snippet (all presets).
	// The durable file is the real deliverable; stdout CTA is a courtesy pointer.
	// BUG-207: destinations use the active parent; the embedded prose stays canonical.
	if err := writeParentFileIfNotExists(opts.ProjectRoot, "gtms/AGENTS-SNIPPET.md", []byte(AgentsSnippetMD), parent, result); err != nil {
		return nil, err
	}

	// ENH-160: Write manual-execute-script adapter (all presets)
	if err := writeParentFileIfNotExists(opts.ProjectRoot, "gtms/adapters/manual-execute-script.sh", []byte(manualExecuteScript), parent, result); err != nil {
		return nil, err
	}

	// ENH-160: Write agent-execute-script adapter (all presets, dormant)
	if err := writeParentFileIfNotExists(opts.ProjectRoot, "gtms/adapters/agent-execute-script.sh", []byte(agentExecuteScript), parent, result); err != nil {
		return nil, err
	}

	// ENH-135: Ship gtms/tasks/.README.md warning file.
	if err := writeParentFileIfNotExists(opts.ProjectRoot, "gtms/tasks/.README.md", []byte(tasksReadmeContent), parent, result); err != nil {
		return nil, err
	}

	// ENH-132/135: Write .vscode/settings.json and extensions.json.
	// ENH-135: Companion-file behaviour -- when the user already has
	// settings.json or extensions.json, write a .snippet companion
	// instead of skipping silently.
	// BUG-213: initVscodeFile classifies each leaf with the Lstat/containment-aware
	// classifier (REV-252 CODEX-002) before choosing a branch, so a live leaf
	// symlink is refused as non-regular instead of silently taking the companion
	// branch; the containment-aware writers also create the contained .vscode/
	// directory themselves, so no direct os.MkdirAll is needed here.

	// BUG-207 Owner Decision 4: a new settings.json and the companion snippet map
	// the schema under the active parent; an existing settings.json is untouched.
	vscodeSettings := vscodeSettingsFor(parent)
	if err := initVscodeFile(opts.ProjectRoot, ".vscode/settings.json", ".vscode/gtms-settings.json.snippet",
		[]byte(vscodeSettings),
		".vscode/settings.json already exists; merge the snippet at "+
			".vscode/gtms-settings.json.snippet into your settings.json to "+
			"enable schema validation.", result); err != nil {
		return nil, err
	}

	if err := initVscodeFile(opts.ProjectRoot, ".vscode/extensions.json", ".vscode/gtms-extensions.json.snippet",
		[]byte(vscodeExtensionsTemplate),
		".vscode/extensions.json already exists; add redhat.vscode-yaml to "+
			"the recommendations array. See .vscode/gtms-extensions.json.snippet "+
			"for the snippet to merge in.", result); err != nil {
		return nil, err
	}

	// ENH-135: Ship VSCode snippet library for manual result authoring.
	// Reverses BUG-027 Finding 1 -- snippets are appropriate now that
	// manual result files are hand-edited YAML (CON-020), not skeleton-generated.
	if err := guardedCreateFile(opts.ProjectRoot, ".vscode/gtms.code-snippets", []byte(vscodeGtmsSnippets), 0o644, result); err != nil {
		return nil, err
	}

	// Write .gtms/guidance.yaml (create-only). BUG-207 Owner Decision 4: a newly
	// written guidance.yaml uses the active parent for actionable tips that name
	// adapter files; an existing file is left untouched. BUG-213: the contained
	// .gtms/ directory is created by the writer.
	if err := guardedCreateFile(opts.ProjectRoot, ".gtms/guidance.yaml", []byte(guidanceYAMLFor(parent)), 0o644, result); err != nil {
		return nil, err
	}

	// BUG-207 Owner Decisions 4 and 7: in a renamed-parent project, init now
	// refreshes the renamed folder and generates the .vscode schema mapping (and
	// its companion snippet) under the active parent. The only remaining caution is
	// for a user whose EXISTING settings.json still maps the schema under gtms/:
	// tell them to merge the companion snippet. No user-facing string names BUG-207,
	// and the old "automatic refresh does not support renamed GTMS folders" message
	// is retired.
	if renamedParentName != "" {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("This project uses a renamed parent directory (%s/). If your existing "+
				".vscode/settings.json still maps the schema under gtms/, merge "+
				".vscode/gtms-settings.json.snippet (generated for %s/) so schema "+
				"validation points at the renamed folder.", renamedParentName, renamedParentName))
	}

	// ENH-135: Always-on workspace-root informational note.
	// Fires whenever settings.json or its companion was handled.
	// Stored in Notes (rendered with "Note:" prefix, no `!` warning glyph)
	// because the message is informational, not actionable.
	result.Notes = append(result.Notes,
		"The workspace .vscode/settings.json mapping assumes VSCode is opened "+
			"at this directory. The inline yaml-language-server directive on each "+
			"result file is the universal fallback.")

	// ENH-135: Red Hat YAML extension recommendation (surface b per CON-020 Decision 9).
	result.Warnings = append(result.Warnings,
		"For schema validation in VSCode, install the Red Hat YAML extension (redhat.vscode-yaml).")

	return result, nil
}

// resolveActiveParent resolves the GTMS parent folder init must scaffold into,
// implementing BUG-207 Owner Decision 3's binding state table. Every refusal it
// returns happens before any directory creation, config backup/rewrite or
// .gitignore change in Init.
//
// BUG-213 Owner Decision 1: the resolved active parent -- canonical gtms/ as
// well as a renamed parent -- must itself resolve inside the project, in fresh
// initialization, sentinel restoration and rebuild. This single containment
// gate closes Reproduction A, where a canonical gtms/ linked to an empty
// outside folder passed both ScanParents (which skips the link) and
// refuseLinkedTopLevelGTMS (which fires only on a GTMS-shaped link target).
func resolveActiveParent(opts Options) (string, error) {
	parent, err := discoverActiveParent(opts)
	if err != nil {
		return "", err
	}
	if err := checkParentContained(opts.ProjectRoot, parent); err != nil {
		return "", err
	}
	return parent, nil
}

// discoverActiveParent names the active GTMS parent by BUG-207 Owner Decision
// 3's state table, without the BUG-213 containment gate (resolveActiveParent
// applies that to every return).
func discoverActiveParent(opts Options) (string, error) {
	scan, err := config.ScanParents(opts.ProjectRoot)
	if err != nil {
		// symlink sentinel, unreadable root, or a non-"does not exist" Lstat error.
		return "", err
	}

	switch len(scan.Names) {
	case 1:
		// The containment of a renamed parent (Owner Decision 8) and of
		// canonical gtms/ (BUG-213) is checked once by resolveActiveParent.
		return scan.Names[0], nil
	case 0:
		// Fall through to the folder/config states below.
	default:
		return "", fmt.Errorf("Multiple %s sentinels found: %s.\n    Exactly one parent directory is allowed. Remove the sentinel from all but one.",
			config.SentinelFileName, strings.Join(quoteParentNames(scan.Names), ", "))
	}

	// No regular sentinel found. Before ANY state-table row proceeds (fresh,
	// restore, or rebuild), refuse a top-level symbolic link or Windows junction
	// that resolves to a GTMS sentinel or a GTMS-shaped tree, or that cannot be
	// inspected (CODEX-003). config.ScanParents skips such a link because
	// DirEntry.IsDir reports the link, not its target, so without this scan a
	// linked GTMS parent with no gtms.config would be mis-classified as a fresh
	// project and a stray canonical gtms/ tree created. This runs on every
	// config-presence branch, closing that fail-open path.
	if err := refuseLinkedTopLevelGTMS(opts.ProjectRoot); err != nil {
		return "", err
	}

	// Decision 3 rows 2-4 branch on the gtms/ folder and config.
	gtmsExists := false
	if fi, e := os.Lstat(filepath.Join(opts.ProjectRoot, "gtms")); e == nil && fi.IsDir() {
		gtmsExists = true
	}
	if gtmsExists {
		// any | none | gtms/ exists -> restore gtms/.gtms-root (S3, unchanged).
		return "gtms", nil
	}
	if _, cfgErr := os.Lstat(filepath.Join(opts.ProjectRoot, "gtms.config")); cfgErr != nil {
		// absent | none | absent -> fresh project.
		return "gtms", nil
	}
	// present | none | no gtms/ -> rebuild check.
	return rebuildCheckParent(opts.ProjectRoot)
}

// rebuildCheckParent implements Owner Decision 3's rebuild check (config present,
// no sentinel, no gtms/). It returns "gtms" for today's canonical rebuild when no
// renamed candidate is evident, or an error that refuses before mutation.
func rebuildCheckParent(projectRoot string) (string, error) {
	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		return "", fmt.Errorf("inspecting project root for a renamed GTMS folder: %w", err)
	}

	var candidates []string
	for _, entry := range entries {
		name := entry.Name()
		if name == ".git" || name == ".gtms" || name == "_demo" {
			continue
		}
		childPath := filepath.Join(projectRoot, name)
		linfo, lerr := os.Lstat(childPath)
		if lerr != nil {
			if os.IsNotExist(lerr) {
				continue
			}
			return "", fmt.Errorf("inspecting %s: %w", name, lerr)
		}
		// A top-level symlink or junction whose target carries a sentinel or the
		// candidate shape is unsafe evidence -- refuse rather than skip (Decision 8).
		if linfo.Mode()&os.ModeSymlink != 0 {
			linked, cerr := linkedTopLevelIsGTMSShaped(childPath)
			if cerr != nil {
				return "", cerr
			}
			if linked {
				return "", fmt.Errorf("refusing to rebuild: top-level entry %q is a symbolic link or junction to a GTMS-shaped tree. Resolve it before re-running init.", name)
			}
			continue
		}
		if !linfo.IsDir() {
			continue
		}
		shaped, linkedChild, serr := candidateShape(childPath)
		if serr != nil {
			return "", serr
		}
		if linkedChild {
			return "", fmt.Errorf("refusing to rebuild: %q looks like a renamed GTMS folder but its tasks/ or test/cases/ is a symbolic link or junction. Resolve it before re-running init.", name)
		}
		if shaped {
			candidates = append(candidates, name)
		}
	}

	switch len(candidates) {
	case 0:
		return "gtms", nil // today's canonical rebuild (tc-c3d6a8b0 unchanged)
	case 1:
		return "", fmt.Errorf("refusing to rebuild: %q looks like a renamed GTMS folder that lost its sentinel.\n    Restore %s to continue, or remove %s/ to rebuild a canonical gtms/ tree.",
			candidates[0], filepath.ToSlash(filepath.Join(candidates[0], config.SentinelFileName)), candidates[0])
	default:
		sort.Strings(candidates)
		return "", fmt.Errorf("refusing to rebuild: multiple folders look like renamed GTMS folders (%s).\n    Restore the sentinel in exactly one of them, or remove the extras.",
			strings.Join(quoteParentNames(candidates), ", "))
	}
}

// candidateShape reports whether dir has the renamed-GTMS-folder shape (both
// tasks/ and test/cases/ present). linked is true when either required child is a
// symlink or junction (Decision 3/8: that is unsafe, not a discard-to-rebuild).
func candidateShape(dir string) (shaped bool, linked bool, err error) {
	for _, sub := range []string{"tasks", filepath.Join("test", "cases")} {
		p := filepath.Join(dir, sub)
		info, lerr := os.Lstat(p)
		if lerr != nil {
			if os.IsNotExist(lerr) {
				return false, false, nil // not candidate-shaped
			}
			return false, false, fmt.Errorf("inspecting %s: %w", filepath.ToSlash(filepath.Join(filepath.Base(dir), sub)), lerr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return true, true, nil // candidate-shaped but linked -> unsafe
		}
		if !info.IsDir() {
			return false, false, nil
		}
	}
	return true, false, nil
}

// refuseLinkedTopLevelGTMS scans the direct children of projectRoot and refuses a
// top-level symbolic link or Windows junction that resolves to a GTMS sentinel or
// a GTMS-shaped tree, or that cannot be inspected (CODEX-003). It excludes the
// .git/.gtms/_demo operational entries, mirroring the discovery and rebuild scans.
// A link that points nowhere (broken) or to a non-GTMS target is left for the
// normal state-table rows. A real (non-link) entry is not this function's concern.
func refuseLinkedTopLevelGTMS(projectRoot string) error {
	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		return fmt.Errorf("inspecting project root for linked GTMS parents: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == ".git" || name == ".gtms" || name == "_demo" {
			continue
		}
		childPath := filepath.Join(projectRoot, name)
		linfo, lerr := os.Lstat(childPath)
		if lerr != nil {
			if os.IsNotExist(lerr) {
				continue
			}
			return fmt.Errorf("inspecting %s: %w", name, lerr)
		}
		if linfo.Mode()&os.ModeSymlink == 0 {
			continue // real dirs/files are handled by the normal rows / rebuild check
		}
		linked, cerr := linkedTopLevelIsGTMSShaped(childPath)
		if cerr != nil {
			return cerr
		}
		if linked {
			return fmt.Errorf("refusing: top-level entry %q is a symbolic link or junction to a GTMS-shaped tree. Resolve it before re-running init.", name)
		}
	}
	return nil
}

// linkedTopLevelIsGTMSShaped reports whether a top-level symlink/junction resolves
// to a tree that carries a .gtms-root sentinel or the tasks/+test/cases/ candidate
// shape. A broken link (target does not exist) is not evidence and returns false;
// any other resolution failure is "cannot be inspected" and returns an error so
// the caller refuses rather than silently skipping (CODEX-003).
func linkedTopLevelIsGTMSShaped(linkPath string) (bool, error) {
	target, err := filepath.EvalSymlinks(linkPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil // broken link: points nowhere, not GTMS evidence
		}
		return false, fmt.Errorf("inspecting link %s: %w", filepath.Base(linkPath), err)
	}
	if fi, e := os.Lstat(filepath.Join(target, config.SentinelFileName)); e == nil && fi.Mode().IsRegular() {
		return true, nil
	}
	tasksOK := false
	casesOK := false
	if fi, e := os.Stat(filepath.Join(target, "tasks")); e == nil && fi.IsDir() {
		tasksOK = true
	}
	if fi, e := os.Stat(filepath.Join(target, "test", "cases")); e == nil && fi.IsDir() {
		casesOK = true
	}
	return tasksOK && casesOK, nil
}

// checkParentContained refuses a renamed parent that resolves outside the project
// root (Owner Decision 8, AC10). pathsafe.ResolveUnderRoot evaluates symlinks and
// Windows junctions (BUG-205 godebug winsymlink=0) and fails closed on an escape.
func checkParentContained(projectRoot, parent string) error {
	if _, _, err := pathsafe.ResolveUnderRoot(projectRoot, parent); err != nil {
		return fmt.Errorf("refusing: the GTMS parent %q resolves outside the project. "+
			"A symbolic link or junction cannot redirect init writes outside the project root (%s).",
			parent, pathsafe.SanitizeControlChars(err.Error()))
	}
	return nil
}

// quoteParentNames wraps each folder name as 'name/' for refusal messages,
// mirroring config.formatDirNames.
func quoteParentNames(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = "'" + n + "/'"
	}
	return out
}

// remapParent translates a canonical, forward-slash scaffold path whose first
// segment is gtms/ into the active parent (BUG-207 Owner Decision 2/5). Any other
// first segment (_demo/, .vscode/, .gtms/) is left unchanged, and parent "gtms"
// (or empty) is a no-op so the default layout is byte-for-byte unchanged (AC6).
func remapParent(canonicalRel, parent string) string {
	if parent == "" || parent == "gtms" {
		return canonicalRel
	}
	if canonicalRel == "gtms" {
		return parent
	}
	const pfx = "gtms/"
	if strings.HasPrefix(canonicalRel, pfx) {
		return parent + "/" + canonicalRel[len(pfx):]
	}
	return canonicalRel
}

// writeParentFileIfNotExists writes a fixed scaffold file at the destination
// derived from a canonical gtms/ relpath and the active parent, reporting the
// destination (not the canonical path) in the Result. BUG-213: it routes
// through the containment-aware create-only writer so a linked parent component
// or a linked leaf cannot redirect the write outside the project; an inspection
// or filesystem error is returned so the caller fails closed.
func writeParentFileIfNotExists(projectRoot, canonicalRel string, content []byte, parent string, result *Result) error {
	return guardedCreateFile(projectRoot, remapParent(canonicalRel, parent), content, 0o644, result)
}

// vscodeSettingsFor returns the .vscode/settings.json body with the schema-mapping
// paths pointed at the active parent (Owner Decision 4). Both tokens are resolvable
// paths, so both are remapped; parent "gtms" returns the canonical template.
//
// CODEX-005: for a renamed parent the document is serialized with encoding/json, so
// a folder name containing a double quote, a backslash or any other JSON-special
// character is correctly escaped and the result is always valid JSON pointing at
// the right path -- never raw substitution into a string literal. The default
// "gtms" layout returns the shipped template verbatim (AC6 byte-for-byte).
func vscodeSettingsFor(parent string) string {
	if parent == "" || parent == "gtms" {
		return vscodeSettingsTemplate
	}
	schemaPath := parent + "/schemas/manual-result.schema.json"
	recordsGlob := parent + "/manual/records/*.result.yaml"
	doc := map[string]map[string]string{
		"yaml.schemas": {schemaPath: recordsGlob},
	}
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // keep <, >, & literal; only JSON-structural escaping
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		// Unreachable for string maps; fall back to raw substitution rather than panic.
		s := vscodeSettingsTemplate
		s = strings.ReplaceAll(s, "gtms/schemas/", parent+"/schemas/")
		s = strings.ReplaceAll(s, "gtms/manual/", parent+"/manual/")
		return s
	}
	return buf.String() // Encoder already appends a trailing newline
}

// guidanceYAMLFor returns the default guidance with the one actionable adapter-file
// tip path pointed at the active parent (Owner Decision 4). Prose is untouched.
func guidanceYAMLFor(parent string) string {
	if parent == "" || parent == "gtms" {
		return DefaultGuidanceYAML
	}
	return strings.ReplaceAll(DefaultGuidanceYAML, "gtms/adapters/", parent+"/adapters/")
}

// demoBridgeGuideFor returns the demo bridge guide with only the GTMS-folder
// cleanup commands pointed at the active parent (Owner Decisions 4 and 6). The
// project-root "rm -rf _demo/" command and all prose stay canonical.
//
// CODEX-001: these are documented shell commands the user copies and runs, so the
// generated path is POSIX shell-quoted. A demonstrably safe name (e.g. testing)
// keeps today's exact unquoted form; a name with a space, quote, ';', command
// substitution or other shell syntax is single-quoted so it is one literal
// argument that cannot split or execute.
func demoBridgeGuideFor(parent string) string {
	if parent == "" || parent == "gtms" {
		return demoBridgeGuide
	}
	s := demoBridgeGuide
	s = strings.ReplaceAll(s, "rm -rf gtms/test/cases/_demo/", "rm -rf "+shellQuotePOSIX(parent+"/test/cases/_demo/"))
	s = strings.ReplaceAll(s, "rm -rf gtms/automation/specs/demo-sh/", "rm -rf "+shellQuotePOSIX(parent+"/automation/specs/demo-sh/"))
	s = strings.ReplaceAll(s, "rm -rf gtms/automation/specs/demo-cmd/", "rm -rf "+shellQuotePOSIX(parent+"/automation/specs/demo-cmd/"))
	return s
}

// shellSafeArg matches values that are safe as a bare POSIX shell argument and so
// need no quoting: letters, digits, and the punctuation that carries no shell
// meaning in any POSIX shell. Anything else (space, quote, $, ;, (), *, etc.) is
// quoted by shellQuotePOSIX.
var shellSafeArg = regexp.MustCompile(`^[A-Za-z0-9._/@%+=:,-]+$`)

// shellQuotePOSIX returns s unchanged when it is a demonstrably safe bare argument,
// and otherwise wraps it in single quotes with embedded single quotes escaped as
// single-quote, backslash, single-quote, single-quote -- the portable POSIX form
// that makes the whole value one literal argument.
func shellQuotePOSIX(s string) string {
	if s != "" && shellSafeArg.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// installPresetAssets writes framework-specific files owned by the given preset.
// BUG-111 / ADR-022: framework assets are deliberate preset policy, not unconditional.
//
// BUG-189 (tc-65df1eb5): existing projects hold their own copy of an asset such as
// gtms/adapters/playwright-runner.sh, and there is no repository source file for them
// to pull. When force is set, this provides a content-aware upgrade path for assets
// that declare known prior versions (PresetAssetPriorVersions):
//   - on-disk content byte-identical to the current shipped content -> skip (current);
//   - byte-identical to a known prior shipped version -> refresh in place (a pristine
//     older copy has no user edits to lose);
//   - anything else -> the file is user-modified or unknown; write a `.gtms-new`
//     companion and warn, never clobbering the original.
//
// Assets with no prior-version catalog entry keep the plain absent->write /
// present->skip behaviour regardless of force, so existing init/force flows are
// byte-for-byte unchanged.
func installPresetAssets(projectRoot, preset string, force bool, result *Result) error {
	return installPresetAssetsForParent(projectRoot, preset, force, result, "gtms")
}

// installPresetAssetsForParent is the BUG-207 parent-aware implementation.
func installPresetAssetsForParent(projectRoot, preset string, force bool, result *Result, parent string) error {
	assets, ok := PresetAssets[preset]
	if !ok {
		return nil // no assets for this preset
	}
	for _, asset := range assets {
		if err := applyUpgradeableAssetForParent(projectRoot, asset, force, result, parent); err != nil {
			return err
		}
	}
	return nil
}

// installCommonAssets applies the same absent->write / upgrade-or-companion logic to the
// all-preset scaffolded producer files (BUG-203). On a fresh init every file is written
// (absent -> created); under --force an unedited v0.3.x copy is refreshed in place, an
// edited copy is kept with a .gtms-new companion, and a current copy is skipped -- exactly
// as installPresetAssets does for the preset-owned runners.
func installCommonAssets(projectRoot string, force bool, result *Result, parent string) error {
	assets, err := commonUpgradeableAssets()
	if err != nil {
		return err
	}
	for _, asset := range assets {
		if err := applyUpgradeableAssetForParent(projectRoot, asset, force, result, parent); err != nil {
			return err
		}
	}
	return nil
}

// applyUpgradeableAsset installs one asset with content-aware --force upgrade handling.
// It is the per-asset body shared by installPresetAssets (preset-owned runners),
// installCommonAssets (BUG-203 all-preset producers) and the demo create adapter:
//   - absent -> write (unchanged behaviour);
//   - present && (!force || no known prior version) -> skip;
//   - present && force && byte-identical to current (EOL-insensitive) -> skip (current);
//   - present && force && byte-identical to a known prior version -> refresh in place;
//   - present && force && anything else -> keep, write a .gtms-new companion, warn.
//
// Known prior versions are read from PresetAssetPriorVersions[asset.RelPath]
// (via isKnownPriorAsset), which the BUG-203 init() populates for the common/demo files.
//
// BUG-207 Owner Decision 5: catalogue identity stays canonical. asset.RelPath
// (always gtms/...) keys the prior-version catalogue and the current-content
// comparison; the DESTINATION path (destRel), computed by swapping the gtms/
// first segment for the active parent, drives every filesystem operation and
// user-facing message. With parent "gtms" destRel == asset.RelPath, so the
// default layout is byte-for-byte unchanged (AC6).
func applyUpgradeableAsset(projectRoot string, asset PresetAsset, force bool, result *Result) error {
	return applyUpgradeableAssetForParent(projectRoot, asset, force, result, "gtms")
}

// applyUpgradeableAssetForParent is the BUG-207 parent-aware implementation. See
// applyUpgradeableAsset's doc comment for the full upgrade contract.
func applyUpgradeableAssetForParent(projectRoot string, asset PresetAsset, force bool, result *Result, parent string) error {
	destRel := remapParent(asset.RelPath, parent)
	// Step 1 -- inspect the LEAF on the LEXICAL path. filepath.Join plus Clean
	// only: no symlink evaluation, so os.Lstat below sees the asset's own final
	// component rather than whatever it points at.
	//
	// REV-165 (CODEX-003): Lstat, never Stat/ReadFile, to decide what is on
	// disk. os.ReadFile and os.WriteFile both FOLLOW symlinks, so a symlinked
	// gtms/adapters/playwright-runner.sh whose target happened to hold known
	// prior bytes had that EXTERNAL target overwritten by --force. Before the
	// upgrade path existed an existing asset was simply skipped, so this was
	// a new out-of-project write. Anything that is not a regular file is
	// refused outright and reported.
	//
	// This veto MUST run against the UNRESOLVED leaf. Handing Lstat a
	// canonicalised path instead would give it a leaf symlink already resolved
	// to its regular target, Mode().IsRegular() would be true, and the veto
	// would silently stop firing.
	lexPath := filepath.Clean(filepath.Join(projectRoot, filepath.FromSlash(destRel)))
	info, statErr := os.Lstat(lexPath)
	if statErr != nil && !os.IsNotExist(statErr) {
		return fmt.Errorf("inspecting %s: %w", destRel, statErr)
	}
	leafPresent := statErr == nil
	if leafPresent && !info.Mode().IsRegular() {
		result.FilesSkipped = append(result.FilesSkipped, destRel)
		result.FilesRefused = append(result.FilesRefused, destRel)
		// BUG-213: shared wording with the direct-write guard (safewrite.go).
		result.Warnings = append(result.Warnings, nonRegularRefusalWarning(destRel, info.Mode()))
		return nil
	}

	// Step 2 -- resolve CONTAINMENT before any MkdirAll, read or write.
	//
	// REV-165 (CODEX-R2-001): the leaf veto above never inspects PARENT
	// components. If gtms/adapters/ is itself a symlink (or a Windows junction)
	// pointing outside the project, the asset, its temporary file and its
	// .gtms-new companions were all created in that EXTERNAL directory --
	// overwriting an out-of-project file whenever it held known prior bytes.
	// That is the CODEX-003 data-loss class one level up, and it is reachable
	// on the absent-leaf path too, not only on a forced upgrade.
	//
	// pathsafe.ResolveUnderRoot resolves symlinks on the nearest existing
	// ancestor and re-attaches the not-yet-created suffix, so an escaping
	// parent is caught whether the leaf exists or not.
	absPath, _, resolveErr := pathsafe.ResolveUnderRoot(projectRoot, destRel)
	if resolveErr != nil {
		result.FilesSkipped = append(result.FilesSkipped, destRel)
		result.FilesRefused = append(result.FilesRefused, destRel)
		// BUG-213: shared wording with the direct-write guard (safewrite.go).
		result.Warnings = append(result.Warnings, containmentRefusalWarning(destRel, resolveErr))
		return nil
	}

	// Step 3 -- from here on every filesystem operation uses absPath, the
	// canonical in-root path. lexPath is never written through: writeFileAtomic
	// derives its temporary file from filepath.Dir(absPath), and writeCompanion
	// derives every .gtms-new companion from absPath.
	//
	// Ensure parent directory exists (e.g. gtms/adapters/lib/)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return fmt.Errorf("creating directory for %s: %w", destRel, err)
	}

	if !leafPresent {
		// Absent -> write (unchanged behaviour).
		if err := os.WriteFile(absPath, []byte(asset.Content), asset.Perm); err != nil {
			return fmt.Errorf("writing %s: %w", destRel, err)
		}
		result.FilesCreated = append(result.FilesCreated, destRel)
		return nil
	}

	existing, readErr := os.ReadFile(absPath)
	if readErr != nil {
		return fmt.Errorf("reading %s: %w", destRel, readErr)
	}

	// Present. Without --force, or for an asset that declares no prior versions,
	// preserve the established present->skip behaviour. The prior-version catalogue
	// is keyed on the CANONICAL identity (asset.RelPath), not the destination.
	if !force || len(PresetAssetPriorVersions[asset.RelPath]) == 0 {
		result.FilesSkipped = append(result.FilesSkipped, destRel)
		return nil
	}

	// --force + present + upgradeable asset: content-aware refresh.
	// CODEX-002: compare EOL-insensitively. A pristine shipped asset checked
	// out on Windows with Git CRLF conversion differs from the LF-only const
	// byte-for-byte but is the same text; treat it as current rather than as
	// a user edit.
	if equalIgnoringEOL(string(existing), asset.Content) {
		// Already the current shipped content.
		result.FilesSkipped = append(result.FilesSkipped, destRel)
		return nil
	}
	if isKnownPriorAsset(asset.RelPath, string(existing)) {
		// A pristine older shipped copy: safe to refresh in place.
		if err := writeFileAtomic(absPath, []byte(asset.Content), asset.Perm); err != nil {
			return fmt.Errorf("upgrading %s: %w", destRel, err)
		}
		result.FilesCreated = append(result.FilesCreated, destRel)
		result.Notes = append(result.Notes,
			fmt.Sprintf("Upgraded %s to the current shipped version.", destRel))
		return nil
	}
	// User-modified or unknown: never clobber. Write a companion and warn.
	return writeCompanion(absPath, asset, destRel, result)
}

// writeCompanion writes the current shipped content beside a user-modified asset
// so the operator can merge it by hand.
//
// REV-165 (CODEX-004): the companion used to be written unconditionally, so a
// second `gtms init --force` silently truncated any hand-merged edits the
// operator had made in it -- in a feature whose whole promise is that it never
// clobbers user changes. An existing companion is now never overwritten: if it
// already holds the current shipped bytes there is nothing to do, and otherwise
// the shipped version goes to the next free `.gtms-new.N` and the operator is
// told their companion was preserved.
func writeCompanion(absPath string, asset PresetAsset, destRel string, result *Result) error {
	baseRel := destRel + ".gtms-new"
	baseAbs := absPath + ".gtms-new"

	companionRel, companionAbs := baseRel, baseAbs
	preserved := ""
	for n := 2; ; n++ {
		info, statErr := os.Lstat(companionAbs)
		if os.IsNotExist(statErr) {
			break // free slot
		}
		if statErr != nil {
			return fmt.Errorf("inspecting %s: %w", companionRel, statErr)
		}
		if info.Mode().IsRegular() {
			existing, readErr := os.ReadFile(companionAbs)
			if readErr != nil {
				return fmt.Errorf("reading %s: %w", companionRel, readErr)
			}
			if equalIgnoringEOL(string(existing), asset.Content) {
				// Already the current shipped content: nothing to write, and
				// nothing of the operator's to lose.
				// CODEX-R2-001: compare EOL-insensitively, matching the main
				// upgrade checks. A current companion checked out on Windows with
				// Git CRLF conversion is the same text as the LF-only const; a
				// byte-for-byte compare treated it as user content and wrote a
				// fresh numbered companion on every forced init.
				result.FilesSkipped = append(result.FilesSkipped, destRel, companionRel)
				result.Warnings = append(result.Warnings, conflictWarning(destRel, companionRel))
				return nil
			}
		}
		if preserved == "" {
			preserved = companionRel
		}
		companionRel = fmt.Sprintf("%s.%d", baseRel, n)
		companionAbs = fmt.Sprintf("%s.%d", baseAbs, n)
	}

	if err := writeFileAtomic(companionAbs, []byte(asset.Content), asset.Perm); err != nil {
		return fmt.Errorf("writing %s: %w", companionRel, err)
	}
	result.FilesSkipped = append(result.FilesSkipped, destRel)
	result.FilesCreated = append(result.FilesCreated, companionRel)
	result.Warnings = append(result.Warnings, conflictWarning(destRel, companionRel))
	if preserved != "" {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("%s already existed and differs from the shipped version, so it was "+
				"left untouched; the current version was written to %s instead.",
				preserved, companionRel))
	}
	return nil
}

// conflictWarning is the operator-facing text for a user-modified preset asset
// that was left alone with the shipped version written alongside it.
func conflictWarning(assetRel, companionRel string) string {
	return fmt.Sprintf("%s differs from the shipped version and was left untouched. "+
		"The current shipped version was written to %s; review and merge the files manually.",
		assetRel, companionRel)
}

// writeFileAtomic writes content to absPath via a same-directory temporary file
// and an atomic rename.
//
// REV-165 (CODEX-003): os.WriteFile opens the destination in place, which
// mutates whatever the path resolves to -- including the far end of a hard link
// shared with a file outside the project -- and leaves a half-written asset
// behind if the write fails part way. Renaming a fresh file over the path
// replaces the directory entry instead, so a hard-linked twin keeps the old
// content and a failed write leaves the original intact.
func writeFileAtomic(absPath string, content []byte, perm os.FileMode) error {
	dir := filepath.Dir(absPath)
	tmp, err := os.CreateTemp(dir, ".gtms-asset-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename has succeeded
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	return os.Rename(tmpName, absPath)
}

// isKnownPriorAsset reports whether content matches any known prior shipped
// version of the asset at relPath (BUG-189 upgrade path).
//
// CODEX-002: the catalogue stores the LF-only shipped bytes, but a pristine
// older asset checked out on Windows with Git CRLF conversion carries CRLF line
// endings. The comparison is EOL-insensitive so such a copy is still recognised
// as a known prior version safe to refresh in place; the refresh then writes the
// current LF content.
func isKnownPriorAsset(relPath, content string) bool {
	for _, prior := range PresetAssetPriorVersions[relPath] {
		if equalIgnoringEOL(content, prior) {
			return true
		}
	}
	return false
}

// normalizeEOL converts Windows CRLF line endings to LF so that text preset
// assets compare equal regardless of how Git checked them out.
func normalizeEOL(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// equalIgnoringEOL reports whether a and b are the same text after CRLF->LF
// normalization. Used by the gtms init --force upgrade path so a pristine
// shipped asset is recognised whether it is on disk with LF or CRLF endings.
func equalIgnoringEOL(a, b string) bool {
	return normalizeEOL(a) == normalizeEOL(b)
}

// CreateDirectories creates the GTMS directory structure with .gitkeep files.
// Returns the list of directories created (relative paths).
//
// ENH-098: These directory name literals are scaffolded into consumer projects
// under the nested gtms/ parent layout. The sentinel file (.gtms-root) is
// created inside the parent to enable rename-resilient discovery.
func CreateDirectories(root string, preset string) ([]string, error) {
	return createDirectories(root, preset, "gtms", &Result{})
}

// createDirectories is the parent-aware implementation (BUG-207). The canonical
// gtms/ directory literals are remapped under the active parent; the returned
// relative paths use the active parent so the Result reports them correctly.
//
// BUG-213 Owner Decisions 1 and 3: each directory is created only when it
// resolves inside the project. A directory below a linked GTMS subfolder is
// skipped (not counted in DirsCreated) with a per-destination warning for both
// the directory and its .gitkeep; a refused directory never redirects a write
// outside the project. .gitkeep files stay silent in the Result (today's
// behaviour) except for a refusal warning.
func createDirectories(root string, preset string, parent string, result *Result) ([]string, error) {
	dirs := []string{
		"gtms/tasks/pending",
		"gtms/tasks/in-progress",
		"gtms/tasks/in-review",
		"gtms/tasks/complete",
		"gtms/tasks/error",
		"gtms/test/cases",
		"gtms/test/templates",
		"gtms/test/guides",
		// ENH-165: gtms/test/prompts/ holds Tier 1 adapter prompt templates
		// (e.g. create-standard.md). Previously the prompt template lived at
		// gtms/cases/prompts/ as the sole vestigial slot left after ENH-164's
		// gtms/cases/ -> gtms/test/ refactor.
		"gtms/test/prompts",
		// CON-023 / ENH-145: wiring/ replaces the legacy records/ directory.
		// Wiring files are tracked (.gitignore must NOT exclude this dir).
		"gtms/automation/wiring",
		"gtms/automation/specs",
		"gtms/scripts",
		"gtms/execution",
	}

	// ENH-132: Manual testing and schema directories (all presets)
	dirs = append(dirs,
		"gtms/manual/records",
		"gtms/manual/templates",
		"gtms/schemas",
	)

	// All presets ship at least one adapter script (manual-create-script.sh)
	// BUG-053: adapters nest under gtms/ for ENH-098 three-item root footprint
	// BUG-111: adapters/lib/ is now created by preset asset installation, not here
	dirs = append(dirs, "gtms/adapters")

	created := []string{}
	for _, canonicalDir := range dirs {
		dir := remapParent(canonicalDir, parent)
		resolveErr, fatalErr := safeMkdirContained(root, dir)
		if fatalErr != nil {
			return nil, fatalErr
		}
		if resolveErr != nil {
			// Escaping directory below a linked GTMS subfolder: skip it (do not
			// count it) and warn for both the directory and its .gitkeep. Record
			// the directory as refused so the Created: banner never lists it, and
			// report the refused .gitkeep (a file destination) under
			// FilesSkipped/FilesRefused per Owner Decision 1 ("a refused
			// destination is reported in FilesSkipped") so it appears under
			// "Skipped (already exist):".
			gitkeepRel := dir + "/.gitkeep"
			result.DirsRefused = append(result.DirsRefused, dir)
			result.FilesRefused = append(result.FilesRefused, dir, gitkeepRel)
			result.FilesSkipped = append(result.FilesSkipped, gitkeepRel)
			result.Warnings = append(result.Warnings,
				containmentRefusalWarning(dir, resolveErr),
				containmentRefusalWarning(gitkeepRel, resolveErr))
			continue
		}
		created = append(created, dir)

		// Write .gitkeep in leaf directories. A created or already-present
		// .gitkeep stays silent in the Result (preserving today's output). A
		// REFUSED .gitkeep (its own leaf is a symlink/special, or a parent
		// component escapes) is reported under FilesSkipped/FilesRefused per
		// Owner Decision 1, matching every other refused destination.
		gitkeepRel := dir + "/.gitkeep"
		outcome, warning, err := safeCreateOnly(root, gitkeepRel, []byte(""), 0o644)
		if err != nil {
			return nil, err
		}
		if outcome == writeRefused {
			result.FilesSkipped = append(result.FilesSkipped, gitkeepRel)
			result.FilesRefused = append(result.FilesRefused, gitkeepRel)
			result.Warnings = append(result.Warnings, warning)
		}
	}

	return created, nil
}

// WriteConfig generates and writes a gtms.config file based on the preset.
// Returns the absolute path to the config file.
func WriteConfig(root, name, repo, preset string) (string, error) {
	return writeConfigForParent(root, name, repo, preset, "gtms")
}

// writeConfigForParent is the parent-aware implementation (BUG-207 Owner
// Decision 2). For a renamed parent it remaps path values whose canonical first
// segment is gtms/ onto the active parent, with safe YAML quoting; for "gtms" it
// writes the preset config byte-for-byte as before (AC6).
func writeConfigForParent(root, name, repo, preset, parent string) (string, error) {
	content := configForPreset(name, repo, preset)
	if parent != "gtms" && parent != "" {
		remapped, err := remapConfigPaths(content, parent)
		if err != nil {
			return "", fmt.Errorf("remapping gtms.config paths to parent %q: %w", parent, err)
		}
		content = remapped
	}
	configPath := filepath.Join(root, "gtms.config")

	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("writing gtms.config: %w", err)
	}

	return configPath, nil
}

// configPathKeys are the adapter-config value keys whose gtms/-rooted values are
// remapped onto the active parent (BUG-207 Owner Decision 2). Project-root values
// such as test/acceptance or _demo/ are left unchanged because their first
// path segment is not gtms.
var configPathKeys = map[string]struct{}{
	"script":          {},
	"prompt-template": {},
	"guide-dir":       {},
	"output-dir":      {},
	"artefact-glob":   {},
}

// remapConfigPaths rewrites, in the generated gtms.config text, every adapter path
// value whose canonical first path segment is gtms/ so it points under parent. It
// round-trips through a yaml.Node to preserve the preset's comments; remapped
// scalars are emitted double-quoted so a parent containing a space or '#' (e.g.
// "qa #1") round-trips as a value rather than being read as a YAML comment.
func remapConfigPaths(configText, parent string) (string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(configText), &doc); err != nil {
		return "", err
	}
	// doc is a DocumentNode; its first content child is the top-level mapping.
	if len(doc.Content) == 0 {
		return configText, nil
	}
	root := doc.Content[0]
	adapters := mappingValue(root, "adapters")
	if adapters != nil && adapters.Kind == yaml.MappingNode {
		// adapters -> command -> adapter-name -> { key: value }
		for i := 1; i < len(adapters.Content); i += 2 {
			commandMap := adapters.Content[i]
			if commandMap.Kind != yaml.MappingNode {
				continue
			}
			for j := 1; j < len(commandMap.Content); j += 2 {
				adapterMap := commandMap.Content[j]
				if adapterMap.Kind != yaml.MappingNode {
					continue
				}
				for k := 0; k+1 < len(adapterMap.Content); k += 2 {
					keyNode := adapterMap.Content[k]
					valNode := adapterMap.Content[k+1]
					if valNode.Kind != yaml.ScalarNode {
						continue
					}
					if _, ok := configPathKeys[keyNode.Value]; !ok {
						continue
					}
					remapped, changed := remapConfigValue(valNode.Value, parent)
					if changed {
						// Force double-quoted style so a parent containing a space or
						// '#' (e.g. "qa #1") always round-trips as a value, never as a
						// plain scalar that YAML could read as a trailing comment.
						valNode.Kind = yaml.ScalarNode
						valNode.Tag = "!!str"
						valNode.Value = remapped
						valNode.Style = yaml.DoubleQuotedStyle
					}
				}
			}
		}
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// remapConfigValue swaps a leading gtms/ segment of a config path value for the
// active parent. It returns the (possibly unchanged) value and whether it changed.
// Only the first path segment is inspected, so test/acceptance, _demo/ and the
// like are left alone.
func remapConfigValue(value, parent string) (string, bool) {
	v := strings.TrimPrefix(value, "./")
	const pfx = "gtms/"
	if v == "gtms" {
		return parent, true
	}
	if strings.HasPrefix(v, pfx) {
		return parent + "/" + v[len(pfx):], true
	}
	return value, false
}

// mappingValue returns the value node for key in a mapping node, or nil.
func mappingValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// RESERVED FOR ENH-176 -- intentionally NOT called by `gtms init`. Under DOC-018
// Direction 2 (Retire), starter prompt templates are user-authored, so no init
// path invokes this. The only caller today is scaffold_test.go (keep-alive).
// Do not delete: ENH-176 will wire this into init when the ship direction lands.
//
// WritePromptTemplates writes starter prompt templates to gtms/test/prompts/
// and gtms/automation/prompts/. Returns the list of files created (relative paths).
//
// ENH-165: create prompt template lives at gtms/test/prompts/create-standard.md
// (under the test/ tree alongside cases/, templates/, guides/). Previously it
// lived at gtms/cases/prompts/ as the sole vestigial slot left after ENH-164.
func WritePromptTemplates(root string) ([]string, error) {
	templates := []struct {
		path    string
		content string
	}{
		{"gtms/test/prompts/create-standard.md", promptCreateStandard},
		{"gtms/automation/prompts/automate-standard.md", promptAutomateStandard},
	}

	var created []string
	for _, tmpl := range templates {
		absPath := filepath.Join(root, filepath.FromSlash(tmpl.path))
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			return nil, fmt.Errorf("creating directory for %s: %w", tmpl.path, err)
		}
		if err := os.WriteFile(absPath, []byte(tmpl.content), 0o644); err != nil {
			return nil, fmt.Errorf("writing %s: %w", tmpl.path, err)
		}
		created = append(created, tmpl.path)
	}

	return created, nil
}

// WriteStarterGuides writes a starter test case guide to gtms/test/guides/.
// Returns the list of files created (relative paths).
func WriteStarterGuides(root string) ([]string, error) {
	guides := []struct {
		path    string
		content string
	}{
		{"gtms/test/guides/gtms-test-case-authoring-guide.md", starterGuideContent},
	}

	var created []string
	for _, g := range guides {
		absPath := filepath.Join(root, filepath.FromSlash(g.path))
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			return nil, fmt.Errorf("creating directory for %s: %w", g.path, err)
		}
		if err := os.WriteFile(absPath, []byte(g.content), 0o644); err != nil {
			return nil, fmt.Errorf("writing %s: %w", g.path, err)
		}
		created = append(created, g.path)
	}

	return created, nil
}

// WriteExampleSkills writes the starter Agent Skills catalog to gtms/skills/.
// Each skill is a directory containing SKILL.md in the Agent Skills open-standard
// format (agentskills.io). A catalog README is written at gtms/skills/README.md.
// Files that already exist are not overwritten (idempotent via writeFileIfNotExists).
func WriteExampleSkills(root string, result *Result) error {
	return writeExampleSkillsForParent(root, result, "gtms")
}

// writeExampleSkillsForParent is the BUG-207 parent-aware implementation.
func writeExampleSkillsForParent(root string, result *Result, parent string) error {
	return fs.WalkDir(skillsFS, "skills", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			// REV-255 chunk-003-001 / CODEX-001: starter-skill
			// directories are not part of createDirectories. Record every
			// embedded directory whose destination escapes, including a nested
			// skill directory, so the Created: summary does not claim that the
			// starter-skills tree was created. Individual file writes still run
			// below and retain their per-destination warnings and Skipped entries.
			dirRel := remapParent("gtms/"+path, parent)
			if _, _, resolveErr := pathsafe.ResolveUnderRoot(root, dirRel); resolveErr != nil {
				result.DirsRefused = append(result.DirsRefused, dirRel)
			}
			return nil
		}

		// Read the embedded file content.
		content, err := fs.ReadFile(skillsFS, path)
		if err != nil {
			return fmt.Errorf("reading embedded %s: %w", path, err)
		}

		// Map from embed path (skills/gtms-tests-create/SKILL.md) to output
		// path (gtms/skills/gtms-tests-create/SKILL.md). Use forward slashes
		// in relPath to match the convention used by all other writeFileIfNotExists
		// callers (fs.WalkDir already returns forward slashes).
		relPath := "gtms/" + path

		// BUG-203: the gtms-tests-create starter skill is now owned by the
		// common-asset upgrade catalogue (installCommonAssets), so init --force can
		// refresh a stale v0.3.x copy. Skip it here to avoid a double write.
		// ENH-232 Decision 9: the catalogue README joins the same upgrade catalogue, so
		// init --force upgrades a pristine five-skill README in place. Skip it here too.
		if relPath == skillCreateRelPath || relPath == skillCatalogueReadmeRelPath {
			return nil
		}

		// BUG-207: the skip test above uses the canonical identity (relPath); the
		// write destination uses the active parent.
		// BUG-213: route through the containment-aware create-only writer so a
		// linked gtms/skills/ cannot redirect a starter skill outside the project.
		destRel := remapParent(relPath, parent)
		return guardedCreateFile(root, destRel, content, 0o644, result)
	})
}

// WriteAdapterStubs writes placeholder adapter scripts to the gtms/adapters/ directory.
// Returns the list of files created (relative paths).
// BUG-053: moved from adapters/ to gtms/adapters/ for ENH-098 three-item root footprint.
func WriteAdapterStubs(root string) ([]string, error) {
	adaptersDir := filepath.Join(root, "gtms", "adapters")
	if err := os.MkdirAll(adaptersDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating gtms/adapters directory: %w", err)
	}

	stubs := map[string]string{
		"gtms/adapters/github-create.sh":          adapterStubScript("github-create", "create"),
		"gtms/adapters/github-create-status.sh":   adapterStatusStubScript("github-create"),
		"gtms/adapters/github-automate.sh":        adapterStubScript("github-automate", "automate"),
		"gtms/adapters/github-automate-status.sh": adapterStatusStubScript("github-automate"),
		"gtms/adapters/github-execute.sh":         adapterStubScript("github-execute", "execute"),
		"gtms/adapters/github-execute-status.sh":  adapterStatusStubScript("github-execute"),
	}

	var created []string
	for relPath, content := range stubs {
		absPath := filepath.Join(root, filepath.FromSlash(relPath))
		if err := os.WriteFile(absPath, []byte(content), 0o755); err != nil {
			return nil, fmt.Errorf("writing %s: %w", relPath, err)
		}
		created = append(created, relPath)
	}

	return created, nil
}

// DemoSeedResult describes what DemoSeed created or modified.
type DemoSeedResult struct {
	FilesCreated     []string // files created (relative to project root)
	FilesSkipped     []string // files not written because they already existed
	ConfigModified   bool     // whether gtms.config was updated with demo adapters
	GuidanceModified bool     // whether guidance.yaml was updated
	Warnings         []string // BUG-203: conflict warnings from the create-demo.sh upgrade path
	Notes            []string // BUG-203: upgrade notes from the create-demo.sh upgrade path
}

// DemoSeed seeds demo data into an existing GTMS project.
// The project must already have a valid gtms.config (run Init first if needed).
// BUG-203: force carries gtms init --force into the demo path so a stale v0.3.x
// _demo/adapters/create-demo.sh is upgraded in place (rather than skipped as present).
func DemoSeed(projectRoot string, force bool) (*DemoSeedResult, error) {
	result := &DemoSeedResult{}

	// BUG-207 Owner Decisions 4 and 6 / CODEX-002: resolve the active parent through
	// the one fail-closed resolution path, BEFORE any mutation, and propagate a
	// discovery error instead of defaulting to gtms. This covers the existing-config
	// `gtms init --demo` CLI path, which calls DemoSeed without a preceding Init: an
	// invalid layout (multiple/symlink/unreadable sentinel, or a linked GTMS parent)
	// now refuses here rather than seeding _demo and rewriting the config. The demo
	// bridge guide is written under this parent; _demo/ stays at the project root.
	parent, err := resolveActiveParent(Options{ProjectRoot: projectRoot})
	if err != nil {
		return nil, err
	}

	// Write demo files (skip if they already exist). create-demo.sh is handled
	// separately below via the upgrade path (BUG-203), so it is not in this list.
	// BUG-213 Owner Decisions 1 and 3: each demo file routes through the
	// containment-aware create-only writer, which creates the contained _demo/
	// and _demo/adapters/ directories itself. A linked _demo/ never redirects a
	// write outside the project; each refused destination gets its own warning.
	demoFiles := []struct {
		relPath string
		content string
		perm    os.FileMode
	}{
		{"_demo/login-feature.md", demoLoginRequirement, 0o644},
		{"_demo/adapters/automate-demo-sh.sh", demoAutomateShScript, 0o755},
		{"_demo/adapters/automate-demo-cmd.sh", demoAutomateCmdScript, 0o755},
		{remapParent("gtms/test/guides/getting-started-with-ai.md", parent), demoBridgeGuideFor(parent), 0o644},
	}

	for _, f := range demoFiles {
		outcome, warning, werr := safeCreateOnly(projectRoot, f.relPath, []byte(f.content), f.perm)
		if werr != nil {
			return nil, werr
		}
		switch outcome {
		case writeCreated:
			result.FilesCreated = append(result.FilesCreated, f.relPath)
		case writeSkippedExists:
			result.FilesSkipped = append(result.FilesSkipped, f.relPath)
		case writeRefused:
			result.FilesSkipped = append(result.FilesSkipped, f.relPath)
			result.Warnings = append(result.Warnings, warning)
		}
	}

	// BUG-203: the demo create adapter joins the init --force upgrade path. A fresh demo
	// writes it (absent -> created); a v0.3.x demo project under --force upgrades its
	// stale copy in place (it wrote the pre-v2 header, so gtms create --adapter demo
	// failed strict validation); an edited copy is kept with a .gtms-new companion. Route
	// its outcome through a throwaway Result and fold it into the DemoSeedResult.
	{
		tmp := &Result{}
		if err := applyUpgradeableAssetForParent(projectRoot,
			PresetAsset{RelPath: demoCreateScriptRelPath, Content: demoCreateScript, Perm: 0o755},
			force, tmp, parent); err != nil {
			return nil, fmt.Errorf("installing %s: %w", demoCreateScriptRelPath, err)
		}
		result.FilesCreated = append(result.FilesCreated, tmp.FilesCreated...)
		result.FilesSkipped = append(result.FilesSkipped, tmp.FilesSkipped...)
		result.Notes = append(result.Notes, tmp.Notes...)
		result.Warnings = append(result.Warnings, tmp.Warnings...)
	}

	// Load and merge config
	configPath := filepath.Join(projectRoot, "gtms.config")
	cfg, err := config.LoadFromFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("loading config for demo merge: %w", err)
	}

	// BUG-203 Owner Decision 1: back up gtms.config before the demo merge rewrites it,
	// never clobbering an earlier backup. On a fresh demo this writes gtms.config.bak
	// (the demo's long-standing backup); under --force Init already backed up the user's
	// config, so this preserves that and writes a further numbered backup of the
	// GTMS-generated config. This call is silent -- the user-facing backup is the one
	// Init named.
	if _, bErr := backupConfigNeverOverwrite(projectRoot); bErr != nil {
		return nil, fmt.Errorf("backing up gtms.config: %w", bErr)
	}

	// Merge demo adapter entries
	if cfg.Adapters == nil {
		cfg.Adapters = make(map[string]map[string]*config.AdapterConfig)
	}
	if cfg.Adapters["create"] == nil {
		cfg.Adapters["create"] = make(map[string]*config.AdapterConfig)
	}
	if cfg.Adapters["automate"] == nil {
		cfg.Adapters["automate"] = make(map[string]*config.AdapterConfig)
	}
	if cfg.Adapters["execute"] == nil {
		cfg.Adapters["execute"] = make(map[string]*config.AdapterConfig)
	}

	cfg.Adapters["create"]["demo"] = &config.AdapterConfig{
		Mode:   "sync",
		Script: "_demo/adapters/create-demo.sh",
	}
	cfg.Adapters["automate"]["demo-sh"] = &config.AdapterConfig{
		Mode:   "sync",
		Script: "_demo/adapters/automate-demo-sh.sh",
	}
	cfg.Adapters["automate"]["demo-cmd"] = &config.AdapterConfig{
		Mode:   "sync",
		Script: "_demo/adapters/automate-demo-cmd.sh",
	}
	cfg.Adapters["execute"]["demo-sh"] = &config.AdapterConfig{
		Mode:    "sync",
		Command: "sh {artefact_file}",
	}
	cfg.Adapters["execute"]["demo-cmd"] = &config.AdapterConfig{
		Mode:    "sync",
		Command: "cmd /c {artefact_file}",
	}

	cfg.DemoSeeded = true

	if err := config.WriteConfig(configPath, cfg); err != nil {
		return nil, fmt.Errorf("writing config with demo entries: %w", err)
	}
	result.ConfigModified = true

	// Update guidance.yaml (BUG-213: may be withheld if a guidance file is unsafe).
	modified, warn, gErr := UpdateDemoGuidance(projectRoot)
	if gErr != nil {
		return nil, gErr
	}
	result.GuidanceModified = modified
	if warn != "" {
		result.Warnings = append(result.Warnings, warn)
	}

	return result, nil
}

// UpdateDemoGuidance refreshes demo-specific guidance in .gtms/guidance.yaml,
// treating the guidance file and its backup as one update (BUG-213 Owner
// Decision 3). It returns modified=true when both files were (re)written,
// modified=false with a single warning when the update was withheld because a
// guidance file is unsafe, and a non-nil error only on an inspection or
// filesystem failure (fail closed, Owner Decision 1).
//
// It validates the active GTMS parent first (Owner Decision 1), so the
// already-seeded `gtms init --demo` CLI route -- which calls this directly --
// refuses before any mutation when the parent resolves outside the project.
//
// The pair rule: if either .gtms/guidance.yaml or .gtms/guidance.yaml.bak is a
// symlink, hard link, directory or special file, or resolves outside the
// project, neither file is read or written, so the guidance is never
// overwritten without a recovery copy and backupFile never copies an external
// file's content into the project backup.
func UpdateDemoGuidance(projectRoot string) (modified bool, warning string, err error) {
	// Owner Decision 1: validate the active parent before touching anything.
	if _, perr := resolveActiveParent(Options{ProjectRoot: projectRoot}); perr != nil {
		return false, "", perr
	}

	const guidanceRel = ".gtms/guidance.yaml"
	const backupRel = ".gtms/guidance.yaml.bak"
	guidancePath := filepath.Join(projectRoot, filepath.FromSlash(guidanceRel))
	backupPath := filepath.Join(projectRoot, filepath.FromSlash(backupRel))

	// Classify both members (with hard-link detection). An inspection error
	// fails closed.
	gState, gErr := classifyScaffoldLeaf(guidancePath)
	if gErr != nil {
		return false, "", fmt.Errorf("inspecting %s: %w", guidanceRel, gErr)
	}
	bState, bErr := classifyScaffoldLeaf(backupPath)
	if bErr != nil {
		return false, "", fmt.Errorf("inspecting %s: %w", backupRel, bErr)
	}

	// Decide the pair jointly: any unsafe member withholds the whole update.
	var phrases []string
	if p := guidanceMemberUnsafePhrase(projectRoot, guidanceRel, gState); p != "" {
		phrases = append(phrases, p)
	}
	if p := guidanceMemberUnsafePhrase(projectRoot, backupRel, bState); p != "" {
		phrases = append(phrases, p)
	}
	if len(phrases) > 0 {
		return false, fmt.Sprintf(
			"skipped refreshing demo guidance: %s; %s and %s were both left unchanged.",
			strings.Join(phrases, " and "), guidanceRel, backupRel), nil
	}

	// Both safe. Back up the current guidance (if any) before overwriting it.
	if gState.kind == kindRegular {
		current, rErr := os.ReadFile(guidancePath)
		if rErr != nil {
			return false, "", fmt.Errorf("reading %s: %w", guidanceRel, rErr)
		}
		if res, oErr := safeOverwrite(projectRoot, backupRel, current, 0o644); oErr != nil {
			return false, "", oErr
		} else if res.outcome != overwriteWrote {
			// A concurrent change made the backup unsafe between the check and
			// the write (accepted race, Owner Decision 7): withhold rather than
			// force a write through it.
			return false, fmt.Sprintf(
				"skipped refreshing demo guidance: %s became unsafe; %s and %s were both left unchanged.",
				backupRel, guidanceRel, backupRel), nil
		}
	}

	guidance := config.LoadGuidance(projectRoot)
	guidance["init"] = demoGuidanceInit
	guidance["create"] = demoGuidanceCreate
	guidance["automate"] = demoGuidanceAutomate
	guidance["execute"] = demoGuidanceExecute

	guidanceData, mErr := yaml.Marshal(guidance)
	if mErr != nil {
		return false, "", fmt.Errorf("marshalling guidance: %w", mErr)
	}
	if res, oErr := safeOverwrite(projectRoot, guidanceRel, guidanceData, 0o644); oErr != nil {
		return false, "", oErr
	} else if res.outcome != overwriteWrote {
		return false, fmt.Sprintf(
			"skipped refreshing demo guidance: %s became unsafe; %s and %s were both left unchanged.",
			guidanceRel, guidanceRel, backupRel), nil
	}
	return true, "", nil
}

// guidanceMemberUnsafePhrase returns a short phrase naming why a guidance member
// is unsafe to write (a symlink, hard link, directory or special file, or a path
// resolving outside the project), or "" when it is safe. An absent member is
// safe (it is created). Used to build the single combined guidance-pair warning.
func guidanceMemberUnsafePhrase(projectRoot, rel string, state gitignoreState) string {
	switch state.kind {
	case kindSymlink:
		return rel + " is a symbolic link"
	case kindHardLinked:
		return rel + " is a hard link"
	case kindDirectory:
		return rel + " is a directory"
	case kindSpecial:
		return rel + " is a special file"
	}
	// Absent or regular single-link: safe only if it also resolves inside the
	// project (a linked .gtms/ makes even an absent leaf resolve outside).
	if _, _, rErr := pathsafe.ResolveUnderRoot(projectRoot, rel); rErr != nil {
		return rel + " resolves outside the project"
	}
	return ""
}

// gitignoreSentinel is the line ensureGitignore treats as the "GTMS already
// manages this project's .gitignore" marker. If it's present, ensureGitignore
// leaves the file byte-for-byte unchanged -- even if newer entries below were
// added in later GTMS versions. This preserves backward compatibility with
// projects initialised before ENH-109 expanded the entry set, and honours the
// ENH-108 contract that `gtms init` must not modify a `.gitignore` that already
// declares GTMS as managed.
const gitignoreSentinel = ".gtms/"

// gitignoreEntries lists the rules ensureGitignore writes when seeding a fresh
// .gitignore (or appending to one that doesn't yet contain the sentinel).
// ENH-109 added the gtms/execution/ rules to keep per-task spill logs and
// TC-keyed binary attachments out of source control while leaving the durable
// per-test results YAMLs (gtms/execution/*.results.yaml) trackable.
var gitignoreEntries = []string{
	gitignoreSentinel,
	"gtms/execution/attachments/",
	"gtms/execution/logs/",
	// REV-209 R3-CODEX-002: the gtms.config backups --force writes at the project root may
	// hold old adapter command strings (credentials, secrets removed from the rewritten
	// config), so keep them out of source control. Root-anchored so only the top-level
	// backups match. ensureGitignoreHasConfigBak adds this to an existing project's
	// .gitignore under --force even when the sentinel is already present.
	configBakIgnorePattern,
}

// configBakIgnorePattern is the root-anchored ignore rule for gtms.config backups.
const configBakIgnorePattern = "/gtms.config.bak*"

// gitignoreEntriesFor returns the GTMS-managed .gitignore entries for the active
// parent (BUG-207 Owner Decision 9). The execution rules move under the active
// parent; .gtms/ and the root-anchored config-backup pattern stay unchanged. The
// one computed set is shared by the writer and BUG-208's unsafe-leaf warning, so
// they can never drift. Parent "gtms" returns the shipped default slice unchanged
// (AC6, AC11).
func gitignoreEntriesFor(parent string) []string {
	if parent == "gtms" || parent == "" {
		return gitignoreEntries
	}
	// CODEX-004 / CLAUDE-002: the parent folder name is a raw filesystem name but
	// goes into a .gitignore pattern, where '#' (line start), '!' (line start),
	// '*', '?', '[' and '\' are syntax. Escape them with gitignore's own rule so
	// the execution logs and attachments are still ignored for a name like '#qa',
	// '!qa' or '[qa]'. The same escaped entries feed BUG-208's unsafe-leaf warning,
	// so writer and warning stay identical.
	seg := gitignoreEscapeSegment(parent)
	return []string{
		gitignoreSentinel,
		seg + "/execution/attachments/",
		seg + "/execution/logs/",
		configBakIgnorePattern,
	}
}

// gitignoreEscapeSegment escapes the gitignore pattern metacharacters in a single
// leading path segment (the GTMS parent folder name): a backslash, the glob
// characters '*', '?' and '[', and a leading '#' or '!' (which are only special at
// the start of a line, i.e. the start of this leading segment). Trailing spaces
// are not escaped because the segment is always followed by "/execution/...", so
// no character of it is at the line end. A name with no metacharacters (such as
// "testing") is returned unchanged.
func gitignoreEscapeSegment(s string) string {
	var b strings.Builder
	for i, r := range s {
		switch {
		case r == '\\' || r == '*' || r == '?' || r == '[':
			b.WriteByte('\\')
			b.WriteRune(r)
		case i == 0 && (r == '#' || r == '!'):
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// gitignoreKind classifies the .gitignore leaf as seen by Lstat (no link
// follow). BUG-208: ensureGitignore must decide what .gitignore is before it
// reads or writes anything, so it never follows a symlink out of the project,
// never blocks on a FIFO, and never rewrites a file shared via a hard link.
type gitignoreKind int

const (
	kindAbsent     gitignoreKind = iota // no entry at the path
	kindRegular                         // regular file, exactly one link -- safe to read/write
	kindSymlink                         // symbolic link, valid or dangling
	kindDirectory                       // directory
	kindHardLinked                      // regular file with link count > 1 (D6)
	kindSpecial                         // FIFO, device, socket or other non-regular type
)

// gitignoreState is the classified .gitignore leaf plus its Lstat mode (used in
// the warning text).
type gitignoreState struct {
	kind gitignoreKind
	mode os.FileMode
}

// stateWord names the unsafe state for the warning (BUG-208 D3). Returns one of
// the stable words "symbolic link", "directory", "hard link" or "special file".
func (s gitignoreState) stateWord() string {
	switch s.kind {
	case kindSymlink:
		return "symbolic link"
	case kindDirectory:
		return "directory"
	case kindHardLinked:
		return "hard link"
	case kindSpecial:
		return "special file"
	default:
		return ""
	}
}

// gitignoreLstat and gitignoreLinkCount are indirection points so the BUG-208
// inspection-error tests can drive both failure paths deterministically (a
// hook, not file permissions, which CI may run elevated enough to defeat).
var (
	gitignoreLstat     = os.Lstat
	gitignoreLinkCount = hardLinkCount
)

// classifyGitignore inspects the .gitignore leaf without following links
// (BUG-208 file-state contract). It returns an error only on an inspection
// failure: Lstat failing for a reason other than "not exist", or a regular
// file whose hard-link count cannot be read. On an inspection error the caller
// must make no .gitignore change and must not treat the file as absent or as a
// single-link regular file (D6, AC11).
func classifyGitignore(path string) (gitignoreState, error) {
	// BUG-213: delegates to the shared leaf classifier (safewrite.go) with the
	// .gitignore inspection hooks, so .gitignore and scaffold-write
	// classification share one implementation and cannot drift.
	return classifyLeaf(path, gitignoreLstat, gitignoreLinkCount)
}

// gitignoreSkipWarning builds the single consolidated warning for an unsafe
// .gitignore (BUG-208 D2/D3). The first line carries the stable token
// `skipped updating .gitignore` and the state word; the entries the user must
// add by hand follow on indented continuation lines, taken from gitignoreEntries
// (not a separate literal, so later entry changes flow through automatically).
// The entries contain no ".gitignore" text, so only the first line counts as a
// ".gitignore" warning line. The wording deliberately avoids the retired forms
// `updating .gitignore: read`, `.gitignore is not a regular file` and
// `skipped adding`.
func gitignoreSkipWarning(state gitignoreState, parent string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "skipped updating .gitignore: it is a %s (%s), so GTMS left it untouched rather than write through it. "+
		"Add these entries to it by hand:", state.stateWord(), state.mode.String())
	for _, entry := range gitignoreEntriesFor(parent) {
		b.WriteString("\n    ")
		b.WriteString(entry)
	}
	return b.String()
}

// ensureGitignore ensures the GTMS-managed entries are listed in .gitignore.
// BUG-208: it classifies the leaf without following links first, so it never
// reads or writes through a symlink, never blocks on a FIFO, and never rewrites
// a hard-linked or directory/special-file .gitignore. Absent -> create the file
// with all entries. Regular single-link file with the sentinel present -> left
// byte-for-byte unchanged (ENH-108 idempotency). Regular single-link file
// without the sentinel -> append every entry. Any unsafe leaf (symlink,
// dangling symlink, directory, FIFO/special file, hard link) -> one consolidated
// warning on result and GitignoreUnchanged, with no read or write. An inspection
// failure returns an error and changes nothing.
func ensureGitignore(projectRoot string, result *Result) (GitignoreAction, error) {
	return ensureGitignoreForParent(projectRoot, result, "gtms")
}

// ensureGitignoreForParent is the BUG-207 parent-aware implementation: the
// execution rules it writes/lists use the active parent.
func ensureGitignoreForParent(projectRoot string, result *Result, parent string) (GitignoreAction, error) {
	gitignorePath := filepath.Join(projectRoot, ".gitignore")

	state, err := classifyGitignore(gitignorePath)
	if err != nil {
		return "", err
	}

	entries := gitignoreEntriesFor(parent)

	switch state.kind {
	case kindAbsent:
		var content strings.Builder
		for _, entry := range entries {
			content.WriteString(entry)
			content.WriteString("\n")
		}
		if werr := os.WriteFile(gitignorePath, []byte(content.String()), 0o644); werr != nil {
			return "", werr
		}
		return GitignoreCreated, nil

	case kindRegular:
		data, rerr := os.ReadFile(gitignorePath)
		if rerr != nil {
			return "", rerr
		}
		content := string(data)
		for _, line := range splitLines(content) {
			if strings.TrimSpace(line) == gitignoreSentinel {
				return GitignoreUnchanged, nil
			}
		}
		if len(content) > 0 && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		for _, entry := range entries {
			content += entry + "\n"
		}
		if werr := os.WriteFile(gitignorePath, []byte(content), 0o644); werr != nil {
			return "", werr
		}
		return GitignoreAppended, nil

	default:
		// Unsafe leaf: warn once (listing every entry) and leave it untouched.
		if result != nil {
			result.Warnings = append(result.Warnings, gitignoreSkipWarning(state, parent))
		}
		return GitignoreUnchanged, nil
	}
}

// ensureGitignoreHasConfigBak guarantees the root-anchored gtms.config backup pattern is
// present in .gitignore (REV-209 R3-CODEX-002). Unlike ensureGitignore, which leaves a
// sentinel-managed file untouched for ENH-108 idempotency, this is called only under
// --force when a backup is about to be written, and appends the pattern to an existing
// .gitignore that lacks it (creating .gitignore if absent). It is idempotent: a file that
// already lists the pattern is left unchanged.
//
// BUG-208: it uses the same leaf classification as ensureGitignore. On any unsafe leaf
// (symlink, dangling symlink, directory, FIFO/special file, hard link) it leaves the file
// untouched and stays SILENT: ensureGitignore runs first on the same path and has already
// emitted the one consolidated warning (D2), whose entry list already includes
// /gtms.config.bak*. Emitting a second warning here would duplicate it, and the retired
// `.gitignore is not a regular file` / `skipped adding` wording is now forbidden.
func ensureGitignoreHasConfigBak(projectRoot string) error {
	gitignorePath := filepath.Join(projectRoot, ".gitignore")
	state, err := classifyGitignore(gitignorePath)
	if err != nil {
		return err
	}
	switch state.kind {
	case kindAbsent:
		return os.WriteFile(gitignorePath, []byte(configBakIgnorePattern+"\n"), 0o644)
	case kindRegular:
		data, rerr := os.ReadFile(gitignorePath)
		if rerr != nil {
			return rerr
		}
		for _, line := range splitLines(string(data)) {
			if strings.TrimSpace(line) == configBakIgnorePattern {
				return nil // already ignored
			}
		}
		content := string(data)
		if len(content) > 0 && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += configBakIgnorePattern + "\n"
		return os.WriteFile(gitignorePath, []byte(content), 0o644)
	default:
		// Unsafe leaf: ensureGitignore already warned (D2); do not write through
		// the link and do not add a second warning.
		return nil
	}
}

// splitLines splits a string into lines, handling both \n and \r\n.
func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Split(s, "\n")
}

// backupConfigNeverOverwrite copies gtms.config to the first FREE backup name
// (gtms.config.bak, then gtms.config.bak.2, .3, ...) so a --force rewrite never destroys
// the user's config and a second --force never clobbers the first backup (BUG-203 Owner
// Decision 1). The numbering mirrors the .gtms-new companion scheme. It returns the
// chosen project-relative backup name, or "" when there is no gtms.config to back up.
//
// Unlike backupFile (which overwrites {path}.bak and is still used for guidance.yaml),
// this preserves every earlier backup: the very first one holds the user's original and
// must survive any number of later forced inits.
func backupConfigNeverOverwrite(projectRoot string) (string, error) {
	configPath := filepath.Join(projectRoot, "gtms.config")
	// REV-209 R3-CODEX-001: Lstat (no symlink follow). Init already aborts --force on a
	// non-regular gtms.config before reaching here; this is defence in depth so the backup
	// path can never read through a symlink into an external target.
	info, statErr := os.Lstat(configPath)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return "", nil
		}
		return "", statErr
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("gtms.config is not a regular file (%s)", info.Mode().String())
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", err
	}

	// CODEX-004: cap the backup's permission bits to the source config's own bits masked
	// by 0600, so a user-protected (0600) gtms.config never yields a world-readable
	// backup that could leak credentials embedded in adapter command strings. On Windows
	// the mode is largely advisory, but 0600 keeps the file owner-accessible, so this is
	// Windows-safe.
	perm := info.Mode().Perm() & 0o600

	// CODEX-003: allocate each candidate with O_CREATE|O_EXCL|O_WRONLY so the create
	// itself is the existence check -- no Lstat-then-write TOCTOU window, and O_EXCL
	// refuses to follow a symlink planted at the path during a race. Retry the next
	// numbered name whenever the candidate already exists.
	const baseRel = "gtms.config.bak"
	rel := baseRel
	for n := 2; ; n++ {
		f, openErr := os.OpenFile(filepath.Join(projectRoot, rel), os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
		if openErr == nil {
			_, writeErr := f.Write(data)
			closeErr := f.Close()
			if writeErr != nil {
				return "", writeErr
			}
			if closeErr != nil {
				return "", closeErr
			}
			return rel, nil
		}
		if !os.IsExist(openErr) {
			return "", openErr
		}
		rel = fmt.Sprintf("%s.%d", baseRel, n)
	}
}
