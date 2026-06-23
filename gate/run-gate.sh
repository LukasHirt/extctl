#!/usr/bin/env bash
# gate/run-gate.sh — validation gate for a built extension
#
# Usage: run-gate.sh <worktree-path> <ext-id> <output-dir> [<spec-bullet-count>] [<main-checkout>]
#
# Outputs:
#   <output-dir>/gate.json  — per-stage verdicts + overall pass/fail
#   <output-dir>/gate.log   — full log of all commands run
#
# Scope: hygiene + build + static + unit + e2e. The e2e stage runs Playwright
# against the live oCIS started via docker-compose in <main-checkout>; it is
# skipped when <main-checkout> is omitted.
# Exit code: 0 if all stages pass, 1 if any stage fails.

set -euo pipefail

WORKTREE="$1"
EXT_ID="$2"
OUTPUT_DIR="$3"
SPEC_BULLET_COUNT="${4:-1}"  # minimum expect() assertions required
MAIN_CHECKOUT="${5:-}"       # web-extensions checkout running oCIS; empty = skip e2e

mkdir -p "$OUTPUT_DIR"
OUTPUT_DIR=$(cd "$OUTPUT_DIR" && pwd)
WORKTREE=$(cd "$WORKTREE" && pwd)
LOG="$OUTPUT_DIR/gate.log"
GATE_JSON="$OUTPUT_DIR/gate.json"

log() { echo "[gate] $*" | tee -a "$LOG"; }
stage_ok() { log "PASS $1"; }
stage_fail() { log "FAIL $1: $2"; }

EXT_DIR="$WORKTREE/packages/web-app-$EXT_ID"

# Track per-stage results
hygiene_result="fail"
build_result="fail"
lint_result="fail"
unit_result="fail"
e2e_result="skip"  # "skip" when no main checkout is provided
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
    "unit": "$unit_result",
    "e2e": "$e2e_result"
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

# Diff must be confined to packages/web-app-<ext-id>/ (root pnpm-lock.yaml is allowed
# since pnpm updates it automatically when a new workspace package is scaffolded, and the
# three registration files below are allowed since every new extension must register itself
# in docker-compose.yml and both ocis.apps.yaml files for local dev, GHA, and oCIS discovery).
DIFF_BASE=$(cd "$WORKTREE" && git merge-base HEAD main 2>/dev/null || echo "HEAD~1")
DIFF_FILES=$(cd "$WORKTREE" && git diff "$DIFF_BASE"..HEAD --name-only 2>/dev/null || git diff --name-only HEAD)
OUTSIDE=$(echo "$DIFF_FILES" \
  | grep -v "^packages/web-app-$EXT_ID/" \
  | grep -v "^pnpm-lock\.yaml$" \
  | grep -v "^docker-compose\.yml$" \
  | grep -v "^dev/docker/ocis\.apps\.yaml$" \
  | grep -v "^support/actions/ocis\.apps\.yaml$" \
  || true)
if [ -n "$OUTSIDE" ]; then
  stage_fail hygiene "diff contains files outside packages/web-app-$EXT_ID/: $OUTSIDE"
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

# No LLM apiKey in extension source. The LLM credential is a server-side proxy concern;
# an apiKey field in extension config or a fetch call means credentials are leaking to the browser.
APIKEY=$(grep -rn "apiKey\|api_key\|LLM_API_KEY" "$EXT_DIR/src/" 2>/dev/null || true)
if [ -n "$APIKEY" ]; then
  stage_fail hygiene "LLM apiKey found in src/ — the LLM credential belongs in ai-llm-proxy env vars, not in the extension: $APIKEY"
  write_json false; exit 1
fi

# No manual Authorization header construction in extension source.
# useLLM attaches the oCIS token internally after enforcing same-origin; duplicating this
# in extension code bypasses that guard and risks forwarding the token cross-origin.
AUTH_MANUAL=$(grep -rn "Authorization.*Bearer\|Bearer.*\${" "$EXT_DIR/src/" 2>/dev/null \
  | grep -v "/useLlm\.ts:" || true)
if [ -n "$AUTH_MANUAL" ]; then
  stage_fail hygiene "manual Authorization/Bearer header construction found in src/ — use useLLM composable instead: $AUTH_MANUAL"
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

# Stub assertion guard: expect(<var>).toBeDefined() is always true and means
# the test was never actually implemented.
if grep -qE 'expect\s*\(\s*[a-zA-Z_$][a-zA-Z0-9_$]*\s*\)\s*\.toBeDefined\s*\(\s*\)' "$ACCEPTANCE" 2>/dev/null; then
  stage_fail hygiene "acceptance.spec.ts contains stub assertions like expect(page).toBeDefined() — write real Playwright assertions that navigate, interact, and check visible state"
  write_json false; exit 1
fi

hygiene_result="ok"
stage_ok hygiene

# ── Stage 2: Build ────────────────────────────────────────────────────────────

log ""
log "--- Stage 2: build ---"

if ! (cd "$EXT_DIR" && pnpm install --frozen-lockfile 2>&1 | tee -a "$LOG"); then
  stage_fail build "pnpm install --frozen-lockfile failed"
  write_json false; exit 1
fi

if ! (cd "$EXT_DIR" && pnpm build 2>&1 | tee -a "$LOG"); then
  stage_fail build "pnpm build failed"
  write_json false; exit 1
fi

build_result="ok"
stage_ok build

# ── Stage 3: Static (lint + typecheck) ───────────────────────────────────────

log ""
log "--- Stage 3: static ---"

if ! (cd "$EXT_DIR" && pnpm lint 2>&1 | tee -a "$LOG"); then
  stage_fail lint "pnpm lint failed"
  write_json false; exit 1
fi

if ! (cd "$EXT_DIR" && pnpm check:types 2>&1 | tee -a "$LOG"); then
  stage_fail lint "tsc --noEmit failed"
  write_json false; exit 1
fi

lint_result="ok"
stage_ok lint

# ── Stage 4: Unit tests ───────────────────────────────────────────────────────

log ""
log "--- Stage 4: unit ---"

if ! (cd "$EXT_DIR" && pnpm test 2>&1 | tee -a "$LOG"); then
  stage_fail unit "pnpm test failed"
  write_json false; exit 1
fi

unit_result="ok"
stage_ok unit

# ── Stage 5: e2e (Playwright against running oCIS) ───────────────────────────
#
# Skipped unless a main checkout is provided. oCIS only scans /web/apps at
# startup, so the built extension is copied into the running container and the
# container is restarted before Playwright runs.
#
# The stage is serialized across concurrent gate runs: parallel Playwright
# sessions share the admin user and would clobber each other's test data, and
# the container restart would disrupt a test mid-flight. flock is absent on
# macOS, so we use a portable atomic mkdir lock with stale-owner recovery.

if [ -n "$MAIN_CHECKOUT" ]; then
  log ""
  log "--- Stage 5: e2e ---"

  E2E_LOCK="${TMPDIR:-/tmp}/extctl-gate-e2e.lock.d"
  while ! mkdir "$E2E_LOCK" 2>/dev/null; do
    # Steal a lock whose owner process is gone (mkdir locks, unlike flock, do
    # not auto-release when the holder is killed).
    owner=$(cat "$E2E_LOCK/pid" 2>/dev/null || echo "")
    if [ -n "$owner" ] && ! kill -0 "$owner" 2>/dev/null; then
      log "e2e: stealing stale lock from dead pid $owner"
      rm -rf "$E2E_LOCK"
      continue
    fi
    log "e2e: waiting for lock held by another gate run…"
    sleep 5
  done
  echo "$$" > "$E2E_LOCK/pid"
  # Release the lock on any exit from here on, in addition to writing gate.json.
  trap 'rmdir "$E2E_LOCK" 2>/dev/null || rm -rf "$E2E_LOCK" 2>/dev/null || true; write_json "$overall"' EXIT

  CONTAINER=$(cd "$MAIN_CHECKOUT" && docker compose ps -q ocis 2>/dev/null || true)
  if [ -z "$CONTAINER" ]; then
    e2e_result="fail"
    stage_fail e2e "oCIS not running in $MAIN_CHECKOUT — run: docker compose up -d"
    write_json false; exit 1
  fi

  # Inject the built extension; oCIS only scans /web/apps at startup, so restart.
  # Run mkdir as root: the container process user has no write permission on /web/apps/
  # for extensions that don't yet have a bind-mount in the running compose stack.
  docker exec -u root "$CONTAINER" mkdir -p "/web/apps/$EXT_ID" 2>&1 | tee -a "$LOG"
  docker cp "$EXT_DIR/dist/." "$CONTAINER:/web/apps/$EXT_ID/" 2>&1 | tee -a "$LOG"
  docker restart "$CONTAINER" 2>&1 | tee -a "$LOG"

  OCIS_URL="https://host.docker.internal:9200"
  for _ in $(seq 1 30); do
    if curl -sk --max-time 2 "$OCIS_URL/health/live" | grep -q "alive"; then break; fi
    sleep 2
  done

  # CI=true switches Playwright to its non-interactive reporter (no cursor-up/erase lines).
  # The sed strips any remaining ANSI escape sequences so gate.log stays plain text.
  if ! (cd "$EXT_DIR" && CI=true pnpm playwright test 2>&1 \
      | sed $'s/\x1b\\[[0-9;]*[a-zA-Z]//g' \
      | tee -a "$LOG"); then
    docker exec -u root "$CONTAINER" rm -rf "/web/apps/$EXT_ID" 2>&1 | tee -a "$LOG" || true
    e2e_result="fail"
    stage_fail e2e "playwright tests failed"
    write_json false; exit 1
  fi

  docker exec -u root "$CONTAINER" rm -rf "/web/apps/$EXT_ID" 2>&1 | tee -a "$LOG" || true
  e2e_result="ok"
  stage_ok e2e

  # Release the lock and restore the plain gate.json trap.
  rm -rf "$E2E_LOCK" 2>/dev/null || true
  trap 'write_json "$overall"' EXIT
fi

# ── All stages passed ─────────────────────────────────────────────────────────

log ""
log "=== Gate PASSED for $EXT_ID ==="
overall=true
write_json true
exit 0
