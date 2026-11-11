package testcase

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	testrequire "github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// nineLevelAliasChain is the BUG-204 reproduction: a nine-level, nine-way alias
// chain under meta. Expanded naively it visits astronomically many nodes.
const nineLevelAliasChain = "meta:\n" +
	"  a: &a [\"x\",\"x\",\"x\",\"x\",\"x\",\"x\",\"x\",\"x\",\"x\"]\n" +
	"  b: &b [*a,*a,*a,*a,*a,*a,*a,*a,*a]\n" +
	"  c: &c [*b,*b,*b,*b,*b,*b,*b,*b,*b]\n" +
	"  d: &d [*c,*c,*c,*c,*c,*c,*c,*c,*c]\n" +
	"  e: &e [*d,*d,*d,*d,*d,*d,*d,*d,*d]\n" +
	"  f: &f [*e,*e,*e,*e,*e,*e,*e,*e,*e]\n" +
	"  g: &g [*f,*f,*f,*f,*f,*f,*f,*f,*f]\n" +
	"  h: &h [*g,*g,*g,*g,*g,*g,*g,*g,*g]\n" +
	"  i: &i [*h,*h,*h,*h,*h,*h,*h,*h,*h]\n"

// TestBUG204_CraftedAliasKeysAreMalformed covers AC1 and the "new" compatibility
// matrix rows: both crafted key shapes fail as malformed YAML (empty Key) while
// the top-level test_case_id stays attributable.
func TestBUG204_CraftedAliasKeysAreMalformed(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
	}{
		{name: "direct alias key", key: "  ? *i\n  : refused\n"},
		{name: "complex key containing the alias", key: "  ? {k: *i}\n  : refused\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := DecodeCandidate(strictV2Document(nineLevelAliasChain + tc.key))
			testrequire.NotNil(t, result.Failure)
			assert.Equal(t, SchemaFailureMalformedYAML, result.Failure.Kind)
			assert.Equal(t, "", result.Failure.Key, "malformed key failure must carry empty Key for create/snapshot envelopes")
			// Attribution survives the malformed classification (AC1).
			assert.Equal(t, "tc-abcdef12", result.ParsedIdentity)
			assert.True(t, result.DeclaresIdentity)
		})
	}
}

// TestBUG204_CyclicKeyRejectedValueAccepted covers Decision 7.
func TestBUG204_CyclicKeyRejectedValueAccepted(t *testing.T) {
	cyclicKey := DecodeCandidate(strictV2Document("meta:\n  ? &k [one, *k]\n  : refused\n"))
	testrequire.NotNil(t, cyclicKey.Failure)
	assert.Equal(t, SchemaFailureMalformedYAML, cyclicKey.Failure.Kind)
	assert.Equal(t, "", cyclicKey.Failure.Key)

	cyclicValue := DecodeCandidate(strictV2Document("meta:\n  cyclic_value: &v [one, *v]\n"))
	testrequire.Nil(t, cyclicValue.Failure, "a cyclic alias used only in a value stays accepted")
}

// TestBUG204_FiniteFormsPreserved covers Decision 1 and the "unchanged" rows.
func TestBUG204_FiniteFormsPreserved(t *testing.T) {
	for _, tc := range []struct {
		name string
		meta string
	}{
		{name: "small alias key", meta: "meta:\n  seed: &small [finite, alias]\n  ? *small\n  : accepted\n"},
		{name: "sequence and mapping keys", meta: "meta:\n  ? [sequence, key]\n  : accepted\n  ? {mapping: key, second: field}\n  : accepted\n"},
		{name: "merge keys nested and direct", meta: "meta:\n  base: &base {x: 1}\n  nested: {<<: *base, y: 2}\n  <<: *base\n"},
		{name: "value only nine level chain with scalar keys", meta: nineLevelAliasChain},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := DecodeCandidate(strictV2Document(tc.meta))
			testrequire.Nil(t, result.Failure)
		})
	}
}

// TestBUG204_DuplicateDetectionUnchanged confirms duplicate-key detection
// (including through an alias of an existing key) is preserved.
func TestBUG204_DuplicateDetectionUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name string
		meta string
	}{
		{name: "alias of an existing key", meta: "meta:\n  ? &key anchored\n  : first\n  ? *key\n  : second\n"},
		{name: "ordinary duplicate scalar keys", meta: "meta:\n  a: first\n  a: second\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := DecodeCandidate(strictV2Document(tc.meta))
			testrequire.NotNil(t, result.Failure)
			assert.Equal(t, SchemaFailureDuplicateYAMLKey, result.Failure.Kind)
		})
	}
}

// TestBUG204_DeepNestedKeyWiredEndToEnd proves the depth guard is reached through
// DecodeCandidate (exact 64/65 boundaries are asserted in the budget unit tests).
func TestBUG204_DeepNestedKeyWiredEndToEnd(t *testing.T) {
	shallow := "meta:\n  ? " + strings.Repeat("[", 10) + "x" + strings.Repeat("]", 10) + "\n  : ok\n"
	testrequire.Nil(t, DecodeCandidate(strictV2Document(shallow)).Failure)

	deep := "meta:\n  ? " + strings.Repeat("[", 100) + "x" + strings.Repeat("]", 100) + "\n  : ok\n"
	result := DecodeCandidate(strictV2Document(deep))
	testrequire.NotNil(t, result.Failure)
	assert.Equal(t, SchemaFailureMalformedYAML, result.Failure.Kind)
}

// --- Boundary unit tests (Decisions 2 and 8): exact arithmetic. ---

// nilChildSequence returns a sequence of n nil children. Each nil renders as the
// four-byte "nil;" marker and costs one visit, so this isolates the visit budget
// (100,000) while staying far under the 1 MiB byte budget: the two budgets
// otherwise interact, since any node with a header costs at least a few bytes and
// 100,000 such nodes would alone exceed 1 MiB.
func nilChildSequence(n int) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode}
	seq.Content = make([]*yaml.Node, n)
	return seq
}

// nestedSequence returns a chain of `levels` nested single-element sequences
// around a scalar leaf. Its relative subtree depth is `levels + 1`.
func nestedSequence(levels int) *yaml.Node {
	node := &yaml.Node{Kind: yaml.ScalarNode, Value: "x"}
	for i := 0; i < levels; i++ {
		node = &yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{node}}
	}
	return node
}

func TestBUG204_VisitBudgetBoundary(t *testing.T) {
	// cost(nilChildSequence(n)) = 1 + n. 99999 -> 100000 visits exactly.
	atLimit := newKeyIdentityBudget()
	atLimit.account(nilChildSequence(99999))
	assert.False(t, atLimit.exceeded, "exactly 100000 logical visits is accepted")
	assert.Equal(t, int64(100000), atLimit.total)

	overLimit := newKeyIdentityBudget()
	overLimit.account(nilChildSequence(100000)) // cost 100001
	assert.True(t, overLimit.exceeded, "100001 logical visits is refused")
}

func TestBUG204_DocumentWideBudgetAccumulates(t *testing.T) {
	// Two keys of 60000 visits each are individually under budget but together
	// exceed it under one document-wide budget.
	budget := newKeyIdentityBudget()
	budget.account(nilChildSequence(59999)) // cost 60000
	assert.False(t, budget.exceeded)
	budget.account(nilChildSequence(59999)) // cumulative 120000
	assert.True(t, budget.exceeded)
}

func TestBUG204_DepthBoundary(t *testing.T) {
	at64 := newKeyIdentityBudget()
	at64.account(nestedSequence(63)) // relative depth 64
	assert.False(t, at64.exceeded, "depth 64 is accepted")

	at65 := newKeyIdentityBudget()
	at65.account(nestedSequence(64)) // relative depth 65
	assert.True(t, at65.exceeded, "depth 65 is refused")
}

// TestBUG204_SaturatesWithoutOverflow builds a shared-child multiplying graph
// (what aliases parse into) deep enough that a naive product would overflow
// int64, and asserts it saturates and fails without panicking.
func TestBUG204_SaturatesWithoutOverflow(t *testing.T) {
	node := &yaml.Node{Kind: yaml.ScalarNode, Value: "x"}
	for i := 0; i < 20; i++ {
		next := &yaml.Node{Kind: yaml.SequenceNode}
		next.Content = []*yaml.Node{node, node, node, node, node, node, node, node, node}
		node = next
	}
	budget := newKeyIdentityBudget()
	assert.NotPanics(t, func() { budget.account(node) })
	assert.True(t, budget.exceeded)
	assert.Equal(t, costOverBudget, budget.total)
}

// TestBUG204_ValueOnlyAliasesConsumeNoKeyBudget confirms a large value-only
// alias graph with scalar keys spends none of the visit or byte key budget.
func TestBUG204_ValueOnlyAliasesConsumeNoKeyBudget(t *testing.T) {
	result := DecodeCandidate(strictV2Document(nineLevelAliasChain))
	testrequire.Nil(t, result.Failure)
}

// TestBUG204_ByteCostMirrorsIdentityBuilder is the drift guard required by Owner
// Decision 8: the computed byte cost must equal len(yamlNodeStructuralIdentity)
// for a representative set of keys, so the mirror cannot silently diverge from
// the real identity builder.
func TestBUG204_ByteCostMirrorsIdentityBuilder(t *testing.T) {
	doc := "" +
		"42: a\n" +
		"3.14: b\n" +
		"true: c\n" +
		"2020-01-02T03:04:05Z: d\n" +
		"plain: e\n" +
		"!!str 7: f\n" +
		"[s1, s2]: g\n" +
		"{mk: mv, mk2: mv2}: h\n" +
		"? &anc anchored-scalar\n" +
		": i\n" +
		"? *anc\n" +
		": j\n"

	var root yaml.Node
	testrequire.NoError(t, yaml.Unmarshal([]byte(doc), &root))
	mapping := documentMapping(&root)
	testrequire.NotNil(t, mapping)
	testrequire.GreaterOrEqual(t, len(mapping.Content), 20)

	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key := mapping.Content[i]
		want := int64(len(yamlNodeStructuralIdentity(key, make(map[*yaml.Node]string))))
		m, cyclic := newKeyIdentityBudget().metric(key, map[*yaml.Node]bool{})
		testrequire.False(t, cyclic, "representative key %d is not cyclic", i/2)
		assert.Equalf(t, want, m.bytes, "byte cost must mirror identity length for key index %d", i/2)
	}
}

// TestBUG204_ByteBudgetBoundary covers Owner Decision 8: exactly 1 MiB of
// rendered identity is accepted, one byte over is refused. The scalar's value
// length is tuned so its identity (ground truth: the real builder) lands on the
// boundary, then the budget is asserted at that exact length.
func TestBUG204_ByteBudgetBoundary(t *testing.T) {
	makeScalar := func(valLen int) *yaml.Node {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: strings.Repeat("a", valLen)}
	}
	idLen := func(n *yaml.Node) int { return len(yamlNodeStructuralIdentity(n, make(map[*yaml.Node]string))) }

	valLen := int(maxKeyIdentityBytes)
	var atLimit *yaml.Node
	for iter := 0; iter < 50; iter++ {
		cand := makeScalar(valLen)
		if int64(idLen(cand)) == maxKeyIdentityBytes {
			atLimit = cand
			break
		}
		valLen += int(maxKeyIdentityBytes) - idLen(cand)
		if valLen < 0 {
			valLen = 0
		}
	}
	testrequire.NotNil(t, atLimit, "could not tune a scalar to exactly the byte budget")
	testrequire.Equal(t, maxKeyIdentityBytes, int64(idLen(atLimit)))

	over := makeScalar(valLen + 1)
	testrequire.Equal(t, maxKeyIdentityBytes+1, int64(idLen(over)), "one more byte of value is one more identity byte in this range")

	accepted := newKeyIdentityBudget()
	accepted.account(atLimit)
	assert.False(t, accepted.exceeded, "exactly 1 MiB of rendered identity is accepted")

	refused := newKeyIdentityBudget()
	refused.account(over)
	assert.True(t, refused.exceeded, "1 MiB + 1 byte is refused")
	assert.Contains(t, refused.detail, "byte")
}

// TestBUG204_LargeScalarUnderVisitLimitRefusedByBytes is the CLAUDE-001
// regression (REV-257): a large anchored scalar aliased many times stays under
// the 100,000-visit budget but renders far more than 1 MiB. It must be refused by
// the byte budget, not the visit budget. Shared child pointers model what the
// aliases parse into.
func TestBUG204_LargeScalarUnderVisitLimitRefusedByBytes(t *testing.T) {
	scalar := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: strings.Repeat("a", 30)}
	const aliasCount = 59000 // under the 100,000-visit budget
	seq := &yaml.Node{Kind: yaml.SequenceNode}
	seq.Content = make([]*yaml.Node, aliasCount)
	for i := range seq.Content {
		seq.Content[i] = scalar
	}

	// The visit cost alone is under budget.
	visitOnly := newKeyIdentityBudget()
	m, cyclic := visitOnly.metric(seq, map[*yaml.Node]bool{})
	testrequire.False(t, cyclic)
	assert.LessOrEqual(t, m.cost, maxKeyIdentityVisits, "the construct stays under the visit budget")

	// The byte budget refuses it.
	budget := newKeyIdentityBudget()
	assert.NotPanics(t, func() { budget.account(seq) })
	assert.True(t, budget.exceeded)
	assert.Contains(t, budget.detail, "byte", "it must be the byte budget, not the visit budget, that refuses this")
}

// TestBUG204_LargeScalarDecodedOncePerDocument is the REV-258 CLAUDE-001
// regression: a large raw scalar that decodes to a tiny value (a ~1 MiB
// underflowing float, and a ~1 MiB underscore int) fanned out through aliases as
// a key stays under both the visit and byte budgets, because only the tiny
// rendered value is charged. Before the fix the identity builder re-decoded the
// raw scalar on every alias reference -- per-reference work bounded by neither
// budget. The shared value cache makes node.Decode run exactly once per scalar
// node per document; this asserts that deterministically (cache entry count),
// not by wall clock.
func TestBUG204_LargeScalarDecodedOncePerDocument(t *testing.T) {
	const megabyte = 1 << 20
	const fanOut = 30000 // under the 100,000-visit budget; tiny rendered value keeps it under the 1 MiB byte budget

	for _, tc := range []struct {
		name string
		doc  string
	}{
		{name: "underflowing float", doc: "v: &s 0." + strings.Repeat("0", megabyte) + "1\n"},
		{name: "underscore int", doc: "v: &s 1" + strings.Repeat("_", megabyte) + "1\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var root yaml.Node
			testrequire.NoError(t, yaml.Unmarshal([]byte(tc.doc), &root))
			mapping := documentMapping(&root)
			testrequire.NotNil(t, mapping)
			scalar := mapping.Content[1]
			testrequire.Equal(t, yaml.ScalarNode, scalar.Kind)
			testrequire.GreaterOrEqual(t, len(scalar.Value), megabyte, "the raw scalar is large")

			key := &yaml.Node{Kind: yaml.SequenceNode}
			key.Content = make([]*yaml.Node, fanOut)
			for i := range key.Content {
				key.Content[i] = &yaml.Node{Kind: yaml.AliasNode, Alias: scalar}
			}

			budget := newKeyIdentityBudget()
			budget.account(key)
			testrequire.False(t, budget.exceeded, "the crafted key stays under both budgets: only the tiny rendered value is charged")
			testrequire.Equal(t, 1, len(budget.valueCache), "metric decodes the shared scalar exactly once")

			// The identity builder shares the cache, so rendering the identity across
			// all fanOut references adds no further decode.
			_ = yamlNodeStructuralIdentity(key, budget.valueCache)
			assert.Equal(t, 1, len(budget.valueCache), "the identity builder reuses the single decode, not one per reference")
		})
	}
}
