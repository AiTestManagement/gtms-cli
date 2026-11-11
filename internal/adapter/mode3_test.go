package adapter

import "testing"

// TestIsMode3ExecuteAdapterName_AllFourNames proves all four reserved
// Mode 3 names are recognised by the exported predicate. This is
// the dispatch-table test required by tc-ba2f54e2: it verifies the
// name-based predicate independently of the CLI dispatch order, which
// is tested separately.
func TestIsMode3ExecuteAdapterName_AllFourNames(t *testing.T) {
	reserved := []string{
		"manual-execute",
		"agent-execute",
		"manual-execute-script",
		"agent-execute-script",
	}
	for _, name := range reserved {
		if !IsMode3ExecuteAdapterName(name) {
			t.Errorf("IsMode3ExecuteAdapterName(%q) = false, want true", name)
		}
	}
}

func TestIsMode3ExecuteAdapterName_NonReserved(t *testing.T) {
	nonReserved := []string{
		"bats-runner",
		"playwright-runner",
		"remote-winrm-01",
		"manual-automate",
		"agent-create",
		"",
	}
	for _, name := range nonReserved {
		if IsMode3ExecuteAdapterName(name) {
			t.Errorf("IsMode3ExecuteAdapterName(%q) = true, want false", name)
		}
	}
}

// --- BUG-210 admission predicate ---

func TestValidateReservedMode3Framework(t *testing.T) {
	const adapterName = "manual-execute-script"

	// Not refused: omitted / manual flag and config, mode sync.
	for _, tc := range []struct{ flag, cfg string }{
		{"", ""},
		{"", "manual"},
		{"manual", ""},
		{"manual", "manual"},
	} {
		if _, _, refused := ValidateReservedMode3Framework(adapterName, tc.flag, tc.cfg, "sync"); refused {
			t.Errorf("flag=%q config=%q: refused=true, want false", tc.flag, tc.cfg)
		}
	}

	// Flag route: non-manual flag gives the flag diagnostic, even when config is also non-manual.
	msg, hint, refused := ValidateReservedMode3Framework(adapterName, "fx", "fx", "sync")
	if !refused {
		t.Fatal("non-manual flag must be refused")
	}
	if msg != "Adapter 'manual-execute-script' records manual results and cannot run under framework 'fx'." {
		t.Errorf("flag message mismatch: %q", msg)
	}
	if hint != "Omit --framework, or use --framework manual. To run the fx automation, use its adapter." {
		t.Errorf("flag hint mismatch: %q", hint)
	}

	// Precedence: --framework manual with config fx gives the CONFIG (framework) diagnostic.
	msg, hint, refused = ValidateReservedMode3Framework(adapterName, "manual", "fx", "sync")
	if !refused {
		t.Fatal("non-manual config must be refused even with --framework manual")
	}
	if msg != "Adapter 'manual-execute-script' records manual results but gtms.config sets framework 'fx'." {
		t.Errorf("config message mismatch: %q", msg)
	}
	if hint != "Remove the framework setting or set it to manual." {
		t.Errorf("config hint mismatch: %q", hint)
	}

	// Config route with omitted flag gives the config (framework) diagnostic.
	if _, _, refused := ValidateReservedMode3Framework(adapterName, "", "fx", "sync"); !refused {
		t.Error("omitted flag + non-manual config must be refused")
	}
}

// TestValidateReservedMode3Framework_FlagHintNamesTheFlagFramework proves the
// flag diagnostic's hint is built from the actual flag value, not hard-coded to
// "fx". A non-fx flag (pester) must name pester in both lines; this fails on the
// old hard-coded hint "To run the fx automation, use its adapter."
func TestValidateReservedMode3Framework_FlagHintNamesTheFlagFramework(t *testing.T) {
	msg, hint, refused := ValidateReservedMode3Framework("agent-execute-script", "pester", "", "sync")
	if !refused {
		t.Fatal("a non-manual flag must be refused")
	}
	if msg != "Adapter 'agent-execute-script' records manual results and cannot run under framework 'pester'." {
		t.Errorf("message must name the flag framework: %q", msg)
	}
	if hint != "Omit --framework, or use --framework manual. To run the pester automation, use its adapter." {
		t.Errorf("hint must name the flag framework, not a hard-coded fx: %q", hint)
	}

	// The approved example value fx is unchanged (byte-for-byte spec text).
	_, fxHint, _ := ValidateReservedMode3Framework("manual-execute-script", "fx", "", "sync")
	if fxHint != "Omit --framework, or use --framework manual. To run the fx automation, use its adapter." {
		t.Errorf("fx hint must match the approved diagnostic exactly: %q", fxHint)
	}
}

// TestValidateReservedMode3Framework_ModePrecedence proves the sync-only rule and
// the three-level precedence for all four reserved names. Fails on the previous
// code, which had no mode parameter and never refused mode: async.
func TestValidateReservedMode3Framework_ModePrecedence(t *testing.T) {
	for _, name := range []string{"manual-execute", "agent-execute", "manual-execute-script", "agent-execute-script"} {
		// Rule 3: framework ok (omitted/manual), mode async -> mode diagnostic.
		for _, cfgFramework := range []string{"", "manual"} {
			msg, hint, refused := ValidateReservedMode3Framework(name, "", cfgFramework, "async")
			if !refused {
				t.Fatalf("%s: configured mode async must be refused (configFramework=%q)", name, cfgFramework)
			}
			wantMsg := "Adapter '" + name + "' records manual results but gtms.config sets mode 'async'."
			if msg != wantMsg {
				t.Errorf("%s: mode message mismatch: %q", name, msg)
			}
			if hint != "Set its mode to sync." {
				t.Errorf("%s: mode hint mismatch: %q", name, hint)
			}
		}

		// --framework manual with mode async still gives the mode diagnostic.
		if msg, _, refused := ValidateReservedMode3Framework(name, "manual", "manual", "async"); !refused ||
			msg != "Adapter '"+name+"' records manual results but gtms.config sets mode 'async'." {
			t.Errorf("%s: --framework manual + mode async must give the mode diagnostic, got refused=%v msg=%q", name, refused, msg)
		}

		// Precedence: a non-manual flag outranks mode async (flag diagnostic).
		if msg, _, refused := ValidateReservedMode3Framework(name, "fx", "", "async"); !refused ||
			msg != "Adapter '"+name+"' records manual results and cannot run under framework 'fx'." {
			t.Errorf("%s: non-manual flag must outrank mode async, got refused=%v msg=%q", name, refused, msg)
		}

		// Precedence: a non-manual configured framework outranks mode async.
		if msg, _, refused := ValidateReservedMode3Framework(name, "", "fx", "async"); !refused ||
			msg != "Adapter '"+name+"' records manual results but gtms.config sets framework 'fx'." {
			t.Errorf("%s: non-manual configured framework must outrank mode async, got refused=%v msg=%q", name, refused, msg)
		}

		// Not refused: framework ok and mode sync.
		if _, _, refused := ValidateReservedMode3Framework(name, "", "manual", "sync"); refused {
			t.Errorf("%s: framework manual + mode sync must be permitted", name)
		}
	}
}
