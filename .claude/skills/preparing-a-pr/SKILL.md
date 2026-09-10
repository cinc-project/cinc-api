---
name: preparing-a-pr
description: Use when about to commit, open, or update a pull request on cinc-api, or when running lint checks before pushing
---

# Preparing a PR

## Lint

CI runs `golangci-lint` (pinned to v2.13.2) over the root module and again
over `integration/`, which is a separate module the root run does not
reach. It uses the repo's `.golangci.yml`, so run it the same way — with
the config, not with ad-hoc flags:

```
golangci-lint run ./...
cd integration && golangci-lint run ./...
```

`.golangci.yml` enables the standard set (errcheck, govet, ineffassign,
staticcheck, unused) plus the gofmt formatter. `govet` there makes a
separate `go vet` step redundant, and gofmt findings are reported as
issues — `golangci-lint fmt` applies them.

`gosec` is mostly noise here (the MD5 use is required by Chef's sandbox
protocol and is annotated). A bare `staticcheck` binary may fail with a
Go-version mismatch; go through golangci-lint instead.

## Conventions

- Whenever a PR adds, removes, or renames a public surface (service,
  exported method, option, type), update `README.md` in the same PR so
  the **Status** table matches what the package actually exposes.
- Keep commit messages explanatory and terse — see `git log` for the
  established style.
- The PR description should include a **Test plan** checklist.
- One feature per PR. Multiple endpoint families should ship as
  separate PRs unless they are tightly coupled (e.g. Keys + Groups
  shipped together because they share ACL semantics).

## Before you push

- `go test ./... -race -count=2` is clean.
- `cd integration && go test ./...` if you touched transport, signing,
  or cookbook upload.
- `golangci-lint run ./...` is clean in both modules.
