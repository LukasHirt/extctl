# revise.md — interactive candidate follow-up prompt

You are continuing your earlier investigation of the `owncloud/web-extensions`
repository (and, if mounted, `owncloud/web`) from the same session that
produced the candidate spec below. You remain **read-only**: `Read`, `Grep`,
`Glob` only — no writes, no edits, no commands.

A human reviewer is looking at this candidate spec during review and has sent
you a follow-up message. It may be:

- **A question** about the candidate (e.g. "why this extension point over
  another?", "would this overlap with an existing extension?") — in this
  case, just answer it. Do not change the spec.
- **A directive** to change something (e.g. "make the sketch shorter and
  focus on X", "swap the extension point for Y") — in this case, apply the
  requested change and produce an updated spec.

You may re-investigate the repo (extension points, `packages/`, existing
extensions) if the follow-up requires it — you already have this session's
context, so only look up what's actually new to the question.

## Current candidate spec

```
{{CANDIDATE_SPEC}}
```

## Reviewer's follow-up

{{USER_INSTRUCTION}}

## Output format

Output **only** the following, nothing before the first line or after the
last:

```
## RESPONSE
<a short answer to the question, or a 1-3 sentence summary of what you
changed and why>

## CANDIDATE
id: <same id as above, never change it>
title: <human title, <=60 chars>
problem: |
  <1-3 sentences: what's broken or missing today, for whom>
extension_point: <the real extension point(s) this targets, as found in the repo>
sketch: |
  <2-5 sentences: what the user sees and does, concretely. No implementation detail
  beyond what a reviewer needs to picture it. For agentic candidates, name which
  capability tiers the feature degrades through if the configured model lacks
  structured output / tool use / large context.>
why_now: |
  <1-2 sentences: who benefits, why this and not something else>
effort: S|M
evidence: |
  <1-2 sentences: what you found in the repo that supports feasibility>
```

If the follow-up was a pure question, re-emit the `## CANDIDATE` block
**unchanged** (still required — the reviewer's tooling reparses it either
way).

## Hard constraints (unchanged from the original spec)

- **Read-only.** Do not write, edit, create files, or run any command beyond
  Read/Grep/Glob.
- **No invented extension points.** Every `extension_point` value must come
  from the canonical table you grounded yourself in earlier, or be a
  standalone `defineWebApplication` app.
- **Same `id`.** Never rename or regenerate the id — it is how the reviewer's
  tooling matches this response back to the candidate.
- **Elevator-pitch length.** `problem`, `sketch`, and `why_now` combined
  should stay readable in well under a minute.
- **Effort stays S or M.** Do not turn this into an L-sized idea.
