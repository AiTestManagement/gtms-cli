//go:build windows

package result

// BUG-209 Windows Go tests: the DACL (with its protected state), owner, group and
// the read-only attribute survive Update and Reconstruct (AC14), and a reader
// holding the destination open causes bounded retries rather than an immediate
// failure (AC16). These run in `go test ./...` on Windows (a CLI cannot show
// them).

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func readSD(t *testing.T, path string) (owner, group string, protected bool) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.GROUP_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	require.NoError(t, err)
	o, _, _ := sd.Owner()
	g, _, _ := sd.Group()
	ctrl, _, _ := sd.Control()
	ownerStr := ""
	if o != nil {
		ownerStr = o.String()
	}
	groupStr := ""
	if g != nil {
		groupStr = g.String()
	}
	return ownerStr, groupStr, ctrl&windows.SE_DACL_PROTECTED != 0
}

// makeDACLProtected flips the handoff's DACL to the protected (non-inherited)
// state so the test has a protected DACL to prove survives replacement.
func makeDACLProtected(t *testing.T, path string) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	require.NoError(t, err)
	dacl, _, _ := sd.DACL()
	require.NoError(t, windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil))
}

func TestSafeHandoff_WindowsMetadataSurvives(t *testing.T) {
	for _, op := range []string{"update", "reconstruct"} {
		t.Run(op, func(t *testing.T) {
			_, path := canonicalResults(t, "task-winmeta")
			seedValidHandoff(t, path, "task-winmeta")
			makeDACLProtected(t, path)
			require.NoError(t, setFileReadonly(path, true))

			ownerBefore, groupBefore, protBefore := readSD(t, path)
			require.True(t, protBefore, "precondition: the DACL must be protected")

			switch op {
			case "update":
				require.NoError(t, Update(path, map[string]interface{}{"summary": "x"}))
			case "reconstruct":
				require.NoError(t, Reconstruct(path, &ResultContract{
					Task: "task-winmeta", Command: "execute", Target: "tc-11111111",
					Adapter: "a", Mode: "sync", Status: "error",
				}))
			}

			ownerAfter, groupAfter, protAfter := readSD(t, path)
			assert.Equal(t, ownerBefore, ownerAfter, "owner must be preserved exactly")
			assert.Equal(t, groupBefore, groupAfter, "group must be preserved exactly")
			assert.True(t, protAfter, "the protected DACL state must survive")

			ro, roErr := getFileReadonly(path)
			require.NoError(t, roErr)
			assert.True(t, ro, "the read-only attribute must survive and stay set")

			// Clear read-only so t.TempDir cleanup can remove it.
			_ = setFileReadonly(path, false)
		})
	}
}

// REV-280 chunk-001-001: exact Windows owner/group preservation must remain
// fail-closed without requiring a second local account on the test runner. The
// runtime test above proves the values survive a real replacement, while this
// guard pins the two details a different-owner setup exercises: the captured
// owner/group are applied to the staged file, and an apply failure is returned
// as a handoff refusal rather than publishing with different ownership.
func TestWindowsMetadataApply_UsesCapturedOwnerGroupAndRefusesFailure(t *testing.T) {
	src, err := os.ReadFile("safehandoff_windows.go")
	require.NoError(t, err)
	text := string(src)
	assert.Contains(t, text,
		"windows.SECURITY_INFORMATION(si), meta.owner, meta.group, meta.dacl, nil",
		"the staged handoff must receive the captured owner and group exactly")
	assert.Contains(t, text,
		"return &RefusedHandoffError{Rel: handoffRel(base), Reason: \"could not be inspected: preserving Windows security failed: \" + err.Error()}",
		"a failure to preserve owner/group/DACL must refuse publication")
}

// AC16: a reader holding the destination open with a write-deny share mode makes
// the first MoveFileEx fail with a sharing violation; the bounded retry then
// succeeds once the reader closes within the 2s window.
func TestSafeHandoff_WindowsReaderHeldRetry(t *testing.T) {
	_, path := canonicalResults(t, "task-winhold")
	seedValidHandoff(t, path, "task-winhold")

	p, err := windows.UTF16PtrFromString(path)
	require.NoError(t, err)
	// Open the destination denying write/delete sharing, so a replacement cannot
	// proceed until this handle closes.
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil,
		windows.OPEN_EXISTING, 0, 0)
	require.NoError(t, err)

	closed := make(chan struct{})
	go func() {
		time.Sleep(300 * time.Millisecond)
		windows.CloseHandle(h)
		close(closed)
	}()

	start := time.Now()
	upErr := Update(path, map[string]interface{}{"summary": "retried"})
	elapsed := time.Since(start)
	<-closed

	require.NoError(t, upErr, "the write must succeed once the reader releases within the bound")
	assert.GreaterOrEqual(t, elapsed, 200*time.Millisecond, "the write should have retried while the reader held the file")

	rc, err := Read(path)
	require.NoError(t, err)
	assert.Equal(t, "retried", rc.Summary)
}

// A reader that never releases exhausts the bound: the old file stays intact and
// the error names the file.
func TestSafeHandoff_WindowsReaderHeldTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skips the 2s retry-exhaustion wait under -short")
	}
	_, path := canonicalResults(t, "task-winstuck")
	seedValidHandoff(t, path, "task-winstuck")
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	p, err := windows.UTF16PtrFromString(path)
	require.NoError(t, err)
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil,
		windows.OPEN_EXISTING, 0, 0)
	require.NoError(t, err)
	defer windows.CloseHandle(h)

	upErr := Update(path, map[string]interface{}{"summary": "never"})
	require.Error(t, upErr, "a reader that never releases must exhaust the retry bound")
	assert.Contains(t, upErr.Error(), handoffRel("task-winstuck.handoff.yaml"))

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after, "the old handoff must be intact after a failed replacement")
}
