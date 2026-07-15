# Beads

This repo uses Beads for shared issue tracking.

- Prefix: `SYM`
- Dolt remote: `git+https://github.com/tptodorov/symphony.git`
- Review export path: `.beads/issues.jsonl`
- Runtime database files: ignored by `.beads/.gitignore`

Fresh clone setup:

```sh
bd bootstrap --yes
bd dolt pull
```

Common commands:

```sh
bd ready
bd show <issue-id>
bd create "Title" -t task -p 2
bd update <issue-id> --claim
bd close <issue-id>
bd dolt push
```
