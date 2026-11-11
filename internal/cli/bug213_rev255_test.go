package cli

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/scaffold"
	"github.com/stretchr/testify/assert"
)

func TestBUG213_REV255_CreatedSummaryDistinguishesRefusedLeafAndDirectory(t *testing.T) {
	t.Run("refused leaf keeps created directory summary", func(t *testing.T) {
		out := captureBUG213Stdout(t, func() {
			formatInitOutput(t.TempDir(), "Leaf", "org/leaf", "manual", &scaffold.Result{
				Parent:       "gtms",
				FilesRefused: []string{"gtms/tasks/.README.md"},
			}, false)
		})

		assert.Contains(t, out, "gtms/tasks/ (5 status directories)",
			"refusing one leaf must not hide the directory structure that was created")
		assert.NotContains(t, out, "gtms/tasks/.README.md",
			"the refused leaf itself must not be listed under Created")
	})

	t.Run("refused directory hides its summary", func(t *testing.T) {
		out := captureBUG213Stdout(t, func() {
			formatInitOutput(t.TempDir(), "Directory", "org/directory", "manual", &scaffold.Result{
				Parent:       "gtms",
				DirsRefused:  []string{"gtms/tasks"},
				FilesRefused: []string{"gtms/tasks", "gtms/tasks/.gitkeep"},
			}, false)
		})

		assert.NotContains(t, out, "gtms/tasks/ (5 status directories)",
			"a refused directory must not be claimed under Created")
	})

	t.Run("refused nested skill directory hides catalog summary", func(t *testing.T) {
		out := captureBUG213Stdout(t, func() {
			formatInitOutput(t.TempDir(), "Skills", "org/skills", "manual", &scaffold.Result{
				Parent:      "gtms",
				DirsRefused: []string{"gtms/skills/gtms-tests-prime"},
			}, false)
		})

		assert.NotContains(t, out, "gtms/skills/ (starter agent skills)",
			"a refused directory below the catalog must suppress its Created summary")
	})
}

func captureBUG213Stdout(t *testing.T, fn func()) string {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	var buf bytes.Buffer
	done := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(&buf, r)
		done <- copyErr
	}()

	fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = old
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestBUG213_REV255_RefusedAgentSnippetHasNoPastePrompt(t *testing.T) {
	stderr := captureStderr(t, func() {
		formatInitOutput(t.TempDir(), "Snippet", "org/snippet", "manual", &scaffold.Result{
			Parent:       "gtms",
			FilesRefused: []string{"gtms/AGENTS-SNIPPET.md"},
		}, false)
	})

	assert.NotContains(t, stderr, "Wire your AI agent in",
		"init must not direct the user to a snippet it refused to write")
	assert.NotContains(t, stderr, "paste gtms/AGENTS-SNIPPET.md",
		"the refused path must not appear in the paste prompt")
}
