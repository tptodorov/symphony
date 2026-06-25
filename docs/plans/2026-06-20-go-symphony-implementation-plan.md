# Go Symphony Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Implement a Go version of Symphony that satisfies the core spec using Linear + Codex first, with Beads, Pi, HTTP observability, and optional tools added later as extensions.

**Architecture:** Build a modular Go service under `go/` with a small orchestration core, pure domain types, adapter interfaces for trackers and agents, a workspace manager, workflow/config loading with dynamic reload, and a CLI lifecycle. The orchestrator should be the single authority for mutable runtime state; workers and adapters report events but never mutate orchestrator state directly.

**Tech Stack:** Go 1.23+, stdlib `log/slog`, `context`, `os/exec`, `net/http`, `time`, `sync`, `container/heap`; `gopkg.in/yaml.v3`; `github.com/fsnotify/fsnotify`; a Liquid-compatible template engine such as `github.com/osteele/liquid` with a strict unknown-variable/filter wrapper.

---

## Phase 0: Module and Package Skeleton

### Task 0: Create Go module and package skeleton

**Files:**
- Create: `go/go.mod`
- Create: `go/Makefile`
- Create: `go/cmd/symphony/main.go`
- Create: `go/internal/domain/*.go`
- Create: `go/internal/config/*.go`
- Create: `go/internal/workflow/*.go`
- Create: `go/internal/prompt/*.go`
- Create: `go/internal/workspace/*.go`
- Create: `go/internal/tracker/tracker.go`
- Create: `go/internal/tracker/linear/*.go`
- Create: `go/internal/tracker/beads/*.go`
- Create: `go/internal/agent/agent.go`
- Create: `go/internal/agent/codex/*.go`
- Create: `go/internal/agent/pi/*.go`
- Create: `go/internal/orchestrator/*.go`
- Create: `go/internal/observability/*.go`
- Create: `go/internal/server/*.go`

**Steps:**

1. Create `go/go.mod` with module path `github.com/tptodorov/symphony/go`.
2. Add initial dependencies:
   - `gopkg.in/yaml.v3`
   - `github.com/fsnotify/fsnotify`
   - `github.com/osteele/liquid`
3. Add placeholder packages so imports compile.
4. Add `Makefile` targets:
   - `test`
   - `vet`
   - `build`
   - `all`
5. Run `go test ./...` and verify there are no packages yet or all placeholders pass.
6. Commit.

**Validation:**

```bash
cd go
go test ./...
go vet ./...
make all
```

Expected: all commands pass.

---

## Phase 1: Domain Model

### Task 1: Define core domain types

**Files:**
- Create: `go/internal/domain/issue.go`
- Create: `go/internal/domain/workflow.go`
- Create: `go/internal/domain/config.go`
- Create: `go/internal/domain/agent.go`
- Create: `go/internal/domain/orchestrator.go`
- Test: `go/internal/domain/*_test.go`

**Step 1: Define domain types**

Implement:

```go
type Issue struct {
	ID          string
	Identifier  string
	Title       string
	Description *string
	Priority    *int
	State       string
	BranchName  *string
	URL         *string
	Labels      []string
	BlockedBy   []BlockerRef
	CreatedAt   *time.Time
	UpdatedAt   *time.Time
}

type BlockerRef struct {
	ID         *string
	Identifier *string
	State      *string
}

type WorkflowDefinition struct {
	Config         map[string]any
	PromptTemplate string
}

type TokenUsage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

type AgentTotals struct {
	InputTokens   int
	OutputTokens  int
	TotalTokens   int
	SecondsRunning float64
}
```

**Step 2: Add helper functions**

Add deterministic helpers:

- `SanitizeWorkspaceKey(identifier string) string`
- `NormalizeState(state string) string`
- `NormalizeLabels(labels []string) []string`
- `IssueIsEligible(issue Issue, cfg EffectiveConfig) bool`
- `SortIssuesForDispatch(issues []Issue)`

**Step 3: Write tests**

Test:

- workspace key sanitization
- state normalization
- label normalization
- priority sort order
- null priority sorts last
- oldest `created_at` sorts first
- identifier tie-breaker

**Validation:**

```bash
cd go
go test ./internal/domain
```

Expected: all domain tests pass.

---

## Phase 2: Workflow Loader and Config Layer

### Task 2: Implement workflow loading

**Files:**
- Create: `go/internal/workflow/loader.go`
- Test: `go/internal/workflow/loader_test.go`
- Testdata: `go/internal/workflow/testdata/*`

**Step 1: Implement loader**

`workflow.Load(path string) (WorkflowDefinition, error)` must:

- read `WORKFLOW.md`
- detect YAML front matter when file starts with `---`
- parse until the next `---`
- return `workflow_front_matter_not_a_map` when YAML root is not a map
- return `workflow_parse_error` for invalid YAML
- return `missing_workflow_file` when file is absent
- trim prompt body
- treat absent front matter as empty config and full file as prompt

**Step 2: Write tests**

Test:

- missing file
- no front matter
- empty front matter
- YAML map front matter
- non-map YAML root
- prompt body trimming

**Validation:**

```bash
cd go
go test ./internal/workflow
```

Expected: all workflow loader tests pass.

---

### Task 3: Implement typed config resolution

**Files:**
- Create: `go/internal/config/effective.go`
- Create: `go/internal/config/defaults.go`
- Create: `go/internal/config/resolve.go`
- Create: `go/internal/config/validate.go`
- Test: `go/internal/config/*_test.go`

**Step 1: Implement defaults**

Defaults:

- `agent_kind`: `codex`
- `tracker.endpoint`: `https://api.linear.app/graphql`
- `tracker.required_labels`: `[]`
- `tracker.active_states`: Linear defaults
- `tracker.terminal_states`: Linear defaults
- `polling.interval_ms`: `30000`
- `workspace.root`: `<os.TempDir>/symphony_workspaces`
- `hooks.timeout_ms`: `60000`
- `agent.max_concurrent_agents`: `10`
- `agent.max_turns`: `20`
- `agent.max_retry_backoff_ms`: `300000`
- `codex.command`: `codex app-server`
- `codex.turn_timeout_ms`: `3600000`
- `codex.read_timeout_ms`: `5000`
- `codex.stall_timeout_ms`: `300000`

**Step 2: Implement `$VAR` resolution**

Only resolve `$VAR_NAME` for values that explicitly contain it. If resolved env value is empty, treat as missing.

**Step 3: Implement path resolution**

- expand `~`
- resolve relative `workspace.root` relative to the `WORKFLOW.md` directory
- normalize to absolute path

**Step 4: Implement validation**

Validate:

- `tracker.kind` is present and supported
- Linear requires `tracker.api_key` and `tracker.project_slug`
- Beads requires non-empty `tracker.bd_command`
- `agent.max_turns` is positive
- `agent.max_retry_backoff_ms` is non-negative
- `hooks.timeout_ms` is positive
- `codex.command` is non-empty when `agent_kind == codex`
- `pi.command` is non-empty when `agent_kind == pi`

**Step 5: Write tests**

Test:

- missing required fields
- env-backed API key
- empty env-backed API key
- `~` expansion
- relative workspace root
- invalid max turns
- ignored unknown keys
- per-state concurrency invalid entries ignored

**Validation:**

```bash
cd go
go test ./internal/config
```

Expected: all config tests pass.

---

## Phase 3: Strict Prompt Rendering

### Task 4: Implement strict Liquid-compatible prompt renderer

**Files:**
- Create: `go/internal/prompt/renderer.go`
- Test: `go/internal/prompt/renderer_test.go`

**Step 1: Define prompt context**

Prompt rendering input:

- `issue` object with all normalized issue fields
- `attempt` integer or null
- issue object keys as strings

**Step 2: Implement renderer**

`prompt.Render(template string, issue domain.Issue, attempt *int) (string, error)` must:

- use Liquid-compatible semantics
- fail on unknown variables
- fail on unknown filters
- preserve nested arrays/maps
- return fallback prompt when template is empty

**Step 3: Write tests**

Test:

- render issue identifier/title/description
- render labels
- render blockers
- render attempt when present
- fail unknown variable
- fail unknown filter
- empty template fallback

**Validation:**

```bash
cd go
go test ./internal/prompt
```

Expected: all prompt tests pass.

---

## Phase 4: Workspace Manager and Hooks

### Task 5: Implement workspace safety and lifecycle

**Files:**
- Create: `go/internal/workspace/manager.go`
- Create: `go/internal/workspace/hooks.go`
- Create: `go/internal/workspace/safety.go`
- Test: `go/internal/workspace/*_test.go`

**Step 1: Implement workspace manager**

`workspace.Manager.CreateForIssue(identifier string) (Workspace, bool, error)` must:

- sanitize identifier
- compute path under workspace root
- create directory if missing
- return `created_now=true` only when created
- reuse existing directory
- fail safely if path is an existing non-directory

**Step 2: Implement path safety**

`EnsurePathInsideRoot(root, path string) error` must:

- normalize both paths
- reject paths outside root
- handle symlinks safely

**Step 3: Implement hooks**

`RunHook(ctx, script, cwd, timeout) error` must:

- run via `bash -lc <script>`
- set cwd to workspace path
- enforce timeout
- truncate output in logs
- return fatal errors for `after_create` and `before_run`
- return best-effort behavior for `after_run` and `before_remove`

**Step 4: Write tests**

Test:

- deterministic workspace path
- new directory creation
- existing directory reuse
- non-directory path failure
- path escape rejection
- hook success
- hook timeout
- hook failure

**Validation:**

```bash
cd go
go test ./internal/workspace
```

Expected: all workspace tests pass.

---

## Phase 5: Observability

### Task 6: Implement structured logging

**Files:**
- Create: `go/internal/observability/logger.go`
- Test: `go/internal/observability/logger_test.go`

**Step 1: Implement logger**

Use `log/slog` JSON output by default.

Required fields:

- `issue_id`
- `issue_identifier`
- `session_id`

**Step 2: Add logging helpers**

Add helpers for:

- validation failure
- dispatch
- worker start
- worker exit
- retry scheduled
- reconciliation
- hook start/failure/timeout
- tracker error
- reload error

**Step 3: Write tests**

Test that logs are valid JSON and include expected context fields.

**Validation:**

```bash
cd go
go test ./internal/observability
```

Expected: logger tests pass.

---

## Phase 6: Orchestrator Core with Fakes

### Task 7: Define tracker and agent interfaces

**Files:**
- Create: `go/internal/tracker/tracker.go`
- Create: `go/internal/agent/agent.go`

**Step 1: Define tracker interface**

```go
type Tracker interface {
	FetchCandidates(ctx context.Context, cfg config.Effective) ([]domain.Issue, error)
	FetchStatesByID(ctx context.Context, ids []string) (map[string]domain.Issue, error)
	FetchByStates(ctx context.Context, states []string) ([]domain.Issue, error)
}
```

**Step 2: Define agent runner interface**

```go
type Runner interface {
	Run(ctx context.Context, req RunRequest, events chan<- Event) Result
}
```

**Step 3: Add fake implementations**

Create fakes under:

- `go/internal/tracker/fake`
- `go/internal/agent/fake`

**Validation:**

```bash
cd go
go test ./internal/tracker ./internal/agent
```

Expected: interface packages compile and fake tests pass.

---

### Task 8: Implement orchestrator state machine

**Files:**
- Create: `go/internal/orchestrator/state.go`
- Create: `go/internal/orchestrator/events.go`
- Create: `go/internal/orchestrator/retry.go`
- Create: `go/internal/orchestrator/orchestrator.go`
- Test: `go/internal/orchestrator/*_test.go`

**Step 1: Implement single-authority state**

State fields:

- `poll_interval`
- `max_concurrent_agents`
- `running`
- `claimed`
- `retry_attempts`
- `completed`
- `agent_totals`
- `agent_rate_limits`

**Step 2: Implement event loop**

Handle events:

- `TickEvent`
- `WorkerExitEvent`
- `AgentUpdateEvent`
- `RefreshEvent`
- retry timer events

Only the orchestrator may mutate state.

**Step 3: Implement dispatch logic**

Dispatch must enforce:

- active state
- not terminal
- required labels for Linear
- not already running
- not already claimed
- global concurrency
- per-state concurrency
- Todo blocker rule
- priority sort order

**Step 4: Implement retry logic**

Retry behavior:

- normal worker exit schedules continuation retry after `1000ms`
- abnormal worker exit schedules exponential backoff
- backoff formula: `min(10000 * 2^(attempt - 1), max_retry_backoff_ms)`
- retry requeues with explicit error when no slots are available
- retry releases claim if issue is no longer eligible

**Step 5: Implement reconciliation**

On each tick:

1. stall detection
2. fetch running issue states
3. terminal state → terminate and cleanup workspace
4. non-active state → terminate without cleanup
5. active state → update issue snapshot

**Step 6: Write tests**

Test:

- no duplicate dispatch
- global concurrency
- per-state concurrency
- required labels
- Todo blocker filtering
- normal exit continuation retry
- abnormal exit exponential backoff
- backoff cap
- retry no-slot requeue
- terminal cleanup
- non-active cancellation without cleanup
- stall detection
- state refresh failure keeps workers running

Use fake tracker, fake agent runner, and fake clock.

**Validation:**

```bash
cd go
go test ./internal/orchestrator
go test -race ./internal/orchestrator
```

Expected: orchestrator tests pass with no races.

---

## Phase 7: Linear Tracker Adapter

### Task 9: Implement Linear client

**Files:**
- Create: `go/internal/tracker/linear/client.go`
- Create: `go/internal/tracker/linear/query.go`
- Create: `go/internal/tracker/linear/normalize.go`
- Test: `go/internal/tracker/linear/*_test.go`

**Step 1: Implement client**

Client fields:

- endpoint
- api key
- http client with 30s timeout
- project slug

**Step 2: Implement GraphQL operations**

Implement:

- candidate fetch with project filter `project: { slugId: { eq: $projectSlug } }`
- pagination with default page size 50
- state refresh by ID using variable type `[ID!]`
- terminal fetch for startup cleanup
- required label filtering after normalization

**Step 3: Implement normalization**

Normalize:

- labels to lowercase
- blockers from inverse relations of type `blocks`
- priority integer
- timestamps from ISO-8601
- issue ID and identifier

**Step 4: Write tests with `httptest.Server`**

Test:

- Authorization header
- project slug variable
- pagination order
- empty state list avoids API call
- GraphQL errors
- non-200 response
- malformed JSON
- label normalization
- blocker normalization
- state refresh by ID

**Validation:**

```bash
cd go
go test ./internal/tracker/linear
```

Expected: Linear adapter tests pass.

---

## Phase 8: Codex Agent Adapter

### Task 10: Implement Codex runner

**Files:**
- Create: `go/internal/agent/codex/runner.go`
- Create: `go/internal/agent/codex/protocol.go`
- Create: `go/internal/agent/codex/usage.go`
- Test: `go/internal/agent/codex/*_test.go`

**Step 1: Implement subprocess launch**

Launch:

```text
bash -lc <codex.command>
```

with:

- cwd = workspace path
- process group
- stdout/stderr separated
- max line size 10 MB
- request/response timeout
- turn timeout

**Step 2: Implement protocol adapter**

Keep Codex protocol parsing isolated.

Adapter responsibilities:

- session startup
- first prompt
- continuation prompt
- thread/turn identity extraction
- event forwarding
- usage extraction
- rate-limit extraction
- terminal turn detection
- subprocess exit handling

**Step 3: Implement policy pass-through**

Pass Codex policy fields through as `map[string]any` where possible. Do not hardcode unsupported Codex enums.

**Step 4: Write tests**

Test:

- launch command construction
- cwd validation
- process group cancellation
- event normalization
- usage extraction
- terminal event mapping
- timeout mapping

Use fake subprocesses or recorded protocol fixtures.

**Validation:**

```bash
cd go
go test ./internal/agent/codex
```

Expected: Codex adapter tests pass.

---

## Phase 9: CLI and Lifecycle

### Task 11: Implement CLI and service lifecycle

**Files:**
- Modify: `go/cmd/symphony/main.go`
- Create: `go/internal/app/app.go`
- Test: `go/cmd/symphony/*_test.go`

**Step 1: Implement CLI**

CLI must:

- accept optional positional workflow path
- default to `./WORKFLOW.md`
- support `--logs-root`
- support `--port` later
- exit nonzero on startup validation failure
- exit cleanly on normal shutdown

**Step 2: Implement startup**

Startup sequence:

1. configure logging
2. load workflow
3. resolve config
4. validate config
5. create workspace manager
6. create tracker adapter
7. create agent runner
8. create orchestrator
9. start workflow watcher
10. run startup terminal cleanup
11. schedule immediate tick

**Step 3: Implement reload**

Workflow watcher must:

- use `fsnotify`
- reload on `WORKFLOW.md` changes
- keep last good config on invalid reload
- log operator-visible reload errors

**Step 4: Implement graceful shutdown**

On SIGINT/SIGTERM:

- cancel poll loop
- cancel active workers
- wait for worker exits
- stop optional HTTP server
- exit cleanly

**Validation:**

```bash
cd go
go test ./cmd/symphony ./internal/app
go build ./cmd/symphony
```

Expected: CLI tests pass and binary builds.

---

## Phase 10: Optional HTTP Observability

### Task 12: Implement HTTP snapshot API

**Files:**
- Create: `go/internal/server/server.go`
- Test: `go/internal/server/*_test.go`

**Step 1: Implement server**

Server should be optional and only start when `--port` is provided or `server.port` is configured.

Endpoints:

- `GET /api/v1/state`
- `GET /api/v1/<issue_identifier>`
- `POST /api/v1/refresh`

**Step 2: Implement response shapes**

`GET /api/v1/state` returns:

- generated_at
- counts
- running sessions
- retry queue
- agent totals
- rate limits

`GET /api/v1/<issue_identifier>` returns:

- issue details
- workspace
- attempts
- running session
- retry
- recent events
- last error

`POST /api/v1/refresh` queues a best-effort immediate poll/reconcile cycle.

**Step 3: Write tests**

Test:

- state endpoint
- issue endpoint
- 404 for unknown issue
- refresh endpoint
- unsupported method returns 405
- JSON error envelope

**Validation:**

```bash
cd go
go test ./internal/server
```

Expected: HTTP server tests pass.

---

## Phase 11: End-to-End Validation

### Task 13: Add integration-style tests

**Files:**
- Create: `go/internal/app/app_test.go`
- Create: `go/testdata/workflows/*`
- Create: `go/testdata/fixtures/*`

**Step 1: Add fake end-to-end test**

Use:

- fake Linear tracker
- fake Codex runner
- temp workspace root
- temp workflow file

Test:

- service starts
- immediate tick dispatches eligible issue
- worker exit schedules continuation retry
- retry re-dispatches issue
- terminal state stops worker and cleans workspace

**Step 2: Add config reload test**

Test:

- valid reload changes poll interval
- invalid reload keeps last good config
- service does not crash on invalid reload

**Validation:**

```bash
cd go
go test ./internal/app
```

Expected: app integration-style tests pass.

---

## Phase 12: Real Integration Notes

### Task 14: Add real integration hooks

**Files:**
- Create: `go/README.md`
- Create: `go/AGENTS.md`
- Create: `go/.env.example`

**Step 1: Document runtime requirements**

Document:

- `LINEAR_API_KEY`
- `codex app-server`
- `WORKFLOW.md`
- workspace root
- trust boundary
- approval/sandbox policy
- how to run local tests
- how to run real Linear smoke test

**Step 2: Add optional real integration test**

Create a skipped-by-default test that requires:

- `LINEAR_API_KEY`
- network access
- real Codex binary

It should be skipped when credentials are absent.

**Validation:**

```bash
cd go
go test ./...
go test -run TestRealLinearSmoke -count=1
```

Expected: real integration test is skipped unless explicitly enabled.

---

## Final Validation

Run:

```bash
cd go
go fmt ./...
go test ./...
go test -race ./internal/orchestrator
go vet ./...
go build ./cmd/symphony
make all
```

Expected: all commands pass.

---

## Implementation Order Summary

1. Module and package skeleton
2. Domain model
3. Workflow loader
4. Config resolution and validation
5. Strict prompt renderer
6. Workspace manager and hooks
7. Structured logging
8. Tracker and agent interfaces with fakes
9. Orchestrator state machine
10. Linear tracker adapter
11. Codex agent adapter
12. CLI and lifecycle
13. Dynamic workflow reload
14. Optional HTTP observability
15. End-to-end validation
16. Real integration notes

## Out of Scope for First Pass

Do not implement these in the first pass:

- Beads adapter
- Pi RPC adapter
- SSH workers
- persistent retry queue
- tracker write APIs
- full dashboard UI
- `linear_graphql` client-side tool
- persistent retry/session metadata across restarts

These should be later extension PRs after the Linear + Codex core is stable.
