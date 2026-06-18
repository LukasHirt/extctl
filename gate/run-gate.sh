#!/usr/bin/env bash
# gate/run-gate.sh — validation gate for a built extension
#
# Usage: run-gate.sh <worktree-path> <ext-id> <output-dir> [<spec-bullet-count>]
#
# Outputs:
#   <output-dir>/gate.json  — per-stage verdicts + overall pass/fail
#   <output-dir>/gate.log   — full log of all commands run
#
# Scope: hygiene + build + static + unit (no Docker/Playwright smoke — Phase 3)
# Exit code: 0 if all stages pass, 1 if any stage fails.

set -euo pipefail

WORKTREE="$1"
EXT_ID="$2"
OUTPUT_DIR="$3"
SPEC_BULLET_COUNT="${4:-1}"  # minimum expect() assertions required

mkdir -p "$OUTPUT_DIR"
OUTPUT_DIR=$(cd "$OUTPUT_DIR" && pwd)
WORKTREE=$(cd "$WORKTREE" && pwd)
LOG="$OUTPUT_DIR/gate.log"
GATE_JSON="$OUTPUT_DIR/gate.json"

log() { echo "[gate] $*" | tee -a "$LOG"; }
stage_ok() { log "PASS $1"; }
stage_fail() { log "FAIL $1: $2"; }

EXT_DIR="$WORKTREE/extensions/$EXT_ID"

# Track per-stage results
hygiene_result="fail"
build_result="fail"
lint_result="fail"
unit_result="fail"
overall=false

write_json() {
  local passed="$1"
  cat > "$GATE_JSON" << EOF
{
  "passed": $passed,
  "score": $([ "$passed" = "true" ] && echo "1.0" || echo "0.0"),
  "stages": {
    "hygiene": "$hygiene_result",
    "build": "$build_result",
    "lint": "$lint_result",
    "unit": "$unit_result"
  }
}
EOF
}

# Ensure gate.json is always written even on unexpected exit.
trap 'write_json "$overall"' EXIT

log "=== Gate for $EXT_ID ==="
log "Worktree: $WORKTREE"
log "Output:   $OUTPUT_DIR"
log ""

# ── Stage 1: Hygiene ──────────────────────────────────────────────────────────

log "--- Stage 1: hygiene ---"

# Working tree must be clean (all changes committed).
if ( cd "$WORKTREE" && git status --porcelain | grep -q . ); then
  stage_fail hygiene "working tree is not clean (uncommitted changes)"
  write_json false; exit 1
fi

# Diff must be confined to extensions/<ext-id>/.
DIFF_FILES=$(cd "$WORKTREE" && git diff HEAD~1 --name-only 2>/dev/null || git diff --name-only HEAD)
OUTSIDE=$(echo "$DIFF_FILES" | grep -v "^extensions/$EXT_ID/" || true)
if [ -n "$OUTSIDE" ]; then
  stage_fail hygiene "diff contains files outside extensions/$EXT_ID/: $OUTSIDE"
  write_json false; exit 1
fi

# No files larger than 1 MB.
LARGE=$(find "$EXT_DIR" -type f -size +1M 2>/dev/null || true)
if [ -n "$LARGE" ]; then
  stage_fail hygiene "files larger than 1 MB found: $LARGE"
  write_json false; exit 1
fi

# No hardcoded provider hostnames (per spec §12.3).
PROVIDERS="openai\.com|anthropic\.com|azure\.com|api\.openai|generativeai\.googleapis\.com"
HARDCODED=$(grep -rE "$PROVIDERS" "$EXT_DIR/src/" 2>/dev/null || true)
if [ -n "$HARDCODED" ]; then
  stage_fail hygiene "hardcoded provider hostnames found in src/: $HARDCODED"
  write_json false; exit 1
fi

# acceptance.spec.ts must have >= SPEC_BULLET_COUNT expect() calls.
# Prefer tests/e2e/acceptance.spec.ts (scaffold default), fall back to root.
ACCEPTANCE="$EXT_DIR/tests/e2e/acceptance.spec.ts"
if [ ! -f "$ACCEPTANCE" ]; then
  ACCEPTANCE="$EXT_DIR/acceptance.spec.ts"
fi
if [ ! -f "$ACCEPTANCE" ]; then
  stage_fail hygiene "acceptance.spec.ts not found (checked tests/e2e/ and root)"
  write_json false; exit 1
fi
EXPECT_COUNT=$(grep -c "expect(" "$ACCEPTANCE" 2>/dev/null || echo 0)
if [ "$EXPECT_COUNT" -lt "$SPEC_BULLET_COUNT" ]; then
  stage_fail hygiene "acceptance.spec.ts has $EXPECT_COUNT expect() calls; need >= $SPEC_BULLET_COUNT (one per acceptance bullet)"
  write_json false; exit 1
fi

# .only or .skip must not appear in acceptance tests (anti-test-gaming, spec §9.4).
if grep -qE "\.(only|skip)\s*\(" "$ACCEPTANCE" 2>/dev/null; then
  stage_fail hygiene "acceptance.spec.ts contains .only or .skip — remove them"
  write_json false; exit 1
fi

hygiene_result="ok"
stage_ok hygiene

# ── Stage 2: Build ────────────────────────────────────────────────────────────

log ""
log "--- Stage 2: build ---"

if ! (cd "$EXT_DIR" && pnpm install --frozen-lockfile >> "$LOG" 2>&1); then
  stage_fail build "pnpm install --frozen-lockfile failed"
  write_json false; exit 1
fi

if ! (cd "$EXT_DIR" && pnpm build >> "$LOG" 2>&1); then
  stage_fail build "pnpm build failed"
  write_json false; exit 1
fi

build_result="ok"
stage_ok build

# ── Stage 3: Static (lint + typecheck) ───────────────────────────────────────

log ""
log "--- Stage 3: static ---"

if ! (cd "$EXT_DIR" && pnpm lint >> "$LOG" 2>&1); then
  stage_fail lint "pnpm lint failed"
  write_json false; exit 1
fi

if ! (cd "$EXT_DIR" && pnpm check:types >> "$LOG" 2>&1); then
  stage_fail lint "tsc --noEmit failed"
  write_json false; exit 1
fi

lint_result="ok"
stage_ok lint

# ── Stage 4: Unit tests ───────────────────────────────────────────────────────

log ""
log "--- Stage 4: unit ---"

if ! (cd "$EXT_DIR" && pnpm test >> "$LOG" 2>&1); then
  stage_fail unit "pnpm test failed"
  write_json false; exit 1
fi

unit_result="ok"
stage_ok unit

# ── All stages passed ─────────────────────────────────────────────────────────

log ""
log "=== Gate PASSED for $EXT_ID ==="
overall=true
write_json true
exit 0
