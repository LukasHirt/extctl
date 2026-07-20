Your branch for `{{EXT_ID}}` is being rebased onto `origin/{{DEFAULT_BRANCH}}` and git has stopped on a conflict.

Your task is to resolve the conflict(s) and let the rebase continue to completion.

## What's happening

`git rebase origin/{{DEFAULT_BRANCH}}` is in progress in this worktree. Run `git status` first — it lists every conflicted file for the current stop. There may be more than one stop across the whole rebase; after resolving one and running `git rebase --continue`, run `git status` again to see if another conflict follows.

## Resolution rules

1. Touch ONLY files under `packages/web-app-{{EXT_ID}}/` and the shared registration files (`docker-compose.yml`, `dev/docker/ocis.apps.yaml`, `support/actions/ocis.apps.yaml`, `pnpm-lock.yaml`). A conflict anywhere else shouldn't happen — if you see one, stop without guessing.
2. For the shared registration files: a conflict here is almost always two additive entries — this extension's registration block and another candidate's, added independently to the same file. Keep BOTH additions unless they are genuinely the same entry.
3. For `pnpm-lock.yaml`: do not hand-merge the conflict markers. Resolve every other conflict first and `git add` it, then regenerate the lockfile with `pnpm install --lockfile-only` from the repo root and `git add pnpm-lock.yaml`.
4. For files inside `packages/web-app-{{EXT_ID}}/`: these are your own extension's code from a prior stage — resolve by understanding what each side intended and combining them correctly, not by blindly picking one side.
5. Do NOT modify `gate/run-gate.sh` or any file in `gate/`.
6. After resolving each conflict: `git add <file>` for every resolved path, then `git rebase --continue`. Repeat until `git status` reports no in-progress rebase and no unmerged paths.
7. If a conflict is genuinely unresolvable (contradictory intent, not just adjacent additions), stop where you are and make no further changes — leave the rebase in its current conflicted state for manual review.

You are done when `git status` no longer reports an in-progress rebase and there are no unmerged paths.
