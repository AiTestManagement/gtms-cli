package testcase

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/adrg/frontmatter"
	"gopkg.in/yaml.v3"
)

const testcaseSchemaV2 = "test-case/v2"

var testcaseYAMLFormat = frontmatter.NewFormat("---", "---", yaml.Unmarshal)

// Metadata is the normalized testcase document model. Scalar values retain the
// exact text authored in YAML, irrespective of the scalar's resolved YAML tag.
type Metadata struct {
	TestCaseID  string
	Title       string
	Requirement string
	Priority    string
	Created     string
	Tags        []string
	Updated     string
	Status      string
	TestType    string
}

// SchemaFailureKind identifies a strict testcase document failure.
type SchemaFailureKind int

const (
	SchemaFailureNoFrontmatterBlock SchemaFailureKind = iota + 1
	SchemaFailureMissingSchemaDiscriminator
	SchemaFailureMalformedSchemaDiscriminator
	SchemaFailureUnsupportedSchemaVersion
	SchemaFailureWrongConceptType
	SchemaFailureMissingTestCategory
	SchemaFailureDuplicateYAMLKey
	SchemaFailureWrongTypeRetainedField
	SchemaFailureMalformedYAML
)

// SchemaFailure is a typed strict-decoder failure. The structured fields are
// retained so ownership and create-validation callers can render their own
// established envelopes without parsing Error text.
type SchemaFailure struct {
	Kind         SchemaFailureKind
	Key          string
	Value        string
	DeclaredKind string
	FoundKind    string
	Detail       string
	// RetainedFields records every wrong-kind retained field in fixed decoder
	// order. Key, DeclaredKind, and FoundKind remain the first failure for
	// pinned diagnostics.
	RetainedFields []RetainedFieldFailure
}

// RetainedFieldFailure describes one retained field whose YAML node kind does
// not match ADR-025.
type RetainedFieldFailure struct {
	Key          string
	DeclaredKind string
	FoundKind    string
}

func (f *SchemaFailure) Error() string {
	if f == nil {
		return ""
	}
	key := sanitizeIdentityString(f.Key)
	value := sanitizeIdentityString(f.Value)
	declaredKind := sanitizeIdentityString(f.DeclaredKind)
	foundKind := sanitizeIdentityString(f.FoundKind)
	detail := sanitizeIdentityString(f.Detail)
	switch f.Kind {
	case SchemaFailureNoFrontmatterBlock:
		return "no frontmatter block: expected a leading YAML frontmatter block"
	case SchemaFailureMissingSchemaDiscriminator:
		return "missing schema discriminator: key 'gtms_schema' is absent"
	case SchemaFailureMalformedSchemaDiscriminator:
		return fmt.Sprintf("malformed or empty schema discriminator: key 'gtms_schema' has %s value", foundKind)
	case SchemaFailureUnsupportedSchemaVersion:
		return fmt.Sprintf("unsupported schema version: key 'gtms_schema' has value '%s'; expected 'test-case/v2'", value)
	case SchemaFailureWrongConceptType:
		switch foundKind {
		case "absent":
			return "wrong concept type: key 'type' has value absent; expected 'Test Case Spec'"
		case "sequence", "mapping":
			return fmt.Sprintf("wrong concept type: key 'type' has %s value; expected scalar 'Test Case Spec'", foundKind)
		default:
			return fmt.Sprintf("wrong concept type: key 'type' has value '%s'; expected 'Test Case Spec'", value)
		}
	case SchemaFailureMissingTestCategory:
		switch foundKind {
		case "absent":
			return "missing or empty test category: key 'test_type' has value absent; expected a non-empty scalar string"
		case "empty string":
			return "missing or empty test category: key 'test_type' has empty string value; expected a non-empty scalar string"
		case "whitespace-only":
			return "missing or empty test category: key 'test_type' has whitespace-only value; expected a non-empty scalar string"
		}
	case SchemaFailureDuplicateYAMLKey:
		return fmt.Sprintf("duplicate YAML key: key '%s' appears more than once", key)
	case SchemaFailureWrongTypeRetainedField:
		return fmt.Sprintf("wrong-type retained field: key '%s' is declared %s but has %s value", key, declaredKind, foundKind)
	case SchemaFailureMalformedYAML:
		return "malformed YAML: " + detail
	}
	return "invalid testcase schema"
}

// DecodeResult retains identity evidence even when strict schema validation
// fails. ParsedIdentity is authored text and is never trimmed or lowercased.
type DecodeResult struct {
	Metadata         Metadata
	ParsedIdentity   string
	DeclaresIdentity bool
	Failure          *SchemaFailure
}

type decodedCandidate struct {
	result        DecodeResult
	fields        map[string]*yaml.Node
	hasBlock      bool
	schemaPresent bool
}

// DecodeCandidate strictly decodes one already-read physical testcase
// candidate. It performs no discovery, ownership resolution, or filesystem I/O.
func DecodeCandidate(data []byte) DecodeResult {
	return decodeCandidate(data).result
}

func decodeCandidate(data []byte) decodedCandidate {
	doc := decodedCandidate{}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})

	if !hasOpeningDelimiter(data) {
		if hasPreambleBeforeDelimiter(data) {
			doc.result.Failure = malformedYAMLFailure("opening YAML frontmatter delimiter must be the first line")
		} else {
			doc.result.Failure = &SchemaFailure{Kind: SchemaFailureNoFrontmatterBlock}
		}
		return doc
	}
	doc.hasBlock = true
	if !hasClosingDelimiter(data) {
		doc.result.Failure = malformedYAMLFailure("frontmatter block is not terminated by a closing delimiter")
		return doc
	}

	var root yaml.Node
	if _, err := frontmatter.Parse(bytes.NewReader(data), &root, testcaseYAMLFormat); err != nil {
		doc.result.Failure = malformedYAMLFailure(err.Error())
		return doc
	}
	if root.IsZero() {
		doc.fields = map[string]*yaml.Node{}
		doc.result.Failure = &SchemaFailure{Kind: SchemaFailureMissingSchemaDiscriminator, Key: "gtms_schema", FoundKind: "absent"}
		return doc
	}
	mapping := documentMapping(&root)
	if mapping == nil {
		doc.result.Failure = malformedYAMLFailure("frontmatter must be a mapping")
		return doc
	}
	fields, failure := mappingFields(mapping)
	doc.fields = fields
	_, doc.schemaPresent = fields["gtms_schema"]

	budget := newKeyIdentityBudget()
	duplicateKey, duplicate := duplicateYAMLKey(mapping, budget)
	var identityFailure *SchemaFailure
	if topLevelScalarKeyCount(mapping, "test_case_id") == 1 {
		identityFailure = decodeIdentity(fields["test_case_id"], &doc.result)
	}
	// BUG-204: an over-budget, over-depth, or cyclic key has no finite identity.
	// Fail as malformed YAML (empty Key) so the malformed-frontmatter repair
	// guidance and the create "could not parse frontmatter" envelope apply, while
	// ParsedIdentity (decoded just above) still attributes the spec.
	if budget.exceeded {
		doc.result.Failure = malformedYAMLFailure(budget.detail)
		return doc
	}
	if duplicate {
		doc.result.Failure = &SchemaFailure{Kind: SchemaFailureDuplicateYAMLKey, Key: duplicateKey}
		return doc
	}
	if failure != nil {
		doc.result.Failure = failure
		return doc
	}
	if failure := validateV2RequiredFields(fields, &doc.result.Metadata); failure != nil {
		doc.result.Failure = failure
		return doc
	}
	if identityFailure != nil {
		doc.result.Failure = identityFailure
		return doc
	}
	if failure := decodeRetainedFields(fields, &doc.result.Metadata); failure != nil {
		doc.result.Failure = failure
	}
	return doc
}

func topLevelScalarKeyCount(mapping *yaml.Node, name string) int {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return 0
	}
	count := 0
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key := mapping.Content[i]
		if key.Kind == yaml.ScalarNode && !isNullNode(key) && key.Value == name {
			count++
		}
	}
	return count
}

func hasOpeningDelimiter(data []byte) bool {
	line, _, _ := bytes.Cut(data, []byte("\n"))
	return isSchemaDelimiter(line)
}

func hasClosingDelimiter(data []byte) bool {
	lines := bytes.Split(data, []byte("\n"))
	for _, line := range lines[1:] {
		if isSchemaDelimiter(line) {
			return true
		}
	}
	return false
}

func hasPreambleBeforeDelimiter(data []byte) bool {
	if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		return true
	}
	lines := bytes.Split(data, []byte("\n"))
	for opening := 1; opening < len(lines); opening++ {
		if !isSchemaDelimiter(lines[opening]) {
			continue
		}
		for closing := opening + 1; closing < len(lines); closing++ {
			if isSchemaDelimiter(lines[closing]) {
				return true
			}
		}
	}
	return false
}

func isSchemaDelimiter(line []byte) bool {
	line = bytes.TrimSuffix(line, []byte("\r"))
	line = bytes.TrimRight(line, " \t")
	return bytes.Equal(line, []byte("---"))
}

func documentMapping(root *yaml.Node) *yaml.Node {
	if root.Kind != yaml.DocumentNode || len(root.Content) != 1 {
		return nil
	}
	node := root.Content[0]
	if node.Kind != yaml.MappingNode {
		return nil
	}
	return node
}

// BUG-204: mapping-key structural identity is computed under a fixed,
// document-wide, memoised, saturating budget. A crafted key that expands
// exponentially through aliases, nests past the depth limit, forms a cyclic
// alias, or renders too many identity bytes is refused as malformed YAML instead
// of expanding without limit. The budget counts logical expanded node visits --
// what a naive render would traverse through every alias path -- not unique
// parsed nodes, so a small parsed alias graph that expands exponentially is still
// caught.
//
// Owner Decision 8 (2026-11-05) adds a document-wide byte budget alongside the
// visit and depth limits: the visit count alone does not bound rendered size,
// because a single large anchored scalar aliased many times stays under the visit
// budget while rendering a multi-gigabyte identity string. byteCost mirrors
// appendYAMLNodeStructuralIdentity's output length exactly (header framing,
// mapping pair framing, decoded scalar values, nil/cycle markers) and is computed
// before any identity string is constructed, so the oversized identity is never
// allocated.
const (
	maxKeyIdentityVisits int64 = 100000
	maxKeyDepth          int   = 64
	// maxKeyIdentityBytes is the document-wide limit on logical rendered identity
	// bytes (1 MiB). Exactly this many is accepted; one byte over is malformed.
	maxKeyIdentityBytes int64 = 1048576
	// costOverBudget / bytesOverBudget are the saturation sentinels, one past each
	// budget, so neither accumulation can overflow int64.
	costOverBudget  int64 = maxKeyIdentityVisits + 1
	bytesOverBudget int64 = maxKeyIdentityBytes + 1
)

// nodeMetric is the memoised logical cost, relative depth, and rendered identity
// byte length of one acyclic node. depth counts the node as level 1; bytes
// mirrors the length appendYAMLNodeStructuralIdentity would write for the node.
type nodeMetric struct {
	cost  int64
	depth int
	bytes int64
}

// keyIdentityBudget accumulates the logical expansion cost and rendered identity
// byte length of every mapping key in one document. metric results are memoised
// per acyclic node so the whole document stays linear in parsed nodes while the
// summed totals reflect the naive expansion through every alias path.
type keyIdentityBudget struct {
	total     int64
	byteTotal int64
	memo      map[*yaml.Node]nodeMetric
	// valueCache holds each scalar node's rendered identity value, keyed by node
	// and shared across every key in the document. It makes node.Decode run at
	// most once per scalar per document (REV-258 CLAUDE-001): a large raw scalar
	// that decodes to a tiny value, aliased many times, would otherwise be
	// re-decoded on every reference by the identity builder -- per-reference work
	// that neither the visit nor the byte budget bounds. metric and the identity
	// builder share this cache so they can never disagree on a rendered value.
	valueCache map[*yaml.Node]string
	exceeded   bool
	detail     string
}

func newKeyIdentityBudget() *keyIdentityBudget {
	return &keyIdentityBudget{
		memo:       make(map[*yaml.Node]nodeMetric),
		valueCache: make(map[*yaml.Node]string),
	}
}

// renderedScalarValue returns the identity value string for a scalar node -- the
// node's own Value, or the "%T:%v" of its decoded YAML when decoding succeeds --
// caching the result in cache so node.Decode runs at most once per node. It is
// called only for scalar nodes and produces exactly the value the identity
// builder wrote inline before REV-258, so builder output is byte-for-byte
// unchanged; only the decode count drops.
func renderedScalarValue(cache map[*yaml.Node]string, node *yaml.Node) string {
	if v, ok := cache[node]; ok {
		return v
	}
	value := node.Value
	var resolved any
	if err := node.Decode(&resolved); err == nil {
		value = fmt.Sprintf("%T:%v", resolved, resolved)
	}
	cache[node] = value
	return value
}

// satAdd adds two visit costs, saturating at costOverBudget. Inputs are always
// below costOverBudget here, so the sum cannot overflow int64.
func satAdd(a, b int64) int64 {
	if a >= costOverBudget || b >= costOverBudget {
		return costOverBudget
	}
	if sum := a + b; sum < costOverBudget {
		return sum
	}
	return costOverBudget
}

// satAddBytes adds two byte lengths, saturating at bytesOverBudget. Inputs are
// always below bytesOverBudget, so the sum cannot overflow int64. Exactness only
// matters below the budget; once saturated the key is already refused.
func satAddBytes(a, b int64) int64 {
	if a >= bytesOverBudget || b >= bytesOverBudget {
		return bytesOverBudget
	}
	if sum := a + b; sum < bytesOverBudget {
		return sum
	}
	return bytesOverBudget
}

func capDepth(d int) int {
	if d > maxKeyDepth+1 {
		return maxKeyDepth + 1
	}
	return d
}

// decDigits returns the number of decimal bytes fmt's %d would write for n (n is
// always a non-negative length or kind here).
func decDigits(n int) int64 { return int64(len(strconv.Itoa(n))) }

// decDigits64 is decDigits for an int64 length.
func decDigits64(n int64) int64 { return int64(len(strconv.FormatInt(n, 10))) }

// headerBytes mirrors the length of appendYAMLNodeStructuralIdentity's
// "%d:%d:%s:%d:%s:" header for a node whose rendered value is value: the kind,
// the tag length, the tag, the value length, and the value, each followed by a
// colon.
func headerBytes(node *yaml.Node, value string) int64 {
	return decDigits(int(node.Kind)) + 1 +
		decDigits(len(node.Tag)) + 1 + int64(len(node.Tag)) + 1 +
		decDigits(len(value)) + 1 + int64(len(value)) + 1
}

// metric returns the memoised logical cost, relative depth, and rendered identity
// byte length of node, and whether a cyclic alias was reached. active holds the
// real (non-alias) nodes currently on the expansion stack; an alias that resolves
// to an active node is cyclic. Memoised nodes are necessarily acyclic (a cycle is
// detected before the node is memoised), so reusing a memoised metric never hides
// a cycle. The byte length mirrors appendYAMLNodeStructuralIdentity exactly, so
// the budget can reject before the identity string is built.
func (b *keyIdentityBudget) metric(node *yaml.Node, active map[*yaml.Node]bool) (nodeMetric, bool) {
	if node == nil {
		return nodeMetric{cost: 1, depth: 1, bytes: int64(len("nil;"))}, false
	}
	if node.Kind == yaml.AliasNode && node.Alias != nil {
		return b.metric(node.Alias, active)
	}
	if active[node] {
		return nodeMetric{}, true
	}
	if m, ok := b.memo[node]; ok {
		return m, false
	}

	// Mirror the rendered value: a scalar whose YAML decodes is rendered as
	// "%T:%v" of the decoded value; otherwise the node's own Value is used. This
	// is computed once per node (memoised), never per alias reference, so a large
	// scalar reached through many aliases is measured a single time.
	value := node.Value
	if node.Kind == yaml.ScalarNode {
		value = renderedScalarValue(b.valueCache, node)
	}
	m := nodeMetric{cost: 1, depth: 1, bytes: headerBytes(node, value)}

	switch node.Kind {
	case yaml.MappingNode:
		active[node] = true
		maxChild := 0
		for i := 0; i+1 < len(node.Content); i += 2 {
			km, cyclic := b.metric(node.Content[i], active)
			if cyclic {
				delete(active, node)
				return nodeMetric{}, true
			}
			vm, cyclic := b.metric(node.Content[i+1], active)
			if cyclic {
				delete(active, node)
				return nodeMetric{}, true
			}
			m.cost = satAdd(m.cost, satAdd(km.cost, vm.cost))
			if km.depth > maxChild {
				maxChild = km.depth
			}
			if vm.depth > maxChild {
				maxChild = vm.depth
			}
			// Each pair is framed as "%d:%s": the pair's byte length, a colon, then
			// the pair bytes. The builder sorts pairs, which does not change the
			// total length.
			pairBytes := satAddBytes(km.bytes, vm.bytes)
			m.bytes = satAddBytes(m.bytes, satAddBytes(decDigits64(pairBytes)+1, pairBytes))
		}
		delete(active, node)
		m.depth = capDepth(1 + maxChild)
	case yaml.SequenceNode:
		active[node] = true
		maxChild := 0
		for _, child := range node.Content {
			cm, cyclic := b.metric(child, active)
			if cyclic {
				delete(active, node)
				return nodeMetric{}, true
			}
			m.cost = satAdd(m.cost, cm.cost)
			if cm.depth > maxChild {
				maxChild = cm.depth
			}
			m.bytes = satAddBytes(m.bytes, cm.bytes)
		}
		delete(active, node)
		m.depth = capDepth(1 + maxChild)
	}
	b.memo[node] = m
	return m, false
}

// account charges one mapping key's identity cost against the document budget,
// setting exceeded when the key is cyclic, nests too deep, pushes the
// document-wide visit total past the visit budget, or pushes the document-wide
// rendered-identity byte total past the byte budget.
func (b *keyIdentityBudget) account(key *yaml.Node) {
	if b.exceeded {
		return
	}
	m, cyclic := b.metric(key, make(map[*yaml.Node]bool))
	if cyclic {
		b.fail("a mapping key contains a cyclic alias reference")
		return
	}
	if m.depth > maxKeyDepth {
		b.fail(fmt.Sprintf("a mapping key nests deeper than the %d-level limit", maxKeyDepth))
		return
	}
	b.total = satAdd(b.total, m.cost)
	if b.total > maxKeyIdentityVisits {
		b.fail(fmt.Sprintf("mapping key identities exceed the %d-node expansion budget", maxKeyIdentityVisits))
		return
	}
	b.byteTotal = satAddBytes(b.byteTotal, m.bytes)
	if b.byteTotal > maxKeyIdentityBytes {
		b.fail(fmt.Sprintf("mapping key identities exceed the %d-byte rendered-identity budget", maxKeyIdentityBytes))
	}
}

func (b *keyIdentityBudget) fail(detail string) {
	b.exceeded = true
	b.detail = detail
}

func duplicateYAMLKey(node *yaml.Node, budget *keyIdentityBudget) (string, bool) {
	if node == nil {
		return "", false
	}
	switch node.Kind {
	case yaml.MappingNode:
		seen := make(map[string]struct{}, len(node.Content)/2)
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			budget.account(key)
			if budget.exceeded {
				return "", false
			}
			identity := yamlNodeStructuralIdentity(key, budget.valueCache)
			if _, exists := seen[identity]; exists {
				return key.Value, true
			}
			seen[identity] = struct{}{}
			if duplicate, ok := duplicateYAMLKey(key, budget); ok {
				return duplicate, true
			}
			if budget.exceeded {
				return "", false
			}
			if duplicate, ok := duplicateYAMLKey(node.Content[i+1], budget); ok {
				return duplicate, true
			}
			if budget.exceeded {
				return "", false
			}
		}
	case yaml.SequenceNode:
		for _, child := range node.Content {
			if duplicate, ok := duplicateYAMLKey(child, budget); ok {
				return duplicate, true
			}
			if budget.exceeded {
				return "", false
			}
		}
	}
	return "", false
}

// yamlNodeStructuralIdentity renders a key's structural identity string. The
// valueCache is shared with the budget's metric so each scalar node is decoded at
// most once per document (REV-258 CLAUDE-001); the rendered output is unchanged.
func yamlNodeStructuralIdentity(node *yaml.Node, valueCache map[*yaml.Node]string) string {
	var identity strings.Builder
	appendYAMLNodeStructuralIdentity(&identity, node, make(map[*yaml.Node]bool), valueCache)
	return identity.String()
}

func appendYAMLNodeStructuralIdentity(identity *strings.Builder, node *yaml.Node, active map[*yaml.Node]bool, valueCache map[*yaml.Node]string) {
	if node == nil {
		identity.WriteString("nil;")
		return
	}
	if node.Kind == yaml.AliasNode && node.Alias != nil {
		appendYAMLNodeStructuralIdentity(identity, node.Alias, active, valueCache)
		return
	}
	if active[node] {
		identity.WriteString("cycle;")
		return
	}
	active[node] = true
	defer delete(active, node)

	value := node.Value
	if node.Kind == yaml.ScalarNode {
		value = renderedScalarValue(valueCache, node)
	}
	fmt.Fprintf(identity, "%d:%d:%s:%d:%s:", node.Kind, len(node.Tag), node.Tag, len(value), value)
	if node.Kind == yaml.MappingNode {
		pairs := make([]string, 0, len(node.Content)/2)
		for i := 0; i+1 < len(node.Content); i += 2 {
			var pair strings.Builder
			appendYAMLNodeStructuralIdentity(&pair, node.Content[i], active, valueCache)
			appendYAMLNodeStructuralIdentity(&pair, node.Content[i+1], active, valueCache)
			pairs = append(pairs, pair.String())
		}
		sort.Strings(pairs)
		for _, pair := range pairs {
			fmt.Fprintf(identity, "%d:%s", len(pair), pair)
		}
		return
	}
	for _, child := range node.Content {
		appendYAMLNodeStructuralIdentity(identity, child, active, valueCache)
	}
}

func mappingFields(mapping *yaml.Node) (map[string]*yaml.Node, *SchemaFailure) {
	fields := make(map[string]*yaml.Node, len(mapping.Content)/2)
	var failure *SchemaFailure
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key := mapping.Content[i]
		if key.Kind != yaml.ScalarNode || isNullNode(key) {
			if failure == nil {
				failure = malformedYAMLFailure("frontmatter mapping keys must be scalars")
			}
			continue
		}
		fields[key.Value] = mapping.Content[i+1]
	}
	return fields, failure
}

func decodeIdentity(node *yaml.Node, result *DecodeResult) *SchemaFailure {
	if node == nil {
		return nil
	}
	result.DeclaresIdentity = true
	if isNullNode(node) {
		return nil
	}
	if node.Kind != yaml.ScalarNode {
		return malformedYAMLFailure(fmt.Sprintf("key 'test_case_id' has %s value; expected a scalar string", yamlKind(node)))
	}
	result.ParsedIdentity = node.Value
	result.Metadata.TestCaseID = node.Value
	return nil
}

func validateV2RequiredFields(fields map[string]*yaml.Node, metadata *Metadata) *SchemaFailure {
	schema, ok := fields["gtms_schema"]
	if !ok {
		return &SchemaFailure{Kind: SchemaFailureMissingSchemaDiscriminator, Key: "gtms_schema", FoundKind: "absent"}
	}
	if isNullNode(schema) {
		return &SchemaFailure{Kind: SchemaFailureMalformedSchemaDiscriminator, Key: "gtms_schema", FoundKind: "null"}
	}
	if schema.Kind != yaml.ScalarNode {
		return &SchemaFailure{Kind: SchemaFailureMalformedSchemaDiscriminator, Key: "gtms_schema", FoundKind: yamlKind(schema)}
	}
	if schema.Value == "" {
		return &SchemaFailure{Kind: SchemaFailureMalformedSchemaDiscriminator, Key: "gtms_schema", FoundKind: "empty string"}
	}
	if schema.Value != testcaseSchemaV2 {
		return &SchemaFailure{Kind: SchemaFailureUnsupportedSchemaVersion, Key: "gtms_schema", Value: schema.Value}
	}

	concept, ok := fields["type"]
	if !ok || isNullNode(concept) {
		return &SchemaFailure{Kind: SchemaFailureWrongConceptType, Key: "type", FoundKind: "absent"}
	}
	if concept.Kind != yaml.ScalarNode {
		return &SchemaFailure{Kind: SchemaFailureWrongConceptType, Key: "type", FoundKind: yamlKind(concept)}
	}
	if concept.Value != "Test Case Spec" {
		return &SchemaFailure{Kind: SchemaFailureWrongConceptType, Key: "type", Value: concept.Value, FoundKind: "scalar"}
	}

	testType, ok := fields["test_type"]
	if !ok || isNullNode(testType) {
		return &SchemaFailure{Kind: SchemaFailureMissingTestCategory, Key: "test_type", FoundKind: "absent"}
	}
	if testType.Kind != yaml.ScalarNode {
		foundKind := yamlKind(testType)
		return &SchemaFailure{
			Kind:      SchemaFailureMalformedYAML,
			Key:       "test_type",
			FoundKind: foundKind,
			Detail:    fmt.Sprintf("key 'test_type' has %s value; expected a non-empty scalar string", foundKind),
		}
	}
	if testType.Value == "" {
		return &SchemaFailure{Kind: SchemaFailureMissingTestCategory, Key: "test_type", FoundKind: "empty string"}
	}
	if strings.TrimSpace(testType.Value) == "" {
		return &SchemaFailure{Kind: SchemaFailureMissingTestCategory, Key: "test_type", FoundKind: "whitespace-only"}
	}
	metadata.TestType = testType.Value
	return nil
}

func decodeRetainedFields(fields map[string]*yaml.Node, metadata *Metadata) *SchemaFailure {
	var firstFailure *SchemaFailure
	scalars := []struct {
		key string
		dst *string
	}{
		{key: "title", dst: &metadata.Title},
		{key: "requirement", dst: &metadata.Requirement},
		{key: "priority", dst: &metadata.Priority},
		{key: "created", dst: &metadata.Created},
		{key: "updated", dst: &metadata.Updated},
		{key: "status", dst: &metadata.Status},
	}
	for _, field := range scalars {
		node, ok := fields[field.key]
		if !ok || isNullNode(node) {
			continue
		}
		if node.Kind != yaml.ScalarNode {
			recordRetainedKindFailure(&firstFailure, field.key, "scalar", yamlKind(node))
			continue
		}
		*field.dst = node.Value
	}

	if tags, ok := fields["tags"]; ok && !isNullNode(tags) {
		if tags.Kind != yaml.SequenceNode {
			found := yamlKind(tags)
			if tags.Kind == yaml.ScalarNode && tags.Value == "" {
				found = "empty scalar"
			}
			recordRetainedKindFailure(&firstFailure, "tags", "sequence of non-null scalars", found)
		} else {
			metadata.Tags = make([]string, 0, len(tags.Content))
			for _, item := range tags.Content {
				if isNullNode(item) {
					recordRetainedKindFailure(&firstFailure, "tags", "sequence of non-null scalars", "null element")
					metadata.Tags = append(metadata.Tags, "")
					continue
				}
				if item.Kind != yaml.ScalarNode {
					recordRetainedKindFailure(&firstFailure, "tags", "sequence of non-null scalars", yamlKind(item))
					metadata.Tags = nil
					break
				}
				metadata.Tags = append(metadata.Tags, item.Value)
			}
		}
	}

	if meta, ok := fields["meta"]; ok && !isNullNode(meta) {
		// ADR-025 constrains only the node kind. The mapping contents are opaque,
		// so do not decode them into a Go map that would reject complex YAML keys.
		if meta.Kind != yaml.MappingNode {
			found := yamlKind(meta)
			if meta.Kind == yaml.ScalarNode && meta.Value == "" {
				found = "empty scalar"
			}
			recordRetainedKindFailure(&firstFailure, "meta", "mapping", found)
		}
	}
	return firstFailure
}

func isNullNode(node *yaml.Node) bool {
	return node != nil && node.Kind == yaml.ScalarNode && node.Tag == "!!null"
}

func yamlKind(node *yaml.Node) string {
	if node == nil {
		return "absent"
	}
	switch node.Kind {
	case yaml.ScalarNode:
		return "scalar"
	case yaml.SequenceNode:
		return "sequence"
	case yaml.MappingNode:
		return "mapping"
	default:
		return "scalar"
	}
}

func malformedYAMLFailure(detail string) *SchemaFailure {
	return &SchemaFailure{Kind: SchemaFailureMalformedYAML, Detail: detail}
}

func retainedKindFailure(key, declared, found string) *SchemaFailure {
	return &SchemaFailure{
		Kind:         SchemaFailureWrongTypeRetainedField,
		Key:          key,
		DeclaredKind: declared,
		FoundKind:    found,
		RetainedFields: []RetainedFieldFailure{{
			Key:          key,
			DeclaredKind: declared,
			FoundKind:    found,
		}},
	}
}

func recordRetainedKindFailure(first **SchemaFailure, key, declared, found string) {
	if *first == nil {
		*first = retainedKindFailure(key, declared, found)
		return
	}
	(*first).RetainedFields = append((*first).RetainedFields, RetainedFieldFailure{
		Key:          key,
		DeclaredKind: declared,
		FoundKind:    found,
	})
}
