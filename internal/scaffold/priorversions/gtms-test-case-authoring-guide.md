# Test Case Template

This is the authoring reference for test cases in `gtms/test/cases/`. It documents
the section structure produced by `gtms create` and the richer frontmatter
keys you can add as a TC matures from skeleton to fully-authored spec.

## Frontmatter

### What `gtms create` writes (skeleton default)

```yaml
---
test_case_id: tc-<8-char-hex-lowercase>
title: "<slug if [name] was passed, empty otherwise>"
requirement: <reference value -- present only when --reference was passed>
priority: Medium
type: Functional
created: YYYY-MM-DD
---
```

This is the contract the skeleton create adapter emits and the pipeline reads.
`test_case_id` is the join key for every downstream stage; do not change it.
`priority` and `type` are stamped with safe defaults so the dashboard always
has something to sort and filter by; edit them as the TC matures.

### Recommended additions when authoring a richer spec

When you grow a test case beyond the skeleton -- by hand or via an AI adapter --
this header line gives reviewers a one-glance summary; rewrite the stamped
`title` slug into a full sentence as soon as the behaviour is clear:

```yaml
title: "<Action> should <expected outcome> when <condition>"
```

`priority` and `type` accept the following ranges; the skeleton defaults are
safe starting points:

```yaml
priority: High | Medium | Low
type: Functional | Performance | Security | Usability | Integration
```

These additions are optional. Nothing in the pipeline requires them beyond the
defaults the stamper already writes; rewrite them when they earn their keep.

## Test Objective

State what specific behaviour this test case verifies and why it matters.
One test case should cover one behaviour -- if you need an "and" in the
objective, split into separate test cases.

## Preconditions

List every condition that must be true before Step 1 begins. Include
environment state, user authentication, data that must already exist,
and configuration settings.

## Test Data

Provide exact values used during execution. Avoid placeholders like
"valid email" -- instead use a concrete value such as "user@example.com".
Include both valid and boundary data where relevant.

## Test Steps

Each step is one atomic action with an expected observation. Use the
format:

```
1. Perform <action>
   - Expected observation: <what should happen>
```

Steps must be unambiguous -- another tester should produce the same
result by following them exactly.

## Expected Final Outcome

Describe the overall success criteria after all steps complete. This is
the definitive pass/fail signal: if this outcome is observed, the test
passes.

## Postconditions

Document the expected system state after the test. Include any cleanup
that should happen or side-effects the tester should be aware of.

## Notes

Optional section for context, edge cases to consider, links to
related test cases, or known issues.

## Principles

- One test case per specific behavior
- Steps must be atomic and unambiguous
- Expected results must be specific and verifiable
- Use exact values, not ranges or placeholders
- Link back to the source requirement
