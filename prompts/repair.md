The gate validation for `{{EXT_ID}}` just failed. Below is the full gate log.

Your task is to fix the failing stage(s) and recommit.

---

## Gate log

```
{{GATE_LOG}}
```

---

## Repair rules

1. Fix only the failing stage(s) shown in the log above.
2. Do NOT modify `gate/run-gate.sh` or any file in `gate/`.
3. Do NOT weaken tests: do not remove assertions, replace them with tautologies, or add `.only`, `.skip`, or `test.todo`.
4. When an e2e test fails because a modal, overlay, or backdrop is blocking a click: read the component's source file first to identify the actual dismissal mechanism (close button selector, emitted event, keyboard binding, etc.), then fix the **test** by adding a step that uses that specific mechanism before the next action. Do NOT guess with generic shortcuts like `Escape` without confirming the component handles them. Do NOT modify production source (components, CSS, composables, or any `.vue`/`.ts` file) to make the element click-through — setting `pointer-events: none`, `display: none`, or any equivalent bypass in production code is always wrong here.
5. Do NOT weaken lint rules or add `// eslint-disable` to silence errors.
6. Touch ONLY `packages/web-app-{{EXT_ID}}/`. Never edit other packages or pipeline files.
7. No hardcoded provider hostnames, API keys, or secrets.

## After fixing

Run the following and ensure all pass before committing:

```bash
cd packages/web-app-{{EXT_ID}}
pnpm install --frozen-lockfile
pnpm build
pnpm lint
pnpm check:types
pnpm test
```

Then commit:

```bash
git add packages/web-app-{{EXT_ID}}/
git status   # verify only packages/web-app-{{EXT_ID}}/ is staged
git commit -m "fix(web-app-{{EXT_ID}}): repair failing stage"
```

Do not commit anything outside `packages/web-app-{{EXT_ID}}/`.

You are done when the build, lint, typecheck, and test all pass and the fix is committed.
