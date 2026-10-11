package target

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
)

// TestValidateFolderSafety_ReservedPrefix_UnicodeParent covers REV-119 CODEX-002:
// the reserved-prefix check must be segment-aware (strings.EqualFold) and must
// NEVER slice the original input by a byte offset taken from a case-transformed
// string. A Unicode renamed GTMS parent whose lowercase mapping changes UTF-8 byte
// length (e.g. "gtmsİ", where ToLower(İ) shrinks 2 bytes -> 1) previously panicked
// the slice; here every case must produce a validation result, never a panic.
func TestValidateFolderSafety_ReservedPrefix_UnicodeParent(t *testing.T) {
	// İ (U+0130) lowercases to a shorter byte sequence under Go's simple mapping,
	// so len(casesPrefix) != the matching prefix length in the original input.
	layout.InitFromParent("gtmsİ")
	t.Cleanup(func() { layout.InitFromParent("gtms") })

	rejected := []string{
		"gtmsİ/test/cases",          // exact full prefix
		"gtmsİ/test/cases/FeatureA", // child beneath the full prefix
		"GTMSİ/test/cases/FeatureA", // mixed-case full prefix (EqualFold)
		`gtmsİ\test\cases\FeatureA`, // backslash-normalised form
		"cases",                     // exact short prefix
		"cases/FeatureA",            // short prefix child
		"CASES/FeatureA",            // mixed-case short prefix
	}
	for _, in := range rejected {
		t.Run("reject "+in, func(t *testing.T) {
			// Must not panic; must reject with the reserved-prefix diagnostic.
			_, err := ValidateFolderArg(in)
			require.Error(t, err, "%q must be rejected under a Unicode parent", in)
			assert.Contains(t, err.Error(), "don't include")
		})
	}

	valid := []string{
		"cases-extra/FeatureA", // near-match: "cases-extra" does not EqualFold "cases"
		"FeatureA",             // ordinary folder
		"gtmsX/test/cases",     // different parent segment, not reserved
	}
	for _, in := range valid {
		t.Run("accept "+in, func(t *testing.T) {
			_, err := ValidateFolderArg(in)
			assert.NoError(t, err, "%q must remain a valid folder under a Unicode parent", in)
		})
	}
}

// seedCases builds a gtms/test/cases tree under a temp root. dirs are created as
// folders; specs maps a relative spec path (under cases) to its content.
func seedCases(t *testing.T, dirs []string, specs map[string]string) string {
	t.Helper()
	root := t.TempDir()
	casesDir := filepath.Join(root, "gtms/test/cases")
	require.NoError(t, os.MkdirAll(casesDir, 0o755))
	for _, d := range dirs {
		require.NoError(t, os.MkdirAll(filepath.Join(casesDir, d), 0o755))
	}
	for rel, content := range specs {
		full := filepath.Join(casesDir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	}
	return root
}

// TestClassify_FolderAndTCID covers the folder-vs-TC classification matrix that
// the BUG-167 frozen specs pin: mixed-case folder preservation, backslash
// normalisation, the combined uppercase+.md+qualified boundary, .md stripping,
// folder-qualified base-ID extraction, uppercase TC IDs, and tc-prefixed folder
// existence-first classification.
func TestClassify_FolderAndTCID(t *testing.T) {
	root := seedCases(t,
		[]string{"FeatureA", "feature/nested", "tc-aaaa1111", "tc-regression-pack"},
		nil,
	)

	cases := []struct {
		name       string
		arg        string
		wantKind   Kind
		wantTarget string
		wantTCID   string // "" for folders
	}{
		{"mixed-case folder preserved", "FeatureA", KindFolder, "FeatureA", ""},
		{"backslash folder normalised", `feature\nested`, KindFolder, "feature/nested", ""},
		{"forward-slash nested folder", "feature/nested", KindFolder, "feature/nested", ""},
		{"tc-prefixed folder existence-first (hex lookalike)", "tc-aaaa1111", KindFolder, "tc-aaaa1111", ""},
		{"tc-prefixed folder existence-first (non-hex)", "tc-regression-pack", KindFolder, "tc-regression-pack", ""},
		{"uppercase TC id lowercased", "TC-11111111", KindTCID, "tc-11111111", "tc-11111111"},
		{".md suffix stripped", "tc-11111111.md", KindTCID, "tc-11111111", "tc-11111111"},
		// BUG-059 (REV-113): Target keeps the folder qualifier for scoped
		// resolution; TCID is the bare flat-wiring key -- separately available.
		{"folder-qualified TC keeps qualifier in Target, bare in TCID", "feature/tc-abc12345", KindTCID, "feature/tc-abc12345", "tc-abc12345"},
		{"folder-qualified .md keeps qualifier in Target, bare in TCID", "feature/tc-abc12345.md", KindTCID, "feature/tc-abc12345", "tc-abc12345"},
		{"combined boundary: folder case kept in Target, TC segment normalised", "FeatureA/TC-22222222.md", KindTCID, "FeatureA/tc-22222222", "tc-22222222"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Classify(root, tc.arg)
			require.NoError(t, err)
			assert.Equal(t, tc.wantKind, res.Kind, "kind for %q", tc.arg)
			assert.Equal(t, tc.wantTarget, res.Target, "Target for %q", tc.arg)
			assert.Equal(t, tc.wantTCID, res.TCID, "TCID for %q", tc.arg)
		})
	}
}

// TestClassify_CollisionFolderWins pins the PRP decision: when a directory named
// tc-aaaa1111 exists AND a real test case with ID tc-aaaa1111 exists elsewhere,
// folder existence wins (no ambiguity error). Classify only stats the folder, so
// the colliding spec does not change the outcome; this test documents the
// scenario explicitly.
func TestClassify_CollisionFolderWins(t *testing.T) {
	root := seedCases(t,
		[]string{"tc-aaaa1111"},
		map[string]string{
			// A genuine spec carrying the colliding ID, under a different folder.
			"other/tc-aaaa1111-elsewhere.md": "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-aaaa1111\n---\n",
		},
	)

	res, err := Classify(root, "tc-aaaa1111")
	require.NoError(t, err)
	assert.Equal(t, KindFolder, res.Kind, "folder existence must win the collision")
	assert.Equal(t, "tc-aaaa1111", res.Target)
	assert.Equal(t, "", res.TCID)
}

// TestClassify_WouldBeFolder covers the step-5 path: a safe, folder-shaped arg
// that does not exist and is not a TC ID classifies as a (non-existent) folder,
// preserving control-parity leniency rather than erroring.
func TestClassify_WouldBeFolder(t *testing.T) {
	root := seedCases(t, nil, nil)

	res, err := Classify(root, "typo-folder")
	require.NoError(t, err)
	assert.Equal(t, KindFolder, res.Kind)
	assert.Equal(t, "typo-folder", res.Target)
	assert.Equal(t, "", res.TCID)
}

// TestClassify_GuardsRejectBeforeTCClassification is the guard-preservation gate
// (review C1): a folder-safety violation must be rejected BEFORE TC
// classification, even when the final path segment is TC-shaped -- otherwise a
// TC-shaped tail could launder an unsafe path into a bare TC ID.
func TestClassify_GuardsRejectBeforeTCClassification(t *testing.T) {
	root := seedCases(t, nil, nil)

	cases := []struct {
		name      string
		arg       string
		wantToken string
	}{
		{"traversal, non-TC tail", "../outside", "path traversal"},
		{"traversal, TC-shaped tail", "../tc-abc12345", "path traversal"},
		{"cases prefix, non-TC tail", "gtms/test/cases/FeatureA", "don't include"},
		{"cases prefix, TC-shaped tail", "gtms/test/cases/tc-abc12345", "don't include"},
		{"empty arg", "", "must not be empty"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Classify(root, tc.arg)
			require.Error(t, err, "%q must be rejected, not classified", tc.arg)
			assert.Contains(t, err.Error(), tc.wantToken)
			// Must NOT have laundered into a TC ID.
			assert.NotEqual(t, KindTCID, res.Kind)
		})
	}
}

// TestClassify_LengthOrdering covers REV-116 CODEX-002: the length limit is
// applied AFTER classification, not against the raw argument. A valid TC ID
// written with a ".md" suffix must not be rejected for the three extra bytes,
// while a genuinely oversized TC or folder must still be rejected. This restores
// the pre-BUG-167 ordering (TC normalisation before target-length validation).
func TestClassify_LengthOrdering(t *testing.T) {
	// "notes.md" seeded as a real folder to prove an existing .md-suffixed folder
	// is classified as a folder without TC stripping.
	root := seedCases(t, []string{"notes.md"}, nil)

	id128 := "tc-" + strings.Repeat("a", maxTargetIDLength-3) // normalised len 128
	id129 := "tc-" + strings.Repeat("a", maxTargetIDLength-2) // normalised len 129
	id126 := "tc-" + strings.Repeat("a", maxTargetIDLength-5) // normalised len 126

	t.Run("normalised TC length exactly 128 accepted", func(t *testing.T) {
		require.Len(t, id128, maxTargetIDLength)
		res, err := Classify(root, id128)
		require.NoError(t, err)
		assert.Equal(t, KindTCID, res.Kind)
		assert.Equal(t, id128, res.Target)
		assert.Equal(t, id128, res.TCID)
	})

	t.Run("normalised TC length 129 rejected", func(t *testing.T) {
		require.Len(t, id129, maxTargetIDLength+1)
		_, err := Classify(root, id129)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "target ID exceeds maximum length")
	})

	t.Run("valid TC with .md making raw exceed 128 accepted after normalisation", func(t *testing.T) {
		raw := id126 + ".md" // 129 raw bytes, 126 once .md is stripped
		require.Greater(t, len(raw), maxFolderArgLength)
		res, err := Classify(root, raw)
		require.NoError(t, err, "the 3 .md bytes must not count against the limit")
		assert.Equal(t, KindTCID, res.Kind)
		assert.Equal(t, id126, res.Target)
		assert.Equal(t, id126, res.TCID)
	})

	t.Run("oversized ordinary folder rejected", func(t *testing.T) {
		big := strings.Repeat("a", maxFolderArgLength+1) // 129, no tc- prefix
		_, err := Classify(root, big)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "folder name exceeds maximum length")
	})

	t.Run("existing folder ending .md classified as folder, not stripped", func(t *testing.T) {
		res, err := Classify(root, "notes.md")
		require.NoError(t, err)
		assert.Equal(t, KindFolder, res.Kind)
		assert.Equal(t, "notes.md", res.Target)
		assert.Equal(t, "", res.TCID)
	})
}

// TestClassify_TrailingSeparatorForcesFolder covers REV-117 CODEX-001: a trailing
// path separator ("/" or "\") is an explicit folder signal, so the argument is
// ALWAYS a folder scope and never falls through to a bare TC ID -- closing the
// data-safety hole where "delete tc-abc12345/" became a global TC deletion.
// Existence-first still wins for an existing tc-shaped directory.
func TestClassify_TrailingSeparatorForcesFolder(t *testing.T) {
	// "tc-exists11" seeded as a real tc-shaped folder; tc-abc12345 exists only as a
	// spec under other/ (a real TC elsewhere) so the broadening risk is realistic.
	root := seedCases(t,
		[]string{"tc-exists11"},
		map[string]string{"other/tc-abc12345-x.md": "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-abc12345\n---\n"},
	)

	cases := []struct {
		name       string
		arg        string
		wantKind   Kind
		wantTarget string
		wantTCID   string
	}{
		{"unqualified TC + trailing slash -> folder", "tc-abc12345/", KindFolder, "tc-abc12345", ""},
		{"unqualified TC + trailing backslash -> folder", `tc-abc12345\`, KindFolder, "tc-abc12345", ""},
		{"qualified TC + trailing slash -> folder", "feature/tc-abc12345/", KindFolder, "feature/tc-abc12345", ""},
		{"qualified TC + trailing backslash -> folder", `feature\tc-abc12345\`, KindFolder, "feature/tc-abc12345", ""},
		{"multiple trailing slashes -> folder", "tc-abc12345//", KindFolder, "tc-abc12345", ""},
		{"mixed trailing separators -> folder", `tc-abc12345/\`, KindFolder, "tc-abc12345", ""},
		{"bare TC, no separator -> TC", "tc-abc12345", KindTCID, "tc-abc12345", "tc-abc12345"},
		{".md TC, no separator -> TC", "tc-abc12345.md", KindTCID, "tc-abc12345", "tc-abc12345"},
		{"existing tc-shaped folder + trailing slash -> folder", "tc-exists11/", KindFolder, "tc-exists11", ""},
		{"existing tc-shaped folder, no separator -> folder (existence-first)", "tc-exists11", KindFolder, "tc-exists11", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Classify(root, tc.arg)
			require.NoError(t, err)
			assert.Equal(t, tc.wantKind, res.Kind, "kind for %q", tc.arg)
			assert.Equal(t, tc.wantTarget, res.Target, "Target for %q", tc.arg)
			assert.Equal(t, tc.wantTCID, res.TCID, "TCID for %q", tc.arg)
		})
	}
}

// TestClassify_TrailingSeparatorRejections confirms REV-117 CODEX-001 does not
// weaken the safety gate: separator-only input is empty (error, not folder), and
// traversal / reserved-prefix inputs ending in a separator remain rejected before
// any folder/TC classification.
func TestClassify_TrailingSeparatorRejections(t *testing.T) {
	root := seedCases(t, nil, nil)
	cases := []struct{ name, arg, token string }{
		{"slash only", "/", "must not be empty"},
		{"backslash only", `\`, "must not be empty"},
		{"double slash only", "//", "must not be empty"},
		{"traversal ending in slash", "../foo/", "path traversal"},
		{"traversal TC tail ending in slash", "../tc-abc12345/", "path traversal"},
		{"reserved prefix ending in slash", "gtms/test/cases/foo/", "don't include"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Classify(root, tc.arg)
			require.Error(t, err, "%q must be rejected", tc.arg)
			assert.Contains(t, err.Error(), tc.token)
			assert.NotEqual(t, KindTCID, res.Kind, "%q must never classify as a TC ID", tc.arg)
		})
	}
}

// TestValidateFolderArg_ReservedPrefixCaseInsensitive covers REV-114 CODEX-001:
// BUG-167 preserves case, so the reserved test-cases prefix must be recognised
// case-insensitively -- otherwise "GTMS/test/cases/FeatureA" or "CASES/FeatureA"
// slip through and produce a doubled prefix. Legitimate folder casing (and
// near-matches like "cases-extra/") must be untouched.
func TestValidateFolderArg_ReservedPrefixCaseInsensitive(t *testing.T) {
	rejected := []string{
		"GTMS/test/cases/FeatureA",
		`GTMS\test\cases\FeatureA`, // backslash form normalises then rejects
		"CASES/FeatureA",
		`CASES\FeatureA`,
		"Gtms/Test/Cases/FeatureA",
	}
	for _, in := range rejected {
		t.Run("reject "+in, func(t *testing.T) {
			_, err := ValidateFolderArg(in)
			require.Error(t, err, "%q must be rejected as a reserved-prefix bypass", in)
			assert.Contains(t, err.Error(), "don't include")
		})
	}

	// Near-match must REMAIN valid, with original casing preserved in the return.
	got, err := ValidateFolderArg("cases-extra/FeatureA")
	require.NoError(t, err, "a near-match reserved prefix must not be a false positive")
	assert.Equal(t, "cases-extra/FeatureA", got, "legitimate folder casing must be preserved")

	// The remediation suffix must preserve the user's original casing.
	_, err = ValidateFolderArg("GTMS/test/cases/FeatureA")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gtms create FeatureA", "echoed suffix must keep original casing")
}

// TestClassify_MixedCaseReservedPrefixTail_Rejected confirms the fail-closed gate
// also blocks a TC-shaped tail hidden under a mixed-case reserved prefix.
func TestClassify_MixedCaseReservedPrefixTail_Rejected(t *testing.T) {
	root := seedCases(t, nil, nil)
	res, err := Classify(root, "GTMS/test/cases/tc-abc12345")
	require.Error(t, err, "a TC-shaped tail under a mixed-case reserved prefix must be rejected")
	assert.Contains(t, err.Error(), "don't include")
	assert.NotEqual(t, KindTCID, res.Kind)
}
