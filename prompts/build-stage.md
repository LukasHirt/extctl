You are building a web extension for ownCloud Infinite Scale (oCIS).
This is stage {{STAGE_NUM}} of {{TOTAL_STAGES}} in a multi-stage build.

## Extension

- **ID:** {{EXT_ID}}
- **Title:** {{EXT_TITLE}}
- **Effort:** {{EFFORT}}

The extension lives in `packages/web-app-{{EXT_ID}}/` within this repository.

## Spec

{{SPEC_MD}}

## Issue Comments

The following comments were left on the Jira issue before it entered the build
pipeline. They are listed in chronological order — replies appear directly after
the comment they respond to and may refine, scope-down, or override it. Read the
full thread before drawing any conclusions; a later comment takes precedence over
an earlier one on the same point. Treat the resulting consensus as binding
constraints that override or refine the spec above.

{{ISSUE_COMMENTS}}

## Prior stages

The following commits have already been made for this extension. Read whichever
of these files are relevant before implementing stage {{STAGE_NUM}} — do not
recreate anything already committed.

{{PRIOR_WORK}}

## Context files

1. Read the implementation plan first:
   {{PLAN_PATH}}

2. Read the stages breakdown to understand what has already been completed and
   what remains:
   {{STAGES_PATH}}

## Your task

Implement **stage {{STAGE_NUM}}: {{STAGE_DESC}}**

Do not implement work that belongs to earlier or later stages. Focus only on
what is described for stage {{STAGE_NUM}}.

## Design rules

- Use the ownCloud Design System (ODS) for all UI components. Do NOT use plain
  HTML elements or custom CSS where an ODS component exists.
- For all LLM calls, use the `useLLM` composable. Find it at
  `src/composables/useLLM.ts` in this repository — read it before writing any
  LLM-related code to understand the API. The composable enforces same-origin
  and attaches the oCIS token internally; you do not need to handle auth.
- All new files must live inside `packages/web-app-{{EXT_ID}}/`.
- Do NOT touch any files outside `packages/web-app-{{EXT_ID}}/`, **with the sole exception of
  the three registration files** described in the section below.
- Do NOT push to remote. Do NOT open pull requests.

## Registration files

Every extension must be registered in three files so that oCIS, local dev, and GHA can discover
it. **Add these entries exactly once — on the scaffold stage.** If the entries are already
present (e.g. you are building a later stage), skip this step entirely.

1. **`docker-compose.yml`** — add a volume mount under the `ocis` service `volumes:` list,
   following the pattern of the existing extension mounts:
   ```yaml
   - ./packages/web-app-{{EXT_ID}}/dist:/web/apps/{{EXT_ID}}
   ```

2. **`dev/docker/ocis.apps.yaml`** — add an entry keyed by `{{EXT_ID}}` (no `web-app-` prefix),
   following the pattern of the existing entries:
   ```yaml
   {{EXT_ID}}:
     config:
       llm:
         endpoint: 'https://host.docker.internal:9200/ai-llm-proxy/v1'
         model: 'llama3.2'
   ```

3. **`support/actions/ocis.apps.yaml`** — add an entry keyed by `web-app-{{EXT_ID}}`,
   following the pattern of the existing entries:
   ```yaml
   web-app-{{EXT_ID}}:
     config:
       llm:
         endpoint: 'https://localhost:9200/ai-llm-proxy/v1'
         model: 'llama3.2'
   ```

Read each file before editing to understand the exact indentation and ordering used.

## Security rules

- **Never put an `apiKey` in `LLMConfig` or any LLM-related config.** The LLM
  API key lives server-side in the `ai-llm-proxy` environment; the browser
  never sees it. An `apiKey` field in extension config is always wrong.
- **Never construct an `Authorization` header manually** for LLM requests. The
  `useLLM` composable reads `useAuthStore().accessToken` and attaches it for
  you, but only after verifying the endpoint is same-origin. Duplicating this
  logic in extension code bypasses the guard.
- **Never send the oCIS access token to a cross-origin endpoint.** The proxy
  validates the token on the same server; forwarding it to an external URL
  (even one from admin config) leaks user credentials to a third party.

## Acceptance test rules

`tests/e2e/acceptance.spec.ts` is executed by the gate against a live oCIS
instance. Write real tests that log in, navigate, and assert visible state.

**Required:**
- Import shared helpers from `../../../../support/` — `loginAsUser`/`logout` from
  `helpers/authHelper`, `FilesPage`/`FilesAppBar` from `pages/`. Read these files first.
- Every `test()` block must contain at least one assertion that can actually fail
  (e.g. `await expect(page.locator('...')).toBeVisible()`).
- Put extension-specific page objects in this extension's own `tests/e2e/pages/`
  directory — NOT in the shared `support/pages/` root. The gate forbids writing
  outside `packages/web-app-{{EXT_ID}}/`.
- Mock LLM HTTP calls with `page.route()` so tests need no live LLM endpoint:
  ```typescript
  await page.route('**/ai-llm-proxy/**', route =>
    route.fulfill({ body: JSON.stringify({ choices: [{ message: { content: 'mock result' } }] }) })
  )
  ```

**Forbidden:**
- `expect(page).toBeDefined()` or any `expect(<variable>).toBeDefined()` — always true.
- `expect(true).toBe(true)` or other tautologies.
- `.only()` / `.skip()` modifiers.
- Writing test/source files anywhere outside `packages/web-app-{{EXT_ID}}/` (the registration
  files above are the only permitted exception).

See `packages/web-app-unzip/tests/e2e/` and `packages/web-app-file-comments/tests/e2e/`
for real examples.

## After implementation

Run the following checks in order and fix any errors before committing:

1. `pnpm install` — only if you added or changed dependencies
2. `pnpm build` — must succeed with no errors
3. `pnpm lint packages/web-app-{{EXT_ID}}/...` — fix all lint errors
4. `pnpm test packages/web-app-{{EXT_ID}}/...` — all tests must pass

Once all checks pass, commit your work using a conventional commit message:

```
git add packages/web-app-{{EXT_ID}}/
# On the scaffold stage only, also stage the three registration files if you edited them:
# git add docker-compose.yml dev/docker/ocis.apps.yaml support/actions/ocis.apps.yaml
git commit -s -m "<type>(web-app-{{EXT_ID}}): {{STAGE_DESC}}"
```

Choose `<type>` based on what this stage implements:
- `feat` — new functionality or UI components (most stages)
- `test` — test-only changes
- `docs` — documentation-only changes
- `chore` — scaffolding, build config, or package setup with no production code

Do not include any other files in the commit.
