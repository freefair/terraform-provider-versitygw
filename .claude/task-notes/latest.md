# Task: Feature-completeness roadmap, Dependabot, weekly compat CI

**Started:** 2026-08-28
**Last update:** 2026-08-28 13:50

## Scope
docs/plans/* for every missing versitygw bucket-level resource; dependabot.yml; compat.yml testing versitygw:latest against Terraform latest and OpenTofu latest; pin test.yml to v1.7.0

## Progress

- 2026-08-28 13:50 — CI + roadmap committed; compat.yml not yet run on GitHub (needs push + workflow_dispatch)

## Decisions

- **Only plans + CI in this task, features implemented one task per plan** (2026-08-28): 8 resources plus client and acceptance tests is a diff nobody reviews in one go; each plan is self-contained

- **test.yml pinned to versity/versitygw:v1.7.0; compat.yml runs weekly against :latest with Terraform latest and OpenTofu latest** (2026-08-28): PR checks stay deterministic; upstream drift surfaces in one place with version info in the job summary

## Open

- [ ] test.yml: go test -timeout 30m exceeds job timeout-minutes 15 (pre-existing) — decide whether to align

## Next session
Implement docs/plans in README order, one task per plan, starting with 00 + 01
