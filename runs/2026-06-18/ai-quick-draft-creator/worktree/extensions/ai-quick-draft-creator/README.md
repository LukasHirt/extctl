# AI Quick Draft Creator

Adds a **"Draft from description"** item to the oCIS Web files upload menu. A modal lets users
describe the document they need, choose Markdown or plain text, and generate a structured draft that
is saved directly to their current folder.

**Extension point:** `app.files.upload-menu`

**LLM / Privacy:** All generation goes through the admin-configured BYO-LLM endpoint (OpenAI-compatible).
No telemetry, no hardcoded provider hostnames, no API keys in code.
