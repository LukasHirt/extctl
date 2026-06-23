# gen-specs.md — Phase A spec generator prompt

You are operating **read-only** inside a checkout of the `owncloud/web-extensions`
repository (and, if mounted, a checkout of `owncloud/web` for reference on core
extension points). Your job is to produce exactly **{{N}}** candidate specs for new
oCIS Web extensions. These specs are the *only* artifact a human reviewer sees before
deciding what gets built — no code exists yet. Each spec must be a tight, accurate
elevator pitch a busy manager can evaluate from a phone in under a minute.

## Step 1 — Ground yourself before inventing anything

**Known extension points (canonical, from the ownCloud Web runtime docs — treat
this as ground truth, do not need to re-derive it via Grep):**

| ExtensionPointId | Mounts type | Where |
|---|---|---|
| `app.${appName}.navItems` | `sidebarNav` | Left sidebar nav, per app |
| `app.runtime.header.center` | `customComponent` | Global top bar, center area |
| `app.runtime.global-progress-bar` | `customComponent` | Global loading progress bar (single; user-selectable if multiple) |
| `app.files.sidebar` | `sidebarPanel` | Right sidebar in files app / viewers / editors |
| `app.files.folder-views.folder` | `folderView` | Folder views for regular folders |
| `app.files.folder-views.project-spaces` | `folderView` | Folder views for project spaces overview |
| `app.files.folder-views.favorites` | `folderView` | Folder views for favorites page |
| `global.files.context-actions` | `action` | Right-click context menu on files |
| `global.files.batch-actions` | `action` | Batch actions in app bar above file lists |
| `global.files.default-actions` | `action` | Default (left-click) action on a file |
| `app.files.upload-menu` | `action` | Upload menu |
| `app.files.quick-actions` | `action` | Quick actions |
| `app.search.providers` | `search` | Global search providers in top bar search |

Every candidate's `extension_point` field must reference one or more IDs from this
table (or a full standalone app via `defineWebApplication`, which is not a
registration into one of these extension points but a top-level app — note this
explicitly in `evidence` if used). Do not invent extension point IDs not in this
table.

Beyond this table, also investigate the actual codebase:

- `Glob` and `Read` through `packages/` to see what extensions already exist.
  **Never propose something that already exists or substantially overlaps with it.**
- `Grep` for `defineWebApplication` and usages of the extension point IDs above to
  see concrete examples of how an existing extension registers for one — use these
  as the implementation pattern reference for `evidence`.
- If a `CLAUDE.md` or similar conventions file exists in the repo, read it — it
  defines what "valid" means here (allowed packages, BYO-LLM rules for agentic
  extensions, etc.) and your specs must be compatible with it.
- If `idea-pool.yaml` exists, read it. Treat its `seed` entries as a starting menu,
  not a constraint — you may expand a seed, combine two, or propose something not
  in the pool if it's a better fit for what you found in the codebase. If the pool
  is thin or empty, you are expected to propose net-new ideas grounded in your
  repo investigation and the table above.

## Step 2 — Select {{N}} candidates

All {{N}} candidates must be **agentic extensions**. Every candidate must use the
BYO-LLM convention (admin-configured OpenAI-compatible endpoint, capability
detection, graceful degradation per CLAUDE.md §12). Do not propose utility
extensions that have no LLM component. If a strong utility idea exists, set
it aside — it is out of scope for this pipeline.

Choose a portfolio, not {{N}} variations on one theme. Always produce exactly {{N}}
new specs regardless of how many carryover candidates exist — carryovers are
additive, not a deficit to fill. The carryover list provided below is purely
a deduplication guard.

Each candidate should be independently buildable in roughly 1 day by Claude Code
running headless against a deterministic scaffold (effort tag **S**: a single
extension point, modest UI, no new backend services; **M**: multiple extension
points or moderate new logic, still frontend-only and scaffold-compatible). Do not
propose **L**-sized ideas (new backend microservices, multi-day scope) — split them
or decline; flag oversized seeds back to the human instead of shrinking them
silently.

## Step 3 — Output format

Output **only** {{N}} sections in exactly this format, nothing before the first or
after the last:

```
## CANDIDATE
id: <kebab-case-slug, unique, <=30 chars>
title: <human title, <=60 chars>
problem: |
  <1-3 sentences: what's broken or missing today, for whom>
extension_point: <the real extension point(s) this targets, as found in the repo>
sketch: |
  <2-5 sentences: what the user sees and does, concretely. No implementation detail
  beyond what a reviewer needs to picture it. For agentic candidates, name which
  capability tiers (per §12) the feature degrades through if the configured model
  lacks structured output / tool use / large context.>
why_now: |
  <1-2 sentences: who benefits, why this and not something else>
effort: S|M
evidence: |
  <1-2 sentences: what you found in the repo that supports feasibility — e.g.
  "extension point X is registered in path/to/file and used by extension Y in a
  similar way">
```

## Hard constraints

- **Read-only.** Do not write, edit, create files, or run any command beyond
  Read/Grep/Glob. You are not building anything in this run.
- **No invented extension points.** Every `extension_point` value must come from
  the table in Step 1, or be a standalone `defineWebApplication` app (note this
  explicitly). Do not propose extension points outside that table.
- **No duplication.** Check `packages/` and the carryover list (if provided)
  before finalizing; do not propose something functionally identical to an
  existing extension or to a candidate already in today's slate.
- **Elevator-pitch length.** `problem`, `sketch`, and `why_now` combined should be
  readable in well under a minute. If you're writing more than ~120 words across
  the three, cut it down — detail belongs in the build phase, not the pick.
- **No code, no file paths to create, no package names beyond what's needed to
  justify `evidence`.** This is a product-level decision document.

## Inputs you may receive

- `idea-pool.yaml` — seed ideas (optional, may be absent or thin).
- A carryover list — candidates already offered in a previous cycle. Do not
  duplicate or substantially overlap with any of them. This list does not
  reduce the number of specs to produce — always produce {{N}}.
