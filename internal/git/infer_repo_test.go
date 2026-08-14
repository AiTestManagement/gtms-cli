package git

import (
	"context"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setOrigin points the repo at dir's origin remote at url.
func setOrigin(t *testing.T, dir, url string) {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "remote", "add", "origin", url)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git remote add failed: %s", string(out))
}

// TestInferRepo_RemoteURLForms pins ENH-190's correction to origin parsing.
//
// The SSH branch used to be selected by "there is no / before the colon",
// which is true of the scheme in "https://github.com/..." as well as of
// "git@github.com:...". Every HTTPS remote therefore returned
// "//github.com/org/repo" and that mangled value was persisted into
// project.repo -- for the most common remote format there is. Scheme URLs and
// SCP-style SSH are now told apart by the presence of "://".
func TestInferRepo_RemoteURLForms(t *testing.T) {
	skipIfShort(t)

	cases := []struct {
		name string
		url  string
		want string
	}{
		// The defect this fixes.
		{"https with .git", "https://github.com/acme/repo.git", "acme/repo"},
		{"https without .git", "https://github.com/acme/repo", "acme/repo"},
		{"https with trailing slash", "https://github.com/acme/repo/", "acme/repo"},
		// Unchanged by ENH-190 -- these already worked and must keep working.
		{"scp-style ssh with .git", "git@github.com:acme/repo.git", "acme/repo"},
		{"scp-style ssh without .git", "git@github.com:acme/repo", "acme/repo"},
		// Regression boundary: ssh:// is a SCHEME, not SCP style. Before the
		// fix its colon also triggered the SCP branch.
		{"ssh scheme url", "ssh://git@github.com/acme/repo.git", "acme/repo"},
		{"git scheme url", "git://github.com/acme/repo.git", "acme/repo"},
		// Nested paths: the identity must NOT depend on transport, so both
		// forms keep the full group path (REV CODEX-001).
		{"scp-style nested group", "git@gitlab.com:group/sub/repo.git", "group/sub/repo"},
		{"https nested group", "https://gitlab.com/group/sub/repo.git", "group/sub/repo"},
		{"ssh scheme nested group", "ssh://git@gitlab.com/group/sub/repo.git", "group/sub/repo"},
		// Host information (userinfo@, :port) precedes the first path slash and
		// must never bleed into the identity.
		{"https with port", "https://gitlab.example.com:8443/group/repo.git", "group/repo"},
		{"ssh scheme with port", "ssh://git@host:2222/group/repo.git", "group/repo"},
		// CLAUDE-001: ".git/" must not survive as part of the identity. The
		// trims run slash -> .git -> slash for exactly this shape.
		{"https with .git and trailing slash", "https://github.com/acme/repo.git/", "acme/repo"},
		{"scp-style with .git and trailing slash", "git@github.com:acme/repo.git/", "acme/repo"},
		// CLAUDE-002: a bare path remote is a real origin. It must not fall
		// through to the caller's fallback, which init uses for "no origin".
		{"filesystem path remote", "/srv/git/acme/repo.git", "acme/repo"},
		{"relative path remote", "../mirrors/acme/repo.git", "acme/repo"},
		// CODEX-001 (final round): a colon inside a filesystem path is part of a
		// directory name, not an SCP host:path separator. In SCP syntax the
		// colon always precedes any "/", so a path like this must fall through
		// to the path handling rather than being split at the colon.
		{"path remote containing a colon", "/srv/acme:archive/repo.git", "acme:archive/repo"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := initTempRepo(t)
			setOrigin(t, dir, tc.url)

			got := InferRepo(context.Background(), dir, "FALLBACK")
			assert.Equal(t, tc.want, got, "remote %q", tc.url)
			assert.NotContains(t, got, "://", "a scheme must never survive into project.repo")
		})
	}
}

// TestInferRepo_NoOriginReturnsFallback: with no origin remote the caller's
// fallback is returned verbatim. This is the whole mechanism behind ENH-190's
// no-origin sentinel -- init passes config.NoOriginRepo as the fallback, so
// this function needs no knowledge of the sentinel itself.
func TestInferRepo_NoOriginReturnsFallback(t *testing.T) {
	skipIfShort(t)

	dir := initTempRepo(t)
	assert.Equal(t, "FALLBACK", InferRepo(context.Background(), dir, "FALLBACK"))
}

// TestInferRepo_DifferentlyNamedRemoteStillFallsBack: InferRepo only ever
// queries "origin", so a repo whose only remote is named something else takes
// the fallback path. This is why ENH-190's sentinel says "origin" rather than
// the looser "git remote".
func TestInferRepo_DifferentlyNamedRemoteStillFallsBack(t *testing.T) {
	skipIfShort(t)

	dir := initTempRepo(t)
	cmd := exec.Command("git", "-C", dir, "remote", "add", "upstream", "https://github.com/acme/repo.git")
	require.NoError(t, cmd.Run())

	assert.Equal(t, "FALLBACK", InferRepo(context.Background(), dir, "FALLBACK"))
}
