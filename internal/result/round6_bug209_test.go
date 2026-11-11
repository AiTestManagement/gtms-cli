package result

// BUG-209 round 6 (REV-277): the concurrent Update decision (section 6), the escaping
// of control characters in diagnostics (section 10), and leaf-level checks. Every
// replacement happens at a fixed seam, not by timing.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func readContractFields(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	out := map[string]interface{}{}
	require.NoError(t, yaml.Unmarshal(data, &out))
	return out
}

// runOtherWriterOnce makes one other writer's Update land after this writer has read
// and merged, which is the race section 6 is about.
func runOtherWriterOnce(t *testing.T, path string, other map[string]interface{}) *int {
	t.Helper()
	calls := 0
	withRaceHook(t, func(stage string) {
		if stage != "after-read" {
			return
		}
		calls++
		if calls == 1 {
			saved := raceHookFn
			raceHookFn = nil
			require.NoError(t, Update(path, other))
			raceHookFn = saved
		}
	})
	return &calls
}

// Section 6, AC32: disjoint updates from two writers both survive.
func TestUpdate_TwoWritersDisjointFieldsBothSurvive(t *testing.T) {
	_, path := canonicalResults(t, "task-two")
	seedValidHandoff(t, path, "task-two")
	calls := runOtherWriterOnce(t, path, map[string]interface{}{"summary": "from-writer-b"})

	require.NoError(t, Update(path, map[string]interface{}{"log": "from-writer-a"}))

	assert.Equal(t, 2, *calls, "writer A must have run one retry")
	got := readContractFields(t, path)
	assert.Equal(t, "from-writer-b", got["summary"], "writer B's field must survive")
	assert.Equal(t, "from-writer-a", got["log"], "writer A's field must survive")
}

// Section 6, AC32: an overlapping field takes the final successful writer's value.
func TestUpdate_TwoWritersOverlapTakesFinalWriter(t *testing.T) {
	_, path := canonicalResults(t, "task-over")
	seedValidHandoff(t, path, "task-over")
	runOtherWriterOnce(t, path, map[string]interface{}{"summary": "from-writer-b"})

	require.NoError(t, Update(path, map[string]interface{}{"summary": "from-writer-a"}))

	assert.Equal(t, "from-writer-a", readContractFields(t, path)["summary"], "writer A published last")
}

// Section 6: past three retries the result is the concurrency error. It is not a
// refusal and it carries no remediation, because the file is healthy.
func TestUpdate_RetryExhaustionIsAConcurrencyError(t *testing.T) {
	_, path := canonicalResults(t, "task-busy")
	seedValidHandoff(t, path, "task-busy")
	var last string
	calls := 0
	withRaceHook(t, func(stage string) {
		if stage != "after-read" {
			return
		}
		calls++
		last = "task: task-busy\nstatus: pending\nsummary: replaced-" + strings.Repeat("x", calls) + "\n"
		require.NoError(t, os.Remove(path))
		require.NoError(t, os.WriteFile(path, []byte(last), 0o644))
	})

	err := Update(path, map[string]interface{}{"log": "never-lands"})
	raceHookFn = nil

	require.Error(t, err)
	assert.Equal(t, 4, calls, "one initial attempt and three retries")
	assert.True(t, IsConcurrentUpdate(err), "got %v", err)
	assert.False(t, IsRefusal(err), "a healthy file must not be reported as unsafe")
	assert.NotContains(t, err.Error(), handoffRemediation, "there is nothing to remove by hand")
	assert.NotContains(t, err.Error(), "refused")

	got, rerr := os.ReadFile(path)
	require.NoError(t, rerr)
	assert.Equal(t, last, string(got), "the last other writer's file stays")
	leftovers, gerr := filepath.Glob(path + ".tmp-*")
	require.NoError(t, gerr)
	assert.Empty(t, leftovers, "no staged file may be left")
}

// Section 6: only a verified safe replacement is retried. A hard link is refused at once.
func TestUpdate_UnsafeReplacementIsNotRetried(t *testing.T) {
	_, path := canonicalResults(t, "task-unsafe")
	seedValidHandoff(t, path, "task-unsafe")
	calls := 0
	withRaceHook(t, func(stage string) {
		if stage == "after-read" {
			calls++
			require.NoError(t, os.Link(path, filepath.Join(t.TempDir(), "other-link")))
		}
	})

	err := Update(path, map[string]interface{}{"log": "x"})
	raceHookFn = nil

	var rh *RefusedHandoffError
	require.True(t, errors.As(err, &rh), "got %v", err)
	assert.Equal(t, reasonHardLinked, rh.Reason)
	assert.Equal(t, 1, calls, "an unsafe leaf must not be retried")
}

// Section 6: malformed content is not retried past the read that found it.
func TestUpdate_MalformedReplacementIsNotRetried(t *testing.T) {
	_, path := canonicalResults(t, "task-bad")
	seedValidHandoff(t, path, "task-bad")
	calls := 0
	withRaceHook(t, func(stage string) {
		if stage == "after-read" {
			calls++
			require.NoError(t, os.Remove(path))
			require.NoError(t, os.WriteFile(path, []byte("not: [valid\n"), 0o644))
		}
	})

	err := Update(path, map[string]interface{}{"log": "x"})
	raceHookFn = nil

	require.Error(t, err)
	assert.False(t, IsConcurrentUpdate(err), "got %v", err)
	assert.Contains(t, err.Error(), "parsing result contract for update")
	assert.Equal(t, 1, calls, "the re-read finds malformed content and stops there")
}

// Section 6: a removed file is not a safe replacement, so it is not retried either.
func TestUpdate_RemovedFileIsNotRetried(t *testing.T) {
	_, path := canonicalResults(t, "task-gone")
	seedValidHandoff(t, path, "task-gone")
	calls := 0
	withRaceHook(t, func(stage string) {
		if stage == "after-read" {
			calls++
			require.NoError(t, os.Remove(path))
		}
	})

	err := Update(path, map[string]interface{}{"log": "x"})
	raceHookFn = nil

	require.Error(t, err)
	assert.False(t, IsConcurrentUpdate(err), "got %v", err)
	assert.Equal(t, 1, calls)
	assert.NoFileExists(t, path, "nothing may be created in its place")
}

// Section 10 (CLAUDE-005, CODEX-002): the first line escapes control characters, while
// Rel keeps the raw name that identifies the entry.
func TestRefusal_ControlCharactersEscapedForDisplayOnly(t *testing.T) {
	raw := ".gtms/results/task\x1b[31m\nforged line.handoff.yaml"
	h := &RefusedHandoffError{Rel: raw, Reason: "could not be inspected: bad\x07cause"}
	for _, text := range []string{h.FirstLine(), h.Error()} {
		assert.NotContains(t, text, "\x1b")
		assert.NotContains(t, text, "\x07")
		assert.NotContains(t, text, "\nforged", "a name must not be able to forge a line")
	}
	assert.Contains(t, h.FirstLine(), "refused handoff .gtms/results/task?[31m?forged line.handoff.yaml: could not be inspected: bad?cause")
	assert.Equal(t, raw, h.Rel, "the raw path must not change")

	d := &RefusedDirError{Rel: ".gtms/results", Reason: "could not be inspected: bad\x1bcause"}
	assert.NotContains(t, d.FirstLine(), "\x1b")

	c := &ConcurrentUpdateError{Rel: raw, Retries: 3}
	assert.NotContains(t, c.Error(), "\x1b")
}

// Section 9: a reconstruct against a captured state replaces only that state.
func TestReconstructIfUnchanged(t *testing.T) {
	rc := &ResultContract{Task: "task-rc", Command: "execute", Target: "tc-11111111", Adapter: "a", Mode: "sync", Status: "error"}

	t.Run("unchanged file is replaced", func(t *testing.T) {
		_, path := canonicalResults(t, "task-rc")
		require.NoError(t, os.WriteFile(path, []byte("not: [valid\n"), 0o644))
		state, err := CaptureHandoffState(path)
		require.NoError(t, err)
		require.NoError(t, ReconstructIfUnchanged(path, rc, state))
		assert.Equal(t, "task-rc", readContractFields(t, path)["task"])
	})
	t.Run("replaced file is refused and kept", func(t *testing.T) {
		_, path := canonicalResults(t, "task-rc")
		require.NoError(t, os.WriteFile(path, []byte("first\n"), 0o644))
		state, err := CaptureHandoffState(path)
		require.NoError(t, err)
		require.NoError(t, os.Remove(path))
		require.NoError(t, os.WriteFile(path, []byte("a longer second file\n"), 0o644))

		rerr := ReconstructIfUnchanged(path, rc, state)
		assert.True(t, IsRefusal(rerr), "got %v", rerr)
		got, _ := os.ReadFile(path)
		assert.Equal(t, "a longer second file\n", string(got))
	})
	t.Run("captured as missing, now present, is refused", func(t *testing.T) {
		_, path := canonicalResults(t, "task-rc")
		state, err := CaptureHandoffState(path)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, []byte("appeared\n"), 0o644))
		assert.True(t, IsRefusal(ReconstructIfUnchanged(path, rc, state)))
	})
	t.Run("captured as present, now missing, is refused", func(t *testing.T) {
		_, path := canonicalResults(t, "task-rc")
		require.NoError(t, os.WriteFile(path, []byte("x\n"), 0o644))
		state, err := CaptureHandoffState(path)
		require.NoError(t, err)
		require.NoError(t, os.Remove(path))
		assert.True(t, IsRefusal(ReconstructIfUnchanged(path, rc, state)))
		assert.NoFileExists(t, path)
	})
	t.Run("captured as missing, still missing, is created", func(t *testing.T) {
		_, path := canonicalResults(t, "task-rc")
		state, err := CaptureHandoffState(path)
		require.NoError(t, err)
		require.NoError(t, ReconstructIfUnchanged(path, rc, state))
		assert.FileExists(t, path)
	})
	t.Run("a state never captured is refused", func(t *testing.T) {
		_, path := canonicalResults(t, "task-rc")
		assert.True(t, IsRefusal(ReconstructIfUnchanged(path, rc, HandoffState{})))
	})
	t.Run("an unsafe leaf cannot be captured", func(t *testing.T) {
		_, path := canonicalResults(t, "task-rc")
		require.NoError(t, os.Mkdir(path, 0o755))
		_, err := CaptureHandoffState(path)
		assert.True(t, IsRefusal(err), "got %v", err)
	})
}

// AC35: anything but a directory at .gtms/results is refused before it is opened.
func TestOpenResultsDir_RegularFileRefusedBeforeOpening(t *testing.T) {
	projectRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, ".gtms"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(projectRoot, ".gtms", "results"), []byte("x"), 0o644))
	scan := OpenScan(projectRoot)
	defer scan.Close()
	require.NotNil(t, scan.DirRefusal)
	assert.Equal(t, "could not be inspected: not a directory", scan.DirRefusal.Reason)
}

// AC35: a staged-named entry that is not a regular single-link file is refused by both
// the plan and the removal, and is left in place.
func TestScan_UnsafeStagedEntryIsRefusedAndKept(t *testing.T) {
	projectRoot, path := canonicalResults(t, "task-staged")
	seedValidHandoff(t, path, "task-staged")
	safe := path + ".tmp-0123456789abcdef"
	unsafe := path + ".tmp-fedcba9876543210"
	require.NoError(t, os.WriteFile(safe, []byte("x"), 0o644))
	require.NoError(t, os.Mkdir(unsafe, 0o755))

	scan := OpenScan(projectRoot)
	defer scan.Close()
	safeNames, refused := scan.LeftoversFor(filepath.Base(path))
	assert.Equal(t, []string{filepath.Base(safe)}, safeNames)
	require.Len(t, refused, 1)
	assert.Contains(t, refused[0].FirstLine(), filepath.Base(unsafe)+": is not a regular file")

	removedRefusals, err := scan.Remove(filepath.Base(path))
	require.NoError(t, err)
	require.Len(t, removedRefusals, 1, "the removal reports what the plan reported")
	assert.NoFileExists(t, path)
	assert.NoFileExists(t, safe)
	assert.DirExists(t, unsafe)
}
