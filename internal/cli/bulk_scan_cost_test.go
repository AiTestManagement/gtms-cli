package cli

// BUG-209 scan-cost measurement (REV-270 follow-up). It times the bulk
// already-passing read two ways over a realistic corpus (thousands of handoffs,
// dozens of test cases):
//
//   - OLD (d54425d25): isAlreadyPassing enumerated .gtms/results and parsed every
//     handoff ONCE PER TEST CASE -- O(testCases x handoffs).
//   - NEW (BUG-209): one result.OpenScan builds the pass candidates, then each
//     test case does an O(1) map lookup -- O(handoffs) once.
//
// Run: go test ./internal/cli/ -run TestBulkAlreadyPassingScanCost -v
// The durations are logged so the ratio is reported, not asserted (timing varies
// by host), but the asymmetry (one scan vs N scans) is the point.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/aitestmanagement/gtms-cli/internal/result"
)

func TestBulkAlreadyPassingScanCost(t *testing.T) {
	skipIfShort(t)
	const (
		testCases       = 50
		handoffsPerCase = 40 // 50 x 40 = 2000 handoffs
	)
	root := t.TempDir()
	resultsDir := filepath.Join(root, ".gtms", "results")
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	tcIDs := make([]string, testCases)
	for i := 0; i < testCases; i++ {
		tcID := fmt.Sprintf("tc-%08x", i)
		tcIDs[i] = tcID
		for j := 0; j < handoffsPerCase; j++ {
			task := fmt.Sprintf("task-%04x%04x", i, j)
			body := fmt.Sprintf("task: %s\ncommand: execute\ntarget: %s\nadapter: fx\nmode: sync\nframework: fx\nstatus: complete\nresult: pass\ncompleted: 2026-11-06T10:%02d:%02dZ\n",
				task, tcID, i%60, j%60)
			if err := os.WriteFile(filepath.Join(resultsDir, task+".handoff.yaml"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	// OLD cost: per test case, enumerate .gtms/results and os.ReadFile + unmarshal
	// every handoff -- the d54425d25 isAlreadyPassing Source-1 loop (its result.Read
	// was os.ReadFile + yaml.Unmarshal, with no per-call os.Root). O(TC x handoffs).
	oldStart := time.Now()
	oldMatches := 0
	for _, tcID := range tcIDs {
		entries, err := os.ReadDir(resultsDir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".handoff.yaml") {
				continue
			}
			data, rErr := os.ReadFile(filepath.Join(resultsDir, e.Name()))
			if rErr != nil {
				continue
			}
			var rc result.ResultContract
			if yaml.Unmarshal(data, &rc) != nil || rc.Target != tcID {
				continue
			}
			if result.IsExecuteContract(&rc) {
				oldMatches++
			}
		}
	}
	oldDur := time.Since(oldStart)

	// NEW cost: one scan builds the candidates, then O(1) lookups per test case.
	newStart := time.Now()
	scan := result.OpenScan(root)
	cand := buildExecutePassCandidates(scan)
	scan.Close()
	newMatches := 0
	for _, tcID := range tcIDs {
		newMatches += len(cand[tcID])
	}
	newDur := time.Since(newStart)

	if oldMatches != newMatches {
		t.Fatalf("match mismatch: old=%d new=%d (the new path must select the same candidates)", oldMatches, newMatches)
	}

	ratio := float64(oldDur) / float64(newDur)
	t.Logf("BUG-209 bulk already-passing scan cost over %d test cases x %d handoffs = %d handoffs:",
		testCases, handoffsPerCase, testCases*handoffsPerCase)
	t.Logf("  OLD (per-TC rescan, O(TC x handoffs)): %s  (%d reads)", oldDur, testCases*testCases*handoffsPerCase)
	t.Logf("  NEW (one scan + O(1) lookups):         %s  (%d reads)", newDur, testCases*handoffsPerCase)
	t.Logf("  speedup: %.1fx", ratio)
}
