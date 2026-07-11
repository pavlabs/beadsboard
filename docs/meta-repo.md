# Meta-repo setup & ops guide

How to wire up beadsboard's meta-repo mode: one **root repo** holds the beads
(`.beads/`) and planning meta; multiple independent **sub-repos** (`web/`, `api/`,
…) live underneath, each with its own GitHub remote. Every bead carries a
`repo::<name>` label (set on the epic, inherited by tasks) that routes it to a
sub-repo.

Design invariants (already settled):

- **Per-repo issues** — a bead's GitHub issue is created in its OWN sub-repo, so
  the agent's PR and the issue are co-located and same-repo `Closes #N`
  auto-closes on merge.
- **One aggregate board** — a single GitHub Projects v2 board aggregates issues
  across ALL sub-repos.
- **Forward Action** (`.github/workflows/beads-project-status.yml`) maps an
  issue's `status::<value>` label onto the board's Status column. It lives in
  each sub-repo and is parameterized by repo variables `BEADS_PROJECT_OWNER` /
  `BEADS_PROJECT_NUMBER` and a `PROJECTS_TOKEN` secret.
- **Reverse sync** — beadsboard's `G` key reads the board and reconciles bead
  status back into bd.

All commands below were checked against `gh` 2.93.0.

---

## 1. Prerequisite: sub-repo remotes

Every sub-repo needs a GitHub remote before any issue/PR flow works. beadsboard's
`repo::<name>` convention is: **the label value equals the subdir name**, and
beadsboard derives the GitHub `owner/repo` by reading that subdir's `origin`
remote. So the only requirement is: subdir `web/` has an `origin` pointing at the
GitHub repo you want its beads' issues filed in.

### Existing local subdir → new GitHub repo (one command)

From inside the sub-repo directory (must already be a git repo with at least one
commit):

```sh
cd web
git init -b main          # if not already a repo
git add -A && git commit -m "chore: initial commit"

# Creates the GitHub repo, adds it as `origin`, and pushes in one step:
gh repo create <owner>/web --private --source=. --remote=origin --push
```

`--source=.` uses the current dir, `--remote=origin` names the remote (beadsboard
reads exactly `origin`), `--push` publishes existing commits. Omit `<owner>/` to
default to your user; use `<org>/web` for an org repo.

### Wire a remote manually (repo already exists on GitHub)

```sh
cd web
git remote add origin git@github.com:<owner>/web.git
git push -u origin main
```

### Verify beadsboard will resolve it

```sh
git -C web remote get-url origin
# → git@github.com:<owner>/web.git  ⇒  repo::web routes to <owner>/web
```

Repeat per sub-repo (`api/`, etc.). The subdir name is the `repo::` value; pick
subdir names you're happy to see as `repo::<name>` labels.

---

## 2. One board across many repos

Projects v2 boards are **owner-scoped** (user or org), not repo-scoped — a single
board can hold items from any number of repos under that owner. That is what makes
"one aggregate board" possible.

### Auth: the `project` scope

Projects v2 is gated behind a dedicated OAuth scope. The built-in Actions
`GITHUB_TOKEN` **cannot** write org/user Projects v2 (see §3). For your local `gh`:

```sh
gh auth status                       # check current scopes
gh auth refresh -s project           # add read+write project scope
# read-only board access only: gh auth refresh -s read:project
```

### Create the board

```sh
gh project create --owner <owner> --title "Beads"
# --owner "@me" for your personal account; an org login for an org board.
# Note the returned project number — it's BEADS_PROJECT_NUMBER everywhere below.
```

Find it again later:

```sh
gh project list --owner <owner>
```

### Ensure the Status field + option names exist

The forward Action matches single-select option names **exactly**: `Todo`,
`In Progress`, `Blocked`, `Done`. New boards ship with a `Status` field carrying
`Todo`/`In Progress`/`Done` — add `Blocked` (the workflow falls back to
`In Progress` if it's missing, so this is optional). Inspect what exists:

```sh
gh project field-list <number> --owner <owner> --format json \
  | jq '.fields[] | select(.name=="Status") | {name, options: [.options[].name]}'
```

Add a missing option via the UI, or `gh project field-create --help` for the CLI
path (single-select options are set with `--single-select-options`).

### Two mechanisms to aggregate issues onto the board

**(a) Per-repo built-in "Auto-add to project" workflow (recommended).**
In the board UI: *Workflows → Add item to project → Auto-add*, then attach each
sub-repo, with a filter like `is:issue`. GitHub then adds every matching issue
from that repo automatically, forever. You attach each sub-repo once; no tokens,
no cron, no drift. This is the durable mechanism.

You can also `gh project link` a repo to the project — this makes the board show
up in the repo's Projects tab, but **link alone does not auto-add issues**; you
still need the auto-add workflow (or (b)) for population:

```sh
gh project link <number> --owner <owner> --repo <owner>/web
```

**(b) Add items explicitly via `gh project item-add`.**
Useful for backfilling existing issues, or if you'd rather not rely on the UI
workflow. The forward Action already does this defensively (`item-add` before it
edits Status), so during normal operation items self-register. To backfill by
hand:

```sh
gh project item-add <number> --owner <owner> \
  --url https://github.com/<owner>/web/issues/23
```

**Recommendation:** enable the built-in auto-add workflow (a) per sub-repo — it's
the zero-maintenance path. Keep (b) in your pocket for one-off backfills.

---

## 3. Deploy the forward Action to each sub-repo

Issue events (`opened`, `labeled`, `closed`, …) fire in the repo that owns the
issue, so the workflow must live in **each sub-repo**, not the root. Per sub-repo:

### 3a. Copy the workflow in

```sh
mkdir -p web/.github/workflows
cp .github/workflows/beads-project-status.yml web/.github/workflows/
git -C web add .github/workflows/beads-project-status.yml
git -C web commit -m "ci: add beads project status workflow"
git -C web push
```

The workflow file is identical across sub-repos — it's parameterized entirely by
repo variables/secret, so nothing inside it changes per repo.

### 3b. Set the repo variables

These tell the workflow which board to target. `--repo` selects the sub-repo:

```sh
gh variable set BEADS_PROJECT_OWNER  --repo <owner>/web --body "<owner>"
gh variable set BEADS_PROJECT_NUMBER --repo <owner>/web --body "<number>"
```

Until `BEADS_PROJECT_NUMBER` is set, the job self-skips (`if: vars… != ''`), so a
freshly-copied workflow is inert rather than failing.

### 3c. Add the PROJECTS_TOKEN secret

```sh
# PAT must carry the `project` scope (classic PAT: `project`; fine-grained:
# Projects → Read and write). Store it in an env var, don't paste inline.
gh secret set PROJECTS_TOKEN --repo <owner>/web --body "$PROJECTS_TOKEN"
# or pipe from stdin:  printf %s "$PAT" | gh secret set PROJECTS_TOKEN --repo <owner>/web
```

### Why the default token is insufficient

The auto-provisioned `GITHUB_TOKEN` is scoped to the **single repository** running
the workflow and has no `project` permission for user/org Projects v2 — those
boards live at the owner level, outside any one repo's permission boundary. So
`gh project item-edit` with `GITHUB_TOKEN` gets a 403/permission error. A PAT (or
a GitHub App installation token) with the `project` scope is required, supplied as
`PROJECTS_TOKEN` and injected as `GH_TOKEN` for the `gh` calls in the job.

> Org hardening note: if the org restricts PAT access, the token owner must be
> allowed to act on the org's projects, and (for org repos) SSO must be authorized
> on the PAT.

---

## 4. A per-repo status Action is a maintenance burden

Copying the workflow + variables + secret into every sub-repo means N copies to
keep in sync and N `PROJECTS_TOKEN` secrets to rotate. Honest assessment of the
alternatives:

- **Per-repo workflow (current design).** Reacts instantly to issue events;
  self-contained per repo. Cost: N-way duplication; a workflow edit is an N-repo
  fan-out; N secrets to rotate. Fine at a handful of repos.

- **Org-level `.github` reusable/starter workflow.** Keep the YAML in one place
  (the org's `.github` repo) and have each sub-repo call it via
  `uses: <org>/.github/.github/workflows/beads-project-status.yml@main`. This
  de-duplicates the logic, but each sub-repo *still* needs the caller stub plus
  the variables and secret (or an **org-level** variable/secret shared to the
  repos, which removes the per-repo secret sprawl). Best structural option once
  you're on an org.

- **Single scheduled reconciler.** One workflow (in the root repo) or a cron job
  runs `gh` on a schedule, walks all sub-repos' issues, and sets board Status in
  bulk. One token, one place to maintain, no per-repo files. Cost: not
  event-driven — Status lags by the poll interval — and it re-implements the
  label→column mapping as a loop over repos. This overlaps heavily with
  beadsboard's own reverse sync (`G`), so weigh whether you need a server-side
  reconciler at all.

**Recommendation.**
- **Handful of repos (≲3–4):** keep the per-repo workflow. The duplication is
  cheap and you get instant, event-driven updates. Use an **org-level**
  `PROJECTS_TOKEN` secret + org variables if the owner is an org, to kill the
  per-repo secret rotation pain.
- **Many repos:** move to the org-level reusable workflow (shared YAML) with
  org-level variables/secret — one definition, one secret, still event-driven.
  Only reach for a scheduled reconciler if event-driven latency isn't required and
  you want the absolute minimum number of moving parts.

---

## 5. Onboarding checklist — new sub-repo

```
□ Create the sub-repo's GitHub remote and wire `origin`   (§1)
    gh repo create <owner>/<name> --private --source=. --remote=origin --push
    git -C <name> remote get-url origin   # sanity-check owner/repo

□ Aggregate its issues onto the board                      (§2)
    board UI → Workflows → Auto-add → attach <owner>/<name>, filter is:issue
    (optional) gh project link <number> --owner <owner> --repo <owner>/<name>

□ Deploy the forward Action                                (§3a)
    cp .github/workflows/beads-project-status.yml <name>/.github/workflows/
    git -C <name> add/commit/push

□ Set repo variables                                       (§3b)
    gh variable set BEADS_PROJECT_OWNER  --repo <owner>/<name> --body "<owner>"
    gh variable set BEADS_PROJECT_NUMBER --repo <owner>/<name> --body "<number>"

□ Add the PROJECTS_TOKEN secret (or share an org secret)   (§3c)
    gh secret set PROJECTS_TOKEN --repo <owner>/<name> --body "$PROJECTS_TOKEN"

□ Route beads to it: add repo::<name> label to the epic
    (tasks inherit it; <name> must equal the subdir name from §1)

□ Verify end-to-end
    move a bead to in_progress → sync → issue appears in <owner>/<name>,
    lands on the board, Status column reflects the label
```
