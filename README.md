# beadsboard

A terminal UI for browsing open [beads](https://github.com/gastownhall/beads) epics,
their inter-dependencies, and the tasks within each epic — with live descriptions as
you move the cursor.

```
beadsboard [--source DIR]      # --source defaults to the current directory; must contain a .beads/
```

## What it shows

- **Epics list** ordered by priority then build order (a topological sort of the
  inter-epic dependency graph), each annotated with a status glyph and the epics it
  `needs`.
- **Drill in** (→ / enter) to an epic's tasks, ordered so prerequisites precede the
  work they unblock; each task shows `ready` / `blocked` / `in progress` / `done` and
  what it `waits` on. `←` / esc goes back.
- **Detail pane** updates live as the cursor moves: title, id, status, priority,
  labels, blockers/unlocks, and the full description. Cross-epic blockers are
  shown epic-qualified (`<epic>#N`) so a bare `#N` is never ambiguous.
- **Edit** the highlighted epic or task with `e`: it hands the terminal to
  `bd edit <id>` (your `$EDITOR` on the item's description) and reloads on return.

Tasks whose epic can't be resolved are surfaced under a synthetic `orphaned tasks`
epic rather than silently dropped.

Keys: `↑/↓` move · `→` open epic · `←` back · `e` edit · `r` refresh · `pgup/pgdn` scroll detail · `q` quit.

## How it stays live

Data is loaded with a single `bd export --all` (one cold Dolt start, ~0.3s), not
per-issue `bd show` calls — concurrent `bd` invocations contend on the embedded Dolt
engine and are slower than one bulk export.

The board auto-refreshes when the issue data changes: a lightweight fingerprint of the
`.beads/` tree (`path`, `size`, `modtime`) is polled once a second. Because `bd export`
itself churns Dolt's journal, the fingerprint is re-baselined immediately after each
load, so only an *external* `bd` write triggers a reload — never the app's own reads.

## Layout

- `internal/beads` — `bd` client (`export --all`), graph derivation (epic DAG,
  per-epic topo order, task/epic statuses), and the `.beads` fingerprint watcher.
- `internal/ui` — the bubbletea model, two-level browser, and detail rendering.
- `main.go` — argument handling and program launch.

## Development

```bash
go test ./...
go build -o beadsboard .
```

Stack: bubbletea + lipgloss + bubbles.
