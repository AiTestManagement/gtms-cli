package adapter

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/result"
)

// validReservedHandoffRC returns a complete, valid terminal execute contract that
// result.Reconstruct accepts, with the GTMS-owned fields already canonicalised.
func validReservedHandoffRC() *result.ResultContract {
	return &result.ResultContract{
		Task:      "task-abcd1234",
		Command:   "execute",
		Target:    "tc-11111111",
		Adapter:   "manual-execute-script",
		Mode:      "sync",
		Created:   "2026-11-06T10:00:00Z",
		Status:    "complete",
		Result:    "pass",
		Framework: "manual",
		Completed: "2026-11-06T10:00:01Z",
		Attempts:  1,
	}
}

// BUG-210: normalizeReservedMode3Runtime is the GTMS-owned runtime normalisation
// applied in handleSyncResult for a reserved Mode 3 execute adapter. It forces
// framework "manual" and mode "sync" on the post-invocation handoff, catching a
// user-edited script that wrote framework: fx and/or mode: async into its own
// contract. The mode branch fails on the previous code, which normalised only
// framework and left mode untouched.

func TestNormalizeReservedMode3Runtime_OverwritesFrameworkAndMode(t *testing.T) {
	rc := &result.ResultContract{Framework: "fx", Mode: "async"}
	updates := normalizeReservedMode3Runtime(rc)

	if rc.Framework != "manual" {
		t.Errorf("framework not normalised: %q", rc.Framework)
	}
	if rc.Mode != "sync" {
		t.Errorf("mode not normalised: %q", rc.Mode)
	}
	if updates["framework"] != "manual" {
		t.Errorf("updates must carry framework=manual, got %v", updates["framework"])
	}
	if updates["mode"] != "sync" {
		t.Errorf("updates must carry mode=sync, got %v", updates["mode"])
	}
}

func TestNormalizeReservedMode3Runtime_ModeOnly(t *testing.T) {
	// Framework already manual, script wrote mode: async.
	rc := &result.ResultContract{Framework: "manual", Mode: "async"}
	updates := normalizeReservedMode3Runtime(rc)

	if rc.Mode != "sync" {
		t.Errorf("mode not normalised: %q", rc.Mode)
	}
	if _, ok := updates["framework"]; ok {
		t.Errorf("framework was already manual; must not be in the update set: %v", updates)
	}
	if updates["mode"] != "sync" {
		t.Errorf("updates must carry mode=sync, got %v", updates["mode"])
	}
}

func TestNormalizeReservedMode3Runtime_NoOpWhenAlreadyCanonical(t *testing.T) {
	rc := &result.ResultContract{Framework: "manual", Mode: "sync"}
	updates := normalizeReservedMode3Runtime(rc)
	if len(updates) != 0 {
		t.Errorf("already-canonical contract must yield no updates, got %v", updates)
	}
	if rc.Framework != "manual" || rc.Mode != "sync" {
		t.Errorf("already-canonical contract must be unchanged: framework=%q mode=%q", rc.Framework, rc.Mode)
	}
}

// REV-272 CODEX-002: when the ordinary field-merge Update fails on the handoff,
// persistReservedMode3Handoff falls back to Reconstruct and still persists the
// canonical contract. Fails on the previous code, which had no fail-closed
// persistence (the restore write was best effort and only warned).
func TestPersistReservedMode3Handoff_UpdateFailsReconstructSucceeds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "handoff.yaml")
	// A YAML sequence cannot unmarshal into the map Update expects, so the field
	// merge fails deterministically on every platform; Reconstruct does not read
	// the old file, so it succeeds.
	if err := os.WriteFile(path, []byte("- not-a-mapping\n"), 0644); err != nil {
		t.Fatalf("seeding corrupt handoff: %v", err)
	}
	rc := validReservedHandoffRC()

	if err := persistReservedMode3Handoff(path, map[string]interface{}{"framework": "manual", "mode": "sync"}, rc); err != nil {
		t.Fatalf("persist must succeed via Reconstruct, got %v", err)
	}
	got, err := result.Read(path)
	if err != nil {
		t.Fatalf("handoff must be readable after reconstruct: %v", err)
	}
	if got.Framework != "manual" || got.Mode != "sync" {
		t.Errorf("reconstructed handoff not canonical: framework=%q mode=%q", got.Framework, got.Mode)
	}
	if got.Status != "complete" || got.Result != "pass" {
		t.Errorf("reconstructed handoff lost its terminal outcome: status=%q result=%q", got.Status, got.Result)
	}
}

// Both writes failing returns an error naming both failures -- the BUG-218
// total-persistence limitation the caller routes to error. REV-280 corrected
// this test to inject failures after the initial safe-leaf inspection; the old
// directory fixture stopped at that inspection and never reached either write.
func TestPersistReservedMode3Handoff_BothFailReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "handoff.yaml")
	if err := result.Reconstruct(path, validReservedHandoffRC()); err != nil {
		t.Fatalf("seeding regular handoff: %v", err)
	}

	origUpdate, origReconstruct := persistUpdate, persistReconstruct
	persistUpdate = func(string, map[string]interface{}) error { return errors.New("injected update failure") }
	persistReconstruct = func(string, *result.ResultContract, result.HandoffState) error {
		return errors.New("injected reconstruct failure")
	}
	t.Cleanup(func() { persistUpdate, persistReconstruct = origUpdate, origReconstruct })

	err := persistReservedMode3Handoff(path, map[string]interface{}{"framework": "manual"}, validReservedHandoffRC())
	if err == nil {
		t.Fatal("persist must return an error when both Update and Reconstruct fail")
	}
	if !strings.Contains(err.Error(), "injected update failure") {
		t.Fatalf("error must name Update failure, got %v", err)
	}
	if !strings.Contains(err.Error(), "injected reconstruct failure") {
		t.Fatalf("error must name Reconstruct failure, got %v", err)
	}
}
