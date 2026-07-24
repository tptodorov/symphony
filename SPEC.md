# Symphony Service Specification

Status: Draft v1 (language-agnostic)

Purpose: Define a service that orchestrates coding agents to get project work done.

## Normative Language

The key words `MUST`, `MUST NOT`, `REQUIRED`, `SHOULD`, `SHOULD NOT`, `RECOMMENDED`, `MAY`, and
`OPTIONAL` in this document are to be interpreted as described in RFC 2119.

`Implementation-defined` means the behavior is part of the implementation contract, but this
specification does not prescribe one universal policy. Implementations MUST document the selected
behavior.

## 1. Problem Statement

Symphony is a long-running automation service that continuously reads work from an issue
tracker (Linear, Jira, or Beads in this specification version), creates an isolated workspace for
each issue, and runs a coding agent session for that issue inside the workspace.

The service solves four operational problems:

- It turns issue execution into a repeatable daemon workflow instead of manual scripts.
- It isolates agent execution in per-issue workspaces so agent commands run only inside per-issue
  workspace directories.
- It keeps the workflow policy in-repo (`WORKFLOW.md`) so teams version the agent prompt and runtime
  settings with their code.
- It provides enough observability to operate and debug multiple concurrent agent runs.

Implementations are expected to document their trust and safety posture explicitly. This
specification does not require a single approval, sandbox, or operator-confirmation policy; some
implementations target trusted environments with a high-trust configuration, while others require
stricter approvals or sandboxing.

Important boundary:

- Symphony is a scheduler/runner and tracker reader.
- Ticket writes (state transitions, comments, PR links) are typically performed by the coding agent
  using tools available in the workflow/runtime environment.
- A successful run can end at a workflow-defined handoff state (for example `Human Review`), not
  necessarily `Done`.

## 2. Goals and Non-Goals

### 2.1 Goals

- Poll the issue tracker on a fixed cadence and dispatch work with bounded concurrency.
- Maintain a single authoritative orchestrator state for dispatch, retries, and reconciliation.
- Create deterministic per-issue workspaces and preserve them across runs.
- Stop active runs when issue state changes make them ineligible.
- Recover from transient failures with exponential backoff.
- Load runtime behavior from a repository-owned `WORKFLOW.md` contract.
- Expose operator-visible observability (at minimum structured logs).
- Support tracker/filesystem-driven restart recovery without requiring a persistent database; exact
  in-memory scheduler state is not restored.

### 2.2 Non-Goals

- Rich web UI or multi-tenant control plane.
- Prescribing a specific dashboard or terminal UI implementation.
- General-purpose workflow engine or distributed job scheduler.
- Built-in business logic for how to edit tickets, PRs, or comments. (That logic lives in the
  workflow prompt and agent tooling.)
- Mandating strong sandbox controls beyond what the coding agent and host OS provide.
- Mandating a single default approval, sandbox, or operator-confirmation posture for all
  implementations.

## 3. System Overview

### 3.1 Main Components

1. `Workflow Loader`
   - Reads `WORKFLOW.md`.
   - Parses YAML front matter and prompt body.
   - Returns `{config, prompt_template}`.

2. `Config Layer`
   - Exposes typed getters for workflow config values.
   - Applies defaults and environment variable indirection.
   - Performs validation used by the orchestrator before dispatch.

3. `Issue Tracker Client`
   - Fetches candidate issues in active states.
   - Fetches current states for specific issue IDs (reconciliation).
   - Fetches terminal-state issues during startup cleanup.
   - Normalizes tracker payloads into a stable issue model.

4. `Orchestrator`
   - Owns the poll tick.
   - Owns the in-memory runtime state.
   - Decides which issues to dispatch, retry, stop, or release.
   - Tracks session metrics and retry queue state.

5. `Workspace Manager`
   - Maps issue identifiers to workspace paths.
   - Ensures per-issue workspace directories exist.
   - Runs workspace lifecycle hooks.
   - Cleans workspaces for terminal issues.

6. `Agent Runner`
   - Creates workspace.
   - Builds prompt from issue + workflow template.
   - Launches the selected coding-agent subprocess and protocol client.
   - Streams agent updates back to the orchestrator.

7. `Status Surface` (OPTIONAL)
   - Presents human-readable runtime status (for example terminal output, dashboard, or other
     operator-facing view).

8. `Logging`
   - Emits structured runtime logs to one or more configured sinks.

### 3.2 Abstraction Levels

Symphony is easiest to port when kept in these layers:

1. `Policy Layer` (repo-defined)
   - `WORKFLOW.md` prompt body.
   - Team-specific rules for ticket handling, validation, and handoff.

2. `Configuration Layer` (typed getters)
   - Parses front matter into typed runtime settings.
   - Handles defaults, environment tokens, and path normalization.

3. `Coordination Layer` (orchestrator)
   - Polling loop, issue eligibility, concurrency, retries, reconciliation.

4. `Execution Layer` (workspace + agent subprocess)
   - Filesystem lifecycle, workspace preparation, coding-agent protocol.

5. `Integration Layer` (tracker adapter)
   - API calls and normalization for tracker data.

6. `Observability Layer` (logs + OPTIONAL status surface)
   - Operator visibility into orchestrator and agent behavior.

### 3.3 External Dependencies

- Issue tracker API or CLI (Linear GraphQL API for `tracker.kind == "linear"`; Jira Cloud REST API
  for `tracker.kind == "jira"`; `bd` CLI for `tracker.kind == "beads"`).
- Local filesystem for workspaces and logs.
- OPTIONAL workspace population tooling (for example Git CLI, if used).
- Coding-agent executable that supports the targeted agent mode (`codex` app-server or `pi` RPC
  mode).
- Host environment authentication for the issue tracker and coding agent.

## 4. Core Domain Model

### 4.1 Entities

#### 4.1.1 Issue

Normalized issue record used by orchestration, prompt rendering, and observability output.

Fields:

- `id` (string)
  - Stable tracker-internal ID.
- `identifier` (string)
  - Human-readable ticket key (example: `ABC-123`).
- `title` (string)
- `description` (string or null)
- `priority` (integer or null)
  - Lower numbers are higher priority in dispatch sorting.
- `state` (string)
  - Current tracker state name.
- `branch_name` (string or null)
  - Tracker-provided branch metadata if available.
- `assignee` (string or null)
  - Tracker-provided assignee/owner/routing identity if available.
- `url` (string or null)
- `labels` (list of strings)
  - Normalized to lowercase. For Beads, tags are normalized into this field.
- `blocked_by` (list of blocker refs)
  - Each blocker ref contains:
    - `id` (string or null)
    - `identifier` (string or null)
    - `state` (string or null)
- `created_at` (timestamp or null)
- `updated_at` (timestamp or null)

#### 4.1.2 Workflow Definition

Parsed `WORKFLOW.md` payload:

- `config` (map)
  - YAML front matter root object.
- `prompt_template` (string)
  - Markdown body after front matter, trimmed.

#### 4.1.3 Service Config (Typed View)

Typed runtime values derived from `WorkflowDefinition.config` plus environment resolution.

Examples:

- poll interval
- workspace root
- active and terminal issue states
- concurrency limits
- coding-agent executable/args/timeouts
- workspace hooks

#### 4.1.4 Workspace

Filesystem workspace assigned to one issue identifier.

Fields (logical):

- `path` (absolute workspace path)
- `workspace_key` (sanitized issue identifier)
- `created_now` (boolean, used to gate `after_create` hook)

#### 4.1.5 Run Attempt

One execution attempt for one issue.

Fields (logical):

- `issue_id`
- `issue_identifier`
- `attempt` (integer or null, `null` for first run, `>=1` for retries/continuation)
- `workspace_path`
- `started_at`
- `status`
- `error` (OPTIONAL)

#### 4.1.6 Live Session (Agent Session Metadata)

State tracked while a coding-agent subprocess is running.

Fields:

- `session_id` (string, `<thread_id>-<turn_id>`)
- `thread_id` (string)
- `turn_id` (string)
- `agent_pid` (string or null)
- `last_agent_event` (string/enum or null)
- `last_agent_timestamp` (timestamp or null)
- `last_agent_message` (summarized payload)
- `agent_input_tokens` (integer)
- `agent_output_tokens` (integer)
- `agent_total_tokens` (integer)
- `last_reported_input_tokens` (integer)
- `last_reported_output_tokens` (integer)
- `last_reported_total_tokens` (integer)
- `turn_count` (integer)
  - Number of coding-agent turns started within the current worker lifetime.

#### 4.1.7 Retry Entry

Scheduled retry state for an issue.

Fields:

- `issue_id`
- `identifier` (best-effort human ID for status surfaces/logs)
- `attempt` (integer, 1-based for retry queue)
- `due_at_ms` (monotonic clock timestamp)
- `timer_handle` (runtime-specific timer reference)
- `error` (string or null)

#### 4.1.8 Orchestrator Runtime State

Single authoritative in-memory state owned by the orchestrator.

Fields:

- `poll_interval_ms` (current effective poll interval)
- `max_concurrent_agents` (current effective global concurrency limit)
- `running` (map `issue_id -> running entry`)
- `claimed` (set of issue IDs reserved/running/retrying)
- `retry_attempts` (map `issue_id -> RetryEntry`)
- `completed` (set of issue IDs; bookkeeping only, not dispatch gating)
- `agent_totals` (aggregate tokens + runtime seconds)
- `agent_rate_limits` (latest rate-limit snapshot from agent events)

### 4.2 Stable Identifiers and Normalization Rules

- `Issue ID`
  - Use for tracker lookups and internal map keys.
- `Issue Identifier`
  - Use for human-readable logs and workspace naming.
- `Workspace Key`
  - Derive from `issue.identifier` by replacing any character not in `[A-Za-z0-9._-]` with `_`.
  - Use the sanitized value for the workspace directory name.
- `Normalized Issue State`
  - Compare states after `lowercase`.
- `Session ID`
  - Compose from coding-agent `thread_id` and `turn_id` as `<thread_id>-<turn_id>`.

## 5. Workflow Specification (Repository Contract)

### 5.1 File Discovery and Path Resolution

Workflow file path precedence:

1. Explicit application/runtime setting (set by the CLI positional workflow path).
2. Default: `WORKFLOW.md` in the effective Symphony working directory.

The effective Symphony working directory defaults to the current process working directory.
The CLI MAY accept an explicit startup working directory. When provided, the runtime applies it
before resolving the default workflow path or any relative positional workflow path.

Loader behavior:

- If the file cannot be read, return `missing_workflow_file` error.
- The workflow file is expected to be repository-owned and version-controlled.

### 5.2 File Format

`WORKFLOW.md` is a Markdown file with OPTIONAL YAML front matter.

Design note:

- `WORKFLOW.md` SHOULD be self-contained enough to describe and run different workflows (prompt,
  runtime settings, hooks, and tracker selection/config) without requiring out-of-band
  service-specific configuration.

Parsing rules:

- If file starts with `---`, parse lines until the next `---` as YAML front matter.
- Remaining lines become the prompt body.
- If front matter is absent, treat the entire file as prompt body and use an empty config map.
- YAML front matter MUST decode to a map/object; non-map YAML is an error.
- Prompt body is trimmed before use.

Returned workflow object:

- `config`: front matter root object (not nested under a `config` key).
- `prompt_template`: trimmed Markdown body.

### 5.3 Front Matter Schema

Top-level keys:

- `project`
- `tracker`
- `prompt`
- `polling`
- `workspace`
- `hooks`
- `agent`
- `agent_kind`
- `codex`
- `pi`

Unknown keys SHOULD be ignored for forward compatibility.

Note:

- The workflow front matter is extensible. Extensions MAY define additional top-level keys without
  changing the core schema above.
- Extensions SHOULD document their field schema, defaults, validation rules, and whether changes
  apply dynamically or require restart.

The optional `project` object MAY contain `name` (string). If `project.name` is absent or resolves
to an empty string, implementations MUST derive the project name from the folder containing
`WORKFLOW.md`.

#### 5.3.1 `agent_kind` (string)

- Default: `codex`.
- Selects which coding-agent integration the runtime uses for this workflow.
- Current supported values:
  - `codex` — use the Codex app-server integration defined in Section 10.
  - `pi` — use the Pi RPC integration defined in Section 10.2.
- Changes to `agent_kind` generally require a new worker run because the underlying process runtime
  and protocol differ; implementations are not REQUIRED to hot-swap an in-flight agent from one
  integration to another.
- When `agent_kind` is absent, implementations MUST default to `codex` for backward compatibility
  with existing `WORKFLOW.md` files.

#### 5.3.2 `prompt` (object)

Fields:

- `include_files` (array of strings)
  - Default: `[]`.
  - Each value is a workspace-relative file path.
  - After workspace preparation hooks complete and before the coding agent is launched,
    implementations SHOULD read each existing file and append its bounded contents to the rendered
    workflow prompt.
  - Missing include files are skipped.
  - Include paths MUST NOT escape the per-issue workspace. Invalid include paths fail the current
    run attempt before the coding agent is launched.
  - Implementations SHOULD bound included content. A 64 KiB cap per file is sufficient.
  - Hooks MAY write files listed here, for example `.symphony/setup-packet.md`, to contribute
    deterministic setup context to the agent prompt without printing hook stdout into the prompt.

#### 5.3.3 `tracker` (object)

Fields:

- `kind` (string)
  - REQUIRED for dispatch.
  - Current supported values: `linear`, `jira`, `beads`.
- `endpoint` (string)
  - Default for `tracker.kind == "linear"`: `https://api.linear.app/graphql`.
  - REQUIRED for `tracker.kind == "jira"`; this is the Jira site base URL, for example
    `https://example.atlassian.net`.
  - MAY be a literal URL or `$VAR_NAME`; workflows MAY use any environment variable name, for
    example `$JIRA_URL`.
  - NOT used for `tracker.kind == "beads"`.
- `api_key` (string)
  - MAY be a literal token or `$VAR_NAME`.
  - Canonical environment variable for `tracker.kind == "linear"`: `LINEAR_API_KEY`.
  - For `tracker.kind == "jira"`, implementations MAY accept this as a compatibility alias for
    `tracker.api_token`, but `tracker.api_token` is the canonical Jira field.
  - NOT used for `tracker.kind == "beads"`.
  - If `$VAR_NAME` resolves to an empty string, treat the key as missing.
- `api_token` (string)
  - Jira API token for `tracker.kind == "jira"`.
  - MAY be a literal token or `$VAR_NAME`.
  - Canonical environment variable for `tracker.kind == "jira"`: `JIRA_API_TOKEN`.
  - REQUIRED for dispatch when `tracker.kind == "jira"`.
  - NOT used for `tracker.kind == "linear"` or `tracker.kind == "beads"`.
- `email` (string)
  - Jira account email for `tracker.kind == "jira"`.
  - MAY be a literal value or `$VAR_NAME`.
  - Canonical environment variable for `tracker.kind == "jira"`: `JIRA_EMAIL`.
  - Workflows MAY use any environment variable name, for example `$JIRA_USERNAME`.
  - REQUIRED for dispatch when `tracker.kind == "jira"`.
  - NOT used for `tracker.kind == "linear"` or `tracker.kind == "beads"`.
- `project_slug` (string)
  - REQUIRED for dispatch when `tracker.kind == "linear"`.
  - For `tracker.kind == "jira"`, implementations MUST accept this as a compatibility alias for
    `tracker.project_key` when `tracker.project_key` is absent.
  - NOT used for `tracker.kind == "beads"`.
- `project_key` (string)
  - Jira project key for `tracker.kind == "jira"` (example: `ABC`).
  - REQUIRED for default Jira candidate queries and project-scoped terminal-state cleanup.
  - `tracker.project_slug` MAY be used instead for workflow portability between Linear and Jira
    configurations, but `tracker.project_key` is the canonical Jira field.
  - If `tracker.jql` is configured, implementations MAY allow dispatch without `tracker.project_key`
    or `tracker.project_slug`, but they MUST document how startup terminal cleanup is scoped in that
    mode.
  - NOT used for `tracker.kind == "linear"` or `tracker.kind == "beads"`.
- `jql` (string)
  - OPTIONAL custom Jira candidate query for `tracker.kind == "jira"`.
  - When present, this query fully replaces the default Jira candidate query.
  - Candidate rows returned by the query are still filtered by the generic dispatch eligibility
    rules, including `tracker.active_states`, `tracker.terminal_states`, assignee, labels, and
    blocker readiness.
  - It does not replace state-refresh or terminal-cleanup queries unless an implementation documents
    an additional tracker-specific extension.
  - NOT used for `tracker.kind == "linear"` or `tracker.kind == "beads"`.
- `page_size` (integer)
  - Page size for paginated HTTP tracker requests.
  - Default: `50`.
  - Applies to `tracker.kind == "linear"` and `tracker.kind == "jira"`.
  - NOT used for `tracker.kind == "beads"`.
- `assignee` (string)
  - OPTIONAL.
  - If configured, an issue MUST be routed to this assignee/owner to dispatch or continue.
  - Matching ignores case and surrounding whitespace.
  - If a tracker adapter cannot return assignee/owner metadata, configuring this field MUST fail
    dispatch preflight validation for that tracker kind.
- `required_labels` (list of strings)
  - Default: `[]`.
  - An issue MUST contain every configured label/tag to dispatch or continue. Matching ignores case
    and surrounding whitespace. A blank configured label matches no issue.
  - For `tracker.kind == "linear"`, labels are Linear labels.
  - For `tracker.kind == "jira"`, labels are Jira labels.
  - For `tracker.kind == "beads"`, labels are Beads tags normalized into `issue.labels`.
- `active_states` (list of strings)
  - Default: `Todo`, `In Progress` for `linear` and `jira`; `open`, `in_progress` for `beads`.
  - Eligibility logic MUST use this configured list rather than hardcoding state-name checks.
- `terminal_states` (list of strings)
  - Default: `Closed`, `Cancelled`, `Canceled`, `Duplicate`, `Done` for `linear` and `jira`;
    `closed`, `tombstone` for `beads`.
  - Eligibility logic MUST use this configured list rather than hardcoding state-name checks.
- `bd_command` (string)
  - Shell command for the `bd` CLI.
  - Default: `bd`.
  - Applies only when `tracker.kind == "beads"`.
  - The runtime launches this command via `bash -lc` in the repository working directory (the
    directory containing `WORKFLOW.md`).

#### 5.3.3 `polling` (object)

Fields:

- `interval_ms` (integer)
  - Default: `30000`
  - Changes SHOULD be re-applied at runtime and affect future tick scheduling without restart.

#### 5.3.4 `workspace` (object)

Fields:

- `root` (path string or `$VAR`)
  - Default: `<system-temp>/symphony_workspaces`
  - `~` is expanded.
  - Relative paths are resolved relative to the directory containing `WORKFLOW.md`.
  - The effective workspace root is normalized to an absolute path before use.

#### 5.3.5 `hooks` (object)

Fields:

- `after_create` (multiline shell script string, OPTIONAL)
  - Runs during staged workspace preparation when the canonical workspace is missing or empty.
  - Failure aborts workspace creation.
- `before_run` (multiline shell script string, OPTIONAL)
  - Runs before each agent attempt after workspace preparation and before launching the coding
    agent.
  - Failure aborts the current attempt.
- `after_run` (multiline shell script string, OPTIONAL)
  - Runs after each agent attempt (success, failure, timeout, or cancellation) once the workspace
    exists.
  - Failure is logged but ignored.
- `before_remove` (multiline shell script string, OPTIONAL)
  - Runs before workspace deletion if the directory exists.
  - Failure is logged but ignored; cleanup still proceeds.
- `timeout_ms` (integer, OPTIONAL)
  - Default: `60000`
  - Applies to all workspace hooks.
  - Invalid values fail configuration validation.
  - Changes SHOULD be re-applied at runtime for future hook executions.

#### 5.3.6 `agent` (object)

Fields:

- `max_concurrent_agents` (integer)
  - Default: `10`
  - Changes SHOULD be re-applied at runtime and affect subsequent dispatch decisions.
- `max_turns` (positive integer)
  - Default: `20`
  - Limits the number of coding-agent turns within one worker session.
  - Invalid values fail configuration validation.
- `max_retry_backoff_ms` (integer)
  - Default: `300000` (5 minutes)
  - Changes SHOULD be re-applied at runtime and affect future retry scheduling.
- `max_concurrent_agents_by_state` (map `state_name -> positive integer`)
  - Default: empty map.
  - State keys are normalized (`lowercase`) for lookup.
  - Invalid entries (non-positive or non-numeric) are ignored.

#### 5.3.7 `codex` (object)

Fields:

For Codex-owned config values such as `approval_policy`, `thread_sandbox`, and
`turn_sandbox_policy`, supported values are defined by the targeted Codex app-server version.
Implementors SHOULD treat them as pass-through Codex config values rather than relying on a
hand-maintained enum in this spec. To inspect the installed Codex schema, run
`codex app-server generate-json-schema --out <dir>` and inspect the relevant definitions referenced
by `v2/ThreadStartParams.json` and `v2/TurnStartParams.json`. Implementations MAY validate these
fields locally if they want stricter startup checks.

- `command` (string shell command)
  - Default: `codex app-server`
  - The runtime launches this command via `bash -lc` in the workspace directory.
  - The launched process MUST speak a compatible app-server protocol over stdio.
- `approval_policy` (Codex `AskForApproval` value)
  - Default: implementation-defined.
- `thread_sandbox` (Codex `SandboxMode` value)
  - Default: implementation-defined.
- `turn_sandbox_policy` (Codex `SandboxPolicy` value)
  - Default: implementation-defined.
- `turn_timeout_ms` (integer)
  - Default: `3600000` (1 hour)
- `read_timeout_ms` (integer)
  - Default: `5000`
- `stall_timeout_ms` (integer)
  - Default: `300000` (5 minutes)
  - If `<= 0`, stall detection is disabled.

#### 5.3.8 `pi` (object)

Fields:

The `pi` section configures the Pi coding-agent integration and is used only when
`agent_kind == "pi"`. Implementations MUST ignore these fields when `agent_kind == "codex"`.

- `command` (string shell command)
  - Default: `pi --mode rpc --no-session`.
  - The runtime launches this command via `bash -lc` in the workspace directory.
  - The launched process MUST speak the Pi RPC protocol over stdio.
  - The command is the complete Pi invocation. Implementations SHOULD include `--mode rpc` and
    SHOULD include `--no-session` unless they explicitly manage persistent Pi sessions across
    worker runs.
- `provider` (string)
  - Default: implementation-defined.
  - Passed to Pi as `--provider` when starting RPC mode.
  - Maps to Pi provider identifiers such as `anthropic`, `openai`, `google`, or custom provider
    names.
- `model` (string)
  - Default: implementation-defined.
  - Either a model ID or a provider/model pattern accepted by Pi
    (for example `anthropic/claude-sonnet-4-20250514` or `openai/gpt-4o`).
  - If omitted, Pi uses its own default model selection rules.
- `approval_policy` (string or map)
  - Default: implementation-defined.
  - Controls how Pi handles the extension UI approval protocol in RPC mode.
  - Implementations MAY define accepted string values (for example `auto`, `operator`, `strict`) or
    a structured policy object. The implementation MUST document the accepted shape.
- `session_sync` (string)
  - Default: `none`.
  - Controls whether Pi session data is synchronized after the worker run.
  - Accepted values:
    - `none` — do not sync or export session data.
    - `export` — export the session to an implementation-defined location or format after the run.
    - `sync` — synchronize session data with a remote destination if configured.
  - Implementations MUST document what `export` and `sync` do and where data is stored.
- `read_timeout_ms` (integer)
  - Default: `5000`.
  - Request/response timeout used during Pi RPC startup and synchronous commands.
- `turn_timeout_ms` (integer)
  - Default: `3600000` (1 hour).
  - Total turn stream timeout for a single Pi agent turn or continuation turn.
- `stall_timeout_ms` (integer)
  - Default: `300000` (5 minutes).
  - If `<= 0`, stall detection is disabled.
  - Elapsed time is computed from the last Pi event timestamp or from the worker start time when
    no event has been received yet.

For Pi-owned config values such as `provider`, `model`, and extension UI behavior, supported values
are defined by the targeted Pi version. Implementations SHOULD consult the Pi documentation or
generated schema instead of treating this specification as a protocol schema. If this specification
appears to conflict with the Pi protocol, the Pi protocol controls protocol shape and behavior.

### 5.4 Prompt Template Contract

The Markdown body of `WORKFLOW.md` is the per-issue prompt template.

Rendering requirements:

- Use a strict template engine (Liquid-compatible semantics are sufficient).
- Unknown variables MUST fail rendering.
- Unknown filters MUST fail rendering.

Template input variables:

- `issue` (object)
  - Includes all normalized issue fields, including labels and blockers.
- `attempt` (integer or null)
  - `null`/absent on first attempt.
  - Integer on retry or continuation run.

Prompt include files:

- `prompt.include_files` is evaluated after hook execution, so hooks can generate include files.
- Include files are appended to the rendered prompt for future agent launches.
- Include files are context only; they do not change tracker state, workspace state, or retry
  decisions except when an include path is invalid or an existing include file cannot be read.

Fallback prompt behavior:

- If the workflow prompt body is empty, the runtime MAY use a minimal default prompt
  (`You are working on an issue from the configured tracker.`).
- Workflow file read/parse failures are configuration/validation errors and SHOULD NOT silently fall
  back to a prompt.

### 5.5 Workflow Validation and Error Surface

Error classes:

- `missing_workflow_file`
- `workflow_parse_error`
- `workflow_front_matter_not_a_map`
- `template_parse_error` (during prompt rendering)
- `template_render_error` (unknown variable/filter, invalid interpolation)

Dispatch gating behavior:

- Workflow file read/YAML errors block new dispatches until fixed.
- Template errors fail only the affected run attempt.

## 6. Configuration Specification

### 6.1 Configuration Resolution Pipeline

Configuration is resolved in this order:

1. Select the workflow file path (explicit runtime path, otherwise effective working directory
   default).
2. Parse YAML front matter into a raw config map.
3. Apply built-in defaults for missing OPTIONAL fields.
4. Resolve `$VAR_NAME` indirection only for config values that explicitly contain `$VAR_NAME`.
5. Coerce and validate typed values.

Environment variables do not globally override YAML values. They are used only when a config value
explicitly references them.

Value coercion semantics:

- All string config values support `$VAR` expansion when they explicitly reference environment
  variables.
- `~` home expansion applies to filesystem path values.
- Do not apply expansion to values that do not contain `$VAR_NAME` references.
- Relative `workspace.root` values resolve relative to the directory containing the selected
  `WORKFLOW.md`.

### 6.2 Dynamic Reload Semantics

Dynamic reload is REQUIRED:

- The software MUST detect `WORKFLOW.md` changes.
- On change, it MUST re-read and re-apply workflow config and prompt template without restart.
- The software MUST attempt to adjust live behavior to the new config (for example polling
  cadence, concurrency limits, active/terminal states, codex settings, workspace paths/hooks, and
  prompt content for future runs).
- Reloaded config applies to future dispatch, retry scheduling, reconciliation decisions, hook
  execution, and agent launches.
- Implementations are not REQUIRED to restart in-flight agent sessions automatically when config
  changes.
- Extensions that manage their own listeners/resources (for example an HTTP server port change) MAY
  require restart unless the implementation explicitly supports live rebind.
- Implementations SHOULD also re-validate/reload defensively during runtime operations (for example
  before dispatch) in case filesystem watch events are missed.
- Invalid reloads MUST NOT crash the service; keep operating with the last known good effective
  configuration and emit an operator-visible error.

### 6.3 Dispatch Preflight Validation

This validation is a scheduler preflight run before attempting to dispatch new work. It validates
the workflow/config needed to poll and launch workers, not a full audit of all possible workflow
behavior.

Startup validation:

- Validate configuration before starting the scheduling loop.
- If startup validation fails, fail startup and emit an operator-visible error.

Per-tick dispatch validation:

- Re-validate before each dispatch cycle.
- If validation fails, skip dispatch for that tick, keep reconciliation active, and emit an
  operator-visible error.

Validation checks:

- Workflow file can be loaded and parsed.
- `tracker.kind` is present and supported.
- `tracker.api_key` is present after `$` resolution when `tracker.kind == "linear"`.
- `tracker.endpoint`, `tracker.email`, and `tracker.api_token` are present after `$` resolution when
  `tracker.kind == "jira"`.
- `tracker.project_slug` is present when REQUIRED by the selected tracker kind
  (`tracker.kind == "linear"` requires it).
- `tracker.project_key` or `tracker.project_slug` is present when REQUIRED by the selected tracker
  kind (`tracker.kind == "jira"` requires one of them for default candidate queries and
  project-scoped terminal cleanup; implementations that allow `tracker.jql` without either field
  MUST document the terminal-cleanup scope).
- `tracker.bd_command` is present and non-empty when `tracker.kind == "beads"`.
- `bd` CLI is reachable when `tracker.kind == "beads"` (for example `tracker.bd_command --version`).
- If `tracker.assignee` is configured, the selected tracker adapter can return comparable
  assignee/owner metadata.
- `codex.command` is present and non-empty when `agent_kind == "codex"`.
- `pi.command` is present and non-empty when `agent_kind == "pi"`.
- Pi CLI is reachable when `agent_kind == "pi"` (for example `pi.command --version` or `pi.command --help`).

### 6.4 Core Config Fields Summary (Cheat Sheet)

This section is intentionally redundant so a coding agent can implement the config layer quickly.
Extension fields are documented in the extension section that defines them. Core conformance does
not require recognizing or validating extension fields unless that extension is implemented.

- `tracker.kind`: string, REQUIRED, currently `linear`, `jira`, or `beads`
- For `tracker.kind=linear`:
  - `tracker.endpoint`: string, default `https://api.linear.app/graphql`
  - `tracker.api_key`: string or `$VAR`, canonical env `LINEAR_API_KEY`
  - `tracker.project_slug`: string, REQUIRED
- For `tracker.kind=jira`:
  - `tracker.endpoint`: string or `$VAR`, REQUIRED, Jira site base URL
  - `tracker.email`: string or `$VAR`, REQUIRED, canonical env `JIRA_EMAIL`
  - `tracker.api_token`: string or `$VAR`, REQUIRED, canonical env `JIRA_API_TOKEN`
  - `tracker.api_key`: optional compatibility alias for `tracker.api_token`
  - `tracker.project_key`: string, canonical Jira project scope, REQUIRED for default candidate
    queries and project-scoped terminal cleanup unless `tracker.project_slug` is provided
  - `tracker.project_slug`: optional compatibility alias for `tracker.project_key` when
    `tracker.project_key` is absent
  - `tracker.jql`: optional string; when present, replaces the default candidate query
  - `tracker.page_size`: integer, default `50`
- For `tracker.kind=beads`:
  - `tracker.bd_command`: shell command string, default `bd`
  - `tracker.endpoint`, `tracker.api_key`, and `tracker.project_slug` are NOT used and SHOULD be
    ignored if present
- Common tracker fields:
  - `tracker.assignee`: optional string; when set, candidates must match tracker assignee/owner
  - `tracker.required_labels`: list of strings, default `[]` (Linear labels, Jira labels, or Beads
    tags)
  - `tracker.active_states`: list of strings, default `["Todo", "In Progress"]` for Linear and
    Jira, `["open", "in_progress"]` for Beads
  - `tracker.terminal_states`: list of strings, default `["Closed", "Cancelled", "Canceled",
    "Duplicate", "Done"]` for Linear and Jira, `["closed", "tombstone"]` for Beads
- `polling.interval_ms`: integer, default `30000`
- `workspace.root`: path resolved to absolute, default `<system-temp>/symphony_workspaces`
- `hooks.after_create`: shell script or null
- `hooks.before_run`: shell script or null
- `hooks.after_run`: shell script or null
- `hooks.before_remove`: shell script or null
- `hooks.timeout_ms`: integer, default `60000`
- `agent.max_concurrent_agents`: integer, default `10`
- `agent.max_turns`: integer, default `20`
- `agent.max_retry_backoff_ms`: integer, default `300000` (5m)
- `agent.max_concurrent_agents_by_state`: map of positive integers, default `{}`
- `codex.command`: shell command string, default `codex app-server`
- `codex.approval_policy`: Codex `AskForApproval` value, default implementation-defined
- `codex.thread_sandbox`: Codex `SandboxMode` value, default implementation-defined
- `codex.turn_sandbox_policy`: Codex `SandboxPolicy` value, default implementation-defined
- `codex.turn_timeout_ms`: integer, default `3600000`
- `codex.read_timeout_ms`: integer, default `5000`
- `codex.stall_timeout_ms`: integer, default `300000`
- `agent_kind`: string, default `codex`; supported values are `codex` and `pi`
- `pi.command`: shell command string, default `pi --mode rpc --no-session`; used when `agent_kind == "pi"`
- `pi.provider`: string, default implementation-defined; used when `agent_kind == "pi"`
- `pi.model`: string, default implementation-defined; used when `agent_kind == "pi"`
- `pi.approval_policy`: string or map, default implementation-defined; used when `agent_kind == "pi"`
- `pi.session_sync`: string, default `none`; used when `agent_kind == "pi"`
- `pi.read_timeout_ms`: integer, default `5000`; used when `agent_kind == "pi"`
- `pi.turn_timeout_ms`: integer, default `3600000`; used when `agent_kind == "pi"`
- `pi.stall_timeout_ms`: integer, default `300000`; used when `agent_kind == "pi"`

## 7. Orchestration State Machine

The orchestrator is the only component that mutates scheduling state. All worker outcomes are
reported back to it and converted into explicit state transitions.

### 7.1 Issue Orchestration States

This is not the same as tracker states (`Todo`, `In Progress`, etc.). This is the service's internal
claim state.

1. `Unclaimed`
   - Issue is not running and has no retry scheduled.

2. `Claimed`
   - Orchestrator has reserved the issue to prevent duplicate dispatch.
   - In practice, claimed issues are either `Running` or `RetryQueued`.

3. `Running`
   - Worker task exists and the issue is tracked in `running` map.

4. `RetryQueued`
   - Worker is not running, but a retry timer exists in `retry_attempts`.

5. `Released`
   - Claim removed because issue is terminal, non-active, missing, or retry path completed without
     re-dispatch.

Important nuance:

- A successful worker exit does not mean the issue is done forever.
- The worker MAY continue through multiple back-to-back coding-agent turns before it exits.
- After each normal turn completion, the worker re-checks the tracker issue state.
- If the issue is still in an active state, the worker SHOULD start another turn on the same live
  coding-agent thread in the same workspace, up to `agent.max_turns`.
- The first turn SHOULD use the full rendered task prompt.
- Continuation turns SHOULD send only continuation guidance to the existing thread, not resend the
  original task prompt that is already present in thread history.
- Once the worker exits normally, the orchestrator still schedules a short continuation retry
  (about 1 second) so it can re-check whether the issue remains active and needs another worker
  session.

### 7.2 Run Attempt Lifecycle

A run attempt transitions through these phases:

1. `PreparingWorkspace`
2. `BuildingPrompt`
3. `LaunchingAgentProcess`
4. `InitializingSession`
5. `StreamingTurn`
6. `Finishing`
7. `Succeeded`
8. `Failed`
9. `TimedOut`
10. `Stalled`
11. `CanceledByReconciliation`

Distinct terminal reasons are important because retry logic and logs differ.

### 7.3 Transition Triggers

- `Poll Tick`
  - Reconcile active runs.
  - Validate config.
  - Fetch candidate issues.
  - Dispatch until slots are exhausted.

- `Worker Exit (normal)`
  - Remove running entry.
  - Update aggregate runtime totals.
  - Schedule continuation retry (attempt `1`) after the worker exhausts or finishes its in-process
    turn loop.

- `Worker Exit (abnormal)`
  - Remove running entry.
  - Update aggregate runtime totals.
  - Schedule exponential-backoff retry.

- `Codex Update Event`
  - Update live session fields, token counters, and rate limits.

- `Retry Timer Fired`
  - Re-fetch active candidates and attempt re-dispatch, or release claim if no longer eligible.

- `Reconciliation State Refresh`
  - Stop runs whose issue states are terminal or no longer active.

- `Stall Timeout`
  - Kill worker and schedule retry.

### 7.4 Idempotency and Recovery Rules

- The orchestrator serializes state mutations through one authority to avoid duplicate dispatch.
- `claimed` and `running` checks are REQUIRED before launching any worker.
- The orchestrator MUST add the issue to `claimed` before spawning the worker; if worker spawn fails,
  it MUST remove that claim before scheduling the retry.
- Reconciliation runs before dispatch on every tick.
- Restart recovery is tracker-driven and filesystem-driven (without a durable orchestrator DB).
- Startup terminal cleanup removes stale workspaces for issues already in terminal states.

## 8. Polling, Scheduling, and Reconciliation

### 8.1 Poll Loop

At startup, the service validates config, performs startup cleanup, schedules an immediate tick, and
then repeats every `polling.interval_ms`.

The effective poll interval SHOULD be updated when workflow config changes are re-applied.

Tick sequence:

1. Reconcile running issues.
2. Run dispatch preflight validation.
3. Fetch candidate issues from tracker using active states.
4. Sort issues by dispatch priority.
5. Dispatch eligible issues while slots remain.
6. Notify observability/status consumers of state changes.

If per-tick validation fails, dispatch is skipped for that tick, but reconciliation still happens
first.

### 8.2 Candidate Selection Rules

An issue is dispatch-eligible only if all are true:

- It has `id`, `identifier`, `title`, and `state`.
- Its state is in `active_states` and not in `terminal_states`.
- If `tracker.assignee` is configured, it is routed to that assignee/owner.
- It contains every label/tag in `tracker.required_labels` when non-empty.
  - For `tracker.kind == "linear"`, labels are Linear labels.
  - For `tracker.kind == "beads"`, labels are Beads tags.
- It is not already in `running`.
- It is not already in `claimed`.
- Global concurrency slots are available.
- Per-state concurrency slots are available.
- Blocker readiness passes:
  - Do not dispatch when any blocker is absent from `terminal_states`.

Sorting order (stable intent):

1. `priority` ascending (1..4 are preferred; null/unknown sorts last)
2. `created_at` oldest first
3. `identifier` lexicographic tie-breaker

### 8.3 Concurrency Control

Global limit:

- `available_slots = max(max_concurrent_agents - running_count, 0)`

Per-state limit:

- `max_concurrent_agents_by_state[state]` if present (state key normalized)
- otherwise fallback to global limit

The runtime counts issues by their current tracked state in the `running` map.

### 8.4 Retry and Backoff

Retry entry creation:

- Cancel any existing retry timer for the same issue.
- Store `attempt`, `identifier`, `error`, `due_at_ms`, and new timer handle.

Backoff formula:

- Normal continuation retries after a clean worker exit use a short fixed delay of `1000` ms.
- Failure-driven retries use `delay = min(10000 * 2^(attempt - 1), agent.max_retry_backoff_ms)`.
- Power is capped by the configured max retry backoff (default `300000` / 5m).

Retry handling behavior:

1. Fetch active candidate issues (not all issues).
2. Find the specific issue by `issue_id`.
3. If not found, release claim.
4. If found and still candidate-eligible:
   - Dispatch if slots are available.
   - Otherwise requeue with error `no available orchestrator slots`.
5. If found but no longer active, release claim.

Note:

- Terminal-state workspace cleanup is handled by startup cleanup and active-run reconciliation
  (including terminal transitions for currently running issues).
- Retry handling mainly operates on active candidates and releases claims when the issue is absent,
  rather than performing terminal cleanup itself.

### 8.5 Active Run Reconciliation

Reconciliation runs every tick and has two parts.

Part A: Stall detection

- For each running issue, compute `elapsed_ms` since:
  - `last_agent_event_timestamp` if any event has been seen, else
  - `started_at`
- For `agent_kind == "codex"`: if `elapsed_ms > codex.stall_timeout_ms`, terminate the worker and queue
  a retry.
- For `agent_kind == "pi"`: if `elapsed_ms > pi.stall_timeout_ms`, terminate the worker and queue a
  retry.
- The stall timeout field for the active agent kind is used; if that timeout is `<= 0`, stall
  detection for that kind is disabled.

Part B: Tracker state refresh

- Fetch current issue states for all running issue IDs.
- For each running issue:
  - If tracker state is terminal: terminate worker and clean workspace.
  - If tracker state is still active: update the in-memory issue snapshot.
  - If tracker state is neither active nor terminal: terminate worker without workspace cleanup.
- If state refresh fails, keep workers running and try again on the next tick.

### 8.6 Startup Workspace Cleanup

When the service starts:

1. Remove entries older than 24 hours from `<workspace.root>/.preparing` and
   `<workspace.root>/.failed`.
2. List existing canonical workspace directory names under `workspace.root`, excluding `.preparing`
   and `.failed`.
3. If the selected tracker supports state refresh by issue identifier, fetch current states for those
   existing workspace identifiers and remove only the corresponding canonical workspaces whose
   refreshed state is in `tracker.terminal_states`.
4. If the selected tracker does not support identifier refresh, query tracker issues in
   `tracker.terminal_states` and remove the corresponding canonical workspace directories.
5. If workspace listing or tracker fetch fails, log a warning and continue startup.

Startup cleanup is conservative. A workspace is preserved when the tracker reports the issue as
active, reports it as non-active but non-terminal, omits it from the refresh result, or cannot be
queried. This prevents stale terminal workspaces and abandoned preparation artifacts from
accumulating after restarts without deleting workspaces that might still be useful for retries or
diagnosis.

## 9. Workspace Management and Safety

### 9.1 Workspace Layout

Workspace root:

- `workspace.root` (normalized absolute path)

Per-issue workspace path:

- `<workspace.root>/<sanitized_issue_identifier>`

Reserved internal workspace paths:

- `<workspace.root>/.preparing/<sanitized_issue_identifier>-<unique_suffix>`
- `<workspace.root>/.failed/<sanitized_issue_identifier>-<unique_suffix>`
- These directories MUST NOT be treated as issue identifiers during workspace listing or startup
  terminal cleanup.

Workspace persistence:

- Workspaces are reused across runs for the same issue.
- Successful runs do not auto-delete workspaces.
- Failed runs and retries do not auto-delete non-empty canonical workspaces.
- Empty canonical workspaces are considered unprepared when `hooks.after_create` is configured.

### 9.2 Workspace Creation and Reuse

Input: `issue.identifier`

Algorithm summary:

1. Sanitize identifier to `workspace_key`.
2. Compute canonical workspace path `<workspace.root>/<workspace_key>`.
3. If the canonical workspace exists and is a non-empty directory, reuse it and do not run
   `hooks.after_create`.
4. If the canonical workspace exists and is an empty directory while `hooks.after_create` is
   configured, treat it as unprepared.
5. If no `hooks.after_create` hook is configured, create or reuse the canonical workspace directly.
6. If `hooks.after_create` is configured and the canonical workspace is missing or empty, create a
   staging workspace under `<workspace.root>/.preparing`.
7. Run `hooks.after_create` in the staging workspace.
8. On success, promote the staging workspace to the canonical workspace path and use that canonical
   workspace for the run.
9. On failure, move the staging workspace to `<workspace.root>/.failed`, fail the current attempt,
   and do not dispatch the coding agent.
10. Write `prepare-error.txt` in the retained failed workspace with the bounded `after_create` error
    details.

Notes:

- This section does not assume any specific repository/VCS workflow.
- Workspace preparation beyond directory creation (for example dependency bootstrap, checkout/sync,
  code generation) is implementation-defined and is typically handled via hooks.
- Promotion SHOULD be an atomic rename within the same workspace root when the host filesystem
  supports it.
- Before a new staging preparation, implementations SHOULD remove entries older than 24 hours from
  `.preparing` and `.failed`.

### 9.3 OPTIONAL Workspace Population (Implementation-Defined)

The spec does not require any built-in VCS or repository bootstrap behavior.

Implementations MAY populate or synchronize the workspace using implementation-defined logic and/or
hooks (for example `after_create` and/or `before_run`).

Failure handling:

- Workspace population/synchronization failures return an error for the current attempt.
- If `hooks.after_create` fails in a staging workspace, retain the failed staging workspace under
  `<workspace.root>/.failed` for diagnosis and include that path in logs or status where possible.
- Retained failed staging workspaces MUST contain `prepare-error.txt` with the hook failure summary
  so failures can be diagnosed even when the hook failed before writing any repository files.
- Failed staging workspaces older than 24 hours are cleaned up automatically.
- Reused non-empty canonical workspaces are not destructively reset on population failure. A
  `hooks.before_run` failure, agent failure, timeout, or retry preserves the canonical workspace
  unless the issue later reaches a configured terminal state.

### 9.4 Workspace Hooks

Supported hooks:

- `hooks.after_create`
- `hooks.before_run`
- `hooks.after_run`
- `hooks.before_remove`

Execution contract:

- Execute in a local shell context appropriate to the host OS, with the workspace directory as
  `cwd`.
- On POSIX systems, `sh -lc <script>` (or a stricter equivalent such as `bash -lc <script>`) is a
  conforming default.
- Hooks inherit the Symphony process environment.
- Symphony-defined hook environment variables MUST use the `SYMPHONY_` prefix. Implementations MUST
  NOT synthesize non-`SYMPHONY_` aliases such as `SOURCE_DIR`; workflows that need tool-specific
  variable names can assign them inside the hook script.
- `SYMPHONY_WORKDIR` MUST be set to the effective absolute working directory after applying the
  runtime `-workdir` option. Workflows MAY use it to locate the source checkout that owns
  `WORKFLOW.md`.
- Hook processes MUST receive the same base environment for every supported hook.
- Hook timeout uses `hooks.timeout_ms`; default: `60000 ms`.
- Log hook start, failures, and timeouts.

Failure semantics:

- `after_create` failure or timeout is fatal to workspace creation. The failed staging workspace is
  retained under `.failed` and the canonical workspace is not promoted.
- `before_run` failure or timeout is fatal to the current run attempt.
- `after_run` failure or timeout is logged and ignored.
- `before_remove` failure or timeout is logged and ignored.

### 9.5 Safety Invariants

This is the most important portability constraint.

Invariant 1: Run the coding agent only in the per-issue workspace path.

- Before launching the coding-agent subprocess, validate:
  - `cwd == workspace_path`

Invariant 2: Workspace path MUST stay inside workspace root.

- Normalize both paths to absolute.
- Require `workspace_path` to have `workspace_root` as a prefix directory.
- Reject any path outside the workspace root.

Invariant 3: Workspace key is sanitized.

- Only `[A-Za-z0-9._-]` allowed in workspace directory names.
- Replace all other characters with `_`.

## 10. Agent Runner Protocol (Coding Agent Integration)

This section defines Symphony's language-neutral responsibilities when integrating a coding-agent
runtime. Two integrations are defined in this version:

1. `codex` — the Codex app-server integration (Section 10.1).
2. `pi` — the Pi RPC integration (Section 10.2).

The targeted coding-agent protocol for the selected `agent_kind` is the source of truth for
protocol schemas, message payloads, transport framing, and method names.

Protocol source of truth:

- Implementations MUST send messages that are valid for the targeted coding-agent protocol version.
- Implementations MUST consult the targeted coding-agent documentation or generated schema instead
  of treating this specification as a protocol schema.
- If this specification appears to conflict with the targeted coding-agent protocol, that protocol
  controls protocol shape and transport behavior.
- Symphony-specific requirements in this section still control orchestration behavior, workspace
  selection, prompt construction, continuation handling, and observability extraction.

### 10.1 Codex App-Server Integration

This section applies when `agent_kind == "codex"`.

#### 10.1.1 Launch Contract

Subprocess launch parameters:

- Command: `codex.command`
- Invocation: `bash -lc <codex.command>`
- Working directory: workspace path
- Transport/framing: the protocol transport required by the targeted Codex app-server version

Notes:

- The default command is `codex app-server`.
- Approval policy, sandbox policy, cwd, prompt input, and OPTIONAL tool declarations are supplied
  using fields supported by the targeted Codex app-server version.

RECOMMENDED additional process settings:

- Max line size: 10 MB (for safe buffering)

#### 10.1.2 Session Startup Responsibilities

Reference: https://developers.openai.com/codex/app-server/

Startup MUST follow the targeted Codex app-server contract. Symphony additionally requires the
client to:

- Start the app-server subprocess in the per-issue workspace.
- Initialize the app-server session using the targeted Codex app-server protocol.
- Create or resume a coding-agent thread according to the targeted protocol.
- Supply the absolute per-issue workspace path as the thread/turn working directory wherever the
  targeted protocol accepts cwd.
- Start the first turn with the rendered issue prompt.
- Start later in-worker continuation turns on the same live thread with continuation guidance rather
  than resending the original issue prompt.
- Supply the implementation's documented approval and sandbox policy using fields supported by the
  targeted protocol.
- Include issue-identifying metadata, such as `<issue.identifier>: <issue.title>`, when the targeted
  protocol supports turn or session titles.
- Advertise implemented client-side tools using the targeted protocol.

Session identifiers:

- Extract `thread_id` from the thread identity returned by the targeted Codex app-server protocol.
- Extract `turn_id` from each turn identity returned by the targeted Codex app-server protocol.
- Emit `session_id = "<thread_id>-<turn_id>"`
- Reuse the same `thread_id` for all continuation turns inside one worker run

#### 10.1.3 Streaming Turn Processing

The client processes app-server updates according to the targeted Codex app-server protocol until
the active turn terminates.

Completion conditions:

- Targeted-protocol turn completion signal -> success
- Targeted-protocol turn failure signal -> failure
- Targeted-protocol turn cancellation signal -> failure
- turn timeout (`codex.turn_timeout_ms`) -> failure
- subprocess exit -> failure

Continuation processing:

- If the worker decides to continue after a successful turn, it SHOULD start another turn on the same
  live thread using the targeted protocol.
- The app-server subprocess SHOULD remain alive across those continuation turns and be stopped only
  when the worker run is ending.

Transport handling requirements:

- Follow the transport and framing rules of the targeted Codex app-server version.
- For stdio-based transports, keep protocol stream handling separate from diagnostic stderr
  handling unless the targeted protocol specifies otherwise.

#### 10.1.4 Emitted Runtime Events (Upstream to Orchestrator)

The app-server client emits structured events to the orchestrator callback. Each event SHOULD
include:

- `event` (enum/string)
- `timestamp` (UTC timestamp)
- `agent_pid` (if available)
- OPTIONAL `usage` map (token counts)
- payload fields as needed

Important emitted events include, for example:

- `session_started`
- `startup_failed`
- `turn_completed`
- `turn_failed`
- `turn_cancelled`
- `turn_ended_with_error`
- `turn_input_required`
- `approval_auto_approved`
- `unsupported_tool_call`
- `notification`
- `other_message`
- `malformed`

#### 10.1.5 Timeouts and Error Mapping

Timeouts:

- `codex.read_timeout_ms`: request/response timeout during startup and sync requests
- `codex.turn_timeout_ms`: total turn stream timeout
- `codex.stall_timeout_ms`: enforced by orchestrator based on event inactivity

Error mapping (RECOMMENDED normalized categories):

- `codex_not_found`
- `invalid_workspace_cwd`
- `response_timeout`
- `turn_timeout`
- `port_exit`
- `response_error`
- `turn_failed`
- `turn_cancelled`
- `turn_input_required`

### 10.2 Pi RPC Integration

This section applies when `agent_kind == "pi"`.

Reference: https://pi.dev/docs/latest/rpc

Protocol overview:

- Pi RPC mode enables headless operation of the coding agent via a JSON protocol over stdin/stdout.
- Commands are JSON objects sent to stdin, one per line.
- Responses are JSON objects with `type: "response"` indicating command success or failure.
- Events are agent events streamed to stdout as JSON lines.
- All commands support an optional `id` field for request/response correlation.

Framing requirements:

- The Pi RPC client MUST use strict JSONL semantics with LF (`\n`) as the only record delimiter.
- The client MUST split records on `\n` only.
- The client MUST accept optional `\r\n` input by stripping a trailing `\r`.
- The client MUST NOT use generic line readers that treat Unicode separators as newlines.

#### 10.2.1 Launch Contract

Subprocess launch parameters:

- Command: `pi.command`, plus `--provider <pi.provider>` and `--model <pi.model>` when those
  fields are configured.
- Invocation: `bash -lc <resolved-pi-command>`.
- Working directory: workspace path
- Transport/framing: Pi RPC JSONL over stdio

Notes:

- The default command is `pi --mode rpc --no-session`.
- `pi.command` is treated as a complete shell command and is not automatically suffixed with cwd
  flags. Workspace binding is provided by the subprocess working directory.
- `provider` and `model` are supplied as CLI arguments if configured in front matter
  (for example `--provider <pi.provider> --model <pi.model>`).
- Implementations MAY omit `--no-session` only if they explicitly manage persistent Pi sessions and
  document the lifecycle semantics.
- The implementation MUST verify the launched process speaks the Pi RPC protocol before treating it
  as a valid agent runtime.

RECOMMENDED additional process settings:

- Max line size: 10 MB (for safe buffering)

#### 10.2.2 Session Startup Responsibilities

Startup MUST follow the Pi RPC protocol. Symphony additionally requires the client to:

- Start the Pi subprocess in the per-issue workspace.
- Send the initial prompt using the `prompt` RPC command with the rendered issue prompt as
  `message`.
- Supply the absolute per-issue workspace context. When the Pi protocol supports working-directory
  configuration (for example through CLI flags, environment, or prompt instructions), the
  implementation MUST pass the workspace path. If the protocol does not expose a cwd mechanism,
  the implementation MUST document how workspace confinement is achieved.
- Include issue-identifying metadata, such as `<issue.identifier>: <issue.title>`, in the initial
  prompt when the implementation's prompt template contract supports it.
- Advertise implemented client-side tools using the mechanism supported by the targeted Pi version
  if applicable.

Session identifiers:

- Pi RPC mode does not expose `thread_id` and `turn_id` as first-class concepts in the same way as
  Codex app-server.
- The implementation MUST derive a stable `session_id` from Pi session metadata or process lifetime.
- A conforming implementation MAY use the Pi `sessionId` from `get_state` responses, the CLI
  `--name` argument, the Pi process ID (for example `pi-<pid>`), or another stable process-scoped
  identifier.
- The implementation MUST document the chosen `session_id` derivation.

#### 10.2.3 Session State and Model Management

If the implementation exposes runtime model or state controls, it MAY use the Pi RPC commands
listed below. Use of these commands is OPTIONAL unless the implementation documents them as part of
its Pi integration contract.

State query:

- `get_state` returns the current session state, including model info, streaming status, session
  file, and context usage.
- The implementation MAY use this to populate orchestrator runtime snapshots.

Model management:

- `set_model` switches the active model.
- `cycle_model` cycles to the next available model.
- `get_available_models` lists configured models.

Thinking level:

- `set_thinking_level` sets the reasoning level for supported models.
- `cycle_thinking_level` cycles through available levels.

Session name:

- `set_session_name` sets a display name for the session.
- The implementation MAY pass `--name <issue_identifier>` at process startup instead.

#### 10.2.4 Turn Processing and Continuation

The client sends prompts and processes Pi events until the active operation terminates.

Completion conditions:

- Pi `agent_end` event -> success for that turn
- Pi `message_update` event with `assistantMessageEvent.type == "error"` -> failure
- Turn timeout (`pi.turn_timeout_ms`) -> failure
- Tool execution result marked `isError: true` for a terminal operation -> failure
- Subprocess exit before the required `agent_end` event -> failure

Continuation processing:

- If the worker decides to continue after a successful turn, it SHOULD issue another `prompt` or
  `steer` command on the same live Pi process.
- The Pi subprocess SHOULD remain alive across those continuation turns and be stopped only when
  the worker run is ending.
- Continuation turns are bounded by `agent.max_turns`, using the same semantics as the Codex
  integration.
- The implementation MUST avoid resending the original issue prompt on continuation turns when the
  Pi session already retains conversation history.

Transport handling requirements:

- Follow Pi RPC framing rules strictly (LF-delimited JSONL).
- Keep the protocol stdin stream separate from stdout event stream.
- Do not treat stdout events as command responses.

#### 10.2.5 Emitted Runtime Events (Upstream to Orchestrator)

The Pi RPC client emits structured events to the orchestrator callback. Each event SHOULD include:

- `event` (enum/string)
- `timestamp` (UTC timestamp)
- `pi_pid` (if available)
- OPTIONAL `usage` map (token counts)
- payload fields as needed

Important emitted events include, for example:

- `agent_start`
- `agent_end`
- `turn_start`
- `turn_end`
- `message_start`
- `message_update`
- `message_end`
- `tool_execution_start`
- `tool_execution_update`
- `tool_execution_end`
- `queue_update`
- `compaction_start`
- `compaction_end`
- `auto_retry_start`
- `auto_retry_end`
- `extension_error`
- `malformed`

#### 10.2.6 Tool Calls, Bash, and Extension UI

Tool calls:

- Tool execution is surfaced through `tool_execution_start`, `tool_execution_update`, and
  `tool_execution_end` events.
- Implementations MAY expose client-side tools to the Pi session when the Pi protocol supports
  advertisement or registration.
- If an unsupported tool is requested, the implementation MUST return a failure result through the
  Pi protocol and continue the session.

Bash execution:

- Pi exposes a `bash` RPC command that executes shell commands and returns structured results.
- The implementation SHOULD treat `bash` results as part of the agent's internal session flow and
  MUST still enforce workspace safety invariants from Section 9.5.
- Bash output MAY be truncated; if `truncated` is true and `fullOutputPath` is present, the
  implementation MAY log or retain the full output for debugging.

Extension UI protocol:

- Pi extensions can request user interaction via dialog methods (`select`, `confirm`, `input`,
  `editor`) and fire-and-forget methods (`notify`, `setStatus`, `setWidget`, `setTitle`,
  `set_editor_text`) over the extension UI sub-protocol.
- Dialog methods emit an `extension_ui_request` on stdout and block until the client sends an
  `extension_ui_response` on stdin.
- The implementation MUST respond to extension UI requests in a conforming way or document a
  policy for resolving them automatically.
- The Go implementation cancels blocking dialog requests by default and cancels them with an
  explicit policy error when `pi.approval_policy` is `strict` or `{mode: "strict"}`. Fire-and-forget
  extension UI requests are ignored.
- A run MUST NOT stall indefinitely waiting for extension UI responses.

#### 10.2.7 Queue Modes, Compaction, and Retry

Queue modes:

- The implementation MAY set steering mode and follow-up mode using `set_steering_mode` and
  `set_follow_up_mode`.
- If implemented, the default behavior SHOULD preserve Pi's documented defaults unless the
  workflow requires a different policy.

Compaction:

- The implementation MAY trigger manual compaction using the `compact` command.
- Auto-compaction behavior is controlled by `set_auto_compaction`.
- Compaction emits `compaction_start` and `compaction_end` events that SHOULD be forwarded to the
  orchestrator for observability.

Auto-retry:

- Pi retries transient errors automatically when enabled via `set_auto_retry`.
- Auto-retry emits `auto_retry_start` and `auto_retry_end` events.
- The implementation SHOULD NOT interfere with Pi's internal auto-retry unless the orchestrator's
  retry policy explicitly overrides it.

#### 10.2.8 Timeouts and Error Mapping

Timeouts:

- `pi.read_timeout_ms`: request/response timeout during startup and synchronous commands
- `pi.turn_timeout_ms`: total turn stream timeout for a single agent operation
- `pi.stall_timeout_ms`: enforced by orchestrator based on event inactivity

Error mapping (RECOMMENDED normalized categories):

- `pi_not_found`
- `invalid_workspace_cwd`
- `response_timeout`
- `turn_timeout`
- `subprocess_exit`
- `response_error`
- `turn_failed`
- `turn_cancelled`
- `extension_ui_timeout`
- `agent_input_required`

### 10.3 Approval, Tool Calls, and User Input Policy

Approval, sandbox, and user-input behavior is implementation-defined.

Policy requirements:

- Each implementation MUST document its chosen approval, sandbox, and operator-confirmation
  posture.
- Approval requests and user-input-required events MUST NOT leave a run stalled indefinitely. An
  implementation MAY either satisfy them, surface them to an operator, auto-resolve them, or
  fail the run according to its documented policy.

Example high-trust behavior:

- Auto-approve command execution approvals for the session.
- Auto-approve file-change approvals for the session.
- Treat user-input-required turns as hard failure.

Unsupported dynamic tool calls:

- Supported dynamic tool calls that are explicitly implemented and advertised by the runtime SHOULD
  be handled according to their extension contract.
- If the agent requests a dynamic tool call that is not supported, return a tool failure response
  using the targeted protocol and continue the session.
- This prevents the session from stalling on unsupported tool execution paths.

Optional client-side tool extension:

- An implementation MAY expose a limited set of client-side tools to the coding-agent session.
- Standardized optional tools:
  - `linear_graphql` — for `tracker.kind == "linear"` with any supported `agent_kind`.
  - `jira_rest` — for `tracker.kind == "jira"` with any supported `agent_kind`.
  - `beads_cli` — for `tracker.kind == "beads"` with any supported `agent_kind`.
- If implemented, supported tools SHOULD be advertised using the mechanism supported by the targeted
  coding-agent protocol during session startup.
- Unsupported tool names SHOULD still return a failure result using the targeted protocol and
  continue the session.

User-input-required policy:

- Implementations MUST document how targeted-protocol user-input-required signals are handled.
- A run MUST NOT stall indefinitely waiting for user input.
- A conforming implementation MAY fail the run, surface the request to an operator, satisfy it
  through an approved operator channel, or auto-resolve it according to its documented policy.
- The example high-trust behavior above fails user-input-required turns immediately.
- For `agent_kind == "pi"`, extension UI dialog requests (`extension_ui_request`) count as
  user-input-required signals for this policy.

`linear_graphql` extension contract:

- Purpose: execute a raw GraphQL query or mutation against Linear using Symphony's configured
  tracker auth for the current session.
- Availability: only meaningful when `tracker.kind == "linear"` and valid Linear auth is configured.
- Preferred input shape:

  ```json
  {
    "query": "single GraphQL query or mutation document",
    "variables": {
      "optional": "graphql variables object"
    }
  }
  ```

- `query` MUST be a non-empty string.
- `query` MUST contain exactly one GraphQL operation.
- `variables` is OPTIONAL and, when present, MUST be a JSON object.
- Implementations MAY additionally accept a raw GraphQL query string as shorthand input.
- Execute one GraphQL operation per tool call.
- If the provided document contains multiple operations, reject the tool call as invalid input.
- `operationName` selection is intentionally out of scope for this extension.
- Reuse the configured Linear endpoint and auth from the active Symphony workflow/runtime config; do
  not require the coding agent to read raw tokens from disk.
- Tool result semantics:
  - transport success + no top-level GraphQL `errors` -> `success=true`
  - top-level GraphQL `errors` present -> `success=false`, but preserve the GraphQL response body
    for debugging
  - invalid input, missing auth, or transport failure -> `success=false` with an error payload
- Return the GraphQL response or error payload as structured tool output that the model can inspect
  in-session.

`jira_rest` extension contract:

- Purpose: execute one Jira REST API request using Symphony's configured Jira endpoint and auth for
  the current session.
- Availability: only meaningful when `tracker.kind == "jira"` and valid Jira auth is configured.
- Preferred input shape:

  ```json
  {
    "method": "GET",
    "path": "/rest/api/3/issue/ABC-123",
    "query": {
      "fields": "summary,status"
    },
    "body": {
      "optional": "json request body"
    }
  }
  ```

- `method` MUST be one of `GET`, `POST`, `PUT`, `PATCH`, or `DELETE`.
- `path` MUST be a relative Jira REST path beginning with `/rest/api/`; absolute URLs and paths
  containing a scheme or host MUST be rejected.
- `query` is OPTIONAL and, when present, MUST be a JSON object whose values are strings, numbers,
  booleans, nulls, or arrays of those scalar values.
- `body` is OPTIONAL and, when present, MUST be JSON-serializable.
- Execute one Jira REST request per tool call.
- Reuse the configured Jira endpoint, email, and API token from the active Symphony
  workflow/runtime config; do not require the coding agent to read raw tokens from disk.
- Tool result semantics:
  - HTTP 2xx -> `success=true`, preserving the parsed JSON response when available
  - HTTP non-2xx -> `success=false`, preserving status and a bounded response body for debugging
  - invalid input, missing auth, or transport failure -> `success=false` with an error payload
- Return response status, headers needed for debugging/rate limits, parsed JSON or text body, and
  truncation metadata as structured tool output that the model can inspect in-session.

`beads_cli` extension contract:

- Purpose: execute one `bd` CLI operation using Symphony's configured Beads command and repository
  context for the current session.
- Availability: only meaningful when `tracker.kind == "beads"` and `tracker.bd_command` is valid.
- Preferred input shape:

  ```json
  {
    "args": ["show", "bd-a1b2", "--json"]
  }
  ```

- `args` MUST be a non-empty array of strings and MUST NOT include the `bd` executable itself.
- Execute exactly one `bd` invocation per tool call.
- The runtime MUST invoke the configured `tracker.bd_command` plus `args` in the same working
  directory used by the Beads tracker adapter.
- Implementations SHOULD execute arguments without shell interpolation. If `tracker.bd_command` is a
  shell command string, only that configured command may be evaluated by the shell; tool-provided
  `args` MUST be passed or quoted so they cannot add extra shell commands.
- Implementations MAY additionally accept a raw argument string as shorthand, but MUST parse/quote it
  safely and MUST reject command separators, redirects, pipes, and environment assignments.
- For read operations, implementations SHOULD add or preserve `--json` when the installed `bd`
  supports it.
- Tool result semantics:
  - process exit `0` with parseable output -> `success=true`
  - process exit nonzero -> `success=false`, preserving stdout/stderr for debugging
  - invalid input, missing `bd`, timeout, or malformed JSON when JSON was requested ->
    `success=false` with an error payload
- Return stdout/stderr, parsed JSON when available, exit status, and truncation metadata as
  structured tool output that the model can inspect in-session.

### 10.4 Timeouts and Error Mapping

Timeouts:

- For `agent_kind == "codex"`:
  - `codex.read_timeout_ms`: request/response timeout during startup and sync requests
  - `codex.turn_timeout_ms`: total turn stream timeout
  - `codex.stall_timeout_ms`: enforced by orchestrator based on event inactivity
- For `agent_kind == "pi"`:
  - `pi.read_timeout_ms`: request/response timeout during startup and synchronous commands
  - `pi.turn_timeout_ms`: total turn stream timeout for a single agent operation
  - `pi.stall_timeout_ms`: enforced by orchestrator based on event inactivity

Error mapping (RECOMMENDED normalized categories):

- `codex_not_found` (Codex only)
- `pi_not_found` (Pi only)
- `invalid_workspace_cwd`
- `response_timeout`
- `turn_timeout`
- `subprocess_exit`
- `response_error`
- `turn_failed`
- `turn_cancelled`
- `extension_ui_timeout` (Pi extension UI dialogs)
- `agent_input_required`

### 10.5 Agent Runner Contract

The `Agent Runner` wraps workspace + prompt + agent client.

For `agent_kind == "codex"`:

1. Create/reuse workspace for issue.
2. Build prompt from workflow template.
3. Start Codex app-server session.
4. Forward Codex app-server events to orchestrator.
5. On any error, fail the worker attempt (the orchestrator will retry).

For `agent_kind == "pi"`:

1. Create/reuse workspace for issue.
2. Build prompt from workflow template.
3. Start Pi RPC session.
4. Forward Pi RPC events to orchestrator.
5. On any error, fail the worker attempt (the orchestrator will retry).

Note:

- Workspaces are intentionally preserved after successful runs.

## 11. Issue Tracker Integration Contract (Linear-Compatible, Jira-Compatible, and Beads-Compatible)

### 11.1 REQUIRED Operations

An implementation MUST support these tracker adapter operations:

1. `fetch_candidate_issues()`
   - Return issues in configured active states for the configured tracker/project scope.

2. `fetch_issues_by_states(state_names)`
   - Used for startup terminal cleanup.

3. `fetch_issue_states_by_ids(issue_ids)`
   - Used for active-run reconciliation.

Implementations MAY expose tracker-specific extensions to these operations when the underlying
tracker supports richer queries, but the three operations above define the minimum portable
surface.

### 11.2 Query Semantics (Linear)

Linear-specific requirements for `tracker.kind == "linear"`:

- `tracker.kind == "linear"`
- GraphQL endpoint (default `https://api.linear.app/graphql`)
- Auth token sent in `Authorization` header
- `tracker.project_slug` maps to Linear project `slugId`
- Candidate issue query filters project using `project: { slugId: { eq: $projectSlug } }`
- Candidate and issue-state refresh queries include issue labels. Required
  label filtering happens after normalization so refresh can observe label
  removal and stop or release existing work.
- Issue-state refresh query uses GraphQL issue IDs with variable type `[ID!]`
- Pagination REQUIRED for candidate issues
- Page size default: `50`
- Network timeout: `30000 ms`

Important:

- Linear GraphQL schema details can drift. Keep query construction isolated and test the exact query
  fields/types REQUIRED by this specification.

### 11.3 Query Semantics (Jira)

Jira-specific requirements for `tracker.kind == "jira"`:

- `tracker.kind == "jira"` selects the Jira tracker adapter.
- `tracker.endpoint` is the Jira site base URL and the adapter uses Jira Cloud REST API paths under
  that endpoint.
- Authentication uses Jira Basic auth with `tracker.email` and `tracker.api_token`.
- Candidate issue queries use `/rest/api/3/search/jql`.
- If `tracker.jql` is configured, the adapter uses it as the complete candidate JQL query.
- If `tracker.jql` is not configured, the default candidate JQL is:
  `project = "<project_key_or_slug>" AND status in ("<active_state>", ...) ORDER BY priority ASC, created ASC`.
- `tracker.project_key` scopes default candidate queries and terminal-state cleanup queries. If
  `tracker.project_key` is absent, implementations MUST use `tracker.project_slug` as a Jira
  project-key compatibility alias. Implementations that allow `tracker.jql` without either field
  MUST document how terminal-state cleanup is scoped.
- Candidate and issue-state refresh queries include issue labels. Required label filtering happens
  after normalization so refresh can observe label removal and stop or release existing work.
- Issue-state refresh uses stable Jira issue IDs (`id in (...)`) when normalized `issue.id` is the
  Jira REST `id`. If an implementation normalizes Jira keys as `issue.id`, it MUST use key-based
  refresh consistently and document that implementation-defined choice.
- Terminal-state cleanup fetches issues in configured terminal states and uses `tracker.project_key`,
  or `tracker.project_slug` when `tracker.project_key` is absent, for project scoping when present.
- Pagination uses Jira `nextPageToken` / `isLast` semantics and preserves result order across pages.
- Page size defaults to `50` and is configurable with `tracker.page_size`.
- Network timeout default is `30000 ms`.
- Jira search requests include at least these fields: `summary`, `description`, `priority`,
  `status`, `assignee`, `labels`, `created`, `updated`, and `issuelinks`.
- Blocking issue links are normalized to `issue.blocked_by`; non-blocking issue links are ignored.

Important:

- Custom `tracker.jql` controls which Jira issues are considered candidates. It does not change the
  generic dispatch eligibility rules: required labels, assignee, active states, terminal states, and
  blockers still apply after normalization.
- Jira REST API details can drift. Keep query construction isolated and test exact request paths,
  fields, pagination behavior, auth headers, and JQL strings REQUIRED by this specification.

### 11.4 Query Semantics (Beads)

Beads-specific requirements for `tracker.kind == "beads"`:

- `tracker.kind == "beads"`
- Beads is a local-first, filesystem-backed tracker using the `bd` CLI tool (see
  [https://github.com/gastownhall/beads](https://github.com/gastownhall/beads)).
- The adapter interacts with Beads exclusively through the `bd` CLI subcommands. Direct
  database/SQL access is NOT part of the portable adapter contract.
- CLI invocation:
  - Use `tracker.bd_command` (default `bd`) launched via `bash -lc`.
  - Run in the repository working directory (the directory containing `WORKFLOW.md`) or the
    Beads database directory if the implementation chooses to bind to `BEADS_DIR`.
- Commands used by the adapter:
  - Candidate fetch: `bd ready --json` when available; otherwise
    `bd list --status <active_states> --json` plus dependency filtering in the adapter.
  - State refresh: `bd show <issue_id> --json` (one call per issue, or batch if `bd` supports it)
  - Terminal-state fetch for startup cleanup: `bd list --status <terminal_states> --json`
- Output processing:
  - All commands MUST be invoked with `--json` when available.
  - If the installed `bd` version does not support `--json`, the adapter MAY parse human-readable
    output, but MUST document this as an implementation-defined behavior and the minimum required
    `bd` version.
  - JSON envelope handling: Beads supports `BD_JSON_ENVELOPE=1` to wrap output in
    `{"schema_version": 1, "data": ...}`. Implementations SHOULD support both enveloped and
    non-enveloped output.
- Issue IDs:
  - Beads uses hash-based IDs such as `bd-a1b2` (prefix-hash) and optionally hierarchical IDs
    such as `bd-a3f8.1`, `bd-a3f8.1.1`.
  - The adapter MUST treat the full hierarchical ID string as `issue.id` and as `issue.identifier`.
- Labels/tags:
  - Beads `tags` MUST normalize to `issue.labels`.
  - `tracker.required_labels` filters Beads tags with the same semantics as Linear labels.
- Assignee/owner:
  - If Beads output includes assignee, owner, or equivalent routing metadata, normalize it to
    `issue.assignee`.
  - If no comparable metadata exists, normalize `issue.assignee` to null; configuring
    `tracker.assignee` is then a validation error for Beads.
- Dependencies/blockers:
  - Beads models blockers via typed dependencies (e.g., `blocks`, `parent-child`, `waits-for`,
    `conditional-blocks`).
  - The adapter MUST treat only dependencies with blocking types as `blocked_by` entries.
  - Non-blocking types (`related`, `tracks`, `discovered-from`, `caused-by`, `validates`,
    `supersedes`, `replies_to`) MUST NOT populate `blocked_by`.
  - `bd ready` SHOULD be used as the authoritative candidate source for unblocked Beads work when
    available; otherwise the adapter MUST filter out issues with non-terminal blocking dependencies.
- Priority mapping:
  - Beads priority values are integers `0`–`4` where `0` is critical.
  - The adapter MUST pass these through as `issue.priority` to match the Linear model where lower
    numbers are higher priority.
- State mapping:
  - `open` and `in_progress` are active by default.
  - `blocked` and `deferred` are non-terminal but not dispatch-active by default unless included in
    `tracker.active_states`.
  - `closed` and `tombstone` are terminal by default.
  - Custom states configured via `bd config set status.custom` are dispatch-active only when listed
    in `tracker.active_states` and terminal only when listed in `tracker.terminal_states`.
- No pagination, network timeout, or HTTP transport concerns apply to Beads. Errors are service
  execution errors from the CLI invocation.

An implementation MAY vary tracker transport details, but the normalized outputs MUST match the
domain model in Section 4.

### 11.5 Normalization Rules

Candidate issue normalization SHOULD produce fields listed in Section 4.1.1.

Additional normalization details:

- `labels` -> trimmed, lowercase strings
- `assignee` -> trimmed string or null
- `blocked_by` -> tracker-native blocking relations only; non-blocking relations are ignored
- `priority` -> integer only (non-integers become null)
- `created_at` and `updated_at` -> parse ISO-8601 timestamps

Jira-specific normalization:

- `issue.id` -> Jira REST issue `id` (stable tracker-internal ID)
- `issue.identifier` -> Jira issue key (example: `ABC-123`)
- `issue.title` -> `fields.summary`
- `issue.description` -> Jira ADF `fields.description` converted to plain text, or null when empty
- `issue.state` -> `fields.status.name`
- `issue.assignee` -> Jira assignee email when available, otherwise display name or account ID
- `issue.labels` -> `fields.labels`, lowercased
- `issue.priority` -> numeric `fields.priority.id` when parseable, otherwise null
- `issue.created_at` -> `fields.created` parsed as a timestamp
- `issue.updated_at` -> `fields.updated` parsed as a timestamp
- `issue.blocked_by` -> derived from Jira `fields.issuelinks` where the link type is `Blocks` and
  the link has an `inwardIssue`; each entry uses the linked issue's stable ID, key, and status when
  present
- `issue.url` -> Jira browse URL for the issue key when the implementation can construct one from
  `tracker.endpoint`; otherwise null

Beads-specific normalization:

- `issue.id` and `issue.identifier` -> the Beads issue ID string (e.g. `bd-a1b2` or `bd-a3f8.1.1`)
- `issue.title` -> `title`
- `issue.description` -> `description`
- `issue.state` -> `status` from Beads
- `issue.assignee` -> Beads assignee/owner/routing metadata when available, otherwise null
- `issue.labels` -> Beads `tags`, lowercased
- `issue.priority` -> Beads `priority` integer mapped through
- `issue.created_at` -> Beads `created_at` parsed as a timestamp
- `issue.updated_at` -> Beads `updated_at` parsed as a timestamp
- `issue.closed_at` -> Beads `closed_at` parsed as a timestamp; if present, the issue is terminal
- `issue.blocked_by` -> derived from Beads `dependencies` where the dependency type is a blocking
  type and the dependency target is still open or in progress; each entry uses the dependency
  target's issue ID as `id` and identifier as `identifier`
- `issue.url` -> null unless the implementation constructs one from repository metadata

### 11.6 Error Handling Contract

RECOMMENDED error categories:

- `unsupported_tracker_kind`
- `missing_tracker_api_key` (Linear only)
- `missing_tracker_project_slug` (Linear only)
- `missing_jira_endpoint` (Jira only)
- `missing_jira_email` (Jira only)
- `missing_jira_api_token` (Jira only)
- `missing_jira_project_scope` (Jira only, when neither `tracker.project_key` nor
  `tracker.project_slug` is present and one is required for the selected query/cleanup mode)
- `missing_bd_command` (Beads only)
- `linear_api_request` (transport failures)
- `linear_api_status` (non-200 HTTP)
- `linear_graphql_errors`
- `linear_unknown_payload`
- `linear_missing_end_cursor` (pagination integrity error)
- `jira_api_request` (transport failures)
- `jira_api_status` (non-2xx HTTP)
- `jira_unknown_payload`
- `jira_jql_required`
- `jira_missing_next_page_token` (pagination integrity error)
- `beads_cli_not_found`
- `beads_cli_exec_error`
- `beads_cli_output_error`
- `beads_json_parse_error`

Orchestrator behavior on tracker errors:

- Candidate fetch failure: log and skip dispatch for this tick.
- Running-state refresh failure: log and keep active workers running.
- Startup terminal cleanup failure: log warning and continue startup.

### 11.7 Tracker Writes (Important Boundary)

Symphony does not require first-class tracker write APIs in the orchestrator for any tracker kind.

- Ticket mutations (state transitions, comments, PR metadata) are typically handled by the coding
  agent using tools defined by the workflow prompt.
- The service remains a scheduler/runner and tracker reader.
- Workflow-specific success often means "reached the next handoff state" (for example
  `Human Review`) rather than tracker terminal state `Done`.
- If a tracker-specific client-side tool extension is implemented (for example `linear_graphql` for
  Linear, `jira_rest` for Jira, or `beads_cli` for Beads), it is still part of the agent toolchain
  rather than orchestrator business logic.

## 12. Prompt Construction and Context Assembly

### 12.1 Inputs

Inputs to prompt rendering:

- `workflow.prompt_template`
- normalized `issue` object
- OPTIONAL `attempt` integer (retry/continuation metadata)
- OPTIONAL workspace prompt include files from `prompt.include_files`

### 12.2 Rendering Rules

- Render with strict variable checking.
- Render with strict filter checking.
- Convert issue object keys to strings for template compatibility.
- Preserve nested arrays/maps (labels, blockers) so templates can iterate.

### 12.3 Retry/Continuation Semantics

`attempt` SHOULD be passed to the template because the workflow prompt can provide different
instructions for:

- first run (`attempt` null or absent)
- continuation run after a successful prior session
- retry after error/timeout/stall

### 12.4 Failure Semantics

If prompt rendering fails:

- Fail the run attempt immediately.
- Let the orchestrator treat it like any other worker failure and decide retry behavior.

If prompt include assembly fails:

- Fail the run attempt immediately before launching the coding agent.
- Missing include files do not fail the run attempt.
- Invalid include paths or unreadable existing include files fail the run attempt.

## 13. Logging, Status, and Observability

### 13.1 Logging Conventions

REQUIRED context fields for issue-related logs:

- `issue_id`
- `issue_identifier`

REQUIRED context for coding-agent session lifecycle logs:

- `session_id`

Message formatting requirements:

- Use stable `key=value` phrasing.
- Include action outcome (`completed`, `failed`, `retrying`, etc.).
- Include concise failure reason when present.
- Avoid logging large raw payloads unless necessary.

### 13.2 Logging Outputs and Sinks

The spec does not prescribe where logs are written (stderr, file, remote sink, etc.).

Requirements:

- Operators MUST be able to see startup/validation/dispatch failures without attaching a debugger.
- Operators SHOULD be able to see workspace setup failures before the agent starts, including
  workspace cleanup failures, workspace creation/preparation failures, prompt-render failures, and
  workflow hook failures.
- Workflow hook failure logs SHOULD include the hook name, issue identifier, attempt number,
  workspace path when known, and the error text returned by the hook runner.
- When a failed `after_create` workspace is retained for inspection, logs SHOULD include the retained
  failed workspace path.
- Implementations MAY write to one or more sinks.
- If a configured log sink fails, the service SHOULD continue running when possible and emit an
  operator-visible warning through any remaining sink.

### 13.3 Runtime Snapshot / Monitoring Interface (OPTIONAL but RECOMMENDED)

If the implementation exposes a synchronous runtime snapshot (for dashboards or monitoring), it
SHOULD return:

- `ready` (list of current pending work rows from the latest successful tracker poll)
  - The list SHOULD contain dispatch-eligible tracker candidates that are not currently running,
    claimed, completed/book-kept, or waiting in retry backoff.
  - The list order SHOULD match the order the orchestrator will use when selecting the next work,
    after applying the same dispatch sort used by the scheduler.
  - Snapshot consumers MAY show only a bounded prefix, but `counts.ready` SHOULD report the full
    pending ready count.
- `setup` (list of current or recently failed workspace setup rows)
  - Rows SHOULD be emitted while a ticket is claimed but not yet running an agent because workspace
    preparation, workflow hooks, or prompt rendering are in progress.
  - Rows SHOULD remain visible when setup fails and the issue is moved to retry backoff.
  - Rows SHOULD include issue identity, title, tracker state, attempt, setup stage, setup status,
    hook name when relevant, workspace or failed workspace path when known, setup error text when
    present, and setup diagnostic log paths when available.
- `running` (list of running session rows)
- each running row SHOULD include `turn_count`
- `retrying` (list of retry queue rows)
- retry rows SHOULD include the latest setup snapshot when the retry was caused by setup failure.
- ready, session, and retry rows SHOULD include the tracker-provided issue URL when available
- `agent_totals`
  - `input_tokens`
  - `output_tokens`
  - `total_tokens`
  - `seconds_running` (aggregate runtime seconds as of snapshot time, including active sessions)
- `rate_limits` (latest coding-agent rate limit payload, if available)

RECOMMENDED snapshot error modes:

- `timeout`
- `unavailable`

#### 13.3.1 Dashboard Snapshot Rows

The synchronous snapshot remains OPTIONAL for core conformance. If an implementation ships the HTTP
server extension in Section 13.7, its `/api/v1/state` response MUST provide the dashboard snapshot
fields in this section unless a field is explicitly marked OPTIONAL.

Requirements:

- Snapshot data MUST be observability-only. It MUST NOT be required for dispatch, retry scheduling,
  reconciliation, workspace cleanup, or coding-agent correctness.
- Snapshot rows MUST use real orchestrator, tracker, hook, agent, or configured extension data.
  Implementations MUST NOT fabricate PRs, token counts, runtime, or hook phases to satisfy a
  dashboard layout.
- Snapshot consumers MUST be able to render the active lifecycle using this phase order:
  - `prepare`
  - `after_create`
  - `before_run`
  - `agent_run`
  - `after_run`
  - `before_remove`
  - `completed`
- A row enters `before_remove` only when the implementation is actually removing a workspace. Normal
  successful handoff runs MAY skip directly from `after_run` to `completed`.
- `after_run` and `before_remove` hook failures remain governed by Section 9.4: they are logged and
  surfaced to operators but do not by themselves fail or retry the agent run.

Common row fields:

- `issue_id`
- `issue_identifier`
- `issue_url` when the tracker supplies one
- `title` when known
- `state` when known
- `phase` when the row represents lifecycle progress
- `status` when the row represents an active, completed, or failed phase
- `started_at` when known
- `updated_at` or `completed_at` when known
- `max_turns` when the row displays turn progress and the effective value is known
- `pull_request` when an OPTIONAL PR metadata extension has real data

`runtime_config`:

- HTTP snapshots MUST include dashboard-relevant effective runtime settings when known.
- `project_name` is REQUIRED and contains the effective project name resolved from `project.name` or
  the folder containing `WORKFLOW.md`.
- `agent_max_turns` is REQUIRED and contains the effective `agent.max_turns`.
- `agent_max_concurrent_agents` is REQUIRED and contains the effective `agent.max_concurrent_agents`.
- `dashboard_refresh_ms` is OPTIONAL and contains the dashboard auto-refresh interval when that
  interval is implementation-defined or configurable.

`ready` rows:

- MUST include issue identity and dispatch ordering.
- SHOULD include `created_at` from the tracker when known.
- SHOULD include `queued_since` when the implementation tracks when the issue first entered the
  local ready queue.
- MAY include computed `wait_seconds`.
- Missing timing fields mean the dashboard should omit or de-emphasize queue wait time rather than
  invent it.

`setup` rows:

- MUST include pre-agent setup state for workspace preparation, `after_create`, `before_run`, prompt
  rendering, and setup failures that are waiting in retry backoff.
- MUST include `phase` using `prepare`, `after_create`, or `before_run` where that mapping is known.
- MAY retain a more granular implementation-specific `stage` field such as `preparing_workspace` or
  `building_prompt`.
- SHOULD include hook name, workspace path, failed workspace path, setup error text, and setup
  diagnostic log paths when known.

`running` rows:

- MUST include `phase: "agent_run"`.
- MUST include `turn_count`.
- MUST include `max_turns` directly or through `runtime_config.agent_max_turns`.
- SHOULD include `started_at` and `runtime_seconds` when known.
- SHOULD include current per-session token counts when known.
- SHOULD include `last_event`, `last_message`, raw log paths, and a bounded human-readable agent
  message tail.

Post-run hook rows:

- Implementations that run `after_run` or `before_remove` hooks SHOULD surface those hooks as active
  rows while they are running.
- Rows MUST use `phase: "after_run"` or `phase: "before_remove"` as appropriate.
- Rows SHOULD remain visible briefly after completion or failure so a polling dashboard can observe
  the transition.
- `counts.post_run_hooks` MUST count rows currently in `after_run` or `before_remove` phases when
  the implementation tracks them; otherwise it MUST be `0`.

`retrying` rows:

- MUST include `issue_id`, `issue_identifier`, `attempt`, `due_at`, and the latest retry error when
  present.
- SHOULD include `title`, `state`, and `issue_url` when known.
- SHOULD include the latest setup snapshot when the retry was caused by setup failure.
- MAY include `pull_request` when the OPTIONAL PR metadata extension has real data.

Agent message/event history:

- Running rows SHOULD include `recent_agent_messages`, capped at 100 messages per row by default.
- Each message SHOULD include `at`, `event`, and `text`.
- Implementations MUST keep message ordering stable within one API version and include timestamps so
  dashboards can render newest-first streams.
- A bounded in-memory event/message history is sufficient. Durable event history is OPTIONAL and
  implementation-defined.
- Implementations MAY expose `GET /api/v1/<issue_identifier>/events?limit=100` for deeper inspection
  or to keep `/api/v1/state` smaller. This endpoint is OPTIONAL.

### 13.4 OPTIONAL Human-Readable Status Surface

A human-readable status surface (terminal output, dashboard, etc.) is OPTIONAL and
implementation-defined.

If present, it SHOULD draw from orchestrator state/metrics only and MUST NOT be REQUIRED for
correctness.

### 13.5 Session Metrics and Token Accounting

Token accounting rules:

- Agent events can include token counts in multiple payload shapes.
- For `agent_kind == "codex"`:
  - Prefer absolute thread totals when available, such as:
    - `thread/tokenUsage/updated` payloads
    - `total_token_usage` within token-count wrapper events
  - Ignore delta-style payloads such as `last_token_usage` for dashboard/API totals.
- For `agent_kind == "pi"`:
  - Prefer absolute totals from `get_session_stats` `tokens` fields when available.
  - Use `AssistantMessage.usage` (input/output/cacheRead/cacheWrite/cost) and
    `contextUsage` as supplementary signals.
- Extract input/output/total token counts leniently from common field names within the selected
  payload.
- For absolute totals, track deltas relative to last reported totals to avoid double-counting.
- Do not treat generic `usage` maps as cumulative totals unless the event type defines them that
  way.
- Accumulate aggregate totals in orchestrator state.

Runtime accounting:

- Runtime SHOULD be reported as a live aggregate at snapshot/render time.
- Implementations MAY maintain a cumulative counter for ended sessions and add active-session
  elapsed time derived from `running` entries (for example `started_at`) when producing a
  snapshot/status view.
- Add run duration seconds to the cumulative ended-session runtime when a session ends (normal exit
  or cancellation/termination).
- Continuous background ticking of runtime totals is not REQUIRED.

Rate-limit tracking:

- Track the latest rate-limit payload seen in any agent update.
- Any human-readable presentation of rate-limit data is implementation-defined.

### 13.6 Humanized Agent Event Summaries (OPTIONAL)

Humanized summaries of raw agent protocol events are OPTIONAL.

If implemented:

- Treat them as observability-only output.
- Do not make orchestrator logic depend on humanized strings.

### 13.7 OPTIONAL HTTP Server Extension

This section defines an OPTIONAL HTTP interface for observability and operational control.

If implemented:

- The HTTP server is an extension and is not REQUIRED for conformance.
- The implementation MAY serve server-rendered HTML or a client-side application for the dashboard.
- The dashboard/API MUST be observability/control surfaces only and MUST NOT become REQUIRED for
  orchestrator correctness.

Extension config:

- `server.port` (integer, OPTIONAL)
  - Enables the HTTP server extension.
  - `0` requests an ephemeral port for local development and tests.
  - CLI `--port` overrides `server.port` when both are present.

Enablement (extension):

- Start the HTTP server when a CLI `--port` argument is provided.
- Start the HTTP server when `server.port` is present in `WORKFLOW.md` front matter.
- The `server` top-level key is owned by this extension.
- Positive `server.port` values bind that port.
- Implementations SHOULD bind loopback by default (`127.0.0.1` or host equivalent) unless explicitly
  configured otherwise.
- Changes to HTTP listener settings (for example `server.port`) do not need to hot-rebind;
  restart-required behavior is conformant.

#### 13.7.1 Human-Readable Dashboard (`/`)

- Host a human-readable dashboard at `/`.
- The returned document SHOULD depict the current state of the system (for example active sessions,
  retry delays, token consumption, runtime totals, recent events, and health/error indicators).
- The dashboard SHOULD render the lifecycle phases defined in Section 13.3.1 from real snapshot
  phases. It SHOULD omit, gray, or mark unavailable phases that the implementation does not report
  rather than inventing phase progress.
- The dashboard SHOULD expose the current pending ready-work count before the running count.
- The dashboard SHOULD include a Queued Work or equivalent section above Running Sessions.
  - The queued section SHOULD show the next pending ready issues in the same order the scheduler uses
    for selecting work.
  - Showing the first five pending ready issues is sufficient.
  - Tracker URLs in the queued section SHOULD be clickable when available.
- The dashboard SHOULD include a Running Sessions section for currently running jobs.
  - Tickets whose workspace is being prepared SHOULD be shown in this same section even before an
    agent session exists.
  - Each running job SHOULD show the issue title when available.
  - Each running job SHOULD expose the primary raw agent log path when available.
  - Workspace paths, setup diagnostic log paths, and raw agent log paths SHOULD be shown above the
    agent text tail inside the ticket's nested session/log window.
  - Setup status and setup errors SHOULD be shown in the same nested session/log window.
  - Each running job SHOULD show a bounded tail of recent agent text messages so an operator can
    understand what the agent is currently doing without opening raw logs.
  - The text tail SHOULD prefer human-readable agent text extracted from agent protocol events and
    SHOULD avoid showing raw JSON-RPC payloads.
  - The text tail SHOULD include at most 100 messages per running job.
  - The dashboard SHOULD refresh this section automatically; short polling of the JSON API is
    sufficient and streaming transport is not required.
- If OPTIONAL PR metadata is unavailable, the dashboard SHOULD omit PR chips or show a neutral
  "no PR yet" affordance rather than constructing PR links from issue identifiers or agent text.
- It is up to the implementation whether this is server-generated HTML or a client-side app that
  consumes the JSON API below.

#### 13.7.2 JSON REST API (`/api/v1/*`)

Provide a JSON REST API under `/api/v1/*` for current runtime state and operational debugging.

Minimum endpoints:

- `GET /api/v1/state`
  - Returns a summary view of the current system state (running sessions, retry queue/delays,
    aggregate token/runtime totals, latest rate limits, and any additional tracked summary fields).
  - Suggested response shape:

    ```json
    {
      "generated_at": "2026-02-24T20:15:30Z",
      "counts": {
        "ready": 1,
        "setup": 1,
        "running": 2,
        "post_run_hooks": 0,
        "retrying": 1
      },
      "runtime_config": {
        "project_name": "Symphony Go",
        "agent_max_concurrent_agents": 10,
        "agent_max_turns": 20,
        "dashboard_refresh_ms": 5000
      },
      "ready": [
        {
          "issue_id": "ghi789",
          "issue_identifier": "MT-651",
          "issue_url": "https://tracker.example/issues/MT-651",
          "title": "Add queued work visibility",
          "state": "Todo",
          "priority": 1,
          "created_at": "2026-02-24T19:50:00Z",
          "queued_since": "2026-02-24T20:12:00Z",
          "wait_seconds": 210
        }
      ],
      "setup": [
        {
          "issue_id": "jkl012",
          "issue_identifier": "MT-652",
          "issue_url": "https://tracker.example/issues/MT-652",
          "title": "Prepare workspace visibility",
          "state": "In Progress",
          "attempt": 0,
          "phase": "after_create",
          "stage": "after_create",
          "status": "failed",
          "hook": "after_create",
          "workspace": "/tmp/symphony_workspaces/MT-652",
          "failed_workspace": "/tmp/symphony_workspaces/.failed/MT-652-123",
          "error": "hook failed: exit status 2",
          "log_path": "/tmp/symphony_workspaces/.failed/MT-652-123/prepare-error.txt",
          "logs": [
            {
              "label": "prepare-error",
              "path": "/tmp/symphony_workspaces/.failed/MT-652-123/prepare-error.txt",
              "url": null
            }
          ],
          "started_at": "2026-02-24T20:09:58Z",
          "updated_at": "2026-02-24T20:10:02Z"
        }
      ],
      "running": [
        {
          "issue_id": "abc123",
          "issue_identifier": "MT-649",
          "issue_url": "https://tracker.example/issues/MT-649",
          "title": "Implement queued work visibility",
          "state": "In Progress",
          "phase": "agent_run",
          "session_id": "thread-1-turn-1",
          "turn_count": 7,
          "max_turns": 20,
          "last_event": "turn_completed",
          "last_message": "",
          "log_path": "/var/log/symphony/agents/MT-649/thread-1-turn-1/protocol.jsonl",
          "started_at": "2026-02-24T20:10:12Z",
          "runtime_seconds": 287.0,
          "last_event_at": "2026-02-24T20:14:59Z",
          "recent_agent_messages": [
            {
              "at": "2026-02-24T20:14:45Z",
              "event": "item_agentMessage_delta",
              "text": "I am checking the failing test output."
            }
          ],
          "tokens": {
            "input_tokens": 1200,
            "output_tokens": 800,
            "total_tokens": 2000
          },
          "pull_request": null,
          "setup": {
            "issue_id": "abc123",
            "issue_identifier": "MT-649",
            "phase": "before_run",
            "stage": "building_prompt",
            "status": "completed",
            "workspace": "/tmp/symphony_workspaces/MT-649",
            "updated_at": "2026-02-24T20:10:11Z"
          }
        }
      ],
      "retrying": [
        {
          "issue_id": "def456",
          "issue_identifier": "MT-650",
          "issue_url": "https://tracker.example/issues/MT-650",
          "title": "Retry failed setup visibility",
          "state": "In Progress",
          "attempt": 3,
          "due_at": "2026-02-24T20:16:00Z",
          "error": "hook failed: exit status 2",
          "pull_request": null,
          "setup": {
            "issue_id": "def456",
            "issue_identifier": "MT-650",
            "phase": "after_create",
            "stage": "after_create",
            "status": "failed",
            "hook": "after_create",
            "failed_workspace": "/tmp/symphony_workspaces/.failed/MT-650-123",
            "error": "hook failed: exit status 2",
            "log_path": "/tmp/symphony_workspaces/.failed/MT-650-123/prepare-error.txt",
            "updated_at": "2026-02-24T20:15:10Z"
          }
        }
      ],
      "agent_totals": {
        "input_tokens": 5000,
        "output_tokens": 2400,
        "total_tokens": 7400,
        "seconds_running": 1834.2
      },
      "rate_limits": null
    }
    ```

- `GET /api/v1/<issue_identifier>`
  - Returns issue-specific runtime/debug details for the identified issue, including any information
    the implementation tracks that is useful for debugging.
  - Suggested response shape:

    ```json
    {
      "issue_identifier": "MT-649",
      "issue_id": "abc123",
      "status": "running",
      "workspace": {
        "path": "/tmp/symphony_workspaces/MT-649"
      },
      "attempts": {
        "restart_count": 1,
        "current_retry_attempt": 2
      },
      "running": {
        "session_id": "thread-1-turn-1",
        "turn_count": 7,
        "max_turns": 20,
        "state": "In Progress",
        "phase": "agent_run",
        "started_at": "2026-02-24T20:10:12Z",
        "runtime_seconds": 287.0,
        "last_event": "notification",
        "last_message": "Working on tests",
        "last_event_at": "2026-02-24T20:14:59Z",
        "tokens": {
          "input_tokens": 1200,
          "output_tokens": 800,
          "total_tokens": 2000
        }
      },
      "retry": null,
      "logs": {
        "codex_session_logs": [
          {
            "label": "protocol",
            "path": "/var/log/symphony/agents/MT-649/thread-1-turn-1/protocol.jsonl",
            "url": null
          }
        ]
      },
      "recent_agent_messages": [
        {
          "at": "2026-02-24T20:14:45Z",
          "event": "item_agentMessage_delta",
          "text": "I am checking the failing test output."
        }
      ],
      "recent_events": [
        {
          "at": "2026-02-24T20:14:59Z",
          "event": "notification",
          "message": "Working on tests"
        }
      ],
      "last_error": null,
      "setup": {
        "issue_id": "abc123",
        "issue_identifier": "MT-649",
        "phase": "before_run",
        "stage": "building_prompt",
        "status": "completed",
        "workspace": "/tmp/symphony_workspaces/MT-649",
        "updated_at": "2026-02-24T20:10:11Z"
      },
      "tracked": {}
    }
    ```

  - If the issue is unknown to the current in-memory state, return `404` with an error response (for
    example `{\"error\":{\"code\":\"issue_not_found\",\"message\":\"...\"}}`).

- `POST /api/v1/refresh`
  - Queues an immediate tracker poll + reconciliation cycle (best-effort trigger; implementations
    MAY coalesce repeated requests).
  - Suggested request body: empty body or `{}`.
  - Suggested response (`202 Accepted`) shape:

    ```json
    {
      "queued": true,
      "coalesced": false,
      "requested_at": "2026-02-24T20:15:30Z",
      "operations": ["poll", "reconcile"]
    }
    ```

Optional endpoints:

- `GET /api/v1/<issue_identifier>/events?limit=100`
  - Returns a bounded issue event/message history for deeper debugging or richer activity streams.
  - The `limit` parameter SHOULD be capped by the implementation. This implementation caps it at
    `100` and uses `100` when the query value is missing or invalid.
  - The response includes issue identity, `limit`, `truncated`, `recent_events`, and
    `recent_agent_messages`.
  - This endpoint is OPTIONAL; `/api/v1/state` remains sufficient for a conforming dashboard when it
    includes capped `recent_agent_messages`.

API design notes:

- The JSON shapes above are the RECOMMENDED baseline for interoperability and debugging ergonomics.
- Implementations MAY add fields, but SHOULD avoid breaking existing fields within a version.
- Endpoints SHOULD be read-only except for operational triggers like `/refresh`.
- Unsupported methods on defined routes SHOULD return `405 Method Not Allowed`.
- API errors SHOULD use a JSON envelope such as `{"error":{"code":"...","message":"..."}}`.
- If the dashboard is a client-side app, it SHOULD consume this API rather than duplicating state
  logic.

#### 13.7.3 OPTIONAL Pull Request Metadata Extension

The HTTP/status surface MAY enrich snapshot rows with pull request metadata. This extension is
OPTIONAL and is not part of the core scheduler/tracker-reader contract.

Requirements:

- PR metadata MUST be sourced from a real provider integration or a documented deterministic local
  source.
- PR lookup failures MUST NOT fail startup, dispatch, retries, reconciliation, hook execution, or
  agent runs. They SHOULD be visible as observability warnings when practical.
- Implementations MUST NOT parse agent messages, raw logs, or free-form stdout to discover pull
  requests for the server snapshot.
- Provider authentication and cache behavior are implementation-defined and MUST be documented when
  this extension is enabled.
- Implementations MAY define additional `server` config fields for this extension, such as provider
  name, repository, cache TTL, or matching policy.

Implemented config:

- `server.pull_requests.provider`:
  - OPTIONAL.
  - Supported values: `github`, `local`, `none`, or empty.
  - Empty and `none` disable PR enrichment.
- `server.pull_requests.github_repository`:
  - REQUIRED when provider is `github`.
  - Uses `owner/repo` format.
- `server.pull_requests.github_token`:
  - OPTIONAL when provider is `github`.
  - MAY be a literal token or `$VAR_NAME`.
  - When omitted, the implementation reads `GITHUB_TOKEN`, then `GH_TOKEN`, from the process
    environment.
- `server.pull_requests.local_path`:
  - REQUIRED when provider is `local`.
  - Path to a deterministic local JSON array of PR rows using the recommended row fields plus
    optional `issue_identifier`, `title`, `body`, and `updated_at`.
  - Relative paths resolve from the workflow file directory.
- `server.pull_requests.cache_ttl_ms`:
  - OPTIONAL positive integer.
  - Default: `60000 ms`.

Provider behavior:

- GitHub lookup uses the configured repository and GitHub API search/detail responses.
- Local lookup reads the configured JSON file and caches it for `cache_ttl_ms`.
- Provider lookups run outside the orchestrator state lock.
- Successful lookup results and misses are cached for `cache_ttl_ms`; provider errors are not cached.
- Lookup errors omit `pull_request` and emit an observability warning when a logger is configured.

Recommended row field:

```json
{
  "pull_request": {
    "provider": "github",
    "number": 961,
    "url": "https://github.com/org/repo/pull/961",
    "state": "mergeable",
    "is_draft": false,
    "merged_at": null,
    "head_branch": "MT-649",
    "match": "branch_name"
  }
}
```

Recommended matching order:

1. Match the tracker-provided `issue.branch_name` when present.
2. Otherwise match PR head branches by normalized issue identifier, for example `ABC-123`.
3. Otherwise, when the provider supports PR search, search PR title/body metadata for the exact
   normalized issue identifier token, for example `ABC-123`.
Identifier search MUST be scoped to the configured repository or repositories. It SHOULD use exact
token matching and SHOULD NOT treat arbitrary substring matches as authoritative when the provider
offers a more precise search mode. Implementations SHOULD record the match source, such as
`branch_name`, `head_branch_identifier`, or `identifier_search`.

Recommended state mapping:

- `merged`: PR is merged.
- `draft`: PR is open and draft.
- `blocked`: PR is open and not currently mergeable because of mergeability, checks, reviews, or
  another provider-specific blocking condition.
- `mergeable`: PR is open, non-draft, and currently mergeable according to the provider.
- `unknown`: provider data exists but cannot be mapped confidently.

If multiple PRs match one issue, implementations SHOULD either choose the most recently updated PR
and expose an ambiguity marker, or omit `pull_request` and surface an observability warning.

Agent run logs:

- When file logging is configured, each agent run SHOULD write raw protocol and diagnostic logs under
  the configured logs root.
- A conforming layout is:
  - `agents/<issue_identifier>/<session_id>/protocol.jsonl`
  - `agents/<issue_identifier>/<session_id>/stderr.log`
  - `agents/<issue_identifier>/<session_id>/result.json`
- `protocol.jsonl` SHOULD preserve raw agent protocol lines with enough direction metadata to tell
  messages sent by Symphony from messages received from the agent.
- `stderr.log` SHOULD contain coding-agent diagnostic stderr.
- `result.json` SHOULD contain the final run result, including terminal status, error text when
  present, session/thread/turn identity, and token totals when available.
- Log paths SHOULD be exposed through the JSON API and dashboard when available.
- Agent logs are observability artifacts. They MUST NOT be required for orchestrator correctness,
  retry scheduling, reconciliation, or tracker state updates.

## 14. Failure Model and Recovery Strategy

### 14.1 Failure Classes

1. `Workflow/Config Failures`
   - Missing `WORKFLOW.md`
   - Invalid YAML front matter
   - Unsupported tracker kind or missing tracker credentials/project slug
   - Missing coding-agent executable

2. `Workspace Failures`
   - Workspace directory creation failure
   - Workspace population/synchronization failure (implementation-defined; can come from hooks)
   - Invalid workspace path configuration
   - Hook timeout/failure

3. `Agent Session Failures`
   - Startup handshake failure
   - Turn failed/cancelled
   - Turn timeout
   - User input requested and handled as failure by the implementation's documented policy
   - Subprocess exit
   - Stalled session (no activity)

4. `Tracker Failures`
   - API transport errors
   - Non-200 status
   - GraphQL errors
   - malformed payloads

5. `Observability Failures`
   - Snapshot timeout
   - Dashboard render errors
   - Log sink configuration failure

### 14.2 Recovery Behavior

- Dispatch validation failures:
  - Skip new dispatches.
  - Keep service alive.
  - Continue reconciliation where possible.

- Worker failures:
  - Convert to retries with exponential backoff.

- Tracker candidate-fetch failures:
  - Skip this tick.
  - Try again on next tick.

- Reconciliation state-refresh failures:
  - Keep current workers.
  - Retry on next tick.

- Dashboard/log failures:
  - Do not crash the orchestrator.

### 14.3 Partial State Recovery (Restart)

Current design is intentionally in-memory for scheduler state.
Restart recovery means the service can resume useful operation by polling tracker state and reusing
preserved workspaces. It does not mean retry timers, running sessions, or live worker state survive
process restart.

After restart:

- No retry timers are restored from prior process memory.
- No running sessions are assumed recoverable.
- Service recovers by:
  - startup terminal workspace cleanup
  - fresh polling of active issues
  - re-dispatching eligible work

### 14.4 Operator Intervention Points

Operators can control behavior by:

- Editing `WORKFLOW.md` (prompt and most runtime settings).
- `WORKFLOW.md` changes are detected and re-applied automatically without restart according to
  Section 6.2.
- Changing issue states in the tracker:
  - terminal state -> running session is stopped and workspace cleaned when reconciled
  - non-active state -> running session is stopped without cleanup
- Restarting the service for process recovery or deployment (not as the normal path for applying
  workflow config changes).

## 15. Security and Operational Safety

### 15.1 Trust Boundary Assumption

Each implementation defines its own trust boundary.

Operational safety requirements:

- Implementations SHOULD state clearly whether they are intended for trusted environments, more
  restrictive environments, or both.
- Implementations SHOULD state clearly whether they rely on auto-approved actions, operator
  approvals, stricter sandboxing, or some combination of those controls.
- Workspace isolation and path validation are important baseline controls, but they are not a
  substitute for whatever approval and sandbox policy an implementation chooses.

### 15.2 Filesystem Safety Requirements

Mandatory:

- Workspace path MUST remain under configured workspace root.
- Coding-agent cwd MUST be the per-issue workspace path for the current run.
- Workspace directory names MUST use sanitized identifiers.

RECOMMENDED additional hardening for ports:

- Run under a dedicated OS user.
- Restrict workspace root permissions.
- Mount workspace root on a dedicated volume if possible.

### 15.3 Secret Handling

- Support `$VAR` indirection in workflow config.
- Do not log API tokens or secret env values.
- Validate presence of secrets without printing them.

### 15.4 Hook Script Safety

Workspace hooks are arbitrary shell scripts from `WORKFLOW.md`.

Implications:

- Hooks are fully trusted configuration.
- Hooks run inside the workspace directory.
- Hook output SHOULD be truncated in logs.
- Hook timeouts are REQUIRED to avoid hanging the orchestrator.

### 15.5 Harness Hardening Guidance

Running Codex agents against repositories, issue trackers, and other inputs that can contain
sensitive data or externally-controlled content can be dangerous. A permissive deployment can lead
to data leaks, destructive mutations, or full machine compromise if the agent is induced to execute
harmful commands or use overly-powerful integrations.

Implementations SHOULD explicitly evaluate their own risk profile and harden the execution harness
where appropriate. This specification intentionally does not mandate a single hardening posture, but
implementations SHOULD NOT assume that tracker data, repository contents, prompt inputs, or tool
arguments are fully trustworthy just because they originate inside a normal workflow.

Possible hardening measures include:

- Tightening Codex approval and sandbox settings described elsewhere in this specification instead
  of running with a maximally permissive configuration.
- Adding external isolation layers such as OS/container/VM sandboxing, network restrictions, or
  separate credentials beyond the built-in Codex policy controls.
- Filtering which tracker issues, projects, teams, labels, JQL scopes, or other tracker sources are
  eligible for dispatch so untrusted or out-of-scope tasks do not automatically reach the agent.
- Narrowing the `linear_graphql` or `jira_rest` tool so it can only read or mutate data inside the
  intended project scope, rather than exposing general workspace-wide tracker access.
- Reducing the set of client-side tools, credentials, filesystem paths, and network destinations
  available to the agent to the minimum needed for the workflow.

The correct controls are deployment-specific, but implementations SHOULD document them clearly and
treat harness hardening as part of the core safety model rather than an optional afterthought.

## 16. Reference Algorithms (Language-Agnostic)

### 16.1 Service Startup

```text
function start_service():
  configure_logging()
  start_observability_outputs()
  start_workflow_watch(on_change=reload_and_reapply_workflow)

  state = {
    poll_interval_ms: get_config_poll_interval_ms(),
    max_concurrent_agents: get_config_max_concurrent_agents(),
    running: {},
    claimed: set(),
    retry_attempts: {},
    completed: set(),
    agent_totals: {input_tokens: 0, output_tokens: 0, total_tokens: 0, seconds_running: 0},
    agent_rate_limits: null
  }

  validation = validate_dispatch_config()
  if validation is not ok:
    log_validation_error(validation)
    fail_startup(validation)

  startup_terminal_workspace_cleanup()
  schedule_tick(delay_ms=0)

  event_loop(state)
```

### 16.2 Poll-and-Dispatch Tick

```text
on_tick(state):
  state = reconcile_running_issues(state)

  validation = validate_dispatch_config()
  if validation is not ok:
    log_validation_error(validation)
    notify_observers()
    schedule_tick(state.poll_interval_ms)
    return state

  issues = tracker.fetch_candidate_issues()
  if issues failed:
    log_tracker_error()
    notify_observers()
    schedule_tick(state.poll_interval_ms)
    return state

  for issue in sort_for_dispatch(issues):
    if no_available_slots(state):
      break

    if should_dispatch(issue, state):
      state = dispatch_issue(issue, state, attempt=null)

  notify_observers()
  schedule_tick(state.poll_interval_ms)
  return state
```

### 16.3 Reconcile Active Runs

```text
function reconcile_running_issues(state):
  state = reconcile_stalled_runs(state)

  running_ids = keys(state.running)
  if running_ids is empty:
    return state

  refreshed = tracker.fetch_issue_states_by_ids(running_ids)
  if refreshed failed:
    log_debug("keep workers running")
    return state

  for issue in refreshed:
    if issue.state in terminal_states:
      state = terminate_running_issue(state, issue.id, cleanup_workspace=true)
    else if issue.state in active_states:
      state.running[issue.id].issue = issue
    else:
      state = terminate_running_issue(state, issue.id, cleanup_workspace=false)

  return state
```

### 16.4 Dispatch One Issue

```text
function dispatch_issue(issue, state, attempt):
  state.claimed.add(issue.id)

  worker = spawn_worker(
    fn -> run_agent_attempt(issue, attempt, parent_orchestrator_pid) end
  )

  if worker spawn failed:
    state.claimed.remove(issue.id)
    return schedule_retry(state, issue.id, next_attempt(attempt), {
      identifier: issue.identifier,
      error: "failed to spawn agent"
    })

  state.running[issue.id] = {
    worker_handle,
    monitor_handle,
    identifier: issue.identifier,
    issue,
    session_id: null,
    agent_pid: null,
    last_agent_message: null,
    last_agent_event: null,
    last_agent_timestamp: null,
    agent_input_tokens: 0,
    agent_output_tokens: 0,
    agent_total_tokens: 0,
    last_reported_input_tokens: 0,
    last_reported_output_tokens: 0,
    last_reported_total_tokens: 0,
    retry_attempt: normalize_attempt(attempt),
    started_at: now_utc()
  }

  state.retry_attempts.remove(issue.id)
  return state
```

### 16.5 Worker Attempt (Workspace + Prompt + Agent)

```text
function run_agent_attempt(issue, attempt, orchestrator_channel):
  workspace = workspace_manager.create_for_issue(issue.identifier)
  if workspace failed:
    fail_worker("workspace error")

  if run_hook("before_run", workspace.path) failed:
    fail_worker("before_run hook error")

  session = app_server.start_session(workspace=workspace.path)
  if session failed:
    run_hook_best_effort("after_run", workspace.path)
    fail_worker("agent session startup error")

  max_turns = config.agent.max_turns
  turn_number = 1

  while true:
    prompt = build_turn_prompt(workflow_template, issue, attempt, turn_number, max_turns)
    if prompt failed:
      app_server.stop_session(session)
      run_hook_best_effort("after_run", workspace.path)
      fail_worker("prompt error")

    turn_result = app_server.run_turn(
      session=session,
      prompt=prompt,
      issue=issue,
      on_message=(msg) -> send(orchestrator_channel, {codex_update, issue.id, msg})
    )

    if turn_result failed:
      app_server.stop_session(session)
      run_hook_best_effort("after_run", workspace.path)
      fail_worker("agent turn error")

    refreshed_issue = tracker.fetch_issue_states_by_ids([issue.id])
    if refreshed_issue failed:
      app_server.stop_session(session)
      run_hook_best_effort("after_run", workspace.path)
      fail_worker("issue state refresh error")

    issue = refreshed_issue[0] or issue

    if issue.state is not active:
      break

    if turn_number >= max_turns:
      break

    turn_number = turn_number + 1

  app_server.stop_session(session)
  run_hook_best_effort("after_run", workspace.path)

  exit_normal()
```

### 16.6 Worker Exit and Retry Handling

```text
on_worker_exit(issue_id, reason, state):
  running_entry = state.running.remove(issue_id)
  state = add_runtime_seconds_to_totals(state, running_entry)

  if reason == normal:
    state.completed.add(issue_id)  # bookkeeping only
    state = schedule_retry(state, issue_id, 1, {
      identifier: running_entry.identifier,
      delay_type: continuation
    })
  else:
    state = schedule_retry(state, issue_id, next_attempt_from(running_entry), {
      identifier: running_entry.identifier,
      error: format("worker exited: %reason")
    })

  notify_observers()
  return state
```

```text
on_retry_timer(issue_id, state):
  retry_entry = state.retry_attempts.pop(issue_id)
  if missing:
    return state

  candidates = tracker.fetch_candidate_issues()
  if fetch failed:
    return schedule_retry(state, issue_id, retry_entry.attempt + 1, {
      identifier: retry_entry.identifier,
      error: "retry poll failed"
    })

  issue = find_by_id(candidates, issue_id)
  if issue is null:
    state.claimed.remove(issue_id)
    return state

  if available_slots(state) == 0:
    return schedule_retry(state, issue_id, retry_entry.attempt + 1, {
      identifier: issue.identifier,
      error: "no available orchestrator slots"
    })

  return dispatch_issue(issue, state, attempt=retry_entry.attempt)
```

## 17. Test and Validation Matrix

A conforming implementation SHOULD include tests that cover the behaviors defined in this
specification.

Validation profiles:

- `Core Conformance`: deterministic tests REQUIRED for all conforming implementations.
- `Extension Conformance`: REQUIRED only for OPTIONAL features that an implementation chooses to
  ship.
- `Real Integration Profile`: environment-dependent smoke/integration checks RECOMMENDED before
  production use.

Unless otherwise noted, Sections 17.1 through 17.10 are `Core Conformance`. Bullets that begin with
`If ... is implemented` are `Extension Conformance`.

### 17.1 Workflow and Config Parsing

- Workflow file path precedence:
  - explicit runtime path is used when provided
  - cwd default is `WORKFLOW.md` when no explicit runtime path is provided
- Workflow file changes are detected and trigger re-read/re-apply without restart
- Invalid workflow reload keeps last known good effective configuration and emits an
  operator-visible error
- Missing `WORKFLOW.md` returns typed error
- Invalid YAML front matter returns typed error
- Front matter non-map returns typed error
- Config defaults apply when OPTIONAL values are missing, including tracker-kind state defaults
- `project.name` is exposed when configured and falls back to the folder containing `WORKFLOW.md`
- `tracker.kind` validation enforces currently supported kinds (`linear`, `jira`, `beads`)
- `tracker.api_key` works (including `$VAR` indirection)
- `tracker.api_token`, `tracker.email`, `tracker.endpoint`, `tracker.project_key`,
  `tracker.project_slug`, and `tracker.jql` work for Jira, including `$VAR` indirection where
  applicable
- `$VAR` resolution works for tracker credentials and path values
- `~` path expansion works
- `codex.command` is preserved as a shell command string
- Per-state concurrency override map normalizes state names and ignores invalid values
- Prompt template renders `issue` and `attempt`
- Prompt rendering fails on unknown variables (strict mode)

### 17.2 Workspace Manager and Safety

- Deterministic workspace path per issue identifier
- Missing workspace directory is created
- Existing workspace directory is reused
- Internal `.preparing` and `.failed` directories are excluded from issue workspace listing
- Staged workspace preparation runs `after_create` before promoting to the canonical workspace path
- Empty canonical workspaces are treated as unprepared when `after_create` is configured
- Failed staged preparation is retained under `.failed` and does not leave an empty canonical
  workspace to be reused
- Failed staged preparation writes `prepare-error.txt` into the retained failed workspace
- `.preparing` and `.failed` entries older than 24 hours are cleaned up
- Existing non-directory path at workspace location is handled safely (replace or fail per
  implementation policy)
- OPTIONAL workspace population/synchronization errors are surfaced
- `after_create` hook runs only during staged preparation of missing or empty canonical workspaces
- `before_run` hook runs before each attempt and failure/timeouts abort the current attempt
- `after_run` hook runs after each attempt and failure/timeouts are logged and ignored
- `before_remove` hook runs on cleanup and failures/timeouts are ignored
- Workspace path sanitization and root containment invariants are enforced before agent launch
- Agent launch uses the per-issue workspace path as cwd and rejects out-of-root paths

### 17.3 Issue Tracker Client

- Candidate issue fetch uses configured active states and tracker scope
- Empty `fetch_issues_by_states([])` returns empty without invoking the tracker
- Issue state refresh by ID returns minimal normalized issues
- Labels/tags are normalized to lowercase
- Blocking relationships are normalized to `blocked_by`; non-blocking relationships are ignored
- Assignee/owner metadata is normalized when the selected tracker exposes it
- Error mapping covers transport/CLI execution failures and malformed payloads
- For `tracker.kind == "linear"`:
  - candidate fetch uses `tracker.project_slug`
  - Linear query uses the specified project filter field (`slugId`)
  - pagination preserves order across multiple pages
  - issue state refresh query uses GraphQL ID typing (`[ID!]`) as specified in Section 11.2
  - request errors, non-200 responses, GraphQL errors, and malformed payloads map to Section 11.6
    categories
- For `tracker.kind == "jira"`:
  - candidate fetch uses `tracker.jql` when configured
  - default candidate fetch uses `tracker.project_key`, or `tracker.project_slug` when
    `tracker.project_key` is absent, and configured active states
  - Jira requests use Basic auth with configured email and API token
  - pagination preserves order across multiple pages using `nextPageToken`
  - issue state refresh query uses stable Jira IDs, or documented key-based IDs if the
    implementation chooses key-as-ID normalization
  - ADF descriptions normalize to plain text
  - Jira labels normalize to labels and support `tracker.required_labels`
  - blocking `Blocks` issue links map to `blocked_by`
  - request errors, non-2xx responses, malformed payloads, and pagination integrity failures map to
    Section 11.6 categories
- For `tracker.kind == "beads"`:
  - candidate fetch uses `bd ready --json` when available, otherwise status listing plus blocker
    filtering
  - tags normalize to labels and support `tracker.required_labels`
  - blocking dependencies map to `blocked_by`
  - CLI execution errors, missing binary, malformed JSON, and unsupported JSON output map to Section
    11.6 categories

### 17.4 Orchestrator Dispatch, Reconciliation, and Retry

- Dispatch sort order is priority then oldest creation time
- Blocker rules:
  - For `tracker.kind == "linear"`:
    - `Todo` issue with non-terminal blockers is not eligible
    - `Todo` issue with terminal blockers is eligible
  - For `tracker.kind == "jira"`:
    - `To Do` issue with non-terminal blockers is not eligible
    - `To Do` issue with terminal blockers is eligible
  - For `tracker.kind == "beads"`:
    - Issues with non-terminal blocking dependencies are not eligible.
    - Implementations SHOULD use `bd ready` as the authoritative source for "has no open blockers"
      when the CLI supports it.
- Active-state issue refresh updates running entry state
- Non-active state stops running agent without workspace cleanup
- Terminal state stops running agent and cleans workspace
- Reconciliation with no running issues is a no-op
- Normal worker exit schedules a short continuation retry (attempt 1)
- Abnormal worker exit increments retries with 10s-based exponential backoff
- Retry backoff cap uses configured `agent.max_retry_backoff_ms`
- Retry queue entries include attempt, due time, identifier, and error
- Stall detection kills stalled sessions and schedules retry
- Slot exhaustion requeues retries with explicit error reason
- If a snapshot API is implemented, it returns running rows, retry rows, token totals, and rate
  limits
- If a snapshot API is implemented, timeout/unavailable cases are surfaced

### 17.5 Coding-Agent App-Server Client

- Launch command uses workspace cwd and invokes `bash -lc <codex.command>`
- Session startup follows the targeted Codex app-server protocol.
- Client identity/capability payloads are valid when the targeted Codex app-server protocol requires
  them.
- Policy-related startup payloads use the implementation's documented approval/sandbox settings
- Thread and turn identities exposed by the targeted protocol are extracted and used to emit
  `session_started`
- Request/response read timeout is enforced
- Turn timeout is enforced
- Transport framing required by the targeted protocol is handled correctly
- For stdio-based transports, diagnostic stderr handling is kept separate from the protocol stream
- Command/file-change approvals are handled according to the implementation's documented policy
- Unsupported dynamic tool calls are rejected without stalling the session
- User input requests are handled according to the implementation's documented policy and do not
  stall indefinitely
- Usage and rate-limit telemetry exposed by the targeted protocol is extracted
- Approval, user-input-required, usage, and rate-limit signals are interpreted according to the
  targeted protocol
- If client-side tools are implemented, session startup advertises the supported tool specs
  using the targeted app-server protocol
- If the `linear_graphql` client-side tool extension is implemented:
  - the tool is advertised to the session
  - valid `query` / `variables` inputs execute against configured Linear auth
  - top-level GraphQL `errors` produce `success=false` while preserving the GraphQL body
  - invalid arguments, missing auth, and transport failures return structured failure payloads
  - unsupported tool names still fail without stalling the session
- If the `jira_rest` client-side tool extension is implemented:
  - the tool is advertised to the session
  - valid `method` / `path` / `query` / `body` inputs execute against configured Jira auth
  - absolute URLs, paths outside `/rest/api/`, invalid methods, and invalid argument shapes are
    rejected
  - non-2xx HTTP responses produce `success=false` while preserving bounded response context
  - invalid arguments, missing auth, and transport failures return structured failure payloads
  - unsupported tool names still fail without stalling the session

### 17.6 Pi Agent RPC Client

If `agent_kind == "pi"` is implemented:

- `pi` subprocess is launched with `bash -lc` in the workspace directory.
- The implementation verifies the process speaks the Pi RPC JSONL protocol.
- Pi RPC framing uses LF-delimited JSONL with no Unicode newline splitting.
- Initial prompt is sent via the `prompt` RPC command with the rendered workflow prompt.
- Workspace path is supplied to Pi through protocol-supported means or documented equivalently.
- Continuation turns reuse the same live Pi process instead of spawning a new subprocess.
- Events include `agent_start`, `agent_end`, `turn_start`, `turn_end`, `tool_execution_start`,
  `tool_execution_update`, `tool_execution_end`, `message_update`, `compaction_start`,
  `compaction_end`, `auto_retry_start`, `auto_retry_end`, and `extension_error`.
- Timeouts map to `pi.read_timeout_ms`, `pi.turn_timeout_ms`, and `pi.stall_timeout_ms`.
- Tool execution events and bash results are surfaced without breaking orchestrator contract.
- Extension UI dialog requests are handled or auto-resolved per documented policy.
- Token/runtime accounting uses Pi `get_session_stats` and message usage fields.
- Session identifier is derived from Pi metadata or process lifetime and documented.
- `beads_cli`, `linear_graphql`, or `jira_rest` client-side tools, if implemented, use the Pi
  tool-call protocol when `agent_kind == "pi"`.

### 17.7 Observability

- Validation failures are operator-visible
- Structured logging includes issue/session context fields
- Logging sink failures do not crash orchestration
- Token/rate-limit aggregation remains correct across repeated agent updates
- If a human-readable status surface is implemented, it is driven from orchestrator state and does
  not affect correctness
- If humanized event summaries are implemented, they cover key wrapper/agent event classes without
  changing orchestrator behavior

### 17.8 Beads Tracker Adapter (Core Conformance for Beads Support)

If `tracker.kind == "beads"` is implemented:

- `bd` CLI invocation uses `tracker.bd_command` with `bash -lc` in the expected working directory.
- Candidate fetch returns issues matching `tracker.active_states`.
- Terminal-state issue fetch returns issues matching `tracker.terminal_states` for startup cleanup.
- Issue-state refresh by ID returns minimal normalized issues.
- Blocking dependencies from Beads are mapped to `blocked_by` with non-blocking types ignored.
- Beads priority integers pass through as `issue.priority`.
- Beads `status` maps to `issue.state`.
- Labels/tags are lowercased.
- Errors from CLI execution, missing binary, or malformed JSON produce the recommended error
  categories in Section 11.6.
- `deferred` is non-terminal and not dispatch-active by default unless configured in
  `tracker.active_states`.

### 17.9 Jira Tracker Adapter (Core Conformance for Jira Support)

If `tracker.kind == "jira"` is implemented:

- Jira connection config validates `tracker.endpoint`, `tracker.email`, and `tracker.api_token`.
- Jira requests use the configured endpoint and Basic auth credentials.
- Candidate fetch uses custom `tracker.jql` when configured.
- Candidate fetch without custom JQL uses `tracker.project_key`, or `tracker.project_slug` when
  `tracker.project_key` is absent, and configured active states.
- Terminal-state issue fetch returns issues matching `tracker.terminal_states` for startup cleanup
  and is project-scoped when `tracker.project_key` or `tracker.project_slug` is configured.
- Issue-state refresh by ID returns minimal normalized issues.
- Search requests request labels, assignee, timestamps, priority, status, description, and
  `issuelinks`.
- Jira issue keys map to `issue.identifier`.
- Jira REST IDs map to `issue.id`, unless the implementation documents key-as-ID normalization and
  applies it consistently to state refresh.
- Jira ADF descriptions normalize to plain text.
- Jira labels are lowercased.
- Blocking `Blocks` issue links are mapped to `blocked_by` with non-blocking links ignored.
- Errors from transport failures, non-2xx responses, malformed JSON, invalid JQL inputs, or
  pagination integrity failures produce the recommended error categories in Section 11.6.

### 17.10 CLI and Host Lifecycle

- CLI accepts a positional workflow path argument (`path-to-WORKFLOW.md`)
- CLI accepts an OPTIONAL startup working directory argument (`-workdir <path>`)
- CLI applies `-workdir` before resolving the default workflow path or a relative positional
  workflow path
- CLI exports `SYMPHONY_WORKDIR` as the effective absolute working directory after applying
  `-workdir`
- CLI-provided hook environment variables use the `SYMPHONY_` prefix; `SYMPHONY_WORKDIR` is the
  canonical source checkout/workflow working directory variable for hooks.
- CLI uses `./WORKFLOW.md` in the effective working directory when no workflow path argument is
  provided
- CLI help shows the positional workflow path form (`[path-to-WORKFLOW.md]`)
- CLI errors on nonexistent explicit workflow path or missing default `./WORKFLOW.md`
- CLI errors when the explicit startup working directory cannot be used
- CLI surfaces startup failure cleanly
- CLI exits with success when application starts and shuts down normally
- CLI exits nonzero when startup fails or the host process exits abnormally

### 17.11 Real Integration Profile (RECOMMENDED)

These checks are RECOMMENDED for production readiness and MAY be skipped in CI when credentials,
network access, or external service permissions are unavailable.

- A real Linear smoke test can be run with valid credentials supplied by `LINEAR_API_KEY` or a
  documented local bootstrap mechanism (for example `~/.linear_api_key`).
- A real Jira smoke test can be run with valid credentials supplied by configured Jira environment
  variables such as `JIRA_EMAIL` and `JIRA_API_TOKEN`, plus an isolated `tracker.project_key`,
  `tracker.project_slug`, or `tracker.jql`.
- A real Beads smoke test can be run against an isolated local Beads database/repository using the
  configured `tracker.bd_command`.
- Real integration tests SHOULD use isolated test identifiers/workspaces and clean up tracker
  artifacts when practical.
- A skipped real-integration test SHOULD be reported as skipped, not silently treated as passed.
- If a real-integration profile is explicitly enabled in CI or release validation, failures SHOULD
  fail that job.

## 18. Implementation Checklist (Definition of Done)

Use the same validation profiles as Section 17:

- Section 18.1 = `Core Conformance`
- Section 18.2 = `Extension Conformance`
- Section 18.3 = `Real Integration Profile`

### 18.1 REQUIRED for Conformance

- Workflow path selection supports explicit runtime path and cwd default
- `WORKFLOW.md` loader with YAML front matter + prompt body split
- Typed config layer with defaults and `$` resolution
- Dynamic `WORKFLOW.md` watch/reload/re-apply for config and prompt
- Polling orchestrator with single-authority mutable state
- Issue tracker client with candidate fetch + state refresh + terminal fetch, supporting at least
  one of `linear`, `jira`, or `beads`.
- Workspace manager with sanitized per-issue workspaces
- Workspace lifecycle hooks (`after_create`, `before_run`, `after_run`, `before_remove`)
- Hook timeout config (`hooks.timeout_ms`, default `60000`)
- Coding-agent runtime client supports at least one of:
  - Codex app-server with JSON line protocol (`codex.command`)
  - Pi RPC mode with JSONL over stdio (`pi.command`)
- Agent config supports `agent_kind` selection with defaults and validation
- Strict prompt rendering with `issue` and `attempt` variables
- Configured workspace prompt include files are appended after hooks and before agent launch
- Exponential retry queue with continuation retries after normal exit
- Configurable retry backoff cap (`agent.max_retry_backoff_ms`, default 5m)
- Reconciliation that stops runs on terminal/non-active tracker states
- Workspace cleanup for terminal issues (startup sweep + active transition)
- Structured logs with `issue_id`, `issue_identifier`, and `session_id`
- Operator-visible observability (structured logs; OPTIONAL snapshot/status surface)

### 18.2 RECOMMENDED Extensions (Not REQUIRED for Conformance)

- HTTP server extension honors CLI `--port` over `server.port`, uses a safe default bind host, and
  exposes the baseline endpoints/error semantics in Section 13.7 if shipped.
- HTTP server extension exposes the dashboard snapshot fields from Section 13.3.1 if shipped,
  including lifecycle phases, retry row titles, effective max turns, and capped recent agent
  messages.
- OPTIONAL pull request metadata extension follows Section 13.7.3 when shipped and never affects
  core orchestration behavior.
- `linear_graphql` client-side tool extension exposes raw Linear GraphQL access through the
  app-server or RPC session using configured Symphony auth.
- `jira_rest` client-side tool extension exposes controlled Jira REST API access through the
  app-server or RPC session using configured Symphony auth.
- `beads_cli` client-side tool extension exposes controlled Beads CLI access through the app-server
  or RPC session using configured Symphony tracker context.
- TODO: Persist retry queue and session metadata across process restarts.
- TODO: Make observability settings configurable in workflow front matter without prescribing UI
  implementation details.
- TODO: Add first-class tracker write APIs (comments/state transitions) in the orchestrator instead
  of only via agent tools.
- TODO: Add pluggable issue tracker adapters beyond the first-class Linear, Jira, and Beads
  adapters.

### 18.3 Operational Validation Before Production (RECOMMENDED)

- Run the `Real Integration Profile` from Section 17.11 with valid credentials and network access.
- Verify hook execution and workflow path resolution on the target host OS/shell environment.
- If the OPTIONAL HTTP server is shipped, verify the configured port behavior and loopback/default
  bind expectations on the target environment.
- For `agent_kind == "pi"`, verify Pi RPC startup, prompt delivery, and event streaming.

## Appendix A. SSH Worker Extension (OPTIONAL)

This appendix describes a common extension profile in which Symphony keeps one central
orchestrator but executes worker runs on one or more remote hosts over SSH.

Extension config:

- `worker.ssh_hosts` (list of SSH host strings, OPTIONAL)
  - When omitted, work runs locally.
- `worker.max_concurrent_agents_per_host` (positive integer, OPTIONAL)
  - Shared per-host cap applied across configured SSH hosts.

### A.1 Execution Model

- The orchestrator remains the single source of truth for polling, claims, retries, and
  reconciliation.
- `worker.ssh_hosts` provides the candidate SSH destinations for remote execution.
- Each worker run is assigned to one host at a time, and that host becomes part of the run's
  effective execution identity along with the issue workspace.
- `workspace.root` is interpreted on the remote host, not on the orchestrator host.
- The coding-agent app-server is launched over SSH stdio instead of as a local subprocess, so the
  orchestrator still owns the session lifecycle even though commands execute remotely.
- Continuation turns inside one worker lifetime SHOULD stay on the same host and workspace.
- A remote host SHOULD satisfy the same basic contract as a local worker environment: reachable
  shell, writable workspace root, coding-agent executable, and any required auth or repository
  prerequisites.

### A.2 Scheduling Notes

- SSH hosts MAY be treated as a pool for dispatch.
- Implementations MAY prefer the previously used host on retries when that host is still
  available.
- `worker.max_concurrent_agents_per_host` is an OPTIONAL shared per-host cap across configured SSH
  hosts.
- When all SSH hosts are at capacity, dispatch SHOULD wait rather than silently falling back to a
  different execution mode.
- Implementations MAY fail over to another host when the original host is unavailable before work
  has meaningfully started.
- Once a run has already produced side effects, a transparent rerun on another host SHOULD be
  treated as a new attempt, not as invisible failover.

### A.3 Problems to Consider

- Remote environment drift:
  - Each host needs the expected shell environment, coding-agent executable, auth, and repository
    prerequisites.
- Workspace locality:
  - Workspaces are usually host-local, so moving an issue to a different host is typically a cold
    restart unless shared storage exists.
- Path and command safety:
  - Remote path resolution, shell quoting, and workspace-boundary checks matter more once execution
    crosses a machine boundary.
- Startup and failover semantics:
  - Implementations SHOULD distinguish host-connectivity/startup failures from in-workspace agent
    failures so the same ticket is not accidentally re-executed on multiple hosts.
- Host health and saturation:
  - A dead or overloaded host SHOULD reduce available capacity, not cause duplicate execution or an
    accidental fallback to local work.
- Cleanup and observability:
  - Operators need to know which host owns a run, where its workspace lives, and whether cleanup
    happened on the right machine.
