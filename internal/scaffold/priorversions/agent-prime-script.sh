#!/bin/sh
# agent-prime-script.sh -- Tier 2 sync adapter for agent test result preparation.
# Stamps one blank manual result template for the given test case.
# Edit this script to customise the agent prime flow.
#
# Receives: GTMS_TESTCASE, GTMS_TESTCASE_HASH, GTMS_TEMPLATE_FILE,
#           GTMS_OUTPUT_FILE, GTMS_BRANCH, GTMS_RESULT_FILE, GTMS_TASK_ID,
#           GTMS_FORCE, GTMS_TC_TITLE, GTMS_TC_REQUIREMENT,
#           GTMS_TC_PRIORITY, GTMS_TC_TYPE

set -e

# Validate required env vars
for var in GTMS_TESTCASE GTMS_TESTCASE_HASH GTMS_TEMPLATE_FILE GTMS_OUTPUT_FILE GTMS_BRANCH GTMS_RESULT_FILE GTMS_TASK_ID; do
  eval "val=\$$var"
  if [ -z "$val" ]; then
    echo "ERROR: $var not set" >&2
    exit 1
  fi
done

# Safety: refuse to overwrite existing result file unless --force
if [ -f "$GTMS_OUTPUT_FILE" ] && [ "$GTMS_FORCE" != "true" ]; then
  echo "Manual result file already exists: $GTMS_OUTPUT_FILE" >&2
  echo "Use --force to overwrite, or delete the file manually." >&2
  exit 1
fi

# Ensure output directory exists
mkdir -p "$(dirname "$GTMS_OUTPUT_FILE")"

# Stamp result from template with variable substitution.
# escape_sed: Escape sed-replacement specials (\ & |) in values so branch names
# containing those characters do not corrupt the substitution.
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

TESTCASE_E=$(escape_sed "$GTMS_TESTCASE")
TESTCASE_HASH_E=$(escape_sed "$GTMS_TESTCASE_HASH")
BRANCH_E=$(escape_sed "$GTMS_BRANCH")

# Snapshot fields: yaml_escape first (\ -> \\, " -> \"), then escape_sed.
TC_TITLE_E=$(escape_sed "$(yaml_escape "$GTMS_TC_TITLE")")
TC_REQUIREMENT_E=$(escape_sed "$(yaml_escape "$GTMS_TC_REQUIREMENT")")
TC_PRIORITY_E=$(escape_sed "$(yaml_escape "$GTMS_TC_PRIORITY")")
TC_TYPE_E=$(escape_sed "$(yaml_escape "$GTMS_TC_TYPE")")

# ENH-161: Check template exists; fall back to inline heredoc if missing.
if [ -f "$GTMS_TEMPLATE_FILE" ]; then
  sed \
    -e "s|\${TESTCASE}|${TESTCASE_E}|g" \
    -e "s|\${TESTCASE_HASH}|${TESTCASE_HASH_E}|g" \
    -e "s|\${BRANCH}|${BRANCH_E}|g" \
    -e "s|\${TC_TITLE}|${TC_TITLE_E}|g" \
    -e "s|\${TC_REQUIREMENT}|${TC_REQUIREMENT_E}|g" \
    -e "s|\${TC_PRIORITY}|${TC_PRIORITY_E}|g" \
    -e "s|\${TC_TYPE}|${TC_TYPE_E}|g" \
    "$GTMS_TEMPLATE_FILE" > "$GTMS_OUTPUT_FILE"
else
  echo "warning: prime template not found: $GTMS_TEMPLATE_FILE -- using built-in default" >&2
  # ENH-161 AC #20: byte-identical fallback output to Tier 0 manualResultTemplateFallback
  # (internal/adapter/builtin_action.go). Section-divider comments are preserved so the
  # Tier 0 / Tier 2 missing-template paths produce identical artefacts.
  cat > "$GTMS_OUTPUT_FILE" <<FALLBACKEOF
# yaml-language-server: \$schema=../../schemas/manual-result.schema.json
# -- GTMS contract (do not edit) ------------------------------------------
test_case_id: ${GTMS_TESTCASE}
test_case_hash: ${GTMS_TESTCASE_HASH}
framework: manual

# -- OVERALL RESULT -------------------------------------------------------
result:

# -- Optional metadata ----------------------------------------------------
title: "$(yaml_escape "$GTMS_TC_TITLE")"
requirement: "$(yaml_escape "$GTMS_TC_REQUIREMENT")"
priority: "$(yaml_escape "$GTMS_TC_PRIORITY")"
type: "$(yaml_escape "$GTMS_TC_TYPE")"
branch: ${GTMS_BRANCH}

# -- Steps (optional) -----------------------------------------------------
steps:
FALLBACKEOF
fi

# Update handoff contract -- ENH-130 orthogonal shape
cat > "$GTMS_RESULT_FILE" <<EOF
task: ${GTMS_TASK_ID}
command: automate
target: ${GTMS_TESTCASE}
adapter: agent-prime-script
mode: sync
status: complete
result: pass
artefact: ${GTMS_OUTPUT_FILE}
summary: "Stamped manual result template for ${GTMS_TESTCASE}"
completed: $(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF

echo "Stamped manual result template: ${GTMS_OUTPUT_FILE}"
