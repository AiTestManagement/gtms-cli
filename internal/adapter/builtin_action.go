package adapter

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/adrg/frontmatter"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
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

// artefactSlugUnsafe matches every character not allowed to survive into a
// derived descriptive slug. The slug is only ever appended to an already-proven
// owner id, but it originates in a filename, so it is narrowed to a
// conservative alphabet.
var artefactSlugUnsafe = regexp.MustCompile(`[^A-Za-z0-9._]+`)

// DeriveArtefactBasename returns the extension-less basename for an automate
// artefact: the RESOLVED OWNER ID followed by a descriptive slug.
//
// IDENTITY IS NOT PARSED, IT IS SUPPLIED. The owner id is already proven by the
// time this runs, so the basename is constructed as ownerID + slug and the slug
// step is never allowed to decide how much of the source filename is identity.
// That distinction is the whole fix. An earlier version stripped a leading
// "id-shaped token" from the filename, which cannot be done correctly: the TC-ID
// grammar (tcIDGrammar, internal/testcase/snapshot.go) admits SINGLE dashes, so
// "tc-aaaa-bbbb" is a legitimate id and "tc-aaaa-bbbb-checkout" is genuinely
// ambiguous -- tc-aaaa with slug "bbbb-checkout", or tc-aaaa-bbbb with slug
// "checkout". Any parser that answers that question answers it wrongly half the
// time, and an artefact name is an ownership statement.
//
// The rule, given the source spec's basename with its extension stripped ("the
// stem"):
//
//  1. The slug is the stem with the OWNER'S OWN ID removed from the front -- an
//     exact literal match on a value that is already proven, not a pattern --
//     then leading separators trimmed and the remainder sanitised. No other
//     text is ever removed from the stem.
//  2. The basename is ownerID, plus "-" and the slug when the slug is non-empty.
//
// There is no fast path and no special case. A normally named spec keeps its
// current artefact name because the CONSTRUCTED answer happens to equal the
// existing stem, which is a computed result rather than a pattern match:
// "tc-cccccccc-checkout.md" owned by tc-cccccccc yields slug "checkout" and
// basename "tc-cccccccc-checkout", the name it already has.
//
// A renamed owner therefore keeps the whole foreign-looking stem as descriptive
// text: "tc-aaaaaaaa-file.md" owned by tc-bbbbbbbb yields
// "tc-bbbbbbbb-tc-aaaaaaaa-file". That is deliberate. The name is deterministic,
// owner-prefixed and descriptive, and -- unlike any stripped variant -- it
// cannot be confused with an artefact tc-aaaaaaaa would itself produce.
//
// ownerID empty is a defensive fallback only (automate cannot reach this without
// a proven owner: requireCarriedOwner) and preserves the filename stem so the
// function is never a source of empty names.
//
// The function is framework-neutral: callers append their own extension (.bats,
// .spec.ts, .Tests.ps1, etc.). BUG-107 introduced the descriptive slug; this
// revision takes identity away from it.
func DeriveArtefactBasename(testCaseFile, ownerID string) string {
	stem := specFileStem(testCaseFile)
	if ownerID == "" {
		// F9: defensive only -- automate cannot reach this without a proven owner
		// (requireCarriedOwner), and the Tier 0 path refuses without a planned
		// path anyway. It still goes through the SAME sanitiser as the slug: this
		// was the one branch that returned raw filename text, and a branch that
		// skips the sanitiser is a branch someone later reaches. It carries no
		// identity authority; it exists so the function is never a source of
		// empty or unsanitised names.
		return sanitiseArtefactText(stem)
	}
	slug := descriptiveSlug(stem, ownerID)
	if slug == "" {
		return ownerID
	}
	return ownerID + "-" + slug
}

// descriptiveSlug reduces a spec filename stem to descriptive text. The ONLY
// thing it removes is the owner's own id, matched as an exact literal prefix.
// Everything else in the stem survives as description, because nothing else in
// the stem can be known to be identity.
func descriptiveSlug(stem, ownerID string) string {
	rest := stem
	// The owner id is stripped only at a TOKEN BOUNDARY -- the id must be
	// followed by a separator or be the whole stem. A bare TrimPrefix also
	// matched a longer word that merely starts with the id, so owner tc-aaaa on
	// the stem "tc-aaaabbbb-x" yielded "tc-aaaa-bbbb-x": a name that looks like
	// it belongs to some other identity's slug space, widening the collision
	// surface for nothing. Refusing to strip there costs only a longer name.
	if after, ok := strings.CutPrefix(rest, ownerID); ok && (after == "" || isArtefactSeparator(after[0])) {
		rest = after
	}
	rest = strings.TrimLeft(rest, "-_.")
	return sanitiseArtefactText(rest)
}

// isArtefactSeparator reports whether b ends an identity token in a filename.
func isArtefactSeparator(b byte) bool {
	return b == '-' || b == '_' || b == '.'
}

// sanitiseArtefactText narrows filename-derived text to the conservative
// alphabet an artefact basename is allowed to carry. It is the single
// definition, so no branch can produce a name the others would have rejected.
func sanitiseArtefactText(text string) string {
	return strings.Trim(artefactSlugUnsafe.ReplaceAllString(text, "-"), "-")
}

// specFileStem returns a spec path's basename with its extension stripped, or
// "" when there is no path. It carries no identity meaning on its own.
func specFileStem(testCaseFile string) string {
	if testCaseFile == "" {
		return ""
	}
	base := filepath.Base(testCaseFile)
	if ext := filepath.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
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
		// BUG-171 / REV-125: typed so the bulk CLI preflight
		// (CheckTier0AutomatePrereqs) can surface the SAME diagnostic. The
		// message is unchanged.
		return nil, &UnsupportedAutomateFrameworkError{Framework: framework}
	}

	// Output path. The path is PLANNED from the resolved owner at the invocation
	// boundary and CARRIED here. It is a hard precondition, not a hint: without
	// it this adapter refuses rather than re-deriving a destination of its own.
	//
	// That is what makes the carrier load-bearing. A recomputing fallback would
	// leave the boundary's planning free to be deleted without any test noticing,
	// because the adapter would silently arrive at the same answer by a path that
	// no ownership check had ever inspected. The write must land where the
	// boundary looked, or not happen.
	if ctx.PlannedArtefact == "" {
		return nil, fmt.Errorf(
			"no planned artefact path was supplied for %s: the built-in automate adapter writes only to the path planned and ownership-checked at the invocation boundary",
			ctx.TestCase)
	}
	outFile := ctx.PlannedArtefact
	outDir := filepath.Dir(outFile)

	// Overwrite authority, re-asserted at the point of writing. The boundary
	// already refused this run when it was reached through Invoke; this is the
	// same refusal for a direct caller, and it is deliberately kept -- it is the
	// last gate before a file is created, and a gate that exists only upstream is
	// one a future caller can walk around.
	//
	// It fails CLOSED. An unreadable or unparseable wiring record does not mean
	// "unclaimed"; it means the claim that would have vetoed this write may be
	// the one that could not be read. Blocking only the --force sweep would be
	// insufficient: the adapter could still overwrite an artefact whose claim the
	// corrupt record was hiding -- the same data loss, reached by writing rather
	// than deleting.
	// The set is the OPERATION's live state, threaded from the boundary that
	// reserved this path. It is not re-scanned here: re-reading every wiring
	// record per item is the O(test cases x wiring records) shape, and it would
	// also be BLIND to what earlier items in this same batch have claimed. A
	// caller that built the context by hand gets a set for its one invocation.
	claims := ctx.Claims
	if claims == nil {
		var claimsErr error
		if claims, claimsErr = NewArtefactClaimSet(ctx.ProjectRoot); claimsErr != nil {
			return nil, claimsErr
		}
	}
	holder, inFlight, foreign, holderErr := claims.foreignHolder(ctx.TestCase, outFile)
	if holderErr != nil {
		return nil, holderErr
	}
	if foreign {
		return nil, &ForeignArtefactClaimError{
			Owner:    ctx.TestCase,
			Claimant: holder,
			Artefact: claims.display(outFile),
			InFlight: inFlight,
		}
	}

	// The same last-gate argument, applied to the other refusal. The invocation
	// boundary also rejects a planned path occupied by a file NOTHING accounts
	// for, and checking only the foreign-claim half here left a direct caller
	// able to overwrite exactly the unaccounted-for occupant the boundary
	// protects. "A gate that exists only upstream is one a future caller can
	// walk around" is why this file re-asserts the foreign check at all; it
	// applies to both halves or it applies to neither.
	occupied, occErr := claims.pathOccupiedByStranger(ctx.TestCase, outFile)
	if occErr != nil {
		return nil, occErr
	}
	if occupied {
		return nil, &OccupiedArtefactPathError{
			Owner:    ctx.TestCase,
			Artefact: claims.display(outFile),
		}
	}

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
		// Fallback: the owner carried on the context (ADR-024 s5). ctx.TestCaseFile
		// is derived from the same owner, so this differs only when that field was
		// not populated -- it must not become a second, independent lookup of the
		// bare id, which could hash a different spec than the run is about.
		if absTC := testcase.OwnerAbsFor(ctx.ProjectRoot, ctx.Owner); absTC != "" {
			if h, err := pipeline.HashFile(absTC); err == nil {
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

// ValidateTestCaseOwner is the ENH-150 post-fill gate, applied to the spec that
// orchestration already PROVED owns the target (ADR-024 s5). It reads the owner it
// was handed. It does not walk the cases tree, and it is not given an id it could
// walk for.
//
// It validates that the owning spec is readable and STILL declares exactly the id
// the resolution proved it owns.
//
// REV-158 CODEX-003: the check used to accept any non-empty test_case_id. Between
// the resolution and this gate the file can change -- an adapter, an editor, or a
// concurrent command can rewrite it -- and a spec that now declares a DIFFERENT
// (or shell-unsafe) id was still waved through. The command then invoked external
// code and hashed or wrote wiring under the stale original identity. Requiring an
// EXACT match against owner.ID() closes that window and, as a side effect, catches
// a caller that passed an Owner for a different target than the one it is acting
// on. The comparison is byte-exact: this is persisted body identity, so it is
// never trimmed or case-folded (ADR-024 s1).
//
// Exact equality also covers the execution grammar without a second check.
// owner.ID() only ever comes from a resolution that already validated it against
// the positive grammar, so an id equal to it is grammar-valid by construction and
// any id that is not -- including a shell-unsafe one written into the file after
// resolution -- fails the equality test first. A separate ValidExecutionID branch
// here would be unreachable, and unreachable guards are how a codebase comes to
// read as if a rule were enforced when it is not (REV-158 CLAUDE-008).
//
// Two checks the old filename-walking version made are deliberately NOT repeated
// here, because the ownership resolution upstream now answers both, earlier and
// more strictly:
//
//   - Filename/body ID mismatch. A file named tc-aaaa.md whose body declares
//     tc-bbbb does not OWN tc-aaaa, so resolving tc-aaaa yields NotFound and the
//     command refuses before reaching this gate. ADR-024 s1 and s4 also make this
//     the correct reading for a spec: body identity is authoritative and a renamed
//     owner is expressly permitted, so filename divergence is not itself an
//     integrity error (that rule is for WIRING, whose filename must reconstruct
//     from its body).
//   - Duplicate test_case_id. Two specs declaring the same id are two physical
//     owners, which resolves as Ambiguous and refuses the command. That check is
//     now project-wide rather than the old folder-scoped scan, so it also catches
//     the cross-folder duplicate the folder scan missed.
//
// A nil owner means the command established there is no owner; there is nothing to
// validate and not-found is handled by the command's own wording.
func ValidateTestCaseOwner(projectRoot string, owner *testcase.Owner) []SpecValidationError {
	abs := testcase.OwnerAbsFor(projectRoot, owner)
	if abs == "" {
		return nil
	}

	f, openErr := os.Open(abs)
	if openErr != nil {
		return []SpecValidationError{{
			File:   filepath.Base(abs),
			Reason: fmt.Sprintf("could not read the test case spec: %v", openErr),
		}}
	}
	defer f.Close()

	var fm specFrontmatter
	if _, parseErr := frontmatter.Parse(f, &fm); parseErr != nil {
		return []SpecValidationError{{
			File:   filepath.Base(abs),
			Reason: fmt.Sprintf("could not parse frontmatter: %v", parseErr),
		}}
	}
	if fm.TestCaseID == "" {
		return []SpecValidationError{{
			File:   filepath.Base(abs),
			Reason: "frontmatter is missing required field 'test_case_id'",
		}}
	}
	if fm.TestCaseID != owner.ID() {
		return []SpecValidationError{{
			File: filepath.Base(abs),
			Reason: fmt.Sprintf(
				"frontmatter now declares test_case_id '%s' but this spec was resolved as the owner of '%s' -- the file changed after ownership was proved; re-run the command",
				sanitizeIdentityForError(fm.TestCaseID), sanitizeIdentityForError(owner.ID())),
		}}
	}
	return nil
}

// sanitizeIdentityForError replaces control characters (C0 + DEL) with '?' so an
// identity read from a file that changed underneath us cannot manipulate the
// terminal when it is echoed in a diagnostic (ADR-024 s7).
func sanitizeIdentityForError(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7F {
			b.WriteRune('?')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// BuiltinPrimeFromTC reimplements the manual-prime context population in Go
// for Tier 0 built-in adapters that lack the Tier 2 env var contract.
// This is called from buildAdapterContext for the "prime" command when
// the resolved adapter is a built-in.
func populateBuiltinPrimeFields(ctx *AdapterContext, projectRoot, target string) {
	tcSource := ownerSource(ctx.Owner)
	if tcSource == "" {
		return
	}

	absTC := testcase.OwnerAbsFor(projectRoot, ctx.Owner)

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
