package cli

import (
	"strings"
	"testing"
)

// REV-160 mechanism C. Refresh and repoint reach the SAME condition -- a scope
// folder that is not there -- and must render it the SAME way.
//
// They did not. Refresh said "folder '<cases>/<name>/' does not exist"; repoint
// surfaced testcase.ErrFolderNotFound as "test case folder not found: <name>".
// The first half was fixed and the second was missed, and the miss stayed
// invisible because BATS halts at the first failed assertion: the acceptance
// test checked refresh on line 103 and repoint on line 112, so line 103's
// failure hid line 112 entirely. Only after the first was fixed did the second
// surface -- in CI, not locally.
//
// This test exists so a third scope-taking path cannot invent a fourth wording
// without something going red.
func TestFolderNotFoundMessage_IsOneWordingForOneCondition(t *testing.T) {
	const folder = "tc-11111111"
	msg := folderNotFoundMessage("", folder)

	// The phrase callers script against.
	if !strings.Contains(msg, "does not exist") {
		t.Errorf("the folder-not-found message must contain \"does not exist\" -- "+
			"that is the phrase the acceptance suite and any scripted caller match on.\ngot: %s", msg)
	}

	// The NORMALISED folder token, trailing separator included. This is not
	// decoration: it is what tells a reader the argument was taken as a FOLDER
	// rather than a test case id, which is the entire subject of the
	// folder-target-normalisation suite.
	if !strings.Contains(msg, folder+"/") {
		t.Errorf("the message must carry the normalised folder token %q with its trailing separator, "+
			"so the reader can see the argument was read as a folder and not as a test case id.\ngot: %s",
			folder+"/", msg)
	}

	// The raw error text this replaced must not leak back in.
	if strings.Contains(msg, "test case folder not found") {
		t.Errorf("the raw testcase.ErrFolderNotFound text is back in the rendered message; "+
			"route the condition through folderNotFoundMessage instead.\ngot: %s", msg)
	}

	// The dry-run marker is a prefix, not a rewrite.
	dry := folderNotFoundMessage("[dry-run] ", folder)
	if !strings.HasPrefix(dry, "[dry-run] ") || !strings.HasSuffix(dry, msg) {
		t.Errorf("the dry-run form must be the same message behind a prefix.\nplain: %s\ndry:   %s", msg, dry)
	}
}
