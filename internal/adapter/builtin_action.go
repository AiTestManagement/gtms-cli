package adapter

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrg/frontmatter"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// testcaseTemplateFallback is the fallback TC skeleton shape used when the
// role-specific template file is missing. ENH-161: must match the scaffolded
// TestcaseTemplateMD constant in internal/scaffold/templates.go byte-for-byte.
// A source-shape test enforces this invariant.
const testcaseTemplateFallback = `---
test_case_id: ${TESTCASE_ID}
title: "${TITLE}"
requirement: ${REQUIREMENT}
priority: Medium
type: Functional
created: ${CREATED}
---

## Test Objective


## Preconditions

-

## Test Data

-

## Test Steps

1.
   - Expected observation:

## Expected Final Outcome

-

## Postconditions

-

## Notes

`

// BuiltinCreate implements the Tier 0 built-in create adapter (ENH-150/ENH-161).
// ENH-161: reads a role-specific template file from ctx.TemplateFile, substitutes
// placeholders, and writes the TC skeleton. Falls back to testcaseTemplateFallback
// when the template file is missing (stderr warning, exit 0).
//
// Mirrors the Tier 2 create-script.sh behaviour:
//   - Takes the first ID from ctx.TestCaseIDs
//   - Reads template, substitutes placeholders, writes the TC
//   - Returns InvocationResult with ExitCode 0
func BuiltinCreate(ctx *AdapterContext) (*InvocationResult, error) {
	if ctx.OutputDir == "" {
		return nil, fmt.Errorf("output directory not set")
	}
	if ctx.TestCaseIDs == "" {
		return nil, fmt.Errorf("no test case IDs available")
	}

	// Take the first ID from the comma-separated batch
	ids := strings.Split(ctx.TestCaseIDs, ",")
	tcID := strings.TrimSpace(ids[0])
	if tcID == "" {
		return nil, fmt.Errorf("first test case ID is empty")
	}

	// Build filename -- include name slug if provided
	var outFile string
	var nameValue string
	if ctx.TestCaseName != "" {
		outFile = filepath.Join(ctx.OutputDir, tcID+"-"+ctx.TestCaseName+".md")
		nameValue = ctx.TestCaseName
	} else {
		outFile = filepath.Join(ctx.OutputDir, tcID+".md")
	}

	// Ensure output directory exists
	if err := os.MkdirAll(ctx.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("creating output directory: %w", err)
	}

	// ENH-161: Read template from disk; fall back to inline constant only when
	// the template file is absent. Other read errors (permission denied, is-a-
	// directory, I/O errors, etc.) are not "missing"-class and must surface so
	// the operator can fix them. Mirrors the Tier 2 shell `[ -f ... ]` check,
	// which would also fail at sed time if a non-missing read failure shows up.
	var tmpl string
	var stderr string
	if ctx.TemplateFile != "" {
		data, readErr := os.ReadFile(ctx.TemplateFile)
		switch {
		case readErr == nil:
			tmpl = string(data)
		case errors.Is(readErr, os.ErrNotExist):
			stderr = fmt.Sprintf("warning: create template not found: %s -- using built-in default\n", ctx.TemplateFile)
			tmpl = testcaseTemplateFallback
		default:
			return nil, fmt.Errorf("reading create template %s: %w", ctx.TemplateFile, readErr)
		}
	} else {
		tmpl = testcaseTemplateFallback
	}

	// Substitute placeholders.
	// ${REQUIREMENT} is user-controlled free text (--reference). The template
	// line is unquoted (`requirement: ${REQUIREMENT}`), so the substitution
	// must produce either a complete YAML double-quoted scalar (carrying its
	// own quotes) or the empty string. yamlEscape alone is unsafe here --
	// it produces a string fit for *inside* double quotes, which lands as a
	// bare unquoted scalar in an unquoted placeholder context and parses
	// badly for values containing `: `, leading `-`, `#`, etc.
	content := tmpl
	content = strings.ReplaceAll(content, "${TESTCASE_ID}", tcID)
	content = strings.ReplaceAll(content, "${TITLE}", nameValue)
	content = strings.ReplaceAll(content, "${REQUIREMENT}", yamlQuotedScalarOrEmpty(ctx.Reference))
	content = strings.ReplaceAll(content, "${CREATED}", time.Now().Format("2006-01-02"))

	if err := os.WriteFile(outFile, []byte(content), 0644); err != nil {
		return nil, fmt.Errorf("writing test case file: %w", err)
	}

	summary := fmt.Sprintf("Created skeleton test case: %s", outFile)
	return &InvocationResult{
		ExitCode:   0,
		Stdout:     summary,
		Stderr:     stderr,
		SavedFiles: []string{outFile},
	}, nil
}

// DeriveArtefactBasename extracts the extension-stripped basename from a
// test-case spec path. When the source spec is slugged (e.g.
// "gtms/test/cases/my-feature/tc-aaa-login-happy.md"), the result preserves the
// slug ("tc-aaa-login-happy"). When testCaseFile is empty or the extracted
// basename would be empty after stripping the extension, fallbackID is
// returned (typically ctx.TestCase, e.g. "tc-aaa").
//
// The function is framework-neutral: callers append their own extension
// (.bats, .spec.ts, .Tests.ps1, etc.) after calling. BUG-107.
func DeriveArtefactBasename(testCaseFile, fallbackID string) string {
	if testCaseFile == "" {
		return fallbackID
	}
	base := filepath.Base(testCaseFile)
	ext := filepath.Ext(base)
	if ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	if base == "" {
		return fallbackID
	}
	return base
}

// BuiltinAutomate implements the Tier 0 built-in automate adapter (ENH-151).
// It performs framework-neutral lifecycle orchestration: validates context,
// looks up framework support, delegates skeleton generation, computes the
// testcase hash, and writes a wiring record with artefact-hash set to
// PendingArtefactHash. The operator (agent or human) fills the test body
// after scaffolding; gtms execute bootstraps the real hash on first run.
//
// Framework-specific artefact generation (file extension, output path,
// skeleton content, helper paths) is owned by FrameworkSupport implementations
// registered in framework_support.go (BUG-108 / ADR-022).
//
// Context fields used:
//   - ctx.TestCase: the tc-ID
//   - ctx.Framework: resolved framework (bats, playwright, etc.)
//   - ctx.TestCaseFile: relative path to the TC spec (for hash + subdir)
//   - ctx.OutputSubdir: work-item subfolder under the framework output dir
//   - ctx.ProjectRoot: project root for wiring.Write and pipeline.HashFile
//
// cfg is needed to resolve the canonical execute adapter for the wiring record.
func BuiltinAutomate(ctx *AdapterContext, cfg *config.Config) (*InvocationResult, error) {
	if ctx.ProjectRoot == "" {
		return nil, fmt.Errorf("project root not set")
	}
	if ctx.TestCase == "" {
		return nil, fmt.Errorf("test case ID not set")
	}

	framework := ctx.Framework
	if framework == "" {
		return nil, fmt.Errorf("no --framework specified for built-in automate adapter")
	}

	// BUG-120: The manual framework is a deliberate degenerate case for the
	// automate stage (CON-023 Q#12 makes manual TCs wiring-free). There is no
	// automation artefact to produce, so the stage has no work to do. Return a
	// targeted diagnostic pointing at the correct manual workflow instead of
	// falling through to the generic "no automate support found" registry miss.
	if framework == "manual" {
		return nil, fmt.Errorf(
			"The manual framework does not require automate. "+
				"Run 'gtms prime %s' to stamp a result file, fill it in, "+
				"then 'gtms execute %s --adapter manual-execute' to record the outcome.",
			ctx.TestCase, ctx.TestCase)
	}

	// Look up framework support -- core never interprets framework-specific
	// values; it delegates to the registered FrameworkSupport implementation.
	support := LookupFrameworkSupport(framework)
	if support == nil {
		return nil, fmt.Errorf(
			"no automate support found for framework %q",
			framework)
	}

	// Compute output path using framework support.
	// BUG-107: derive artefact basename from the source spec filename to
	// preserve the human-readable slug (e.g. tc-aaa-login-happy.bats).
	subdir := ctx.OutputSubdir // e.g. "my-feature/" or ""
	var outDir string
	if ctx.OutputDirConfigured {
		// BUG-125: an explicit output-dir wins over the framework-native default so
		// brownfield projects can redirect specs into their harness testDir. ctx.OutputDir
		// is already absolute (joined with ProjectRoot in invoker); group per work-item by
		// appending the source subdir (trailing slash trimmed; "" leaves the dir unchanged).
		outDir = filepath.Join(ctx.OutputDir, filepath.FromSlash(strings.TrimRight(subdir, "/")))
	} else {
		// Unset: fall back to the framework-native default (ADR-022 / ADR-004 amendment
		// 2026-06-24). support.OutputDir returns a project-relative path.
		outDir = filepath.Join(ctx.ProjectRoot, filepath.FromSlash(support.OutputDir(subdir)))
	}
	artefactBase := DeriveArtefactBasename(ctx.TestCaseFile, ctx.TestCase)
	outFile := filepath.Join(outDir, artefactBase+support.Extension())

	// Resolve canonical execute adapter FIRST -- a missing execute adapter
	// for the framework is a hard precondition failure; we must not produce
	// the skeleton or wiring artefact when the framework cannot be executed.
	executeAdapter, _, err := ResolveCanonicalExecuteAdapter(cfg, framework)
	if err != nil {
		return nil, fmt.Errorf("resolving canonical execute adapter: %w", err)
	}

	// Compute testcase-hash from the TC spec file (also pre-flight, so the
	// skeleton is not written if the TC cannot be hashed).
	var testCaseHash string
	if ctx.TestCaseFile != "" {
		absTC := filepath.Join(ctx.ProjectRoot, filepath.FromSlash(ctx.TestCaseFile))
		if h, err := pipeline.HashFile(absTC); err == nil {
			testCaseHash = h
		}
	}
	if testCaseHash == "" {
		// Fallback: try pipeline.ResolveTestCaseSpec
		if specPath, err := pipeline.ResolveTestCaseSpec(ctx.ProjectRoot, ctx.TestCase); err == nil {
			if h, err := pipeline.HashFile(filepath.Join(ctx.ProjectRoot, filepath.FromSlash(specPath))); err == nil {
				testCaseHash = h
			}
		}
	}
	if testCaseHash == "" {
		return nil, fmt.Errorf("cannot compute testcase-hash for %s: test case spec not found or unreadable", ctx.TestCase)
	}

	// ENH-162: Template-driven skeleton generation. The orchestration layer
	// reads the framework's template file, falls back to the hardcoded const
	// when the file is absent, and surfaces non-missing read errors. Framework-
	// specific placeholder substitution stays in GenerateSkeleton.
	//
	// User-facing diagnostics name the template by its project-relative slash
	// path (gtms/automation/templates/...), not the OS-native absolute path.
	// filepath.Join produces backslashes on Windows; rendering the absolute
	// path directly would emit C:\...\gtms\automation\templates\... and break
	// portable substring assertions in BATS / scripted callers.
	templatePath := support.TemplatePath(ctx.ProjectRoot)
	templateDisplay := templatePath
	if rel, relErr := filepath.Rel(ctx.ProjectRoot, templatePath); relErr == nil {
		templateDisplay = filepath.ToSlash(rel)
	}
	var templateBody string
	var stderrMsg string
	tmplData, readErr := os.ReadFile(templatePath)
	switch {
	case readErr == nil:
		templateBody = string(tmplData)
	case errors.Is(readErr, os.ErrNotExist):
		stderrMsg = fmt.Sprintf("warning: %s automate template not found: %s -- using built-in default\n", framework, templateDisplay)
		templateBody = support.FallbackContent()
	default:
		return nil, fmt.Errorf("reading %s automate template %s: %w", framework, templateDisplay, readErr)
	}

	// Ensure output directory exists.
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return nil, fmt.Errorf("creating output directory: %w", err)
	}

	// Delegate skeleton generation to framework support with template body.
	if err := support.GenerateSkeleton(ctx.TestCase, ctx.ProjectRoot, outFile, templateBody); err != nil {
		return nil, fmt.Errorf("generating skeleton: %w", err)
	}

	// Compute project-relative artefact path (forward slashes).
	relArtefact, err := filepath.Rel(ctx.ProjectRoot, outFile)
	if err != nil {
		return nil, fmt.Errorf("computing relative artefact path: %w", err)
	}
	relArtefact = filepath.ToSlash(relArtefact)

	// Write wiring record with pending artefact-hash.
	rec := &wiring.WiringRecord{
		TestCase:     ctx.TestCase,
		TestCaseHash: testCaseHash,
		Framework:    framework,
		Adapter:      executeAdapter,
		Artefact:     relArtefact,
		ArtefactHash: wiring.PendingArtefactHash,
	}
	wiringPath, err := wiring.Write(ctx.ProjectRoot, rec)
	if err != nil {
		return nil, fmt.Errorf("writing wiring record: %w", err)
	}

	summary := fmt.Sprintf("Stamped %s skeleton: %s\nWiring: %s\nFramework: %s (artefact-hash: pending -- bootstraps on first execute)\nFill the test body, then run: gtms execute %s",
		framework, outFile, wiringPath, framework, ctx.TestCase)

	return &InvocationResult{
		ExitCode:   0,
		Stdout:     summary,
		Stderr:     stderrMsg,
		SavedFiles: []string{outFile},
	}, nil
}

// manualResultTemplateFallback is the fallback result template shape used when
// the role-specific template file is missing. ENH-161: must match the
// manualResultTemplate constant in internal/scaffold/templates.go byte-for-byte.
const manualResultTemplateFallback = "# yaml-language-server: $schema=../../schemas/manual-result.schema.json\n# -- GTMS contract (do not edit) ------------------------------------------\ntest_case_id: ${TESTCASE}\ntest_case_hash: ${TESTCASE_HASH}\nframework: manual\n\n# -- OVERALL RESULT -------------------------------------------------------\nresult:\n\n# -- Optional metadata ----------------------------------------------------\ntitle: \"${TC_TITLE}\"\nrequirement: \"${TC_REQUIREMENT}\"\npriority: \"${TC_PRIORITY}\"\ntype: \"${TC_TYPE}\"\nbranch: ${BRANCH}\n\n# -- Steps (optional) -----------------------------------------------------\nsteps:\n"

// BuiltinPrime implements the Tier 0 built-in prime adapter (ENH-150).
// It stamps a blank manual result template for the given test case,
// reimplementing manual-prime.sh logic in Go.
//
// Context fields used:
//   - ctx.TemplateFile: path to the result template
//   - ctx.OutputFile: path for the stamped output
//   - ctx.TestCase: the tc-ID
//   - ctx.TestCaseHash: hash of the TC file
//   - ctx.Branch: current git branch
//   - ctx.Force: whether to overwrite existing files
//   - ctx.TCTitle, ctx.TCRequirement, ctx.TCPriority, ctx.TCType: snapshot fields
func BuiltinPrime(ctx *AdapterContext) (*InvocationResult, error) {
	if ctx.TemplateFile == "" {
		return nil, fmt.Errorf("template file not set")
	}
	if ctx.OutputFile == "" {
		return nil, fmt.Errorf("output file not set")
	}

	// Safety: refuse to overwrite existing result file unless --force
	if _, err := os.Stat(ctx.OutputFile); err == nil && !ctx.Force {
		return &InvocationResult{
			ExitCode: 1,
			Stderr:   fmt.Sprintf("Manual result file already exists: %s\nUse --force to overwrite, or delete the file manually.", ctx.OutputFile),
		}, nil
	}

	// ENH-161: Read template; fall back to inline constant only when the
	// template file is absent. Other read errors (permission denied, is-a-
	// directory, I/O errors, etc.) must surface so the operator can fix them.
	// See BuiltinCreate above for the same rule and rationale.
	var tmplStr string
	var primeStderr string
	tmplData, readErr := os.ReadFile(ctx.TemplateFile)
	switch {
	case readErr == nil:
		tmplStr = string(tmplData)
	case errors.Is(readErr, os.ErrNotExist):
		primeStderr = fmt.Sprintf("warning: prime template not found: %s -- using built-in default\n", ctx.TemplateFile)
		tmplStr = manualResultTemplateFallback
	default:
		return nil, fmt.Errorf("reading prime template %s: %w", ctx.TemplateFile, readErr)
	}

	// Substitute variables
	content := tmplStr
	content = strings.ReplaceAll(content, "${TESTCASE}", ctx.TestCase)
	content = strings.ReplaceAll(content, "${TESTCASE_HASH}", ctx.TestCaseHash)
	content = strings.ReplaceAll(content, "${BRANCH}", ctx.Branch)
	content = strings.ReplaceAll(content, "${TC_TITLE}", yamlEscape(ctx.TCTitle))
	content = strings.ReplaceAll(content, "${TC_REQUIREMENT}", yamlEscape(ctx.TCRequirement))
	content = strings.ReplaceAll(content, "${TC_PRIORITY}", yamlEscape(ctx.TCPriority))
	content = strings.ReplaceAll(content, "${TC_TYPE}", yamlEscape(ctx.TCType))

	// Ensure output directory exists
	if err := os.MkdirAll(filepath.Dir(ctx.OutputFile), 0755); err != nil {
		return nil, fmt.Errorf("creating output directory: %w", err)
	}

	// Write stamped file
	if err := os.WriteFile(ctx.OutputFile, []byte(content), 0644); err != nil {
		return nil, fmt.Errorf("writing result template: %w", err)
	}

	summary := fmt.Sprintf("Stamped manual result template: %s", ctx.OutputFile)
	return &InvocationResult{
		ExitCode:   0,
		Stdout:     summary,
		Stderr:     primeStderr,
		SavedFiles: []string{ctx.OutputFile},
	}, nil
}

// BuiltinExecute implements the Tier 0 built-in execute adapter (ENH-150).
// It reads the parsed manual result values from the AdapterContext (already
// validated Go-side by populateManualExecuteFields), computes drift
// detection, and returns the verdict.
//
// Context fields used:
//   - ctx.ResultValue: parsed result (pass/fail/skip)
//   - ctx.TestCase: the tc-ID
//   - ctx.TestCaseHash: current hash of the TC file
//   - ctx.ResultTestCaseHash: hash from the result file (at prime time)
//   - ctx.ResultTemplate: path to the filled result file
func BuiltinExecute(ctx *AdapterContext) (*InvocationResult, error) {
	if ctx.ResultValue == "" {
		return nil, fmt.Errorf("result value not set")
	}
	if ctx.ResultTemplate == "" {
		return nil, fmt.Errorf("result template path not set")
	}

	// Drift detection: compare current hash with prime-time hash
	if ctx.ResultTestCaseHash != "" && ctx.TestCaseHash != "" &&
		ctx.TestCaseHash != ctx.ResultTestCaseHash {

		driftTimestamp := time.Now().UTC().Format(time.RFC3339)

		// Read existing result file, strip old drift fields, append new ones
		data, readErr := os.ReadFile(ctx.ResultTemplate)
		if readErr == nil {
			content := string(data)
			// Strip existing drift fields (idempotent)
			lines := strings.Split(content, "\n")
			var cleaned []string
			for _, line := range lines {
				if strings.HasPrefix(line, "drift-detected:") ||
					strings.HasPrefix(line, "drift-detected-at:") ||
					strings.HasPrefix(line, "test_case_hash_at_execute:") {
					continue
				}
				cleaned = append(cleaned, line)
			}
			content = strings.TrimRight(strings.Join(cleaned, "\n"), "\n")
			content += fmt.Sprintf("\ndrift-detected: true\n")
			content += fmt.Sprintf("drift-detected-at: %s\n", driftTimestamp)
			content += fmt.Sprintf("test_case_hash_at_execute: %s\n", ctx.TestCaseHash)

			_ = os.WriteFile(ctx.ResultTemplate, []byte(content), 0644)
		}

		// Surface drift as stderr warning
		summary := fmt.Sprintf("Manual execute recorded: %s -> %s", ctx.TestCase, ctx.ResultValue)
		return &InvocationResult{
			ExitCode:         0,
			Stdout:           summary,
			Stderr:           "WARN: test case has changed since prime -- drift diagnostics recorded",
			ResultOverride:   ctx.ResultValue,
			ArtefactOverride: ctx.ResultTemplate,
		}, nil
	}

	summary := fmt.Sprintf("Manual execute recorded: %s -> %s", ctx.TestCase, ctx.ResultValue)
	return &InvocationResult{
		ExitCode:         0,
		Stdout:           summary,
		ResultOverride:   ctx.ResultValue,
		ArtefactOverride: ctx.ResultTemplate,
	}, nil
}

// yamlEscape encodes a value for safe insertion as a YAML double-quoted scalar.
// Escapes backslash, double-quote, tab, CR, and embedded newlines.
func yamlEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

// yamlQuotedScalarOrEmpty returns a complete YAML double-quoted scalar
// (including surrounding quotes) for non-empty input, or the empty string
// for empty input. Use this for placeholders whose template line is
// unquoted (e.g. `requirement: ${REQUIREMENT}`) so the substituted value
// either lands as a properly-quoted scalar or leaves the value position
// bare (which YAML reads as null) -- never as an unquoted scalar carrying
// YAML-special characters. The shell scripts mirror this with the same
// "quote-only-when-non-empty" rule for byte-identical output.
func yamlQuotedScalarOrEmpty(s string) string {
	if s == "" {
		return ""
	}
	return `"` + yamlEscape(s) + `"`
}

// ValidateTestCasePostFill validates a test case file after the operator has
// filled it. Called at the entry point of downstream commands (automate, prime,
// execute) to catch frontmatter corruption introduced during editing (ENH-150).
//
// Checks:
//  1. Frontmatter test_case_id matches filename ID
//  2. Required frontmatter fields present (test_case_id)
//  3. No duplicate IDs in the same folder (folder-scoped)
//
// Returns a slice of SpecValidationError for each violation. Empty slice = valid.
func ValidateTestCasePostFill(projectRoot, target string) []SpecValidationError {
	casesDir := layout.TestCasesDir(projectRoot)

	// Find the TC file
	var tcPath string
	_ = filepath.Walk(casesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if strings.HasPrefix(base, target+"-") || strings.HasPrefix(base, target+".") {
			tcPath = path
			return filepath.SkipAll
		}
		return nil
	})

	if tcPath == "" {
		// TC not found -- not a validation error (other code handles missing TCs)
		return nil
	}

	var violations []SpecValidationError

	// Parse frontmatter
	f, openErr := os.Open(tcPath)
	if openErr != nil {
		return nil // skip unreadable files
	}
	defer f.Close()

	var fm specFrontmatter
	_, parseErr := frontmatter.Parse(f, &fm)
	if parseErr != nil {
		violations = append(violations, SpecValidationError{
			File:   filepath.Base(tcPath),
			Reason: fmt.Sprintf("could not parse frontmatter: %v", parseErr),
		})
		return violations
	}

	// Check 1: test_case_id is present
	if fm.TestCaseID == "" {
		violations = append(violations, SpecValidationError{
			File:   filepath.Base(tcPath),
			Reason: "frontmatter is missing required field 'test_case_id'",
		})
		return violations
	}

	// Check 2: test_case_id matches filename ID
	base := filepath.Base(tcPath)
	filenameMatch := validShapePattern.FindStringSubmatch(base)
	if filenameMatch != nil {
		filenameID := filenameMatch[1]
		if fm.TestCaseID != filenameID {
			violations = append(violations, SpecValidationError{
				File:   base,
				Reason: fmt.Sprintf("frontmatter test_case_id '%s' does not match filename ID '%s'", fm.TestCaseID, filenameID),
			})
		}
	}

	// Check 3: duplicate IDs in the same folder (folder-scoped)
	tcDir := filepath.Dir(tcPath)
	entries, readErr := os.ReadDir(tcDir)
	if readErr == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			entryBase := entry.Name()
			entryPath := filepath.Join(tcDir, entryBase)
			if entryPath == tcPath {
				continue // skip self
			}
			if !strings.HasSuffix(entryBase, ".md") {
				continue
			}

			ef, err := os.Open(entryPath)
			if err != nil {
				continue
			}
			var efm specFrontmatter
			_, err = frontmatter.Parse(ef, &efm)
			ef.Close()
			if err != nil {
				continue
			}
			if efm.TestCaseID != "" && efm.TestCaseID == fm.TestCaseID {
				violations = append(violations, SpecValidationError{
					File:   base,
					Reason: fmt.Sprintf("duplicate test_case_id '%s' (also in %s)", fm.TestCaseID, entryBase),
				})
				break // one duplicate is enough to report
			}
		}
	}

	return violations
}

// BuiltinPrimeFromTC reimplements the manual-prime context population in Go
// for Tier 0 built-in adapters that lack the Tier 2 env var contract.
// This is called from buildAdapterContext for the "prime" command when
// the resolved adapter is a built-in.
func populateBuiltinPrimeFields(ctx *AdapterContext, projectRoot, target string) {
	tcSource := findTestCaseSource(projectRoot, target)
	if tcSource == "" {
		return
	}

	absTC := filepath.Join(projectRoot, tcSource)

	// Hash
	if hash, hashErr := pipeline.HashFile(absTC); hashErr == nil {
		ctx.TestCaseHash = hash
	}

	// Template and output paths
	// ENH-161: Template path is set by buildAdapterContext via ResolveTemplatePath.
	// Only set here as fallback if not already populated (backward compatibility).
	if ctx.TemplateFile == "" {
		ctx.TemplateFile = filepath.Join(layout.ManualTemplatesDir(projectRoot), "manual-result.template.yaml")
	}
	ctx.OutputFile = filepath.Join(layout.ManualRecordsDir(projectRoot), target+"--manual.result.yaml")
	ctx.OutputDir = layout.ManualRecordsDir(projectRoot)
	ctx.OutputSubdir = ""
	ctx.TestCaseFile = tcSource

	// TC frontmatter snapshot (ENH-142)
	if tcData, readErr := os.ReadFile(absTC); readErr == nil {
		var tcFM struct {
			Title       string `yaml:"title"`
			Requirement string `yaml:"requirement"`
			Priority    string `yaml:"priority"`
			Type        string `yaml:"type"`
		}
		if _, parseErr := frontmatter.Parse(bytes.NewReader(tcData), &tcFM); parseErr == nil {
			ctx.TCTitle = tcFM.Title
			ctx.TCRequirement = tcFM.Requirement
			ctx.TCPriority = tcFM.Priority
			ctx.TCType = tcFM.Type
		}
	}
}
