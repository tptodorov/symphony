# Symphony

Symphony turns project work into isolated, autonomous implementation runs, allowing teams to manage
work instead of supervising coding agents.

[![Symphony demo video preview](.github/media/symphony-demo-poster.jpg)](https://player.vimeo.com/video/1186371009?h=5626e4b899)

_In this [demo video](https://player.vimeo.com/video/1186371009?h=5626e4b899), Symphony monitors a Linear board for work and spawns agents to handle the tasks. The agents complete the tasks and provide proof of work: CI status, PR review feedback, complexity analysis, and walkthrough videos. When accepted, the agents land the PR safely. Engineers do not need to supervise Codex; they can manage the work at a higher level._

> [!WARNING]
> Symphony is a low-key engineering preview for testing in trusted environments.

## Running Symphony

### Requirements

Symphony works best in codebases that have adopted
[harness engineering](https://openai.com/index/harness-engineering/). Symphony is the next step --
moving from managing coding agents to managing work that needs to get done.

### Option 1. Make your own

Tell your favorite coding agent to build Symphony in a programming language of your choice:

> Implement Symphony according to the following spec:
> https://github.com/openai/symphony/blob/main/SPEC.md

### Option 2. Use our experimental reference implementation

Check out [elixir/README.md](elixir/README.md) for instructions on how to set up your environment
and run the Elixir-based Symphony implementation. You can also ask your favorite coding agent to
help with the setup:

> Set up Symphony for my repository based on
> https://github.com/openai/symphony/blob/main/elixir/README.md

### Workflow examples

See [examples/WORKFLOW.full.md](examples/WORKFLOW.full.md) for a fully expanded workflow
configuration with Jira, Linear, Beads, Codex, Pi, hooks, polling, workspace, and server settings.

### This repository's workflow

This repository is configured for Symphony-on-Symphony:

- Tracker: Beads, with issue prefix `SYM`.
- Agent: Codex via `codex app-server`.
- Workspaces: `.symphony/workspaces/`.
- Workflow file: `WORKFLOW.md`.

For a fresh clone, prepare Beads and build the local Symphony binary:

```sh
bd bootstrap --yes
make build
```

Run the local workflow:

```sh
./go/symphony -workdir . -logs-root .symphony/logs WORKFLOW.md
```

Beads project metadata is tracked under `.beads/`; local Dolt runtime files are ignored by
`.beads/.gitignore`. Sync shared issue data with `bd dolt pull` and `bd dolt push`.

### Hook environment

Workflow hooks inherit the Symphony process environment. Environment variables added by Symphony use
the `SYMPHONY_` prefix only.

- `SYMPHONY_WORKDIR`: effective absolute working directory after applying `-workdir`.

If a repository task expects another name, assign it inside the hook script, for example
`SOURCE_DIR="$SYMPHONY_WORKDIR" task worktree:prepare`.

## Releasing

Releases use Go module tags and the GitHub CLI. Because the Go module lives in `go/`, tag releases
as `go/vX.Y.Z`.

```sh
make release VERSION=0.1.0
```

Replace `0.1.0` with the version you are releasing. The release target runs `git town sync`, runs
`make -C go all`, then creates the GitHub Release with:

```sh
gh release create go/v0.1.0 --repo tptodorov/symphony --target main --generate-notes --fail-on-no-commits
```

Users install the released CLI with Go:

```sh
go install github.com/tptodorov/symphony/go/cmd/symphony@v0.1.0
```

---

## License

This project is licensed under the [Apache License 2.0](LICENSE).
