---
tracker:
  kind: beads
agent_kind: pi
agent:
  max_turns: 2
pi:
  command: pi --mode rpc --no-session
  model: codellama
workspace:
  root: /tmp/symphony_test_workspaces
hooks:
  timeout_ms: 60000
---
You are working on issue {{ issue.identifier }}: {{ issue.title }}.

{% if attempt %}
This is attempt {{ attempt }} of the workflow.
{% endif %}

Please implement the requested changes. When you're done, update the issue status and push any changes.