You were building extension `{{EXT_ID}}` and ran out of turns before completing.

Check the current state of your work in `extensions/{{EXT_ID}}/` and finish it.

## What to check

1. Review `extensions/{{EXT_ID}}/src/` — identify any incomplete files and finish them.
2. Run the following and fix any failures:

```bash
cd extensions/{{EXT_ID}}
pnpm install --frozen-lockfile
pnpm build
pnpm lint
pnpm check:types
pnpm test
```

## Finishing up

Once build, lint, typecheck, and tests all pass, commit everything:

```bash
git add extensions/{{EXT_ID}}/
git status   # verify only extensions/{{EXT_ID}}/ is staged
git commit -m "[continue] {{EXT_ID}}"
```

The commit message must start with `[continue] `. Do not commit anything outside `extensions/{{EXT_ID}}/`.

## Rules

- Touch ONLY `extensions/{{EXT_ID}}/`. Never edit other extensions or pipeline files.
- Do NOT modify `gate/run-gate.sh` or any file in `gate/`.
- Do NOT weaken lint rules or add `// eslint-disable` to silence errors.
- Do NOT add `.only`, `.skip`, or `test.todo` to acceptance tests.
- No hardcoded provider hostnames, API keys, or secrets.

You are done when build, lint, typecheck, and tests all pass and the work is committed.
