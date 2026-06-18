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
3. Do NOT modify or delete `acceptance.spec.ts` to make tests pass — fix the implementation instead.
4. Do NOT weaken lint rules or add `// eslint-disable` to silence errors.
5. Do NOT add `.only`, `.skip`, or `test.todo` to acceptance tests.
6. Touch ONLY `extensions/{{EXT_ID}}/`. Never edit other extensions or pipeline files.
7. No hardcoded provider hostnames, API keys, or secrets.

## After fixing

Run the following and ensure all pass before committing:

```bash
cd extensions/{{EXT_ID}}
pnpm install --frozen-lockfile
pnpm build
pnpm lint
pnpm check:types
pnpm test
```

Then commit:

```bash
git add extensions/{{EXT_ID}}/
git status   # verify only extensions/{{EXT_ID}}/ is staged
git commit -m "[repair] {{EXT_ID}}"
```

The commit message must start with `[repair] `. Do not commit anything outside `extensions/{{EXT_ID}}/`.

You are done when the build, lint, typecheck, and test all pass and the fix is committed.
