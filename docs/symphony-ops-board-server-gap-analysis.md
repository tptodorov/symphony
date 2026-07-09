# Symphony Ops Board Server Gap Analysis

Date: 2026-07-07

## Sources

- Current product contract: `SPEC.md`, especially Section 13.
- Current Go server/API: `go/internal/server/server.go`, `go/internal/orchestrator/events.go`, `go/internal/orchestrator/orchestrator.go`.
- Design handoff archive: `/Users/todor.todorov/Downloads/Localhost interface redesign proposal.zip`.

## Summary

The current spec and Go implementation already cover the core live board: queued work, setup/pre-run
state, running sessions, retry rows, token totals, refresh, and recent agent message tails.

The full visual design still needs server-side spec additions for these areas:

1. PR metadata.
2. Live post-run hook phases.
3. Rich "Done today" rows.
4. Queue wait time and retry titles.
5. Explicit per-row `max_turns` and runtime fields.
6. Durable or bounded event history semantics.

## Already Covered

- `GET /api/v1/state` exists and returns `ready`, `setup`, `running`, `retrying`, `counts`,
  `agent_totals`, and `rate_limits`.
- `GET /api/v1/<issue_identifier>` exists for issue runtime/debug detail.
- `POST /api/v1/refresh` exists for manual poll/reconcile trigger.
- The dashboard already polls `/api/v1/state` every 5 seconds and renders queued, setup, running,
  retrying, completed count, and total token KPIs.
- Running rows already expose `started_at`, `tokens`, `turn_count`, `last_event`, `last_message`,
  log paths, and `recent_agent_messages`.

## Evidence

- Design handoff README, "Data source mapping": identifies `ready`, `setup`, `running`, `retrying`,
  and `agent_totals` as available now, marks activity stream as partial, and marks PR data,
  post-run hooks, and Done today as unavailable without more server data.
- Design prototype, `Symphony Ops Board.dc.html`: the lifecycle model includes `prepare`,
  `after_create`, `before_run`, `agent_run`, `after_run`, `before_remove`, and `complete`; mock rows
  include PR state, queued wait time, retry titles, done rows, turns, tokens, runtime, and activity
  log entries.
- `SPEC.md` Section 13.3: current snapshot contract covers `ready`, `setup`, `running`,
  `retrying`, `agent_totals`, and `rate_limits`; it does not define PR metadata, post-run hook rows,
  or completed row history.
- `SPEC.md` Section 13.7: current HTTP contract covers `/`, `GET /api/v1/state`,
  `GET /api/v1/<issue_identifier>`, and `POST /api/v1/refresh`.
- `go/internal/orchestrator/events.go`: current snapshot structs include `Ready`, `Setup`,
  `Running`, `Retrying`, `Counts`, `AgentTotals`, and `RateLimits`. `RunningSnapshot` has
  `RecentAgentMessages`; `RetrySnapshot` does not have `Title` or PR fields.
- `go/internal/orchestrator/orchestrator.go`: current `completed` state is a map of issue IDs to
  times and is exposed as `counts.completed`; no completed rows are emitted.
- `go/internal/server/server.go`: current dashboard hardcodes `turn .../20`, renders a five-node
  lifecycle track, always shows `no PR yet`, renders `Post-run hooks` as unavailable, and renders
  Done today as count-only.

## Gaps To Add To `SPEC.md`

### 1. Snapshot display config

Design need: cards show `turn N/max_turns`.

Current gap: the spec only requires `turn_count`; the Go dashboard hardcodes `/20`.

Spec addition:

- Add `runtime_config` or per-row `max_turns` to `GET /api/v1/state`.
- Prefer per-row `max_turns` on `running`, `setup`, `retrying`, and `completed` rows if future
  workflows can reload while rows are active.

Suggested fields:

```json
{
  "runtime_config": {
    "agent_max_turns": 20,
    "dashboard_refresh_ms": 5000
  }
}
```

### 2. Ready queue wait time

Design need: queued rail shows wait time.

Current gap: `ready` rows expose issue identity, title, state, URL, and priority, but not
`created_at`, `queued_since`, or `wait_seconds`.

Spec addition:

- Require `ready[]` rows to include one of:
  - `created_at` from the normalized issue, or
  - `queued_since` when the issue first entered the local ready queue.
- If the server computes it, include `wait_seconds`.

### 3. Retry row title

Design need: retry rows show issue title.

Current gap: current Go `RetrySnapshot` has no `title` field, and the spec sample omits it.

Spec addition:

- Require `retrying[]` rows to include `title`, `state`, and `issue_url` when known.

### 4. Live post-run hook phases

Design need: seven lifecycle nodes:

`prepare -> after_create -> before_run -> agent_run -> after_run -> before_remove -> completed`

Current gap: the spec only snapshots pre-run setup. `after_run` and `before_remove` are hook
operations, but they are not surfaced as live status rows. The Go dashboard therefore renders a
five-node track and `Post-run hooks` as unavailable.

Spec addition:

- Add an observable lifecycle phase model for dashboard rows.
- Include post-run hooks in the runtime snapshot while they are running or recently failed.
- Add `counts.post_run_hooks`.

Suggested enum:

```text
prepare
after_create
before_run
agent_run
after_run
before_remove
completed
```

Suggested row fields:

```json
{
  "phase": "after_run",
  "status": "running",
  "hook": "after_run",
  "phase_started_at": "2026-07-07T14:40:38Z",
  "phase_updated_at": "2026-07-07T14:40:40Z",
  "phase_error": null
}
```

### 5. Completion history and "Done today"

Design need: "Done today" rail rows show issue link, merged PR chip, title, turns, tokens, runtime,
and completion time.

Current gap: the spec says `completed` is an internal set of issue IDs. The current snapshot exposes
only a count. It does not preserve title, turn count, final tokens, runtime, PR, or completion time
as display rows.

Spec addition:

- Add `completed` or `done_today` rows to the snapshot.
- Define the clock boundary and timezone for "today".
- Define retention and restart behavior.
- Keep this as observability state, not required orchestrator state.

Suggested row:

```json
{
  "issue_id": "abc123",
  "issue_identifier": "MT-649",
  "issue_url": "https://tracker.example/issues/MT-649",
  "title": "Implement queued work visibility",
  "completed_at": "2026-07-07T14:02:00Z",
  "completion_reason": "worker_completed",
  "final_state": "Human Review",
  "turn_count": 9,
  "tokens": {
    "input_tokens": 1000,
    "output_tokens": 800,
    "total_tokens": 1800
  },
  "runtime_seconds": 2280,
  "pull_request": null
}
```

### 6. PR metadata extension

Design need: PR chips on active, retry, and done rows with states `mergeable`, `blocked`, `draft`,
and `merged`.

Current gap: Symphony is a tracker reader/scheduler. The normalized issue model and snapshot do not
include PR fields. The design handoff correctly marks this as unavailable without a separate
integration.

Spec addition:

- Add an optional PR metadata extension under the HTTP/status surface.
- Define config separately from core tracker config.
- Define failure behavior: PR lookup failures must not affect scheduling.
- Define row shape shared by running, retrying, and completed rows.

Suggested row field:

```json
{
  "pull_request": {
    "number": 961,
    "url": "https://github.com/org/repo/pull/961",
    "state": "mergeable",
    "is_draft": false,
    "merged_at": null,
    "head_branch": "MT-649"
  }
}
```

State mapping should be specified. For example:

- `merged` when the PR is merged.
- `draft` when the PR is open and draft.
- `blocked` when mergeability or checks are not passing.
- `mergeable` when open, non-draft, and currently mergeable.

### 7. Activity stream history

Design need: expanded cards show newest-first activity streams.

Current state: running rows already include `recent_agent_messages`, capped at 100 in memory.

Remaining gap: the spec should define history bounds, order, and whether completed/retry rows can
still expose recent messages after a run exits. A durable observability event log would also back
"Done today" details across restart without becoming orchestrator state.

Spec addition:

- Require `recent_agent_messages` ordering and cap.
- Add optional `GET /api/v1/<issue_identifier>/events?limit=100` if the main snapshot should stay
  small.
- Define whether event history survives worker exit and service restart.

## Recommended Spec Patch Shape

Keep the HTTP server optional for language-level conformance, but strengthen the contract when it is
implemented:

1. Extend Section 13.3 with row schemas for `ready`, `setup`, `running`, `retrying`, and
   `completed`.
2. Add a lifecycle `phase` enum and `counts.post_run_hooks`.
3. Add `runtime_config.agent_max_turns`.
4. Add optional PR metadata extension fields.
5. Add completed-row retention semantics.
6. Add event-history bounds and endpoint semantics.

If the repo adopts the local spec-best-practices rules, add stable requirement IDs such as
`REQ-OBS-001` rather than adding untracked prose only.
