#!/bin/sh
# Demo create adapter -- writes 3 pre-baked test cases using GTMS-generated IDs.
# This is a working example of a Tier 2 (script) adapter.

set -e

# GTMS provides these environment variables:
#   GTMS_OUTPUT_DIR   -- where to write test case files
#   GTMS_TC_IDS       -- comma-separated list of pre-generated test case IDs
#   GTMS_RESULT_FILE  -- path to the handoff contract YAML
#                       (e.g. .gtms/results/task-{id}.handoff.yaml)

if [ -z "$GTMS_OUTPUT_DIR" ]; then
  echo "ERROR: GTMS_OUTPUT_DIR not set" >&2
  exit 1
fi

mkdir -p "$GTMS_OUTPUT_DIR"

# Split TC IDs into an array
IFS=',' read -r ID1 ID2 ID3 REST <<EOF
$GTMS_TC_IDS
EOF

# Test case 1: Valid login
if [ -n "$ID1" ]; then
cat > "$GTMS_OUTPUT_DIR/${ID1}-login-valid-credentials.md" <<TCEOF
---
test_case_id: ${ID1}
title: "Valid login succeeds with correct credentials"
requirement: _demo
priority: High
type: Functional
created: $(date +%Y-%m-%d)
---

## Test Objective
Verify that a user can log in with valid email and password and is redirected to the dashboard.

## Preconditions
- User account exists with email "user@example.com" and password "SecurePass123"
- User is on the login page

## Test Data
- Email: user@example.com
- Password: SecurePass123

## Test Steps
1. Enter "user@example.com" in the email field
   - Expected: Email is accepted
2. Enter "SecurePass123" in the password field
   - Expected: Password is masked
3. Click the "Log In" button
   - Expected: Form is submitted

## Expected Final Outcome
- User is redirected to /dashboard
- A session token is stored
- Welcome message displays the user's name

## Postconditions
- User session is active
- Login event is recorded in audit log
TCEOF
fi

# Test case 2: Invalid password
if [ -n "$ID2" ]; then
cat > "$GTMS_OUTPUT_DIR/${ID2}-login-invalid-password.md" <<TCEOF
---
test_case_id: ${ID2}
title: "Invalid password is rejected with error message"
requirement: _demo
priority: High
type: Functional
created: $(date +%Y-%m-%d)
---

## Test Objective
Verify that an incorrect password displays a generic error message without revealing account existence.

## Preconditions
- User account exists with email "user@example.com"
- User is on the login page

## Test Data
- Email: user@example.com
- Password: WrongPassword99

## Test Steps
1. Enter "user@example.com" in the email field
   - Expected: Email is accepted
2. Enter "WrongPassword99" in the password field
   - Expected: Password is masked
3. Click the "Log In" button
   - Expected: Form is submitted

## Expected Final Outcome
- Error message displays: "Invalid email or password"
- User remains on the login page
- No session token is created

## Postconditions
- Failed login attempt is logged
- Account is not locked (first failed attempt)
TCEOF
fi

# Test case 3: Empty fields
if [ -n "$ID3" ]; then
cat > "$GTMS_OUTPUT_DIR/${ID3}-login-empty-fields.md" <<TCEOF
---
test_case_id: ${ID3}
title: "Empty fields show validation errors"
requirement: _demo
priority: Medium
type: Functional
created: $(date +%Y-%m-%d)
---

## Test Objective
Verify that submitting the login form with empty fields shows inline validation errors.

## Preconditions
- User is on the login page
- Both email and password fields are empty

## Test Data
- Email: (empty)
- Password: (empty)

## Test Steps
1. Leave the email field empty
   - Expected: No error yet (validation on submit)
2. Leave the password field empty
   - Expected: No error yet (validation on submit)
3. Click the "Log In" button
   - Expected: Form validation triggers

## Expected Final Outcome
- Email field shows: "Email is required"
- Password field shows: "Password is required"
- Form is not submitted to the server
- Validation messages appear inline next to each field

## Postconditions
- No network request was made
- User remains on the login page
TCEOF
fi

# Count files created
FILE_COUNT=$(ls "$GTMS_OUTPUT_DIR"/*.md 2>/dev/null | wc -l)

# Update handoff contract
if [ -n "$GTMS_RESULT_FILE" ]; then
cat > "$GTMS_RESULT_FILE" <<REOF
task: ${GTMS_TASK_ID}
command: create
target: _demo
adapter: demo
mode: sync
status: complete
result: pass
artefact: ${GTMS_OUTPUT_DIR}
summary: "Created ${FILE_COUNT} demo test cases"
completed: $(date -u +%Y-%m-%dT%H:%M:%SZ)
REOF
fi

echo "Created ${FILE_COUNT} demo test cases in ${GTMS_OUTPUT_DIR}"
