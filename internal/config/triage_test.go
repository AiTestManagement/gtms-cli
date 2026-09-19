package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// parseTriage decodes just the triage presence into a Config, mirroring how
// Load builds the struct, so the presence-semantics tests exercise the real
// pointer-of-map decoding.
func parseTriage(t *testing.T, doc string) *Config {
	t.Helper()
	var c Config
	if err := yaml.Unmarshal([]byte(doc), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &c
}

func TestResolveTriageValues_PresenceSemantics(t *testing.T) {
	defaultsExpected := func(m map[string]string, err error) func(*testing.T) {
		return func(t *testing.T) {
			if err != nil {
				t.Fatalf("expected defaults, got error %v", err)
			}
			if len(m) != len(defaultTriageValues) {
				t.Fatalf("expected %d default keys, got %d", len(defaultTriageValues), len(m))
			}
			if m["test-wrong"] != "Test definition problem" || m["app-wrong"] != "" {
				t.Fatalf("default labels wrong: %+v", m)
			}
		}
	}

	// absent block
	c := parseTriage(t, "project:\n  name: x\n")
	m, err := c.ResolveTriageValues()
	defaultsExpected(m, err)(t)

	// triage: (bare/null)
	c = parseTriage(t, "triage:\n")
	m, err = c.ResolveTriageValues()
	defaultsExpected(m, err)(t)

	// triage: {}
	c = parseTriage(t, "triage: {}\n")
	m, err = c.ResolveTriageValues()
	defaultsExpected(m, err)(t)

	// triage: { values: null }
	c = parseTriage(t, "triage:\n  values:\n")
	m, err = c.ResolveTriageValues()
	defaultsExpected(m, err)(t)

	// triage: { values: {} } -> refuse
	c = parseTriage(t, "triage:\n  values: {}\n")
	_, err = c.ResolveTriageValues()
	if err == nil {
		t.Fatalf("expected empty-values refusal")
	}

	// non-empty map replaces defaults
	c = parseTriage(t, "triage:\n  values:\n    needs-data: Waiting on data\n")
	m, err = c.ResolveTriageValues()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m) != 1 || m["needs-data"] != "Waiting on data" {
		t.Fatalf("expected replacement map, got %+v", m)
	}
	if _, ok := m["environment"]; ok {
		t.Fatalf("non-empty map must replace, not extend defaults")
	}
}

func TestResolveTriageValues_KeyValidation(t *testing.T) {
	bad := []string{"Environment", "needs data", "2fast", "-leading-dash"}
	for _, k := range bad {
		c := &Config{Triage: &TriageConfig{Values: &map[string]string{k: "x"}}}
		if _, err := c.ResolveTriageValues(); err == nil {
			t.Fatalf("expected rejection of key %q", k)
		}
	}
	// empty label is valid
	c := &Config{Triage: &TriageConfig{Values: &map[string]string{"blocked": ""}}}
	m, err := c.ResolveTriageValues()
	if err != nil || m["blocked"] != "" {
		t.Fatalf("empty label should be accepted: %v %+v", err, m)
	}
}
