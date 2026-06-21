---
tracker:
  kind: beads
  bd_command: bd
  active_states: [open, in_progress]
  terminal_states: [closed, tombstone]
  required_labels: []

agent_kind: pi
pi:
  command: pi --mode rpc --no-session --approve
  # provider: openai
  # model: openai/gpt-4o
  session_sync: none
  turn_timeout_ms: 3600000
  read_timeout_ms: 5000
  stall_timeout_ms: 300000

polling:
  interval_ms: 30000

workspace:
  root: .symphony/workspaces

agent:
  max_concurrent_agents: 2
  max_turns: 20
  max_retry_backoff_ms: 300000
---
You are working on a Beads issue.

Issue: {{ issue.identifier }} — {{ issue.title }}
State: {{ issue.state }}
{% if issue.labels %}Tags: {% for label in issue.labels %}{{ label }} {% endfor %}{% endif %}
{% if issue.description %}

Description:
{{ issue.description }}
{% endif %}

Implement the smallest correct change for this issue. Run the relevant checks, then summarize what changed and what passed.
