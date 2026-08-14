package config

import "testing"

// ENH-190: the reserved no-origin sentinel is a fixed, non-empty value and its
// predicate recognises only that exact value.
func TestNoOriginRepoSentinel(t *testing.T) {
	if NoOriginRepo != "no-origin-remote" {
		t.Fatalf("NoOriginRepo = %q, want %q", NoOriginRepo, "no-origin-remote")
	}
	if NoOriginRepo == "" {
		t.Fatal("NoOriginRepo must be non-empty so it satisfies project.repo validation")
	}

	cases := map[string]bool{
		"no-origin-remote": true,
		"org/repo-name":    false,
		"acme/widgets":     false,
		"":                 false,
		"no-origin":        false,
	}
	for in, want := range cases {
		if got := IsNoOriginRepo(in); got != want {
			t.Errorf("IsNoOriginRepo(%q) = %v, want %v", in, got, want)
		}
	}
}

// ENH-190: the reserved sentinel must pass the existing non-empty project.repo
// validation unchanged -- it must NOT be relaxed by this enhancement.
func TestNoOriginRepoPassesValidation(t *testing.T) {
	cfg := &Config{
		Project: ProjectConfig{Name: "p", Repo: NoOriginRepo},
	}
	if err := validate(cfg); err != nil {
		t.Fatalf("validate() with sentinel repo returned error: %v", err)
	}

	cfg.Project.Repo = ""
	if err := validate(cfg); err == nil {
		t.Fatal("validate() must still reject an empty project.repo")
	}
}
