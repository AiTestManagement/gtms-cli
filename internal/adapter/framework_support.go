package adapter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/scaffold"
)

// FrameworkSupport defines the contract for framework-specific automate
// artefact generation. Implementations are registered per framework name
// in frameworkRegistry. Core BuiltinAutomate looks up support by framework
// and delegates generation -- it never interprets framework-specific values
// such as file extensions, output paths, or skeleton content.
//
// BUG-108 / ADR-022: framework-neutral core, adapter-owned framework behaviour.
// ENH-162: added TemplatePath and FallbackContent for template-driven skeletons,
// and GenerateSkeleton now receives the template body from the orchestration layer.
type FrameworkSupport interface {
	// Extension returns the file extension for generated artefacts
	// (e.g. ".bats", ".spec.ts", ".Tests.ps1"). Includes the leading dot.
	Extension() string

	// OutputDir returns the project-relative output directory for artefacts,
	// given the work-item subdir (e.g. "my-feature/" or "a/b/").
	// The returned path uses forward slashes and is relative to the project root.
	OutputDir(subdir string) string

	// TemplatePath returns the absolute path to the framework's automate
	// skeleton template file (e.g. gtms/automation/templates/bats.template.bats).
	// ENH-162: the orchestration layer in BuiltinAutomate reads this path.
	TemplatePath(projectRoot string) string

	// FallbackContent returns the hardcoded skeleton template body used when
	// the template file is absent. ENH-162: this is the same Go const that
	// the scaffold writes to disk, ensuring single-source-of-truth parity.
	FallbackContent() string

	// GenerateSkeleton substitutes framework-specific placeholders into
	// templateBody and writes the result to outPath.
	// tcID is the test case ID (e.g. "tc-aaa12345"), projectRoot is the
	// absolute project root, outPath is the absolute destination path,
	// and templateBody is the template content (already read by the
	// orchestration layer). The caller ensures the parent directory exists.
	// ENH-162: the read/fallback/warn logic moved to BuiltinAutomate.
	GenerateSkeleton(tcID, projectRoot, outPath, templateBody string) error
}

// frameworkRegistry maps framework names to their FrameworkSupport
// implementation. Adding a new framework is a single registry entry --
// core BuiltinAutomate never needs to change.
//
// BUG-111: registered "playwright" to unblock --preset playwright. The
// generated artefact is a deliberately skeletal one-file-per-TC .spec.ts
// (ENH-112 is the ENH for richer Playwright integration).
var frameworkRegistry = map[string]FrameworkSupport{
	"bats":       &BATSSupport{},
	"playwright": &PlaywrightSupport{},
}

// LookupFrameworkSupport returns the FrameworkSupport for the given framework
// name, or nil if no support is registered. Core code uses this to determine
// whether automate can proceed for the requested framework.
func LookupFrameworkSupport(framework string) FrameworkSupport {
	return frameworkRegistry[framework]
}

// BATSSupport implements FrameworkSupport for the BATS testing framework.
// It owns the BATS file extension, output path convention, helper loading
// depth computation, and framework-specific placeholder substitution.
type BATSSupport struct{}

// Extension returns ".bats".
func (b *BATSSupport) Extension() string {
	return ".bats"
}

// OutputDir returns the project-relative BATS output directory.
// BATS artefacts live under test/acceptance/{subdir}/.
func (b *BATSSupport) OutputDir(subdir string) string {
	base := "test/acceptance"
	subdir = strings.TrimRight(subdir, "/")
	if subdir == "" {
		return base
	}
	return base + "/" + subdir
}

// TemplatePath returns the absolute path to the BATS automate template.
// ENH-162: gtms/automation/templates/bats.template.bats.
func (b *BATSSupport) TemplatePath(projectRoot string) string {
	return filepath.Join(layout.AutomationTemplatesDir(projectRoot), "bats.template.bats")
}

// FallbackContent returns the hardcoded BATS skeleton template.
// ENH-162: single source of truth shared with the scaffolded file.
func (b *BATSSupport) FallbackContent() string {
	return scaffold.BATSAutomateTemplate
}

// GenerateSkeleton substitutes BATS-specific placeholders into templateBody
// and writes the result to outPath. The depth-string computation
// (${PROJECT_ROOT_DEPTH}) is BATS-specific; ${TESTCASE_ID} is common.
// ENH-162: templateBody is provided by the orchestration layer in BuiltinAutomate.
func (b *BATSSupport) GenerateSkeleton(tcID, projectRoot, outPath, templateBody string) error {
	// Compute project-relative path for depth calculation.
	relPath, err := filepath.Rel(projectRoot, outPath)
	if err != nil {
		return fmt.Errorf("computing relative path for skeleton: %w", err)
	}
	relPath = filepath.ToSlash(relPath)

	// Depth: count directory segments in the artefact's relative directory.
	// test/acceptance/my-feature/tc-xxx.bats -> dir = test/acceptance/my-feature -> 3 segments -> "../../.."
	dir := filepath.ToSlash(filepath.Dir(relPath))
	segments := strings.Split(dir, "/")
	dotdots := make([]string, len(segments))
	for i := range dotdots {
		dotdots[i] = ".."
	}
	depth := strings.Join(dotdots, "/")

	content := templateBody
	content = strings.ReplaceAll(content, "${TESTCASE_ID}", tcID)
	content = strings.ReplaceAll(content, "${PROJECT_ROOT_DEPTH}", depth)

	return os.WriteFile(outPath, []byte(content), 0644)
}

// PlaywrightSupport implements FrameworkSupport for the Playwright testing
// framework. BUG-111: deliberately skeletal one-file-per-TC starter; ENH-112
// will add richer Playwright integration (shared-file selection, append mode,
// JUnit parsing). Owns the .spec.ts extension, the gtms/scripts/playwright
// output path, and framework-specific placeholder substitution.
type PlaywrightSupport struct{}

// Extension returns ".spec.ts".
func (p *PlaywrightSupport) Extension() string {
	return ".spec.ts"
}

// OutputDir returns the project-relative Playwright output directory.
// Playwright artefacts live under gtms/scripts/playwright/{subdir}/ so the
// playwright-runner.sh execute adapter can resolve them by TC ID without
// colliding with the BATS test/acceptance/ tree.
func (p *PlaywrightSupport) OutputDir(subdir string) string {
	base := "gtms/scripts/playwright"
	subdir = strings.TrimRight(subdir, "/")
	if subdir == "" {
		return base
	}
	return base + "/" + subdir
}

// TemplatePath returns the absolute path to the Playwright automate template.
// ENH-162: gtms/automation/templates/playwright.template.spec.ts.
func (p *PlaywrightSupport) TemplatePath(projectRoot string) string {
	return filepath.Join(layout.AutomationTemplatesDir(projectRoot), "playwright.template.spec.ts")
}

// FallbackContent returns the hardcoded Playwright skeleton template.
// ENH-162: single source of truth shared with the scaffolded file.
func (p *PlaywrightSupport) FallbackContent() string {
	return scaffold.PlaywrightAutomateTemplate
}

// GenerateSkeleton substitutes Playwright-specific placeholders into
// templateBody and writes the result to outPath. Only ${TESTCASE_ID} is
// substituted; Playwright has no depth-string placeholder.
// ENH-162: templateBody is provided by the orchestration layer in BuiltinAutomate.
func (p *PlaywrightSupport) GenerateSkeleton(tcID, projectRoot, outPath, templateBody string) error {
	content := strings.ReplaceAll(templateBody, "${TESTCASE_ID}", tcID)
	return os.WriteFile(outPath, []byte(content), 0644)
}
