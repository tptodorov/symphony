---
tracker:
  kind: beads
  bd_command: bd
  active_states: [open, in_progress]
  terminal_states: [closed]
  required_labels: []

agent_kind: codex

codex:
  command: codex app-server
  approval_policy: on-request
  thread_sandbox: workspace-write
  turn_sandbox_policy:
    type: workspaceWrite
    writableRoots:
      - /Users/todor.todorov/tptodorov/symphony/.beads
      - /Users/todor.todorov/tptodorov/symphony/.git
      - /Users/todor.todorov/tptodorov/symphony/.symphony/workspaces
    networkAccess: true
  turn_timeout_ms: 3600000
  read_timeout_ms: 5000
  stall_timeout_ms: 300000

polling:
  interval_ms: 30000

workspace:
  root: .symphony/workspaces

prompt:
  include_files:
    - .symphony/setup-packet.md

hooks:
  timeout_ms: 120000
  after_create: |
    set -euo pipefail
    source_repo="${SYMPHONY_WORKDIR:?SYMPHONY_WORKDIR is required}"
    branch="$(git -C "$source_repo" branch --show-current || true)"
    remote_url="$(git -C "$source_repo" remote get-url origin 2>/dev/null || true)"

    git clone --no-hardlinks "$source_repo" .
    if [ -n "$remote_url" ]; then
      git remote set-url origin "$remote_url"
    fi
    if [ -n "$branch" ]; then
      git checkout "$branch"
    fi

    if command -v bd >/dev/null 2>&1 && [ -d .beads ]; then
      bd bootstrap --yes >/dev/null 2>&1 || true
    fi
  before_run: |
    set -euo pipefail
    mkdir -p .symphony
    cat > .symphony/setup-packet.md <<'PACKET'
    # Symphony Setup Packet

    This workspace was prepared by Symphony from the repository at `SYMPHONY_WORKDIR`.

    ## Repository

    - Project: Symphony Go implementation.
    - Source of truth: `SPEC.md`.
    - Go agent guidance: `go/AGENTS.md`.
    - Validation: run `make all` from the repository root before handoff.

    ## Tracker

    - Tracker: Beads (`bd`) with repo prefix `SYM`.
    - Use the Beads tool exposed by Symphony for issue lookup and updates.
    - If using the local CLI in this workspace, run `bd bootstrap --yes` first if `bd` reports no database.
    - Sync issue data with `bd dolt pull` before broad triage and `bd dolt push` after authorized issue updates.

    ## GitHub

    - Use `gh` for GitHub access.
    - Do not use GitHub MCP/app connector tools.

    Keep the change small, run relevant checks, and summarize files changed plus validation results.
    PACKET
  after_run: |
    true
  before_remove: |
    true

agent:
  max_concurrent_agents: 2
  max_turns: 20
  max_retry_backoff_ms: 300000

server:
  port: 10000
---
You are working on a Beads issue.

Issue: {{ issue.identifier }} - {{ issue.title }}
State: {{ issue.state }}
{% if issue.labels %}Tags: {% for label in issue.labels %}{{ label }} {% endfor %}{% endif %}
{% if issue.description %}

Description:
{{ issue.description }}
{% endif %}

Implement the smallest correct change for this issue.

Use the repository's Beads tracker context and Codex workspace guidance. Run the relevant checks, then summarize what changed, what passed, and any follow-up issue or pull request state.
