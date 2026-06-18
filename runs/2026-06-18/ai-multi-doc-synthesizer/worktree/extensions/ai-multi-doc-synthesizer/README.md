# AI Multi-Document Synthesizer

Adds a **Synthesize** batch action to oCIS Web. Select 2–10 files, click Synthesize, and receive an LLM-generated overview of shared themes, key differences, and action items across all selected documents. Output can be copied or saved as a new Markdown file in the same folder.

**Extension point:** `global.files.batch-actions`

**LLM:** Uses the admin-configured BYO-LLM endpoint (OpenAI-compatible). The action is hidden when no LLM is configured (tier-3 degradation). All LLM traffic goes to the admin-configured endpoint only — no telemetry, no hardcoded keys.
