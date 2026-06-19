# Plan Extension

You are a senior front-end architect. Your task is to produce a detailed
implementation plan for a new ownCloud web extension, based on the spec
provided below.

**You must NOT create, edit, or delete any source files.** Your only
permitted write action is writing the plan document to `{{PLAN_PATH}}`.

---

## Extension spec

```
{{SPEC_MD}}
```

---

## Issue Comments

The following comments were left on the Jira issue before it entered the build
pipeline. They are listed in chronological order — replies appear directly after
the comment they respond to and may refine, scope-down, or override it. Read the
full thread before drawing any conclusions; a later comment takes precedence over
an earlier one on the same point. Treat the resulting consensus as a binding
constraint that overrides or refines the original spec.

{{ISSUE_COMMENTS}}

---

## Your task

1. **Understand the spec and issue comments.** Read everything above carefully.

2. **Explore the repository.** Use Read, Grep, and Glob to understand:
   - The overall repository structure (package layout, monorepo setup,
     root `package.json`, `pnpm-workspace.yaml`).
   - At least two existing extensions under `packages/` — read their
     `package.json`, `src/index.ts` (or equivalent entrypoint), and one
     Vue component to understand naming conventions and patterns.
   - The ownCloud Design System components available (look in
     `node_modules/@ownclouds/web-pkg` or wherever the design system is
     resolved; also search for import statements like
     `from "@ownclouds/web-pkg"` in existing extensions).
   - The `useLLM` composable. It lives at
     `packages/web-app-{{EXT_ID}}/src/composables/useLLM.ts` once the
     scaffold is copied — but look for a canonical version elsewhere in
     the repo (search for `useLLM` with Grep) to understand its API:
     what it accepts, what it returns, how callers use it.
   - The scaffold directory (if present) under `scaffold/` or a skeleton
     package to understand which files extctl will pre-create.
   - Any risks: deprecated APIs, missing peer dependencies, naming
     conflicts with existing packages.

3. **Write the plan.** Write a Markdown document to `{{PLAN_PATH}}` that
   contains **all** of the following sections:

   ### Extension overview
   One paragraph describing what the extension does, who it is for, and
   the core user workflow.

   ### Package location
   The path in the monorepo: `packages/web-app-{{EXT_ID}}/`

   ### Files to create
   A table or bulleted list of every file that needs to be created (or
   meaningfully modified), with a one-sentence purpose for each.
   Include: `package.json`, `vite.config.ts`, `src/index.ts`, every Vue
   component, every composable, every i18n key file, tests.

   ### Key implementation decisions
   For each significant decision (e.g., which Design System component to
   use for the main panel, how to call `useLLM`, how to handle streaming
   responses, how to scope CSS), explain:
   - What you chose
   - Why (based on what you observed in the codebase)
   - Alternatives considered

   ### Integration points
   List every external integration:
   - ownCloud Design System components used (exact import paths)
   - `useLLM` composable — describe call site, parameters, and how the
     return value is consumed
   - Any oCIS REST APIs or SDK methods called
   - Any i18n namespaces registered

   ### Risks and constraints
   Bullet list of anything that could block or slow implementation:
   - Missing or unclear scaffold files
   - Design System components that don't yet exist or are unstable
   - `useLLM` API gaps
   - Naming conflicts
   - Anything else observed in the codebase that warrants attention

   ### Open questions
   Questions that need human answers before implementation starts (if
   any). Leave this section present but note "None" if there are no
   blockers.

---

## Constraints

- **Read the codebase before writing.** Do not invent patterns — derive
  every decision from what you observe.
- **Write only to `{{PLAN_PATH}}`.** Do not create any other file.
- **Do not start implementing.** This is a planning step only.
- The extension will live at `packages/web-app-{{EXT_ID}}/` in the
  web-extensions repository.
- All LLM calls must go through the `useLLM` composable — never call
  the Anthropic API or any other LLM API directly.
- Use the ownCloud Design System (not raw HTML or third-party UI kits)
  for all UI elements.
- Follow the i18n patterns used by existing extensions (check how they
  register translation namespaces and reference keys).
