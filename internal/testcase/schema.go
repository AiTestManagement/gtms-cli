package testcase

import (
	"bytes"
	"fmt"
	"sort"
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

	duplicateKey, duplicate := duplicateYAMLKey(mapping)
	var identityFailure *SchemaFailure
	if topLevelScalarKeyCount(mapping, "test_case_id") == 1 {
		identityFailure = decodeIdentity(fields["test_case_id"], &doc.result)
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

func duplicateYAMLKey(node *yaml.Node) (string, bool) {
	if node == nil {
		return "", false
	}
	switch node.Kind {
	case yaml.MappingNode:
		seen := make(map[string]struct{}, len(node.Content)/2)
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			identity := yamlNodeStructuralIdentity(key)
			if _, exists := seen[identity]; exists {
				return key.Value, true
			}
			seen[identity] = struct{}{}
			if duplicate, ok := duplicateYAMLKey(key); ok {
				return duplicate, true
			}
			if duplicate, ok := duplicateYAMLKey(node.Content[i+1]); ok {
				return duplicate, true
			}
		}
	case yaml.SequenceNode:
		for _, child := range node.Content {
			if duplicate, ok := duplicateYAMLKey(child); ok {
				return duplicate, true
			}
		}
	}
	return "", false
}

func yamlNodeStructuralIdentity(node *yaml.Node) string {
	var identity strings.Builder
	appendYAMLNodeStructuralIdentity(&identity, node, make(map[*yaml.Node]bool))
	return identity.String()
}

func appendYAMLNodeStructuralIdentity(identity *strings.Builder, node *yaml.Node, active map[*yaml.Node]bool) {
	if node == nil {
		identity.WriteString("nil;")
		return
	}
	if node.Kind == yaml.AliasNode && node.Alias != nil {
		appendYAMLNodeStructuralIdentity(identity, node.Alias, active)
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
		var resolved any
		if err := node.Decode(&resolved); err == nil {
			value = fmt.Sprintf("%T:%v", resolved, resolved)
		}
	}
	fmt.Fprintf(identity, "%d:%d:%s:%d:%s:", node.Kind, len(node.Tag), node.Tag, len(value), value)
	if node.Kind == yaml.MappingNode {
		pairs := make([]string, 0, len(node.Content)/2)
		for i := 0; i+1 < len(node.Content); i += 2 {
			var pair strings.Builder
			appendYAMLNodeStructuralIdentity(&pair, node.Content[i], active)
			appendYAMLNodeStructuralIdentity(&pair, node.Content[i+1], active)
			pairs = append(pairs, pair.String())
		}
		sort.Strings(pairs)
		for _, pair := range pairs {
			fmt.Fprintf(identity, "%d:%s", len(pair), pair)
		}
		return
	}
	for _, child := range node.Content {
		appendYAMLNodeStructuralIdentity(identity, child, active)
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
