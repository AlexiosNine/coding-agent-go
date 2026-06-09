#!/usr/bin/env bash
# Run one SWE-bench case locally and collect all useful artifacts.
#
# Usage:
#   ./swebench/run_case_collect.sh sympy__sympy-11400
#
# The script intentionally keeps secrets out of logs. It reads OPENAI_API_KEY /
# OPENAI_BASE_URL from the environment, or falls back to extracting the local
# e2e test config without printing the key.

set -u
set -o pipefail

INSTANCE_ID="${1:-}"
if [ -z "$INSTANCE_ID" ]; then
  echo "Usage: $0 <instance_id> [--model MODEL] [--dataset PATH] [--max-turns N] [--budget N]"
  exit 2
fi
shift

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
GOAGENT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
CC_ROOT="$(cd "$GOAGENT_ROOT/.." && pwd)"

MODEL="${LLM_MODEL:-xopkimik25}"
DATASET="$CC_ROOT/swebench_lite.json"
OUTPUT_ROOT="$GOAGENT_ROOT/swebench/runs"
WORKSPACE_ROOT="$GOAGENT_ROOT/swebench/workspace"
TEMPLATES_DIR="$GOAGENT_ROOT/templates"
TOOLSET="${SWE_TOOLSET:-lean}"
MAX_TURNS="${SWE_MAX_TURNS:-18}"
BUDGET="${SWE_EXPLORATION_BUDGET:-12}"
MAX_READ_ONLY_TURNS="${SWE_MAX_READ_ONLY_TURNS:-10}"
TURN_DELAY="${TURN_DELAY:-0}"
MAX_TOKENS="${SWE_MAX_TOKENS:-4096}"
TOOL_OUTPUT_CHARS="${SWE_TOOL_OUTPUT_CHARS:-4000}"
TOOL_SUMMARY_CHARS="${SWE_TOOL_SUMMARY_CHARS:-2400}"
CONTEXT_WINDOW="${SWE_CONTEXT_WINDOW:-12000}"
RECENT_WINDOW="${SWE_RECENT_WINDOW:-4}"
BUILD=1
EXTRACT_LOCAL_KEY=1

while [ "$#" -gt 0 ]; do
  case "$1" in
    --model)
      MODEL="$2"
      shift 2
      ;;
    --dataset)
      DATASET="$2"
      shift 2
      ;;
    --output-root)
      OUTPUT_ROOT="$2"
      shift 2
      ;;
    --workspace-root)
      WORKSPACE_ROOT="$2"
      shift 2
      ;;
    --toolset)
      TOOLSET="$2"
      shift 2
      ;;
    --max-turns)
      MAX_TURNS="$2"
      shift 2
      ;;
    --budget)
      BUDGET="$2"
      shift 2
      ;;
    --turn-delay)
      TURN_DELAY="$2"
      shift 2
      ;;
    --max-tokens)
      MAX_TOKENS="$2"
      shift 2
      ;;
    --tool-output-chars)
      TOOL_OUTPUT_CHARS="$2"
      shift 2
      ;;
    --tool-summary-chars)
      TOOL_SUMMARY_CHARS="$2"
      shift 2
      ;;
    --context-window)
      CONTEXT_WINDOW="$2"
      shift 2
      ;;
    --recent-window)
      RECENT_WINDOW="$2"
      shift 2
      ;;
    --no-build)
      BUILD=0
      shift
      ;;
    --no-local-key-extract)
      EXTRACT_LOCAL_KEY=0
      shift
      ;;
    *)
      echo "Unknown option: $1"
      exit 2
      ;;
  esac
done

if [ ! -f "$DATASET" ]; then
  echo "Dataset not found: $DATASET"
  exit 2
fi

RUN_ID="$(date +%Y%m%d_%H%M%S)_${INSTANCE_ID}"
RUN_DIR="$OUTPUT_ROOT/$RUN_ID"
mkdir -p "$RUN_DIR"

STDOUT_FILE="$RUN_DIR/runner.stdout.log"
STDERR_FILE="$RUN_DIR/runner.stderr.log"
PREDICTION_FILE="$RUN_DIR/prediction.jsonl"
ENV_FILE="$RUN_DIR/env_summary.txt"
STATUS_FILE="$RUN_DIR/status.txt"
COMMAND_FILE="$RUN_DIR/command.txt"

if [ "$BUILD" -eq 1 ]; then
  echo "[build] adapter"
  (cd "$GOAGENT_ROOT/swebench/adapter" && GOCACHE="${GOCACHE:-/private/tmp/goagent-gocache}" go build -o adapter .)
  echo "[build] runner"
  (cd "$GOAGENT_ROOT/swebench/runner" && GOCACHE="${GOCACHE:-/private/tmp/goagent-gocache}" go build -o runner .)
fi

KEY_SOURCE="env"
BASE_SOURCE="env"
API_KEY="${OPENAI_API_KEY:-}"
BASE_URL="${OPENAI_BASE_URL:-}"

if [ -z "$API_KEY" ] && [ "$EXTRACT_LOCAL_KEY" -eq 1 ] && [ -f "$GOAGENT_ROOT/e2e_test.go" ]; then
  API_KEY="$(perl -ne 'print "$1\n" if /apiKey = "([^"]+)"/' "$GOAGENT_ROOT/e2e_test.go" | head -1)"
  KEY_SOURCE="local e2e_test.go"
fi

if [ -z "$BASE_URL" ] && [ "$EXTRACT_LOCAL_KEY" -eq 1 ] && [ -f "$GOAGENT_ROOT/e2e_test.go" ]; then
  BASE_URL="$(perl -ne 'print "$1\n" if /baseURL = "([^"]+)"/' "$GOAGENT_ROOT/e2e_test.go" | head -1)"
  BASE_SOURCE="local e2e_test.go"
fi

if [ -z "$API_KEY" ]; then
  echo "OPENAI_API_KEY is not set and no local fallback key was found."
  echo "Set OPENAI_API_KEY or rerun without --no-local-key-extract."
  exit 2
fi

if [ -z "$BASE_URL" ]; then
  echo "OPENAI_BASE_URL is not set and no local fallback base URL was found."
  echo "Set OPENAI_BASE_URL or rerun without --no-local-key-extract."
  exit 2
fi

{
  echo "instance_id=$INSTANCE_ID"
  echo "model=$MODEL"
  echo "dataset=$DATASET"
  echo "run_dir=$RUN_DIR"
  echo "run_id=$RUN_ID"
  echo "workspace_root=$WORKSPACE_ROOT"
  echo "templates_dir=$TEMPLATES_DIR"
  echo "toolset=$TOOLSET"
  echo "max_turns=$MAX_TURNS"
  echo "exploration_budget=$BUDGET"
  echo "max_read_only_turns=$MAX_READ_ONLY_TURNS"
  echo "turn_delay=$TURN_DELAY"
  echo "max_tokens=$MAX_TOKENS"
  echo "context_window=$CONTEXT_WINDOW"
  echo "recent_window=$RECENT_WINDOW"
  echo "tool_output_chars=$TOOL_OUTPUT_CHARS"
  echo "tool_summary_chars=$TOOL_SUMMARY_CHARS"
  echo "openai_api_key_source=$KEY_SOURCE"
  echo "openai_api_key_present=yes"
  echo "openai_base_url=$BASE_URL"
  echo "openai_base_url_source=$BASE_SOURCE"
  echo "started_at=$(date -Iseconds)"
} > "$ENV_FILE"

cat > "$COMMAND_FILE" <<EOF
cd "$GOAGENT_ROOT/swebench/runner"
OPENAI_API_KEY="<redacted>" OPENAI_BASE_URL="$BASE_URL" LLM_MODEL="$MODEL" \\
TURN_DELAY="$TURN_DELAY" SWE_TOOLSET="$TOOLSET" SWE_MAX_TURNS="$MAX_TURNS" \\
SWE_EXPLORATION_BUDGET="$BUDGET" SWE_MAX_READ_ONLY_TURNS="$MAX_READ_ONLY_TURNS" \\
SWE_MAX_TOKENS="$MAX_TOKENS" \\
SWE_CONTEXT_WINDOW="$CONTEXT_WINDOW" SWE_RECENT_WINDOW="$RECENT_WINDOW" \\
SWE_TOOL_OUTPUT_CHARS="$TOOL_OUTPUT_CHARS" SWE_TOOL_SUMMARY_CHARS="$TOOL_SUMMARY_CHARS" \\
SWE_WORKSPACE_ROOT="$WORKSPACE_ROOT" SWE_TEMPLATES_DIR="$TEMPLATES_DIR" \\
SWE_RUN_DIR="$RUN_DIR" SWE_RUN_ID="$RUN_ID" \\
./runner --dataset "$DATASET" \\
  --instances "$INSTANCE_ID" --output "$PREDICTION_FILE"
EOF

echo "=== SWE-bench single-case collection ==="
echo "Instance: $INSTANCE_ID"
echo "Run dir:  $RUN_DIR"
echo "Dataset:  $DATASET"
echo "Model:    $MODEL"
echo "Endpoint: $BASE_URL"
echo

set +e
(
  cd "$GOAGENT_ROOT/swebench/runner" || exit 1
  OPENAI_API_KEY="$API_KEY" \
  OPENAI_BASE_URL="$BASE_URL" \
  LLM_MODEL="$MODEL" \
  TURN_DELAY="$TURN_DELAY" \
  SWE_TOOLSET="$TOOLSET" \
  SWE_MAX_TURNS="$MAX_TURNS" \
  SWE_EXPLORATION_BUDGET="$BUDGET" \
  SWE_MAX_READ_ONLY_TURNS="$MAX_READ_ONLY_TURNS" \
  SWE_MAX_TOKENS="$MAX_TOKENS" \
  SWE_CONTEXT_WINDOW="$CONTEXT_WINDOW" \
  SWE_RECENT_WINDOW="$RECENT_WINDOW" \
  SWE_TOOL_OUTPUT_CHARS="$TOOL_OUTPUT_CHARS" \
  SWE_TOOL_SUMMARY_CHARS="$TOOL_SUMMARY_CHARS" \
  SWE_WORKSPACE_ROOT="$WORKSPACE_ROOT" \
  SWE_TEMPLATES_DIR="$TEMPLATES_DIR" \
  SWE_RUN_DIR="$RUN_DIR" \
  SWE_RUN_ID="$RUN_ID" \
  ./runner --dataset "$DATASET" --instances "$INSTANCE_ID" --output "$PREDICTION_FILE"
) > >(tee "$STDOUT_FILE") 2> >(tee "$STDERR_FILE" >&2)
STATUS=$?
set -e

FAILED_COUNT=""
SUCCESS_COUNT=""
if [ -f "$STDOUT_FILE" ]; then
  FAILED_COUNT="$(awk '/^Failed: / { value=$2 } END { print value }' "$STDOUT_FILE")"
  SUCCESS_COUNT="$(awk '/^Successful: / { value=$2 } END { print value }' "$STDOUT_FILE")"
fi

RUN_RESULT="unknown"
if [ "$STATUS" -ne 0 ]; then
  RUN_RESULT="command_failed"
elif [ -n "$FAILED_COUNT" ] && [ "$FAILED_COUNT" != "0" ]; then
  RUN_RESULT="case_failed"
elif [ -n "$SUCCESS_COUNT" ] && [ "$SUCCESS_COUNT" != "0" ]; then
  RUN_RESULT="case_success"
fi

METRICS_FILE="$RUN_DIR/metrics.json"
if [ -f "$METRICS_FILE" ]; then
  METRICS_RESULT="$(sed -n 's/.*"run_result": *"\([^"]*\)".*/\1/p' "$METRICS_FILE" | head -1)"
  if [ -n "$METRICS_RESULT" ]; then
    RUN_RESULT="$METRICS_RESULT"
  fi
fi

{
  echo "exit_code=$STATUS"
  echo "run_result=$RUN_RESULT"
  [ -n "$SUCCESS_COUNT" ] && echo "successful=$SUCCESS_COUNT"
  [ -n "$FAILED_COUNT" ] && echo "failed=$FAILED_COUNT"
  echo "finished_at=$(date -Iseconds)"
} > "$STATUS_FILE"

CASE_LOG="$WORKSPACE_ROOT/logs/${INSTANCE_ID}.log"
CASE_PATCH="$WORKSPACE_ROOT/patches/${INSTANCE_ID}.diff"
CASE_REPO="$WORKSPACE_ROOT/${INSTANCE_ID}"

if [ -f "$CASE_LOG" ]; then
  cp "$CASE_LOG" "$RUN_DIR/adapter.log"
fi

if [ -f "$CASE_PATCH" ]; then
  cp "$CASE_PATCH" "$RUN_DIR/patch.diff"
fi

if [ -d "$CASE_REPO/.git" ]; then
  git -C "$CASE_REPO" status --short > "$RUN_DIR/repo_status.txt" 2>&1
  git -C "$CASE_REPO" diff HEAD > "$RUN_DIR/repo.diff" 2>&1
fi

if [ -f "$PREDICTION_FILE" ]; then
  python3 - "$PREDICTION_FILE" "$RUN_DIR/prediction_patch.diff" <<'PY'
import json
import sys

prediction_file, patch_file = sys.argv[1], sys.argv[2]
with open(prediction_file) as f:
    text = f.read().strip()
if not text:
    sys.exit(0)
line = text.splitlines()[-1]
try:
    pred = json.loads(line)
except Exception:
    sys.exit(0)
with open(patch_file, "w") as out:
    out.write(pred.get("model_patch", ""))
PY
fi

cat > "$RUN_DIR/README.md" <<EOF
# SWE-bench Case Run

- Instance: \`$INSTANCE_ID\`
- Exit code: \`$STATUS\`
- Run result: \`$RUN_RESULT\`
- Model: \`$MODEL\`
- Endpoint: \`$BASE_URL\`
- Workspace root: \`$WORKSPACE_ROOT\`

Useful files:

- \`runner.stdout.log\`
- \`runner.stderr.log\`
- \`runner_status.jsonl\` structured per-instance runner status
- \`adapter.log\` if the adapter started
- \`events.jsonl\`
- \`metrics.json\`
- \`summary.md\`
- \`prediction.jsonl\` if a prediction was produced
- \`prediction_patch.diff\` extracted from prediction JSONL
- \`repo.diff\` from the case workspace
- \`repo_status.txt\`
- \`env_summary.txt\` with redacted environment metadata
- \`command.txt\` with a redacted reproduction command
EOF

echo
echo "=== Collection complete ==="
echo "Exit code: $STATUS"
echo "Run result: $RUN_RESULT"
echo "Run dir:   $RUN_DIR"
echo "Key files:"
echo "  $STDOUT_FILE"
echo "  $STDERR_FILE"
[ -f "$RUN_DIR/adapter.log" ] && echo "  $RUN_DIR/adapter.log"
[ -f "$RUN_DIR/repo.diff" ] && echo "  $RUN_DIR/repo.diff"
[ -f "$RUN_DIR/prediction_patch.diff" ] && echo "  $RUN_DIR/prediction_patch.diff"

exit "$STATUS"
