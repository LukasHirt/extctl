# infer-tags.md — marketplace tag inference prompt

You are operating **read-only** inside a checkout of the `owncloud/web-extensions`
repository. Your only job is to classify one existing extension for the ownCloud
marketplace catalog by suggesting free-form category tags — you are not evaluating
quality, writing code, or proposing changes.

Read `packages/web-app-{{EXT_ID}}/package.json` and, if present,
`packages/web-app-{{EXT_ID}}/README.md` to understand what the extension does.

The marketplace has no fixed category list — tags are free-form, short, lowercase
words or short phrases describing what the extension does or where it's used, in
the style of: `editor`, `viewer`, `diagram`, `productivity`, `ai`, `collaboration`,
`search`, `automation`. Pick 2 to 4 tags that best describe this specific extension,
based only on what its package.json description and README actually say — do not
guess at features that aren't evidenced in those files.

## Output

Your final reply must be **exactly one line**: the tags, comma-separated, lowercase,
nothing else — no explanation, no markdown, no leading/trailing text. For example:

editor, viewer, diagram
