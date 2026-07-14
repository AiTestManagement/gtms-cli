// Package layoutmigration provides the shared filesystem-native migration core
// for converting legacy gtms/cases/ trees to the ENH-164 gtms/test/ layout.
//
// Consumed by:
//   - Dev-time dogfood content migration (job b)
//   - User-runtime migration shim (job c) wired into scaffold/root command
//
// Constraint: this package must NOT import "os/exec". The runtime shim runs on
// end-user installs where GTMS must not invoke git on the user's working tree
// (and where git may not even be present). The dev-time wrapper (job b) can add
// git mv calls for rename-history preservation, but that wrapper lives outside
// this package. Source-shape test enforces this invariant.
package layoutmigration

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LegacyCasesDir is the v0.2.0-shape directory that triggers migration.
const LegacyCasesDir = "gtms/cases"

// LegacyDefaultGuideDir is the known default guide-dir for v0.2.0-shape installs.
const LegacyDefaultGuideDir = "gtms/cases/guides"

// LegacyDefaultGuideDirSlash is the trailing-slash variant.
const LegacyDefaultGuideDirSlash = "gtms/cases/guides/"

// NewTestCasesDir is the post-ENH-164 test cases directory.
const NewTestCasesDir = "gtms/test/cases"

// NewTestTemplatesDir is the post-ENH-164 test templates directory.
const NewTestTemplatesDir = "gtms/test/templates"

// NewTestGuidesDir is the post-ENH-164 test guides directory.
const NewTestGuidesDir = "gtms/test/guides"

// NewDefaultGuideDir is the post-ENH-164 guide-dir config value.
const NewDefaultGuideDir = "gtms/test/guides"

// LegacyPromptsDir is the v0.2.0-shape and ENH-164-half-migrated location for
// the create prompt template directory. ENH-164's Migrate() intentionally
// preserves this slot (step 4 skips "prompts") rather than expanding ENH-164's
// scope, so it survives into the post-ENH-164 state and is handled by
// MigratePrompts in a separate composable step.
const LegacyPromptsDir = "gtms/cases/prompts"

// LegacyPromptTemplatePath is the create-standard.md file path used by both
// pre-ENH-164 v0.2.0 installs and ENH-164-half-migrated installs.
const LegacyPromptTemplatePath = "gtms/cases/prompts/create-standard.md"

// NewTestPromptsDir is the post-ENH-165 prompts directory.
const NewTestPromptsDir = "gtms/test/prompts"

// NewPromptTemplatePath is the post-ENH-165 create-standard.md file path.
const NewPromptTemplatePath = "gtms/test/prompts/create-standard.md"

// MigrationError represents a safety-check failure during migration.
type MigrationError struct {
	GuideDirPath string
	Message      string
}

func (e *MigrationError) Error() string {
	return e.Message
}

// HasRenamedParentLegacy returns true when a renamed-parent legacy install
// is detected -- the project's parent directory was renamed via ENH-098
// (e.g. gtms -> testing) AND a legacy `cases/` slot still lives directly
// under that renamed parent (e.g. testing/cases/).
//
// The migration core's filesystem constants (LegacyCasesDir, LegacyPromptsDir,
// LegacyDefaultGuideDir) are anchored at the canonical "gtms" parent name.
// Running Migrate() / MigratePrompts() against a renamed-parent legacy
// install would silently no-op (NeedsLegacyMigration only checks gtms/cases),
// while the reader -- which derives its walk from layout.Current() initialised
// from the discovered parentName -- would look under <parentName>/test/cases/
// and find nothing. The user would lose visibility silently.
//
// Callers MUST halt with a clear error when this returns true, directing the
// user to either rename the parent back to gtms/ temporarily so the canonical
// migration can run, or migrate manually.
//
// Returns false for the canonical parent ("gtms" or "" treated as default)
// because NeedsLegacyMigration already covers that case.
func HasRenamedParentLegacy(projectRoot, parentName string) bool {
	if parentName == "" || parentName == "gtms" {
		return false
	}
	legacyCases := filepath.Join(projectRoot, parentName, "cases")
	info, err := os.Stat(legacyCases)
	return err == nil && info.IsDir()
}

// NeedsLegacyMigration checks if the project has a legacy gtms/cases/ tree
// that needs migration. Returns true if gtms/cases/ exists and gtms/test/cases/
// does not exist.
func NeedsLegacyMigration(projectRoot string) bool {
	legacyDir := filepath.Join(projectRoot, LegacyCasesDir)
	newDir := filepath.Join(projectRoot, NewTestCasesDir)

	legacyInfo, legacyErr := os.Stat(legacyDir)
	if legacyErr != nil || !legacyInfo.IsDir() {
		return false
	}

	newInfo, newErr := os.Stat(newDir)
	if newErr == nil && newInfo.IsDir() {
		return false // already migrated
	}

	return true
}

// CheckGuideDir performs the three-way safety check on the guide-dir config value.
// Returns:
//   - nil if migration can proceed (Case 1 or Case 3)
//   - *MigrationError if guide-dir is customised inside the legacy tree (Case 2)
//
// The guideDirValue parameter is the raw guide-dir from gtms.config.
// An empty string is treated as Case 1 (unset = default).
//
// Path normalisation: backslash separators (Windows-style) are converted to
// forward slashes via explicit strings.ReplaceAll before the slash-form
// prefix check. Without this, a config carrying gtms\cases\my-guides would
// slip past the Case 2 prefix check and be misclassified as Case 3,
// defeating the safety rail.
//
// Why strings.ReplaceAll and NOT filepath.ToSlash: filepath.ToSlash only
// converts the host OS's native separator. On Linux, '\' is not a path
// separator at all, so filepath.ToSlash is a no-op. A Windows-authored
// gtms.config checked out on a Linux CI runner would therefore retain its
// backslashes and bypass the safety check there. The classification must
// be cross-platform because the config text origin is independent of the
// host running the check.
func CheckGuideDir(guideDirValue string) error {
	// Normalise: convert backslashes to forward slashes unconditionally,
	// then strip trailing slash.
	normalised := strings.TrimRight(strings.ReplaceAll(guideDirValue, `\`, "/"), "/")

	// Case 1: unset or known default
	if normalised == "" || normalised == "gtms/cases/guides" {
		return nil
	}

	// Case 2: customised inside legacy tree
	if strings.HasPrefix(normalised, "gtms/cases/") || normalised == "gtms/cases" {
		return &MigrationError{
			GuideDirPath: guideDirValue,
			Message: fmt.Sprintf(
				"guide-dir points to a custom path inside the legacy gtms/cases/ tree: %s\n"+
					"    Move your guides to the new gtms/test/guides/ directory and update guide-dir in gtms.config manually.",
				guideDirValue),
		}
	}

	// Case 3: customised outside legacy tree
	return nil
}

// IsCase1GuideDir returns true if the guide-dir is unset or the known default.
//
// Path normalisation: backslash separators (Windows-style) are converted to
// forward slashes via explicit strings.ReplaceAll before comparison. Mirrors
// CheckGuideDir's normalisation -- see the doc-comment there for why this is
// strings.ReplaceAll and not filepath.ToSlash (the latter is a no-op on
// Linux, leaving Windows-authored backslash configs unprotected on Linux CI).
func IsCase1GuideDir(guideDirValue string) bool {
	normalised := strings.TrimRight(strings.ReplaceAll(guideDirValue, `\`, "/"), "/")
	return normalised == "" || normalised == "gtms/cases/guides"
}

// GuideDirEntry describes one (command, adapter) -> guide-dir pair collected
// from gtms.config for the runtime shim's three-way safety check.
type GuideDirEntry struct {
	Command string
	Adapter string
	Value   string
}

// FirstCase2Offender returns the first entry whose guide-dir falls into Case 2
// (customised inside the legacy gtms/cases/ tree), along with the underlying
// MigrationError. Returns (nil, nil) when every entry is Case 1 or Case 3 --
// migration is safe to proceed. The order of inspection is the iteration order
// of the supplied slice; callers wanting a deterministic order should sort
// before calling.
//
// This helper exists so the runtime shim can scan every adapter entry rather
// than just create-facing ones: guide-dir is a field on the shared
// AdapterConfig, so any command-key adapter can semantically carry one. The
// migration safety rule is about filesystem preservation, not command
// semantics -- if any adapter points inside gtms/cases/, the shim must halt.
func FirstCase2Offender(entries []GuideDirEntry) (*GuideDirEntry, *MigrationError) {
	for i := range entries {
		err := CheckGuideDir(entries[i].Value)
		if err == nil {
			continue
		}
		var migErr *MigrationError
		if errors.As(err, &migErr) {
			return &entries[i], migErr
		}
	}
	return nil, nil
}

// Migrate performs the one-shot migration of a legacy gtms/cases/ tree to
// the new gtms/test/ layout. It is filesystem-native only -- no git shell-outs.
//
// Steps:
//  1. Create gtms/test/cases/, gtms/test/templates/, gtms/test/guides/
//  2. Move gtms/cases/templates/* -> gtms/test/templates/
//  3. Move gtms/cases/guides/* -> gtms/test/guides/
//  4. Move remaining user TC folders from gtms/cases/ -> gtms/test/cases/
//  5. Remove the empty gtms/cases/ directory tree
//
// If guideDirRewrite is true (Case 1), the caller is responsible for
// rewriting guide-dir in gtms.config after this function returns.
func Migrate(projectRoot string) error {
	legacyDir := filepath.Join(projectRoot, LegacyCasesDir)
	newCasesDir := filepath.Join(projectRoot, NewTestCasesDir)
	newTemplatesDir := filepath.Join(projectRoot, NewTestTemplatesDir)
	newGuidesDir := filepath.Join(projectRoot, NewTestGuidesDir)

	// 1. Create target directories
	for _, dir := range []string{newCasesDir, newTemplatesDir, newGuidesDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating directory %s: %w", dir, err)
		}
	}

	// 2. Move templates
	legacyTemplatesDir := filepath.Join(legacyDir, "templates")
	if err := moveContents(legacyTemplatesDir, newTemplatesDir); err != nil {
		return fmt.Errorf("moving templates: %w", err)
	}

	// 3. Move guides
	legacyGuidesDir := filepath.Join(legacyDir, "guides")
	if err := moveContents(legacyGuidesDir, newGuidesDir); err != nil {
		return fmt.Errorf("moving guides: %w", err)
	}

	// 4. Move remaining user TC folders and files
	entries, err := os.ReadDir(legacyDir)
	if err != nil {
		return fmt.Errorf("reading legacy directory: %w", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		// Skip already-handled subdirectories and hidden files
		if name == "templates" || name == "guides" || name == "prompts" || strings.HasPrefix(name, ".") {
			continue
		}
		src := filepath.Join(legacyDir, name)
		dst := filepath.Join(newCasesDir, name)
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("moving %s to %s: %w", src, dst, err)
		}
	}

	// 5. Remove empty legacy directories
	removeEmptyDirTree(legacyDir)

	return nil
}

// moveContents moves all files and subdirectories from src to dst.
// If src does not exist, this is a no-op.
func moveContents(src, dst string) error {
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return nil
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("reading %s: %w", src, err)
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if err := os.Rename(srcPath, dstPath); err != nil {
			return fmt.Errorf("moving %s to %s: %w", srcPath, dstPath, err)
		}
	}

	return nil
}

// removeEmptyDirTree walks a directory tree bottom-up and removes only
// directories that are genuinely empty. Non-empty directories (containing
// files like prompts/, .gitkeep, or any other content skipped by step 4)
// are left in place. This is safe because os.Remove on a non-empty directory
// returns an error which we silently ignore.
func removeEmptyDirTree(dir string) {
	// Collect all directories first, then remove bottom-up.
	var dirs []string
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	})
	// Walk in reverse order (deepest first) so children are removed before parents.
	for i := len(dirs) - 1; i >= 0; i-- {
		// os.Remove fails on non-empty directories -- that is the safety net.
		_ = os.Remove(dirs[i])
	}
}

// RewriteGuideDirInConfig reads gtms.config, replaces the old guide-dir
// value with the new one, and writes it back. This is a simple text-level
// replacement to avoid YAML round-trip reordering.
func RewriteGuideDirInConfig(projectRoot, oldValue, newValue string) error {
	configPath := filepath.Join(projectRoot, "gtms.config")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("reading gtms.config: %w", err)
	}

	content := string(data)
	// Replace all occurrences of the old guide-dir value
	updated := strings.ReplaceAll(content, oldValue, newValue)
	if updated == content {
		// Also try without trailing slash
		oldTrimmed := strings.TrimRight(oldValue, "/")
		newTrimmed := strings.TrimRight(newValue, "/")
		updated = strings.ReplaceAll(content, oldTrimmed, newTrimmed)
	}

	if err := os.WriteFile(configPath, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("writing gtms.config: %w", err)
	}

	return nil
}

// NeedsPromptsMigration returns true if the project still carries the legacy
// create-standard.md prompt template at gtms/cases/prompts/. Returns false
// once it has been moved (or was never present).
//
// This is INDEPENDENT of NeedsLegacyMigration: in the ENH-164-half-migrated
// state, gtms/test/cases/ already exists (so NeedsLegacyMigration returns
// false) but gtms/cases/prompts/create-standard.md still exists. MigratePrompts
// must run in that case to finish the structural cleanup.
func NeedsPromptsMigration(projectRoot string) bool {
	legacyPath := filepath.Join(projectRoot, LegacyPromptTemplatePath)
	_, err := os.Stat(legacyPath)
	return err == nil
}

// MigratePrompts moves the create prompt template from the legacy
// gtms/cases/prompts/ slot to the new gtms/test/prompts/ slot (ENH-165),
// rewrites the prompt-template literal in gtms.config, and cleans up the
// emptied legacy directory tree.
//
// Composes with Migrate(): ENH-164's Migrate() intentionally preserves
// prompts/ in place (step 4 skips the "prompts" subdir to avoid a data-loss
// regression). MigratePrompts runs after Migrate() to complete the cleanup.
// In a single shim invocation against a pre-ENH-164 v0.2.0-shape install,
// Migrate() handles steps 1-5 and MigratePrompts() handles the prompts/
// cleanup, producing a clean post-ENH-165 state in one pass.
//
// Filesystem-native: no os/exec, no git shell-outs (preserves the
// runtime-shim invariant shared with Migrate).
//
// Returns:
//   - warning != "" when gtms/cases/ retained content the migration did not
//     understand (left in place; the user reviews and removes manually).
//   - err != nil only on filesystem errors that prevent the prompt-template
//     move or the config rewrite.
//
// The warning is non-fatal: the prompt move and config rewrite still succeed.
// .gitkeep files in the legacy slots are scaffold artefacts (not user content)
// and are removed during cleanup so genuinely-clean trees produce no warning.
func MigratePrompts(projectRoot string) (warning string, err error) {
	if !NeedsPromptsMigration(projectRoot) {
		return "", nil
	}

	legacyTemplatePath := filepath.Join(projectRoot, LegacyPromptTemplatePath)
	newPromptsDir := filepath.Join(projectRoot, NewTestPromptsDir)
	newTemplatePath := filepath.Join(projectRoot, NewPromptTemplatePath)
	legacyPromptsDir := filepath.Join(projectRoot, LegacyPromptsDir)
	legacyCasesDir := filepath.Join(projectRoot, LegacyCasesDir)

	// 1. Ensure target dir exists.
	if err := os.MkdirAll(newPromptsDir, 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", newPromptsDir, err)
	}

	// 2. Move create-standard.md to its new home, handling the collision
	// case where both the legacy and new paths already carry a copy. Without
	// this, os.Rename would silently overwrite the new file on Unix or fail
	// with a generic error on Windows -- either way mishandling the migration's
	// data-preservation posture. Per the review finding: compare contents;
	// no-op if identical, halt if divergent.
	if _, statErr := os.Stat(newTemplatePath); statErr == nil {
		legacyContent, readErr := os.ReadFile(legacyTemplatePath)
		if readErr != nil {
			return "", fmt.Errorf("reading legacy prompt template for collision check: %w", readErr)
		}
		newContent, readErr := os.ReadFile(newTemplatePath)
		if readErr != nil {
			return "", fmt.Errorf("reading new prompt template for collision check: %w", readErr)
		}
		if !bytes.Equal(legacyContent, newContent) {
			return "", fmt.Errorf(
				"both legacy and new create-standard.md exist with different content:\n"+
					"    %s\n"+
					"    %s\n"+
					"    Reconcile manually (keep one and remove the other) before re-running.",
				LegacyPromptTemplatePath, NewPromptTemplatePath)
		}
		// Contents identical -- the legacy copy is redundant. Remove it
		// instead of renaming over the (already-correct) new file.
		if removeErr := os.Remove(legacyTemplatePath); removeErr != nil {
			return "", fmt.Errorf("removing redundant legacy prompt template: %w", removeErr)
		}
	} else {
		// Standard path: only legacy exists. Move it.
		if err := os.Rename(legacyTemplatePath, newTemplatePath); err != nil {
			return "", fmt.Errorf("moving %s to %s: %w", legacyTemplatePath, newTemplatePath, err)
		}
	}

	// 3. Rewrite gtms.config prompt-template literal.
	if err := RewritePromptTemplateInConfig(projectRoot, LegacyPromptTemplatePath, NewPromptTemplatePath); err != nil {
		return "", fmt.Errorf("rewriting gtms.config prompt-template: %w", err)
	}

	// 4. Clean up scaffold .gitkeep artefacts in the emptied legacy slots so
	// genuinely-clean trees collapse to nothing. These are GTMS scaffold
	// markers, not user content -- removing them is what lets gtms/cases/
	// itself disappear after the prompt template has been moved out. Any
	// other content in either slot is preserved by removeEmptyDirTree below.
	removeGitkeepIfPresent(legacyPromptsDir)
	removeGitkeepIfPresent(legacyCasesDir)

	// 5. Remove now-empty legacy directories bottom-up. Per L16, this only
	// removes genuinely empty dirs (os.Remove fails silently on non-empty).
	removeEmptyDirTree(legacyCasesDir)

	// 6. If gtms/cases/ survived the cleanup, it contains user content the
	// migration did not understand. Surface a specific warning so the user
	// can review and remove manually; the prompt move + config rewrite have
	// completed successfully regardless.
	if _, statErr := os.Stat(legacyCasesDir); statErr == nil {
		warning = fmt.Sprintf(
			"%s retained: it still contains files not handled by the migration. "+
				"Review and remove or move them manually.",
			LegacyCasesDir)
	}

	return warning, nil
}

// RewritePromptTemplateInConfig reads gtms.config, replaces the legacy
// prompt-template literal with the new one, and writes it back. Mirrors
// RewriteGuideDirInConfig's text-level replacement to avoid YAML round-trip
// reordering.
//
// Path-separator normalisation: oldValue is the canonical forward-slash
// constant (LegacyPromptTemplatePath), but a Windows-authored gtms.config
// may carry the prompt-template literal in backslash form. Without the
// backslash-form pass, MigratePrompts would move the file successfully
// and leave the config pointing at the now-missing legacy path -- the
// same separator-normalisation class fixed for CheckGuideDir /
// IsCase1GuideDir in commit 906e3c3a. The new value is always written
// in canonical forward-slash form per YAML convention.
//
// Note: RewriteGuideDirInConfig is safe without this normalisation
// because its callers pass the raw oldValue from the parsed config
// (so backslashes match natively). This function gets a hardcoded
// forward-slash constant -- hence the explicit backslash pass.
func RewritePromptTemplateInConfig(projectRoot, oldValue, newValue string) error {
	configPath := filepath.Join(projectRoot, "gtms.config")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("reading gtms.config: %w", err)
	}

	content := string(data)
	updated := strings.ReplaceAll(content, oldValue, newValue)

	// Windows-style backslash form. Only attempt if oldValue contains
	// forward slashes (otherwise oldBackslash == oldValue and the second
	// pass is a no-op).
	oldBackslash := strings.ReplaceAll(oldValue, "/", `\`)
	if oldBackslash != oldValue {
		updated = strings.ReplaceAll(updated, oldBackslash, newValue)
	}

	if err := os.WriteFile(configPath, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("writing gtms.config: %w", err)
	}

	return nil
}

// removeGitkeepIfPresent silently removes a .gitkeep file from dir if present.
// .gitkeep is a GTMS scaffold artefact (created by gtms init to track
// otherwise-empty dirs in git) and is removed during migration cleanup so the
// parent dir itself can be removed afterwards via removeEmptyDirTree. Any
// other content is preserved.
func removeGitkeepIfPresent(dir string) {
	_ = os.Remove(filepath.Join(dir, ".gitkeep"))
}
