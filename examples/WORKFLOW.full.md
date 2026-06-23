---
# Fully expanded Symphony workflow example.
#
# This file is meant to be copied, then reduced. Keep one tracker kind and one
# agent_kind active. The commented blocks show equivalent config for the other
# supported trackers/runtimes.

tracker:
  # Supported values: linear, jira, beads.
  kind: jira

  # Common filters.
  assignee: ""
  required_labels: []
  active_states:
    - To Do
    - In Progress
  terminal_states:
    - Closed
    - Cancelled
    - Canceled
    - Duplicate
    - Done

  # Jira. Active when kind: jira.
  endpoint: $JIRA_URL
  email: $JIRA_USERNAME
  api_token: $JIRA_API_TOKEN
  # project_key is Jira-native. project_slug is accepted as a compatibility
  # alias when project_key is absent.
  project_key: MOD
  project_slug: ""
  jql: 'project = MOD AND assignee = currentUser() AND status in ("To Do", "In Progress") ORDER BY priority ASC, created ASC'
  page_size: 50

  # Linear. Use these fields when kind: linear.
  # endpoint: https://api.linear.app/graphql
  # api_key: $LINEAR_API_KEY
  # project_slug: my-project
  # active_states:
  #   - Todo
  #   - In Progress
  # terminal_states:
  #   - Closed
  #   - Cancelled
  #   - Canceled
  #   - Duplicate
  #   - Done
  # page_size: 50

  # Beads. Use these fields when kind: beads.
  # bd_command: bd
  # active_states:
  #   - open
  #   - in_progress
  # terminal_states:
  #   - closed
  #   - tombstone

polling:
  interval_ms: 30000

workspace:
  # Relative paths are resolved from the directory containing WORKFLOW.md.
  root: .symphony/workspaces

hooks:
  timeout_ms: 60000
  after_create: |
    true
  before_run: |
    true
  after_run: |
    true
  before_remove: |
    true

agent:
  max_concurrent_agents: 1
  max_turns: 20
  max_retry_backoff_ms: 300000
  max_concurrent_agents_by_state:
    "To Do": 1
    "In Progress": 1

agent_kind: codex

codex:
  command: codex app-server
  approval_policy: never
  thread_sandbox: workspace-write
  turn_sandbox_policy:
    type: workspaceWrite
  read_timeout_ms: 5000
  turn_timeout_ms: 3600000
  stall_timeout_ms: 300000

pi:
  # Used only when agent_kind: pi.
  command: pi --mode rpc --no-session
  provider: ""
  model: ""
  approval_policy: auto
  session_sync: none
  read_timeout_ms: 5000
  turn_timeout_ms: 3600000
  stall_timeout_ms: 300000

server:
  # Set to 0 for an ephemeral local port, or omit server.port to disable the
  # HTTP status server.
  port: 10000
---
You are working on issue `{{ issue.identifier }}`.

Title: {{ issue.title }}
State: {{ issue.state }}
Labels: {{ issue.labels }}
URL: {{ issue.url }}

{% if attempt %}
This is retry attempt #{{ attempt }}. Reuse the existing workspace state.
{% endif %}

Description:
{% if issue.description %}
{{ issue.description }}
{% else %}
No description provided.
{% endif %}

Implement the smallest correct change for this issue. Run the relevant checks, then summarize what changed and what passed.
