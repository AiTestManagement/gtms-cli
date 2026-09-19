package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// BUG-196: the scaffolded Tier 2 runners wrote their result-contract log block
// as a bare `log: |`. YAML infers that block's indent from the first content
// line, so a tool whose first output line is indented more than a later line
// closed the block early and made the whole contract unparseable. The fix
// writes `log: |2`, declaring the two spaces the `sed 's/^/  /'` pipeline adds,
// so any extra leading whitespace on a content line is content, not structure.

// TestBUG196_NoBareLogBlockInShippedRunners is a source-shape guard: the two
// LIVE runner constants must carry the explicit `log: |2` indicator and never
// the bare `log: |`. The frozen fingerprints (V1 and the pre-BUG-196 bodies)
// are deliberately excluded -- they are byte-exact snapshots of shipped bytes
// and must keep the bare form so the init --force upgrade path can recognise a
// pristine older copy.
func TestBUG196_NoBareLogBlockInShippedRunners(t *testing.T) {
	live := map[string]string{
		"batsRunnerScript":       batsRunnerScript,
		"playwrightRunnerScript": playwrightRunnerScript,
	}
	for name, body := range live {
		assert.Equal(t, 2, strings.Count(body, "log: |2\n"),
			"%s must write the explicit-indent block twice", name)
		assert.NotContains(t, body, "log: |\n",
			"%s must not carry the fragile bare log: | block", name)
	}

	// The two-space sed indent the |2 declares must still be present: the bats
	// runner inlines it in each of its two heredocs; the playwright runner
	// builds it once into LOG_BODY.
	assert.Equal(t, 2, strings.Count(batsRunnerScript, "sed 's/^/  /'"),
		"batsRunnerScript must keep the two-space indent pipeline in both heredocs")

	frozen := map[string]string{
		"batsRunnerScriptPreBUG196": batsRunnerScriptPreBUG196,
		"playwrightRunnerScriptV1":  playwrightRunnerScriptV1,
		"playwrightRunnerScriptV2":  playwrightRunnerScriptV2,
	}
	for name, body := range frozen {
		assert.Contains(t, body, "log: |\n",
			"%s is a frozen fingerprint and must keep the bare log: | block", name)
		assert.NotContains(t, body, "log: |2\n",
			"%s must not be rewritten to the fixed form", name)
	}

	// The playwright runner builds its indented body once into LOG_BODY.
	assert.Equal(t, 1, strings.Count(playwrightRunnerScript, "LOG_BODY=$(echo \"${OUTPUT}\" | sed 's/^/  /')"),
		"playwrightRunnerScript must build LOG_BODY with the two-space indent pipeline")
}

// renderLogBlockContract builds, in pure Go, the result-contract document the
// shipped runners' heredoc produces for a given tool OUTPUT and log header.
// It mirrors the shell construction exactly: `$(echo "${OUTPUT}" | sed
// 's/^/  /')` prefixes every line with two spaces, the command substitution
// strips the trailing newline, and the heredoc supplies one newline before the
// next key. No shell is spawned: REV-174 CLAUDE-001 found that a bare `sh`
// invocation bypassed the BUG-030 Git-for-Windows fallback and could fail
// `go test ./...` on a Windows developer box. The real-shell proof lives in the
// Linux BATS cases tc-7f53b45e and tc-0224883a; this test is the portable
// YAML-decoding oracle.
func renderLogBlockContract(header, output string) string {
	lines := strings.Split(output, "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return "task: t\n" +
		"command: execute\n" +
		"status: complete\n" +
		"result: pass\n" +
		"summary: \"s\"\n" +
		header + "\n" +
		strings.Join(lines, "\n") + "\n" +
		"completed: 2026-09-15T00:00:00Z\n"
}

// TestBUG196_ShippedRunnersWriteParseableLogBlock renders the shipped log-block
// construction with the indented-first-line output shape that broke the bare
// form, then parses the document with the same YAML library GTMS uses
// (gopkg.in/yaml.v3). It asserts the document parses AND that the decoded log
// field equals the original tool output with its own leading whitespace
// preserved and exactly one trailing newline (clip chomping). Both runners use
// the identical `log: |2` + `sed 's/^/  /'` construction, so one representative
// render exercises the shipped behaviour of both. The bare-header counterfactual
// proves the oracle is not vacuous.
func TestBUG196_ShippedRunnersWriteParseableLogBlock(t *testing.T) {
	// Guard that the construction under test is the one the runners actually
	// ship, so this test cannot silently pass on a stale fragment.
	require.Contains(t, batsRunnerScript, "log: |2\n$(echo \"${OUTPUT}\" | sed 's/^/  /')\n",
		"bats runner must ship the log: |2 + sed construction this test renders")
	require.Contains(t, playwrightRunnerScript, "LOG_BODY=$(echo \"${OUTPUT}\" | sed 's/^/  /')",
		"playwright runner must build LOG_BODY with the same pipeline")
	require.Contains(t, playwrightRunnerScript, "log: |2\n${LOG_BODY}\n",
		"playwright runner must interpolate LOG_BODY under the log: |2 header")

	// OUTPUT: first line indented FOUR, second line indented TWO. Under a bare
	// `log: |` YAML infers a four-space block from the first line and the
	// two-space line terminates it early; under `log: |2` both are content.
	const output = "    first\n  second"

	type contract struct {
		Status    string `yaml:"status"`
		Result    string `yaml:"result"`
		Log       string `yaml:"log"`
		Completed string `yaml:"completed"`
	}

	fixed := renderLogBlockContract("log: |2", output)
	// The rendered bytes must be exactly what the heredoc emits.
	require.Contains(t, fixed, "log: |2\n      first\n    second\ncompleted:",
		"render must carry the two-space sed indent on top of the tool's own indentation")

	var doc contract
	require.NoError(t, yaml.Unmarshal([]byte(fixed), &doc),
		"the rendered contract must be valid YAML:\n%s", fixed)

	// The two spaces sed adds are structural under |2 and are stripped on
	// decode; the tool's own leading whitespace survives as content; clip
	// chomping keeps exactly one trailing newline.
	assert.Equal(t, "    first\n  second\n", doc.Log,
		"decoded log must preserve the tool's own indentation and one trailing newline")
	assert.Equal(t, "complete", doc.Status)
	assert.Equal(t, "pass", doc.Result)
	assert.Equal(t, "2026-09-15T00:00:00Z", doc.Completed,
		"the key after the block must survive as a mapping key, not be swallowed into the block")

	// Counterfactual: the pre-BUG-196 header with the same output does NOT
	// parse. If this ever starts passing, the oracle has stopped discriminating.
	fragile := renderLogBlockContract("log: |", output)
	var bad contract
	require.Error(t, yaml.Unmarshal([]byte(fragile), &bad),
		"the bare log: | header must still fail on the indented-first shape:\n%s", fragile)
}

// TestBUG196_BatsRunnerForceUpgradePath proves BUG-196 made the bats runner an
// upgradeable preset asset (it had no prior-versions entry before) without
// making it clobberable, and that the new playwright V2 fingerprint refreshes
// in place. Mirrors TestInstallPresetAssetsUpgradePath for the bats preset.
func TestBUG196_BatsRunnerForceUpgradePath(t *testing.T) {
	skipIfShort(t)
	runnerRel := "gtms/adapters/bats-runner.sh"
	runnerPath := func(dir string) string {
		return filepath.Join(dir, "gtms", "adapters", "bats-runner.sh")
	}
	writeRunner := func(dir, content string) {
		p := runnerPath(dir)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o755))
	}
	readRunner := func(dir string) string {
		b, err := os.ReadFile(runnerPath(dir))
		require.NoError(t, err)
		return string(b)
	}
	companionPath := func(dir string) string {
		return runnerPath(dir) + ".gtms-new"
	}

	t.Run("pristine pre-BUG-196 bats body + force -> refreshed in place", func(t *testing.T) {
		dir := initGitRepo(t)
		writeRunner(dir, batsRunnerScriptPreBUG196)
		result := &Result{}
		require.NoError(t, installPresetAssets(dir, PresetBats, true, result))
		assert.Equal(t, batsRunnerScript, readRunner(dir),
			"a pristine v0.3.x bats runner must be refreshed to the current shipped body")
		assert.Contains(t, result.FilesCreated, runnerRel)
		assert.NoFileExists(t, companionPath(dir),
			"no companion when a known prior body is refreshed in place")
		assert.Contains(t, strings.Join(result.Notes, "\n"),
			"Upgraded gtms/adapters/bats-runner.sh to the current shipped version.")
	})

	t.Run("user-edited bats runner + force -> kept, companion written", func(t *testing.T) {
		dir := initGitRepo(t)
		edited := batsRunnerScript + "\n# LOCAL EDIT: project-specific TAP tweak, do not lose me.\n"
		writeRunner(dir, edited)
		result := &Result{}
		require.NoError(t, installPresetAssets(dir, PresetBats, true, result))
		assert.Equal(t, edited, readRunner(dir), "a user-edited runner must not be clobbered")
		cb, err := os.ReadFile(companionPath(dir))
		require.NoError(t, err)
		assert.Equal(t, batsRunnerScript, string(cb), "the companion carries the current shipped body")
		joined := strings.Join(result.Warnings, "\n")
		assert.Contains(t, joined, runnerRel)
		assert.Contains(t, joined, ".gtms-new")
	})

	t.Run("v0.2.0-shaped (non-catalogued) bats body + force -> kept, companion written", func(t *testing.T) {
		// The v0.2.0 body is deliberately NOT fingerprinted (tc-55884862). Any
		// body that matches neither the current nor a catalogued prior body must
		// take the user-modified path. A #!/bin/bash body stands in for it here;
		// the exact-bytes pin lives in the BATS spec tc-55884862.
		dir := initGitRepo(t)
		legacy := "#!/bin/bash\n# legacy v0.2.0-style bats runner\nlog: |\necho hi\n"
		require.False(t, isKnownPriorAsset(runnerRel, legacy),
			"a non-catalogued body must not be recognised as a known prior version")
		writeRunner(dir, legacy)
		result := &Result{}
		require.NoError(t, installPresetAssets(dir, PresetBats, true, result))
		assert.Equal(t, legacy, readRunner(dir), "a non-catalogued body must be kept as found")
		assert.FileExists(t, companionPath(dir))
		assert.NotContains(t, strings.Join(result.Notes, "\n"),
			"Upgraded gtms/adapters/bats-runner.sh",
			"a kept body must not be reported as an upgrade")
	})

	t.Run("current bats body + force -> skipped", func(t *testing.T) {
		dir := initGitRepo(t)
		writeRunner(dir, batsRunnerScript)
		result := &Result{}
		require.NoError(t, installPresetAssets(dir, PresetBats, true, result))
		assert.Contains(t, result.FilesSkipped, runnerRel)
		assert.NotContains(t, result.FilesCreated, runnerRel)
	})

	t.Run("pre-BUG-196 bats body WITHOUT force -> left untouched", func(t *testing.T) {
		dir := initGitRepo(t)
		writeRunner(dir, batsRunnerScriptPreBUG196)
		result := &Result{}
		require.NoError(t, installPresetAssets(dir, PresetBats, false, result))
		assert.Equal(t, batsRunnerScriptPreBUG196, readRunner(dir),
			"without --force an existing runner is never modified")
		assert.Contains(t, result.FilesSkipped, runnerRel)
	})

	t.Run("pristine post-BUG-189 playwright V2 body + force -> refreshed in place", func(t *testing.T) {
		dir := initGitRepo(t)
		pwRel := "gtms/adapters/playwright-runner.sh"
		pwPath := filepath.Join(dir, "gtms", "adapters", "playwright-runner.sh")
		require.NoError(t, os.MkdirAll(filepath.Dir(pwPath), 0o755))
		require.NoError(t, os.WriteFile(pwPath, []byte(playwrightRunnerScriptV2), 0o755))
		result := &Result{}
		require.NoError(t, installPresetAssets(dir, PresetPlaywright, true, result))
		b, err := os.ReadFile(pwPath)
		require.NoError(t, err)
		assert.Equal(t, playwrightRunnerScript, string(b),
			"a pristine pre-BUG-196 playwright runner must be refreshed to the current body")
		assert.Contains(t, result.FilesCreated, pwRel)
		assert.NoFileExists(t, pwPath+".gtms-new")
	})
}
