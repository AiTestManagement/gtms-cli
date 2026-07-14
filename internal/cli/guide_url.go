package cli

import "strings"

// refFromVersion returns the git ref a doc permalink should point at:
// the release tag when this is a tagged build, else "main".
func refFromVersion() string {
	if strings.HasPrefix(Version, "v") {
		return Version
	}
	return "main"
}

// guideURL builds a GitHub permalink into the published repo tree.
// relPath is repo-relative (e.g. "USER-GUIDE.md"); anchor is the heading slug
// without '#' (e.g. "adapter-execution-model"), or "" for no fragment.
func guideURL(relPath, anchor string) string {
	u := "https://github.com/aitestmanagement/gtms-cli/blob/" + refFromVersion() + "/" + relPath
	if anchor != "" {
		u += "#" + anchor
	}
	return u
}
