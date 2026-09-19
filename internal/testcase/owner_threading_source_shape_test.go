package testcase_test

// ADR-024 s5 structural guard: the resolved owner travels, and downstream
// consumers have nothing left to resolve.
//
// This REPLACES the instrumented enumeration counter that previously stood in for
// this property. The counter measured calls to NewSnapshot, which is not the same
// thing as project-wide enumeration -- three real tree walks bypassed it entirely,
// so it could read 1 while the tree was walked four times. A more accurate counter
// would still have been a proxy for the architecture rather than the architecture.
//
// The property that actually matters is not "how many walks happened" but "can a
// downstream consumer form its own opinion about identity at all". A consumer
// handed a resolved owner cannot: it has no bare id to resolve from. So the guard
// asserts the shape directly -- a migrated consumer must not call a tree scan or a
// bare-ID resolver after ownership has been decided.
//
// This is the same instrument, and the same reasoning, as the F4 source-shape
// guard: when a behavioural test cannot discriminate between "correct" and
// "accidentally correct", constrain the shape instead. It survives refactors that
// a counter does not, because a reintroduced walk fails the test by existing
// rather than by being counted.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forbiddenInConsumer lists the calls a consumer must not make once it has been
// handed a resolved owner. Each is a way of deciding identity independently:
// walking the tree, or turning a bare id back into a file.
var forbiddenInConsumer = []string{
	"scanTestCases",
	"scanTestCasesAll",
	"scanTestCasesRecursive",
	"filepath.Walk",
	"filepath.WalkDir",
	"testcase.FindSource",
	"testcase.Exists",
	"testcase.ResolveSpec",
	"testcase.FindAllSpecs",
	"pipeline.ResolveTestCaseSpec",
	"FindSource",
	"ResolveSpec",
	"FindAllSpecs",
}

// resolverCalls are the SINGLE resolution an orchestrating consumer is allowed to
// perform. A consumer that receives an owner performs zero; a consumer that IS the
// orchestration for its own operation performs exactly one.
//
// REV-159 CODEX-006: resolveLinkOwner is listed because it is a WRAPPER around
// testcase.ResolveOwner, and a wrapper is exactly how the per-record snapshot got
// past this guard. PlanRefresh called resolveLinkOwner, whose own body holds the
// ResolveOwner call, so a check of PlanRefresh's source text alone saw nothing --
// the same one-level-of-indirection blind spot ADR-024 5a records for the
// delivery-boundary guard. Any further wrapper introduced around a resolver
// belongs in this list.
var resolverCalls = []string{"NewSnapshot", "ResolveOwner", "resolveLinkOwner"}

// consumer names one guarded function and how many resolutions it may perform.
type consumer struct {
	file          string // path relative to the repo root
	fn            string // function name
	maxResolution int    // 0 = receives an owner; 1 = resolves once for its operation
	why           string
}

// The migrated consumers named by ADR-024 s5: adapter source selection, post-fill
// validation, hashing, status detail, and triage classification -- plus the wiring
// write and the manual helpers that hash a spec.
var guardedConsumers = []consumer{
	{"internal/adapter/invoker.go", "ownerSource", 0,
		"adapter source selection reads the owner it was handed"},
	{"internal/adapter/builtin_action.go", "ValidateTestCaseOwner", 0,
		"post-fill validation validates the resolved owner, it does not search for one"},
	{"internal/adapter/builtin_action.go", "populateBuiltinPrimeFields", 0,
		"built-in prime hashes the owner carried on the adapter context"},
	{"internal/adapter/manual.go", "populateManualExecuteFields", 0,
		"manual-execute drift compares against the resolved owner's hash"},
	{"internal/adapter/wiring.go", "WriteAutomateWiring", 0,
		"the wiring testcase-hash is the hash of the spec the caller resolved"},
	{"internal/adapter/invoker.go", "buildPipelineRecords", 0,
		"the pipeline write forwards the owner it was given"},
	{"internal/cli/prime.go", "manualUpdateHash", 0,
		"--update-hash rehashes the owner prime already resolved"},
	{"internal/cli/execute.go", "checkWiringDrift", 0,
		"drift compares the wiring hash against the spec execute resolved, not a fresh lookup"},

	// REV-159 CODEX-006: refresh was NOT in this table, which is why a per-record
	// snapshot build inside PlanRefresh survived four review rounds. Both now
	// receive the operation's snapshot and resolve nothing of their own.
	{"internal/link/link.go", "PlanRefresh", 0,
		"refresh looks the owner up in the snapshot its operation already built"},
	{"internal/link/link.go", "RefreshRecord", 0,
		"the refresh write forwards the snapshot it was given"},

	{"internal/reader/status.go", "PipelineDetail", 1,
		"direct single-TC read resolves once, then reads that owner's spec"},
	{"internal/reader/triage.go", "ClassifyTriage", 1,
		"triage classification resolves once, then hashes that owner's spec"},
	{"internal/cli/validate.go", "resolveTargetOwner", 1,
		"the single-TC orchestration resolver: exactly one snapshot for the operation"},
	{"internal/reader/delete_plan.go", "buildDeletePlan", 1,
		"the destructive plan is built from one snapshot"},
	{"internal/cli/validate.go", "requireScopedSpec", 1,
		"the qualified-target scope guard answers both its checks from one snapshot"},
}

// repoRoot walks up from the test's working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not locate the module root (go.mod)")
	return ""
}

// functionBody returns the source text of fn in file, located by parsing rather
// than by brace counting so a nested composite literal cannot truncate it.
func functionBody(t *testing.T, absFile, fn string) string {
	t.Helper()
	src, err := os.ReadFile(absFile)
	if err != nil {
		t.Fatalf("reading %s: %v", absFile, err)
	}
	fset := token.NewFileSet()
	parsed, perr := parser.ParseFile(fset, absFile, src, 0)
	if perr != nil {
		t.Fatalf("parsing %s: %v", absFile, perr)
	}
	for _, decl := range parsed.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name == nil || fd.Name.Name != fn || fd.Body == nil {
			continue
		}
		start := fset.Position(fd.Body.Pos()).Offset
		end := fset.Position(fd.Body.End()).Offset
		return string(src[start:end])
	}
	t.Fatalf("function %s not found in %s", fn, absFile)
	return ""
}

// stripComments removes // and /* */ comments so a call name mentioned in prose
// (these functions explain at length what they no longer call) is not read as a
// call. Without this the guard would fail on its own documentation.
func stripComments(body string) string {
	var out strings.Builder
	i := 0
	for i < len(body) {
		switch {
		case strings.HasPrefix(body[i:], "//"):
			nl := strings.IndexByte(body[i:], '\n')
			if nl < 0 {
				return out.String()
			}
			i += nl
		case strings.HasPrefix(body[i:], "/*"):
			endIdx := strings.Index(body[i+2:], "*/")
			if endIdx < 0 {
				return out.String()
			}
			i += endIdx + 4
		default:
			out.WriteByte(body[i])
			i++
		}
	}
	return out.String()
}

// countCalls counts occurrences of "name(" in code.
func countCalls(code, name string) int {
	return strings.Count(code, name+"(")
}

// TestOwnerThreading_ConsumersDoNotReResolve is the guard. A consumer handed a
// resolved owner must contain no tree scan and no bare-ID resolver; an
// orchestrating consumer may resolve exactly once.
//
// If this fails, the fix is to thread the owner further -- not to add the new call
// site to the allowlist.
func TestOwnerThreading_ConsumersDoNotReResolve(t *testing.T) {
	root := repoRoot(t)

	for _, c := range guardedConsumers {
		t.Run(fmt.Sprintf("%s/%s", filepath.Base(c.file), c.fn), func(t *testing.T) {
			code := stripComments(functionBody(t, filepath.Join(root, filepath.FromSlash(c.file)), c.fn))

			for _, forbidden := range forbiddenInConsumer {
				if n := countCalls(code, forbidden); n > 0 {
					t.Errorf("%s calls %s() %d time(s): %s.\n"+
						"ADR-024 s5: a consumer holding a resolved owner must not re-derive identity. "+
						"Thread the owner to this call site instead of resolving here.",
						c.fn, forbidden, n, c.why)
				}
			}

			total := 0
			for _, r := range resolverCalls {
				total += countCalls(code, r)
			}
			if total > c.maxResolution {
				t.Errorf("%s performs %d identity resolution(s), at most %d allowed: %s.\n"+
					"ADR-024 s5: one operation resolves once and passes the owner forward.",
					c.fn, total, c.maxResolution, c.why)
			}
		})
	}
}

// TestOwnerThreading_NoOperationCache guards the specific workaround this design
// replaced. A cache makes re-resolution cheap; it does not remove it, and it
// leaves every consumer an independent identity implementation -- the shape that
// produced three defects in three different files. It also carried real hazards:
// process-global state, an unenforced "reads only" contract, no nesting guard, and
// a stale window if an adapter wrote a spec mid-operation.
//
// Reintroducing any of it fails here.
func TestOwnerThreading_NoOperationCache(t *testing.T) {
	root := repoRoot(t)
	banned := []string{"BeginOperation", "EndOperation", "opCache", "EnumerationCount"}

	err := filepath.Walk(filepath.Join(root, "internal"), func(path string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		// This guard names the banned symbols, so exclude itself.
		if strings.HasSuffix(path, "owner_threading_source_shape_test.go") {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		content := stripComments(string(data))
		for _, b := range banned {
			if strings.Contains(content, b) {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s references %q: the operation-scoped snapshot cache is deliberately gone. "+
					"Thread the resolved owner instead of memoising the walk (ADR-024 s5).", rel, b)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scanning internal/: %v", err)
	}
}

// TestOwnerThreading_OwnerCannotBeSynthesised pins the property that makes the
// threading enforceable rather than merely conventional: an Owner has unexported
// fields and no exported constructor, so a consumer in another package cannot mint
// one from a string it is holding. Every Owner in the process came from a
// resolution that actually proved ownership.
func TestOwnerThreading_OwnerCannotBeSynthesised(t *testing.T) {
	root := repoRoot(t)
	src, err := os.ReadFile(filepath.Join(root, "internal", "testcase", "owner.go"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(src)

	start := strings.Index(content, "type Owner struct {")
	if start < 0 {
		t.Fatal("type Owner struct not found")
	}
	end := strings.Index(content[start:], "\n}")
	if end < 0 {
		t.Fatal("unterminated Owner struct")
	}
	body := content[start : start+end]

	for _, line := range strings.Split(body, "\n") {
		field := strings.TrimSpace(line)
		if field == "" || strings.HasPrefix(field, "//") || strings.HasPrefix(field, "type ") {
			continue
		}
		name := strings.Fields(field)[0]
		if name[0] >= 'A' && name[0] <= 'Z' {
			t.Errorf("Owner field %q is exported: an Owner must not be constructible outside "+
				"internal/testcase, or a consumer could synthesise one from a bare id and the "+
				"threading guarantee would be advisory only (ADR-024 s5).", name)
		}
	}
}
