#!/bin/sh
# agent-create-script.sh -- Tier 2 sync adapter for agent test case creation.
#
# To customise the test case shape, edit your role's template at:
#   gtms/test/templates/agent-testcase.template.md
# To customise the stamping behaviour, edit this script.
# To activate: set defaults.create to agent-create-script in gtms.config.

set -e

# GTMS provides these environment variables:
#   GTMS_OUTPUT_DIR      -- where to write the test case file
#   GTMS_TC_IDS          -- comma-separated list of pre-generated test case IDs
#   GTMS_REFERENCE       -- the --reference flag value (may be empty)
#   GTMS_RESULT_FILE     -- path to the handoff contract YAML
#   GTMS_TASK_ID         -- unique task identifier
#   GTMS_TEMPLATE_FILE   -- path to the role-specific testcase template

if [ -z "$GTMS_OUTPUT_DIR" ]; then
  echo "ERROR: GTMS_OUTPUT_DIR not set" >&2
  exit 1
fi

mkdir -p "$GTMS_OUTPUT_DIR"

# Take the first ID from the comma-separated list
ID=$(echo "$GTMS_TC_IDS" | cut -d',' -f1)

if [ -z "$ID" ]; then
  echo "ERROR: GTMS_TC_IDS not set or empty" >&2
  exit 1
fi

# Build filename -- include name slug if provided
if [ -n "$GTMS_TC_NAME" ]; then
  OUTFILE="$GTMS_OUTPUT_DIR/${ID}-${GTMS_TC_NAME}.md"
  NAME_VALUE="${GTMS_TC_NAME}"
else
  OUTFILE="$GTMS_OUTPUT_DIR/${ID}.md"
  NAME_VALUE=""
fi

# escape_sed: Escape sed-replacement specials (\ & |) in values.
escape_sed() { printf '%s' "$1" | sed -e 's/[\\&|]/\\&/g'; }

# yaml_escape: Encode a value for safe insertion as a YAML double-quoted scalar.
yaml_escape() {
  printf '%s' "$1" | awk '
    {
      gsub(/\\/, "\\\\")
      gsub(/"/,  "\\\"")
      gsub(/\t/, "\\t")
      gsub(/\r/, "\\r")
      if (NR > 1) printf "\\n"
      printf "%s", $0
    }
  '
}

# yaml_quoted_or_empty: For non-empty input, print a complete YAML double-quoted
# scalar (carrying its own quotes). For empty input, print nothing. Mirrors the
# Go-side yamlQuotedScalarOrEmpty helper so the substituted value either lands
# as a properly-quoted scalar in an unquoted placeholder context (e.g. the
# requirement line in the testcase template) or leaves the value position bare.
yaml_quoted_or_empty() {
  if [ -z "$1" ]; then
    printf ''
  else
    printf '"%s"' "$(yaml_escape "$1")"
  fi
}

# Prepare substitution values
ID_E=$(escape_sed "$ID")
NAME_E=$(escape_sed "$NAME_VALUE")
REF_E=$(escape_sed "$(yaml_quoted_or_empty "$GTMS_REFERENCE")")
DATE_E=$(escape_sed "$(date +%Y-%m-%d)")

# ENH-161: Read template from GTMS_TEMPLATE_FILE; fall back to heredoc if missing.
if [ -n "$GTMS_TEMPLATE_FILE" ] && [ -f "$GTMS_TEMPLATE_FILE" ]; then
  sed \
    -e "s|\${TESTCASE_ID}|${ID_E}|g" \
    -e "s|\${TITLE}|${NAME_E}|g" \
    -e "s|\${REQUIREMENT}|${REF_E}|g" \
    -e "s|\${CREATED}|${DATE_E}|g" \
    "$GTMS_TEMPLATE_FILE" > "$OUTFILE"
else
  if [ -n "$GTMS_TEMPLATE_FILE" ]; then
    echo "warning: create template not found: $GTMS_TEMPLATE_FILE -- using built-in default" >&2
  fi
  # Fallback: inline heredoc (day-one shape). REF_LINE emits a properly-quoted
  # YAML scalar when --reference is set, or a bare key when empty -- matching
  # the substitution semantics of the template-read path above.
  if [ -n "$GTMS_REFERENCE" ]; then
    REF_LINE="requirement: \"$(yaml_escape "$GTMS_REFERENCE")\""
  else
    REF_LINE="requirement: "
  fi
cat > "$OUTFILE" <<TCEOF
---
test_case_id: ${ID}
title: "${NAME_VALUE}"
${REF_LINE}
priority: Medium
type: Functional
created: $(date +%Y-%m-%d)
---

## Test Objective


## Preconditions

-

## Test Data

-

## Test Steps

1.
   - Expected observation:

## Expected Final Outcome

-

## Postconditions

-

## Notes

TCEOF
fi

# Update handoff contract
if [ -n "$GTMS_RESULT_FILE" ]; then
cat > "$GTMS_RESULT_FILE" <<REOF
task: ${GTMS_TASK_ID}
command: create
target: $(basename "$GTMS_OUTPUT_DIR")
adapter: agent-create-script
mode: sync
status: complete
result: pass
artefact: ${OUTFILE}
summary: "Created agent-create-script test case ${ID}"
completed: $(date -u +%Y-%m-%dT%H:%M:%SZ)
REOF
fi

echo "Created agent-create-script test case: ${OUTFILE}"
