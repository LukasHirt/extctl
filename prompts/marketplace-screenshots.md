# marketplace-screenshots.md — dedicated screenshot spec for `extctl publish`

You are writing a **new** Playwright spec whose only purpose is to produce
good-looking screenshots for this extension's public listing on the ownCloud
marketplace. This is explicitly NOT a functional/acceptance test — nobody
reads its assertions, and it is never committed to the repository. What
matters is what a stranger sees when they land on this extension's
marketplace page.

Read, in this order, to understand the extension before writing anything:

1. `packages/web-app-{{EXT_ID}}/package.json` (name, description)
2. `packages/web-app-{{EXT_ID}}/README.md`, if present
3. `packages/web-app-{{EXT_ID}}/tests/e2e/acceptance.spec.ts` — for the
   existing login/navigation/mocking patterns and selectors, NOT for its
   list of tests or its test titles. Do not copy it.
4. `packages/web-app-{{EXT_ID}}/src/` as needed to understand what the
   extension actually shows the user.

## Write exactly one file

`packages/web-app-{{EXT_ID}}/tests/e2e/marketplace-screenshots.spec.ts` —
nothing else. Do not edit `acceptance.spec.ts`, source files, or anything
outside this one path.

Reuse the same infrastructure `acceptance.spec.ts` uses: shared helpers from
`../../../../support/` (`loginAsUser`/`logout` from `helpers/authHelper`,
`FilesPage`/`FilesAppBar` from `pages/`), and mock any LLM/API calls with
`page.route()`:

```typescript
await page.route('**/ai-llm-proxy/**', route =>
  route.fulfill({ body: JSON.stringify({ choices: [{ message: { content: '...' } }] }) })
)
```

## What makes a good marketplace screenshot

Write **1 to {{MAX_SCREENSHOTS}}** `test()` cases — this is the exact,
final, curated set that will be captured and published, not a larger pool
to pick from later. {{MAX_SCREENSHOTS}} is a ceiling, not a target: fewer,
sharply distinct screenshots beat more mediocre or repetitive ones. If this
extension only has one truly interesting state to show, write one test, not
three padded ones. Most extensions need 2, occasionally 3; reach for more
only if the extension genuinely has that many distinct, valuable things to
show.

- **Every included test must justify its own existence as a screenshot.**
  Before writing a test, ask: "if someone saw only this one image on the
  marketplace, would it tell them something new about what this extension
  does?" If the answer is no — because it's a setup step, a plain
  unmodified oCIS file list, a menu appearing, or anything that doesn't
  show the extension's own UI actually doing its job — don't write it as a
  standalone test. There must be no test whose final screenshot is just
  oCIS's default file browser with nothing extension-specific visible.
- **"Distinct" means a different UI region, layout, or interaction —
  not just different mock data in the same layout.** Two tests that both
  end on "one file selected, same sidebar panel open, same panel structure,
  only the file name and generated text differ" are the SAME screenshot for
  marketplace purposes, no matter how different the mocked content is. If
  you're tempted to write a second test that only changes which file is
  opened, don't — pick the single best example of that state instead and
  spend the remaining budget on a state that's structurally different
  (a different panel, a settings/config view, a different extension point,
  a different part of the UI entirely). When in doubt, prefer one excellent
  screenshot of a state over two so-so screenshots of near-identical ones.
- **End on the actual payoff, not the setup.** If the extension calls an
  LLM or renders generated content, the test must wait for and show that
  content fully rendered — not stop as soon as a sidebar opens or a loading
  spinner appears. A screenshot of an empty panel or a spinner tells a
  marketplace visitor nothing about what the extension does.
- **Mocked LLM/API responses must be realistic and specific to this
  extension** — a real-sounding summary, a real-sounding generated
  description, whatever fits — never a placeholder like `'mock result'` or
  lorem ipsum. Whatever text the mock returns is exactly what appears in the
  screenshot.
- **If the extension displays images/photos and you mock the binary download
  response, the mocked bytes must actually look like a photo when rendered —
  never a single reused 1x1 (or otherwise near-blank) placeholder image
  stretched to fill the frame.** A 1-pixel JPEG scaled up renders as a flat
  solid-color block, not a photo — worthless for a screenshot whose entire
  point is showing photos. Generate real, visually varied, multi-pixel image
  content per photo (e.g. `page.evaluate()` to draw a distinct gradient/scene
  onto an in-page `<canvas>` and export it as a real JPEG/PNG blob, one
  per distinct photo you need to look different) rather than one static
  byte buffer reused for every mocked download.
- **A locator being present in the DOM is not the same as content having
  visually finished rendering — this matters most for anything loaded over
  the real network rather than mocked (map tiles, external images,
  iframes).** A real bug this exact prompt produced: a map test asserted
  `.leaflet-marker-icon` was visible and passed, but the screenshot showed
  blank grey tiles — the marker existed before the tile images had actually
  painted. Element existence/visibility is not proof of visual completeness
  for image-based content. Wait for the specific signal that means "this
  image has actually painted" — e.g. poll `img.complete &&
  img.naturalWidth > 0` on the actual `<img>` elements (map tiles included),
  not just their container; for a library like Leaflet, wait for its own
  `load` event on the tile layer if the mocked/live network is fast enough,
  or add a short deliberate settle wait AFTER confirming elements exist
  specifically for tile-based content, since tile images can finish
  attaching to the DOM before their bytes finish decoding on screen.
- **Never type the same literal generated text (a caption, an AI label, a
  tag) twice — once in mock data and again in an assertion.** You cannot run
  this spec to catch the two copies drifting apart (a reworded caption, a
  singular/plural label mismatch, a typo in only one copy), and it's the
  single most common way a spec you write fails. Define it once as a named
  constant and reference that same constant in both the mock response and
  the assertion, so they're structurally guaranteed to match — e.g.
  `const SHOWCASE_CAPTION = 'Hikers pause at a mountain overlook'` used in
  both the mocked API response body and
  `expect(page.getByText(SHOWCASE_CAPTION))`, never the string written out a
  second time by hand.
- **Every file, folder, or document name visible in ANY screenshot — with
  no exceptions — must look like real-world professional content**, not a
  test fixture. This applies even to files that only appear in the
  background of a shot (e.g. a file list state) — there is no such thing as
  a "throwaway" test where sloppy naming is fine, because every test's
  final frame is a candidate publish. A marketplace visitor will see these
  names. Use something like `Q3-Financial-Report.pdf` or
  `Team-Offsite-Notes.docx`, never `test.txt`, `test-document.txt`,
  `sample1.pdf`, `foo.docx`, `logo.jpeg`, or similar obviously-fake names.
- **Test titles become the public screenshot captions verbatim** — write
  them as short, plain-English marketing copy a visitor would read on the
  extension's page, not as QA/test-report phrasing. For example, prefer
  `"AI-generated summary of a quarterly report appears in the sidebar"`
  over `"clicking Summarize opens the Summary sidebar panel"`. Every title
  must be different — no two tests should produce a caption that reads as a
  near-duplicate of another.
- End each test on a settled, fully-rendered UI state — the final `await`
  should wait for the completed result, not fire immediately after an
  action.
- When a click is blocked by a modal/overlay/backdrop, use
  `page.addLocatorHandler()` or an explicit dismissal step, same as
  `acceptance.spec.ts` does — see `build-stage.md`'s "Demo quality" section
  for the exact pattern if you need it via Grep.

## Run it yourself and fix real failures

You have Bash access, scoped to exactly one command — running the spec you
just wrote. Nothing else: no `pnpm install`, no `git`, no arbitrary shell. A
live oCIS instance is already up and reachable by the time you start (the
env vars a normal test run needs, `BASE_URL_OCIS`/`OCIS_URL`, are already
set) — do not try to confirm this yourself with `docker`, `curl`, or any
other command; it is guaranteed by the time you're invoked, and no tool for
checking it is available to you. If a Bash call outside the one command
below is denied, that is expected, not a problem to solve: do not ask for
permission, do not explain that you're blocked, and do not stop — just
proceed straight to running the command below.

After writing the file, run it:

```bash
pnpm playwright test tests/e2e/marketplace-screenshots.spec.ts --config playwright.config.publish.ts --project={{PLAYWRIGHT_PROJECT}} --retries=0 --reporter=list,json
```

Read the output. If every test passes, you're done — stop, no need to run it
again. If anything fails, read the failure detail (the assertion, the
locator, actual vs. expected) and the relevant source file, fix the one
thing that's actually wrong — usually either a selector that doesn't match
the real DOM, or mock data and an assertion referencing it that drifted
apart (see the constant-reuse rule above) — and run the command again.
Repeat up to about 3 fix-and-rerun cycles. If you're still not fully passing
after that, stop anyway and leave the spec in its best working state — don't
loop indefinitely chasing one stubborn assertion; a human reviews the result
either way.

A failure caused by the extension's own real behavior, not a bug in your
spec, means your assumption about what the UI does was wrong — adjust the
assertion to match reality rather than fighting the app. You cannot edit
anything outside the one spec file, so if a test depends on the extension
behaving differently than it actually does, write a different test against
what actually happens instead of trying to work around it.

Before finishing, review your own draft against this checklist: does every
single test's final state look meaningfully different from every other
test's final state at a glance, not just on close reading? Is there any
test whose screenshot is just a plain file list or setup step? Does every
visible file name look real? Did your last run show every test passing —
and if not, is that a limitation you couldn't resolve within your budget,
not something you're leaving broken by accident? If any answer is wrong,
fix it before stopping.

**Forbidden:** `.only()`/`.skip()`, `expect(page).toBeDefined()` or any
tautological assertion, writing or editing anything other than the one
spec file named above, running anything via Bash other than the exact
`pnpm playwright test` command above — including any command meant only to
check that oCIS is reachable first. Asking the user for permission to run a
different command, or stopping to explain that a tool is unavailable, is
also forbidden: this is a headless, unattended run, so nobody is there to
answer. Treat a denied tool call as confirmation to move on, not a reason to
pause.
