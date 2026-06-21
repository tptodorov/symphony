# AGENTS.md

Guidance for coding agents working in this `go/` implementation.

## Project

This folder contains the Go implementation of **Symphony**, a long-running service that reads work from an issue tracker, creates isolated per-issue workspaces, and runs coding-agent sessions to complete that work.

## Source of truth

Implement behavior from the repository specification at `../SPEC.md`.

- Treat `../SPEC.md` as the authoritative product and behavior contract.
- If implementation details conflict with the spec, update the implementation to match the spec unless explicitly instructed otherwise.
- Keep deviations implementation-defined and documented.

## Operating the project

Always use `make` targets to operate this project instead of invoking Go commands directly.

Common targets:

```sh
make test
make vet
make build
make all
```

Before handing off changes, run:

```sh
make all
```
