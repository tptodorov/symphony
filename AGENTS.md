# AGENTS.md

Guidance for agents working in this repository.

## Project

Symphony is a Go service that reads tracker issues, prepares isolated workspaces, and runs coding agents to complete the work.

## Workflow

- Use `WORKFLOW.md` as the Symphony runtime definition.
- Use Beads (`bd`) for repo issues. Start with `bd prime`, `bd ready`, or `bd show <id>`.
- Use `gh` for GitHub access. Do not use GitHub MCP/app connector tools.
- Follow `.augment/rules` files if they exist in the project folder you touch.

## Development

- The implementation lives in `go/`.
- Treat `SPEC.md` as the product contract.
- Use Make targets instead of raw Go commands.
- Run `make all` before handoff when Go code changes.

## Symphony

- This repo is configured to run Symphony against repo-shared Beads issues.
- Symphony workspaces live under `.symphony/workspaces/`.
- The configured agent is Codex via `codex app-server`.
