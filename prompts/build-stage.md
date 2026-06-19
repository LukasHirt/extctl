You are building a web extension for ownCloud Infinite Scale (oCIS).
This is stage {{STAGE_NUM}} of {{TOTAL_STAGES}} in a multi-stage build.

## Extension

- **ID:** {{EXT_ID}}
- **Title:** {{EXT_TITLE}}
- **Effort:** {{EFFORT}}

The extension lives in `packages/web-app-{{EXT_ID}}/` within this repository.

## Spec

{{SPEC_MD}}

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
  LLM-related code to understand the API.
- All new files must live inside `packages/web-app-{{EXT_ID}}/`.
- Do NOT touch any files outside `packages/web-app-{{EXT_ID}}/`.
- Do NOT push to remote. Do NOT open pull requests.

## After implementation

Run the following checks in order and fix any errors before committing:

1. `pnpm install` — only if you added or changed dependencies
2. `pnpm build` — must succeed with no errors
3. `pnpm lint packages/web-app-{{EXT_ID}}/...` — fix all lint errors
4. `pnpm test packages/web-app-{{EXT_ID}}/...` — all tests must pass

Once all checks pass, commit your work:

```
git add packages/web-app-{{EXT_ID}}/
git commit -s -m "[ext-stage-{{STAGE_NUM}}] {{STAGE_DESC}}"
```

Do not include any other files in the commit.
