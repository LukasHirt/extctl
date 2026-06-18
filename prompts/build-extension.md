You are building an oCIS Web extension that has been approved by ownCloud's manager.
Your goal is to produce a working, tested, committed implementation inside `extensions/{{EXT_ID}}/`.

---

## Context

**Extension ID:** `{{EXT_ID}}`
**Title:** {{EXT_TITLE}}
**Effort:** {{EFFORT}}

**Approved spec:**

{{SPEC_MD}}

---

## Step 1 — Understand the codebase and scaffold

Before writing any code:

1. Read `CLAUDE.md` in this directory. It contains the mandatory conventions you must follow.
2. Read the `extensions/{{EXT_ID}}/` directory — the scaffold is already in place. Understand what's there.
3. Read `extensions/{{EXT_ID}}/src/composables/useLLM.ts` — this is the required BYO-LLM integration
   point. Every agentic extension must use it; do not replace or bypass it.
4. Read existing extensions in `extensions/` to understand the established patterns (architecture,
   component naming, style). Do not copy their logic; understand the conventions.
5. Look up the extension points listed in the spec. Use `Grep` to find how they are registered in
   other extensions.

---

## Step 2 — Implement

You must implement everything inside `extensions/{{EXT_ID}}/`. You must NOT touch any other path.

Hard constraints:
- Touch ONLY `extensions/{{EXT_ID}}/`. Never edit other extensions, gate/, scaffold/, or CLAUDE.md.
- Never hardcode any LLM provider hostname, API key, or model name. All LLM calls go through
  `useLLM` with the admin-configured endpoint from the oCIS extension config.
- Use `useLLM` for every LLM call. The BYO-LLM pattern is non-negotiable.
- Use the capability detection result (`capabilities.toolUse`, `capabilities.structuredOutput`, etc.)
  to degrade gracefully. If a feature requires tool use and the model doesn't support it, hide or
  simplify the affordance — never hard-error.
- Vue 3 Composition API only. No Options API, no class components.
- TypeScript strict mode. No `any`, no type assertions unless unavoidable (comment why).
- pnpm only. Do not use npm or yarn.

What to build:
- Implement all acceptance bullets from the spec as working features.
- Register the correct extension point(s) in `src/index.ts`.
- Write unit tests for all non-trivial logic (composables, utilities).
- Write `tests/e2e/acceptance.spec.ts` with one Playwright test per acceptance bullet from the spec.
  Each test must contain at least one `expect()` assertion. The total expect() count must be
  ≥ the number of acceptance bullets.
- Keep the README short (5 lines max) covering: what it does, the extension point, and the
  privacy/LLM note.

---

## Step 3 — Build and validate

After implementing, run these in order. Fix any failures before proceeding.

```bash
cd extensions/{{EXT_ID}}
pnpm install --frozen-lockfile
pnpm build
pnpm lint
pnpm check:types
pnpm test
```

All four must pass before you commit. If any fails, fix the error and re-run.

Do not weaken or delete lint rules, tests, or acceptance checks to make them pass.

---

## Step 4 — Commit

Once build, lint, and test all pass, commit:

```bash
git add extensions/{{EXT_ID}}/
git status         # verify only extensions/{{EXT_ID}}/ is staged
git diff --cached  # review the diff
git commit -m "[ext] {{EXT_TITLE}}"
```

The commit message must start with `[ext] `. Do not commit anything outside `extensions/{{EXT_ID}}/`.

---

## Definition of done

- [ ] All acceptance bullets from the spec are implemented and covered in `tests/e2e/acceptance.spec.ts`
- [ ] `pnpm build` succeeds
- [ ] `pnpm lint` passes with zero warnings
- [ ] `pnpm test` passes
- [ ] No hardcoded provider hostnames (ollama, openai, anthropic, azure, etc.)
- [ ] No API keys, tokens, or secrets anywhere in the code
- [ ] All LLM calls go through `useLLM`
- [ ] Capability degradation is implemented for at least one tier (structuredOutput or toolUse)
- [ ] One commit on this branch: `[ext] {{EXT_TITLE}}`
- [ ] Diff is confined to `extensions/{{EXT_ID}}/`

You are done when all of the above are met and the code is committed.
