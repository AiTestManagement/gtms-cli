package testcase

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	testrequire "github.com/stretchr/testify/require"
)

func strictV2Document(extra string) []byte {
	return []byte("---\n" +
		"gtms_schema: test-case/v2\n" +
		"type: Test Case Spec\n" +
		"test_type: Functional\n" +
		"test_case_id: tc-abcdef12\n" + extra +
		"---\n\nBody\n")
}

func TestDecodeCandidate_ExactV2(t *testing.T) {
	result := DecodeCandidate(strictV2Document("" +
		"title: Exact case\n" +
		"requirement: ENH-219\n" +
		"priority: Medium\n" +
		"created: 2026-09-26\n" +
		"tags: [7, true, 2026-09-26]\n" +
		"updated: 2026-09-27\n" +
		"status: active\n" +
		"meta: {source: generated}\n" +
		"x_vendor_note: tolerated\n"))

	testrequire.Nil(t, result.Failure)
	assert.True(t, result.DeclaresIdentity)
	assert.Equal(t, "tc-abcdef12", result.ParsedIdentity)
	assert.Equal(t, Metadata{
		TestCaseID:  "tc-abcdef12",
		Title:       "Exact case",
		Requirement: "ENH-219",
		Priority:    "Medium",
		Created:     "2026-09-26",
		Tags:        []string{"7", "true", "2026-09-26"},
		Updated:     "2026-09-27",
		Status:      "active",
		TestType:    "Functional",
	}, result.Metadata)
}

func TestDecodeCandidate_MetaAcceptsAnyYAMLMapping(t *testing.T) {
	result := DecodeCandidate(strictV2Document("meta:\n  ? [sequence, key]\n  : accepted\n"))
	testrequire.Nil(t, result.Failure)
}

func TestDecodeCandidate_DuplicateKeysUseYAMLIdentity(t *testing.T) {
	result := DecodeCandidate(strictV2Document("meta:\n  1: numeric\n  \"1\": string\n"))
	testrequire.Nil(t, result.Failure)

	result = DecodeCandidate(strictV2Document("meta:\n  a: first\n  a: second\n"))
	testrequire.NotNil(t, result.Failure)
	assert.Equal(t, SchemaFailureDuplicateYAMLKey, result.Failure.Kind)
	assert.Equal(t, "a", result.Failure.Key)

	for _, duplicate := range []string{
		"meta:\n  ? [sequence, key]\n  : first\n  ? [sequence, key]\n  : second\n",
		"meta:\n  ? {a: b, c: d}\n  : first\n  ? {c: d, a: b}\n  : second\n",
	} {
		result = DecodeCandidate(strictV2Document(duplicate))
		testrequire.NotNil(t, result.Failure)
		assert.Equal(t, SchemaFailureDuplicateYAMLKey, result.Failure.Kind)
	}

	for _, distinct := range []string{
		"meta:\n  ? [sequence, key]\n  : first\n  ? [sequence, other]\n  : second\n",
		"meta:\n  ? {a: b}\n  : first\n  ? {a: c}\n  : second\n",
	} {
		result = DecodeCandidate(strictV2Document(distinct))
		testrequire.Nil(t, result.Failure)
	}
}

func TestDecodeCandidate_DuplicateKeysCanonicalizeResolvedScalarsAndAliases(t *testing.T) {
	for _, test := range []struct {
		name string
		doc  string
	}{
		{
			name: "equivalent integer spellings",
			doc:  "meta:\n  1: first\n  01: second\n",
		},
		{
			name: "equivalent boolean spellings",
			doc:  "meta:\n  true: first\n  TRUE: second\n",
		},
		{
			name: "alias of existing key",
			doc:  "meta:\n  ? &key anchored\n  : first\n  ? *key\n  : second\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := DecodeCandidate(strictV2Document(test.doc))
			testrequire.NotNil(t, result.Failure)
			assert.Equal(t, SchemaFailureDuplicateYAMLKey, result.Failure.Kind)
		})
	}
}

func TestDecodeCandidate_SchemaFailureMatrix(t *testing.T) {
	tests := []struct {
		name      string
		document  string
		kind      SchemaFailureKind
		errorText string
		key       string
	}{
		{
			name:      "no frontmatter",
			document:  "plain markdown\n",
			kind:      SchemaFailureNoFrontmatterBlock,
			errorText: "no frontmatter block: expected a leading YAML frontmatter block",
		},
		{
			name:      "missing discriminator",
			document:  "---\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-abcdef12\n---\n",
			kind:      SchemaFailureMissingSchemaDiscriminator,
			errorText: "missing schema discriminator: key 'gtms_schema' is absent",
		},
		{
			name:      "empty frontmatter",
			document:  "---\n---\n",
			kind:      SchemaFailureMissingSchemaDiscriminator,
			errorText: "missing schema discriminator: key 'gtms_schema' is absent",
		},
		{
			name:      "null discriminator",
			document:  "---\ngtms_schema:\ntype: Test Case Spec\ntest_type: Functional\n---\n",
			kind:      SchemaFailureMalformedSchemaDiscriminator,
			errorText: "malformed or empty schema discriminator: key 'gtms_schema' has null value",
		},
		{
			name:      "empty discriminator",
			document:  "---\ngtms_schema: \"\"\ntype: Test Case Spec\ntest_type: Functional\n---\n",
			kind:      SchemaFailureMalformedSchemaDiscriminator,
			errorText: "malformed or empty schema discriminator: key 'gtms_schema' has empty string value",
		},
		{
			name:      "sequence discriminator",
			document:  "---\ngtms_schema: [test-case/v2]\ntype: Test Case Spec\ntest_type: Functional\n---\n",
			kind:      SchemaFailureMalformedSchemaDiscriminator,
			errorText: "malformed or empty schema discriminator: key 'gtms_schema' has sequence value",
		},
		{
			name:      "mapping discriminator",
			document:  "---\ngtms_schema: {version: test-case/v2}\ntype: Test Case Spec\ntest_type: Functional\n---\n",
			kind:      SchemaFailureMalformedSchemaDiscriminator,
			errorText: "malformed or empty schema discriminator: key 'gtms_schema' has mapping value",
		},
		{
			name:      "future discriminator",
			document:  "---\ngtms_schema: test-case/v999\ntype: Test Case Spec\ntest_type: Functional\n---\n",
			kind:      SchemaFailureUnsupportedSchemaVersion,
			errorText: "unsupported schema version: key 'gtms_schema' has value 'test-case/v999'; expected 'test-case/v2'",
		},
		{
			name:      "absent concept",
			document:  "---\ngtms_schema: test-case/v2\ntest_type: Functional\n---\n",
			kind:      SchemaFailureWrongConceptType,
			errorText: "wrong concept type: key 'type' has value absent; expected 'Test Case Spec'",
		},
		{
			name:      "null concept",
			document:  "---\ngtms_schema: test-case/v2\ntype:\ntest_type: Functional\n---\n",
			kind:      SchemaFailureWrongConceptType,
			errorText: "wrong concept type: key 'type' has value absent; expected 'Test Case Spec'",
		},
		{
			name:      "sequence concept",
			document:  "---\ngtms_schema: test-case/v2\ntype: [Test Case Spec]\ntest_type: Functional\n---\n",
			kind:      SchemaFailureWrongConceptType,
			errorText: "wrong concept type: key 'type' has sequence value; expected scalar 'Test Case Spec'",
		},
		{
			name:      "mapping concept",
			document:  "---\ngtms_schema: test-case/v2\ntype: {kind: Test Case Spec}\ntest_type: Functional\n---\n",
			kind:      SchemaFailureWrongConceptType,
			errorText: "wrong concept type: key 'type' has mapping value; expected scalar 'Test Case Spec'",
		},
		{
			name:      "hybrid concept",
			document:  "---\ngtms_schema: test-case/v2\ntype: Functional\ntest_type: Functional\n---\n",
			kind:      SchemaFailureWrongConceptType,
			errorText: "wrong concept type: key 'type' has value 'Functional'; expected 'Test Case Spec'",
		},
		{
			name:      "absent category",
			document:  "---\ngtms_schema: test-case/v2\ntype: Test Case Spec\n---\n",
			kind:      SchemaFailureMissingTestCategory,
			errorText: "missing or empty test category: key 'test_type' has value absent; expected a non-empty scalar string",
		},
		{
			name:      "empty category",
			document:  "---\ngtms_schema: test-case/v2\ntype: Test Case Spec\ntest_type: \"\"\n---\n",
			kind:      SchemaFailureMissingTestCategory,
			errorText: "missing or empty test category: key 'test_type' has empty string value; expected a non-empty scalar string",
		},
		{
			name:      "null category",
			document:  "---\ngtms_schema: test-case/v2\ntype: Test Case Spec\ntest_type:\n---\n",
			kind:      SchemaFailureMissingTestCategory,
			errorText: "missing or empty test category: key 'test_type' has value absent; expected a non-empty scalar string",
		},
		{
			name:      "whitespace category",
			document:  "---\ngtms_schema: test-case/v2\ntype: Test Case Spec\ntest_type: \"  \"\n---\n",
			kind:      SchemaFailureMissingTestCategory,
			errorText: "missing or empty test category: key 'test_type' has whitespace-only value; expected a non-empty scalar string",
		},
		{
			name:      "sequence category",
			document:  "---\ngtms_schema: test-case/v2\ntype: Test Case Spec\ntest_type: [Functional]\n---\n",
			kind:      SchemaFailureMalformedYAML,
			errorText: "malformed YAML: key 'test_type' has sequence value; expected a non-empty scalar string",
		},
		{
			name:      "mapping category",
			document:  "---\ngtms_schema: test-case/v2\ntype: Test Case Spec\ntest_type: {category: Functional}\n---\n",
			kind:      SchemaFailureMalformedYAML,
			errorText: "malformed YAML: key 'test_type' has mapping value; expected a non-empty scalar string",
		},
		{
			name:     "malformed yaml",
			document: "---\ngtms_schema: [\n---\n",
			kind:     SchemaFailureMalformedYAML,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DecodeCandidate([]byte(tt.document))
			testrequire.NotNil(t, result.Failure)
			assert.Equal(t, tt.kind, result.Failure.Kind)
			if tt.errorText != "" {
				assert.Equal(t, tt.errorText, result.Failure.Error())
			}
			if tt.name == "sequence category" {
				assert.Equal(t, "test_type", result.Failure.Key)
				assert.Equal(t, "sequence", result.Failure.FoundKind)
			}
		})
	}

	for _, key := range []string{"gtms_schema", "type", "test_type", "test_case_id"} {
		t.Run("duplicate "+key, func(t *testing.T) {
			doc := "---\n" +
				"gtms_schema: test-case/v2\n" +
				"type: Test Case Spec\n" +
				"test_type: Functional\n" +
				"test_case_id: tc-abcdef12\n" +
				fmt.Sprintf("%s: duplicate\n", key) +
				"---\n"
			result := DecodeCandidate([]byte(doc))
			testrequire.NotNil(t, result.Failure)
			assert.Equal(t, SchemaFailureDuplicateYAMLKey, result.Failure.Kind)
			assert.Equal(t, key, result.Failure.Key)
			assert.Equal(t, fmt.Sprintf("duplicate YAML key: key '%s' appears more than once", key), result.Failure.Error())
		})
	}
}

func TestDecodeCandidate_SanitizesAuthoredDuplicateKeyDiagnostics(t *testing.T) {
	doc := strictV2Document("\"bad\\x1b\\nkey\": first\n\"bad\\x1b\\nkey\": second\n")
	result := DecodeCandidate(doc)
	testrequire.NotNil(t, result.Failure)
	assert.Equal(t, SchemaFailureDuplicateYAMLKey, result.Failure.Kind)
	assert.Equal(t, "bad\x1b\nkey", result.Failure.Key)

	reason := result.Failure.Error()
	repair := schemaRepairSentence(result.Failure, "gtms/test/cases/tc-abcdef12.md")
	assert.Equal(t, "duplicate YAML key: key 'bad??key' appears more than once", reason)
	assert.Contains(t, repair, "remove duplicate YAML key 'bad??key'")
	assertNoControlBytes(t, reason)
	assertNoControlBytes(t, repair)
}

func TestDecodeCandidate_DuplicateKeyRetainsUnambiguousIdentity(t *testing.T) {
	result := DecodeCandidate(strictV2Document("title: first\ntitle: second\n"))
	testrequire.NotNil(t, result.Failure)
	assert.Equal(t, SchemaFailureDuplicateYAMLKey, result.Failure.Kind)
	assert.Equal(t, "tc-abcdef12", result.ParsedIdentity)
	assert.True(t, result.DeclaresIdentity)

	result = DecodeCandidate(strictV2Document("test_case_id: tc-fedcba98\n"))
	testrequire.NotNil(t, result.Failure)
	assert.Equal(t, SchemaFailureDuplicateYAMLKey, result.Failure.Kind)
	assert.Empty(t, result.ParsedIdentity)
	assert.False(t, result.DeclaresIdentity)

	result = DecodeCandidate([]byte("---\n" +
		"title: first\n" +
		"title: second\n" +
		"test_case_id: tc-abcdef12\n" +
		"test_case_id: tc-fedcba98\n" +
		"---\n"))
	testrequire.NotNil(t, result.Failure)
	assert.Equal(t, SchemaFailureDuplicateYAMLKey, result.Failure.Kind)
	assert.Equal(t, "title", result.Failure.Key)
	assert.Empty(t, result.ParsedIdentity)
	assert.False(t, result.DeclaresIdentity)
}

func TestSchemaFailureError_SanitizesEveryStructuredDiagnosticField(t *testing.T) {
	tests := []struct {
		name    string
		failure *SchemaFailure
		want    string
	}{
		{
			name: "value",
			failure: &SchemaFailure{
				Kind:  SchemaFailureUnsupportedSchemaVersion,
				Value: "bad\x1b\nversion",
			},
			want: "value 'bad??version'",
		},
		{
			name: "found kind",
			failure: &SchemaFailure{
				Kind:      SchemaFailureMalformedSchemaDiscriminator,
				FoundKind: "bad\x1b\nkind",
			},
			want: "has bad??kind value",
		},
		{
			name: "key declared and found kinds",
			failure: &SchemaFailure{
				Kind:         SchemaFailureWrongTypeRetainedField,
				Key:          "bad\x1b\nkey",
				DeclaredKind: "bad\x1b\ndeclared",
				FoundKind:    "bad\x1b\nfound",
			},
			want: "key 'bad??key' is declared bad??declared but has bad??found value",
		},
		{
			name: "detail",
			failure: &SchemaFailure{
				Kind:   SchemaFailureMalformedYAML,
				Detail: "bad\x1b\ndetail",
			},
			want: "malformed YAML: bad??detail",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diagnostic := tt.failure.Error()
			assert.Contains(t, diagnostic, tt.want)
			assertNoControlBytes(t, diagnostic)
		})
	}
}

func assertNoControlBytes(t *testing.T, value string) {
	t.Helper()
	for _, b := range []byte(value) {
		assert.Truef(t, b >= 0x20 && b != 0x7f, "diagnostic contains control byte 0x%02x: %q", b, value)
	}
}

func TestDecodeCandidate_RetainedFieldKinds(t *testing.T) {
	for _, key := range []string{"title", "requirement", "priority", "created", "updated", "status"} {
		for _, wrong := range []struct {
			name  string
			value string
			kind  string
		}{
			{name: "sequence", value: "[wrong]", kind: "sequence"},
			{name: "mapping", value: "{value: wrong}", kind: "mapping"},
		} {
			t.Run(key+" wrong "+wrong.name+" kind", func(t *testing.T) {
				result := DecodeCandidate(strictV2Document(fmt.Sprintf("%s: %s\n", key, wrong.value)))
				testrequire.NotNil(t, result.Failure)
				assert.Equal(t, SchemaFailureWrongTypeRetainedField, result.Failure.Kind)
				assert.Equal(t, key, result.Failure.Key)
				assert.Equal(t, "scalar", result.Failure.DeclaredKind)
				assert.Equal(t, wrong.kind, result.Failure.FoundKind)
			})
		}

		t.Run(key+" null and empty are absent", func(t *testing.T) {
			for _, value := range []string{"", "null", `""`} {
				result := DecodeCandidate(strictV2Document(fmt.Sprintf("%s: %s\n", key, value)))
				testrequire.Nil(t, result.Failure)
			}
		})
	}

	tests := []struct {
		name     string
		field    string
		key      string
		declared string
		found    string
	}{
		{name: "tags scalar", field: "tags: wrong\n", key: "tags", declared: "sequence of non-null scalars", found: "scalar"},
		{name: "tags empty scalar", field: "tags: \"\"\n", key: "tags", declared: "sequence of non-null scalars", found: "empty scalar"},
		{name: "tags mapping", field: "tags: {a: b}\n", key: "tags", declared: "sequence of non-null scalars", found: "mapping"},
		{name: "tags null element", field: "tags: [alpha, null]\n", key: "tags", declared: "sequence of non-null scalars", found: "null element"},
		{name: "tags mapping element", field: "tags: [alpha, {a: b}]\n", key: "tags", declared: "sequence of non-null scalars", found: "mapping"},
		{name: "meta scalar", field: "meta: wrong\n", key: "meta", declared: "mapping", found: "scalar"},
		{name: "meta empty scalar", field: "meta: \"\"\n", key: "meta", declared: "mapping", found: "empty scalar"},
		{name: "meta sequence", field: "meta: [wrong]\n", key: "meta", declared: "mapping", found: "sequence"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DecodeCandidate(strictV2Document(tt.field))
			testrequire.NotNil(t, result.Failure)
			assert.Equal(t, SchemaFailureWrongTypeRetainedField, result.Failure.Kind)
			assert.Equal(t, tt.key, result.Failure.Key)
			assert.Equal(t, tt.declared, result.Failure.DeclaredKind)
			assert.Equal(t, tt.found, result.Failure.FoundKind)
		})
	}

	for _, field := range []string{"tags:\n", "tags: []\n", "meta:\n", "meta: {}\n"} {
		result := DecodeCandidate(strictV2Document(field))
		testrequire.Nil(t, result.Failure, field)
	}
}

func TestDecodeCandidate_RecordsEveryWrongKindRetainedField(t *testing.T) {
	result := DecodeCandidate(strictV2Document(
		"updated: [a]\n" +
			"status: [b]\n" +
			"tags: [null, {a: b}]\n"))
	testrequire.NotNil(t, result.Failure)
	assert.Equal(t, "updated", result.Failure.Key)
	assert.Equal(t, []RetainedFieldFailure{
		{Key: "updated", DeclaredKind: "scalar", FoundKind: "sequence"},
		{Key: "status", DeclaredKind: "scalar", FoundKind: "sequence"},
		{Key: "tags", DeclaredKind: "sequence of non-null scalars", FoundKind: "null element"},
		{Key: "tags", DeclaredKind: "sequence of non-null scalars", FoundKind: "mapping"},
	}, result.Failure.RetainedFields)
}

func TestDecodeCandidate_BOMAndPreamble(t *testing.T) {
	valid := []string{
		string(strictV2Document("")),
		"\xef\xbb\xbf" + string(strictV2Document("")),
		"---\r\ngtms_schema: test-case/v2\r\ntype: Test Case Spec\r\ntest_type: Functional\r\ntest_case_id: tc-abcdef12\r\n---\r\n",
		"---  \ngtms_schema: test-case/v2\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-abcdef12\n---\t\n",
	}
	for i, doc := range valid {
		result := DecodeCandidate([]byte(doc))
		testrequire.Nil(t, result.Failure, "valid case %d", i)
	}

	invalid := []string{
		"\n" + string(strictV2Document("")),
		"preamble\n" + string(strictV2Document("")),
		"\xef\xbb\xbf\xef\xbb\xbf" + string(strictV2Document("")),
		"---\ngtms_schema: test-case/v2\ntype: Test Case Spec\ntest_type: Functional\n  ---\n",
		"---\ngtms_schema: test-case/v2\ntype: Test Case Spec\ntest_type: Functional\n",
	}
	for i, doc := range invalid {
		result := DecodeCandidate([]byte(doc))
		testrequire.NotNil(t, result.Failure, "invalid case %d", i)
		assert.Equal(t, SchemaFailureMalformedYAML, result.Failure.Kind)
	}

	for _, doc := range []string{
		"# Markdown body\n\n---\n\nMore body\n",
	} {
		result := DecodeCandidate([]byte(doc))
		testrequire.NotNil(t, result.Failure)
		assert.Equal(t, SchemaFailureNoFrontmatterBlock, result.Failure.Kind)
	}
}

func TestDecodeCandidate_CategoriesRoundTrip(t *testing.T) {
	categories := []string{
		"Functional", "Integration", "Security", "Acceptance", "Negative",
		"Usability", "Regression", "Boundary", "Invariant", "Unit",
	}
	for _, category := range categories {
		t.Run(category, func(t *testing.T) {
			doc := []byte("---\n" +
				"gtms_schema: test-case/v2\n" +
				"type: Test Case Spec\n" +
				"test_type: " + category + "\n" +
				"test_case_id: tc-abcdef12\n" +
				"---\n")
			result := DecodeCandidate(doc)
			testrequire.Nil(t, result.Failure)
			assert.Equal(t, category, result.Metadata.TestType)
		})
	}
}

func TestDecodeCandidate_IdentityPreservedExactly(t *testing.T) {
	for _, identity := range []string{"TC-ABCDEF12", " tc-abcdef12 ", "tc-ABCDef12"} {
		t.Run(identity, func(t *testing.T) {
			doc := []byte("---\n" +
				"gtms_schema: test-case/v2\n" +
				"type: Test Case Spec\n" +
				"test_type: Functional\n" +
				"test_case_id: \"" + identity + "\"\n" +
				"---\n")
			result := DecodeCandidate(doc)
			testrequire.Nil(t, result.Failure)
			assert.Equal(t, identity, result.ParsedIdentity)
			assert.Equal(t, identity, result.Metadata.TestCaseID)
		})
	}
}
