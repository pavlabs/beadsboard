# Structuring work for beadsboard

Read this before creating or reshaping beads in a project that is driven by
[beadsboard](../README.md). It is the contract between how you file work and what
the board can do with it: the board derives a two-level epic/task graph from `bd`,
orders it by dependencies, and hands individual beads to autonomous coding agents.
Work that ignores the shape below still exists in `bd`, but the board cannot
display it in the right place, order it, or run it.

This is *not* a `bd` tutorial. Run `bd prime` for the command reference and the
sync architecture; this document only covers what beadsboard adds on top.

## What the board reads

`bd export --all`, on a poll. Nothing else. Of each issue it decodes exactly:

```
id  title  status  priority  issue_type  description  notes  labels
dependencies[]  updated_at  external_ref
```

Anything else `bd` stores — `acceptance_criteria`, `design`, `assignee`,
`close_reason`, `estimate`, metadata — **is invisible on the board**, and editing
it alone will not trigger a GitHub push, because the sync digest is computed over
title, status, priority, description, notes and labels only.

**Put everything an agent or a human needs into `--description` and `--notes`.**
`--acceptance` and `--design` are fine as a secondary record, but never as the
only home for a requirement.

## The shape

The board is strictly two levels: **epics** contain **tasks**. There is no third.

- An issue is an epic when `issue_type == "epic"`. **Every other type — `task`,
  `feature`, `bug`, `chore`, `decision` — is a task**, whatever its name suggests.
- A task's epic is resolved in this order: its `parent-child` dependency edge, then
  the `<epic-id>.N` id convention. Both are set for you by `bd create --parent <epic>`.
- A task whose parent resolves to nothing — no parent, or a parent that is itself a
  task — is dropped into a synthetic `~orphans` epic that sorts last. That bucket is
  a display device, not a bead: GitHub sync skips it and you cannot run an agent on
  it. **Seeing work there means the hierarchy is wrong; fix the parent.**
- Nesting a subtask under a task therefore does not create a third level, it
  creates an orphan. If a task needs decomposing, promote its parent to an epic or
  make the pieces siblings under the existing epic.

```bash
bd create --title "Attention inbox" --type epic --priority 1
bd create --title "Fold PR state into the inbox" --type task --parent beadsboard-x1y
```

## Dependencies drive the ordering, and the ordering drives the agents

Use `blocks` edges — `bd dep add <blocked> <blocker>` — for real prerequisites, and
nothing else. They are load-bearing in four places:

1. **Task order.** Tasks inside an epic are topologically sorted by their in-epic
   `blocks` edges, tiebroken by id. Prerequisites list above the work they unblock.
2. **Derived task status.** `closed → done`, `in_progress → wip`, and explicit
   `blocked` stays blocked. An `open` task is ready when every blocker is closed;
   unresolved blockers make it blocked. Deferred/custom statuses without blockers
   remain unfinished but are not counted as ready. Explicitly blocked beads land
   in the attention inbox.
3. **Epic order.** A `blocks` edge whose two ends live in different epics is lifted
   to an epic-level prerequisite. Epics are sorted by priority first, then by that
   build order, then by title.
4. **Auto-dispatch (`D`).** The board runs an epic's open tasks in dependency order,
   filling free agent slots and holding blocked tasks until their blockers close.
   This is the single biggest reason to get `blocks` edges right: a missing edge
   means two agents work in the wrong order, and a spurious one serialises work
   that could have run in parallel.

Cycles are tolerated but degrade the ordering — the members are appended at the end
deterministically instead of being placed. Do not create them.

**Priority** is `0..4`, 0 highest. On an epic it is the primary sort key of the
whole board. On a task it does not change display order (dependencies do), but it
does decide which ready task claims a scarce agent slot first, because dispatch
admits in `bd ready` order.

The task-list `O` filter puts in-progress tasks first; this changes display order
only. `A` restores all tasks and `C` shows closed tasks. Filters never alter the
dependency graph, dispatch scope, epic progress totals or dashboard metrics.

## Statuses you may write

Only three exist in `bd`: `open`, `in_progress`, `closed`. `done`, `wip`, `ready`
and `blocked` are **derived by the board** and must never be written anywhere.

- `bd update <id> --claim` sets `in_progress` and takes the bead out of `bd ready`.
  That is what stops the board from dispatching a second agent onto work already
  underway — claim before you start, not after.
- `status::*` labels are `bd`'s carrier for GitHub sync in both directions. Never
  hand-write one.

## Writing a task an autonomous agent can actually finish

When the board launches a coding agent on a task, it cuts a fresh git worktree and
branch (`beadsboard/<short-id>-<n>`, in a scratch directory outside the project) and
hands the agent a prompt amounting to: read the bead, implement it, run the
project's checks, commit, update the bead's status, open a PR that `Closes #N`.
One task, one agent, one worktree, one branch, one PR.

Size and write tasks so that holds:

- **One repo per task.** An agent gets exactly one worktree. A task spanning two
  sub-repos in a meta-repo layout cannot be run; split it per repo and wire a
  `blocks` edge between the halves.
- **Finishable in one session,** with a commit at the end. If it cannot end in a
  commit, it is a planning bead, not a coding task.
- **Self-contained description.** The agent starts cold with `bd prime` and
  `bd show <id>` and nothing else. State the change, the files or the approach, how
  to verify it, and what is explicitly out of scope. A reference to "the discussion
  above" is worthless to it.
- **Decisions resolved before filing.** An agent that hits an open question stops
  and asks, which parks the whole task until a human answers. Filing the decision as
  its own bead, blocking the implementation task, is strictly better than making the
  implementer discover it.
- **Verifiable.** Name the check — the test, the command, the observable behaviour.
  "Run the project's checks" is the agent's default; if the task needs more, say so.

Epic-scoped agents exist (`a` on an epic works through its open tasks in order),
but they are a blunt instrument. Prefer well-cut tasks and `D`.

## Labels

- `repo::<name>` — meta-repo routing. Set it on the **epic**; tasks inherit it. The
  value must be a single plain path segment naming a subdirectory of the beads root:
  the agent worktrees `<root>/<name>`, and the bead's GitHub issue is created in that
  subdirectory's `origin` remote so `Closes #N` resolves. Anything with a slash, a
  dot segment or an empty value is refused and the bead falls back to the root repo.
- `status::*` — reserved, see above.
- Everything else is free-form and purely descriptive. Labels are part of the sync
  digest, so adding one does cost a GitHub issue update on the next reload.

## Working as an agent under beadsboard

If you *are* the agent the board launched, the prompt already told you most of this.
The parts worth restating:

- **The beads live outside your worktree.** Prefix every command with
  `-C <beads-root>`: `bd -C /path/to/root show <id>`. The worktree is cut into a
  scratch directory outside the project, so a bare `bd` there finds no `.beads` at
  all.
- **Stop rather than guess.** End your final message with the marker
  `⟨NEEDS INPUT⟩` followed by the question. That is what promotes the bead into the
  attention inbox instead of leaving a silent failure; the worktree is kept so the
  human can resume your session in place.
- **Do not announce a PR you did not open.** The board detects your branch and PR
  from git and GitHub, never from your prose.
- **Comments tagged `[beadsboard]`** on a bead are the board's own lifecycle record
  (`spawn`, `session`, `finish`). Read them; do not write them.
- **Update the bead's status before you finish.** The board reflects `bd`, not your
  summary.
- External sessions you launched yourself can be surfaced on their bead by working
  on a `bead/<id>` branch with `hooks/session-agent.sh` wired into
  `SessionStart`/`SessionEnd` — see the README.

## Anti-patterns

- Markdown TODO lists, `TODO:` comments, or a plan file as the record of work. The
  board cannot see them and `bd` already does the job.
- Editing `.beads/issues.jsonl`, or reaching for `bd import` in normal operation.
  It is a passive export; the Dolt database is the source of truth.
- `bd edit` — it opens `$EDITOR` and hangs a non-interactive agent. Use
  `bd update --title/--description/--notes/--status/--priority` inline.
- Subtasks under tasks (creates orphans), tasks with no parent (same), epics with no
  tasks (they read `open` forever and are never dispatchable).
- Writing a derived status (`done`, `wip`, `ready`, `blocked`) into `--status`.
- One giant task per epic. It defeats dependency ordering, gives auto-dispatch a
  single slot to fill, and produces an unreviewable PR.
- Filing an epic without deciding its dependencies. An epic with no `blocks` edges
  is sorted by priority alone, which is usually not the order it must be built in.

## Checklist before handing a plan to the board

- [ ] Every task has an epic parent; nothing sits in `~orphans`.
- [ ] Every epic has at least one task.
- [ ] `blocks` edges exist for every real prerequisite, and nowhere else.
- [ ] No dependency cycles.
- [ ] Epic priorities reflect the order you actually want to build in.
- [ ] Every task is one repo, one session, one commit, one PR.
- [ ] Every task description stands alone: change, approach, verification, scope.
- [ ] Open questions are their own beads, blocking the work they gate.
- [ ] Statuses are only `open` / `in_progress` / `closed`.
- [ ] Meta-repo work carries `repo::<name>` on its epic.

## Command crib

```bash
bd prime                                    # full bd context; run this first
bd create --title T --type epic --priority 1
bd create --title T --type task --parent <epic> --description D
bd dep add <blocked> <blocker>              # a 'blocks' edge
bd update <id> --claim                      # -> in_progress, leaves bd ready
bd update <id> --description D --notes N
bd update <id> --add-label repo::web
bd close <id>
bd ready                                    # what is dispatchable right now
bd blocked                                  # what is waiting, and on what
bd show <id>
```
