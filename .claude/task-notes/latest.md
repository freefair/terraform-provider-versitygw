# Task: Feature-completeness roadmap, Dependabot, weekly compat CI

**Started:** 2026-08-28
**Last update:** 2026-08-28 13:40

## Scope
docs/plans/* for every missing versitygw bucket-level resource; dependabot.yml; compat.yml testing versitygw:latest against Terraform latest and OpenTofu latest; pin test.yml to v1.7.0

## Progress

## Decisions

- **Only plans + CI in this task, features implemented one task per plan** (2026-08-28): 8 resources plus client and acceptance tests is a diff nobody reviews in one go; each plan is self-contained

- **test.yml pinned to versity/versitygw:v1.7.0; compat.yml runs weekly against :latest with Terraform latest and OpenTofu latest** (2026-08-28): PR checks stay deterministic; upstream drift surfaces in one place with version info in the job summary

## Open

## Next session
