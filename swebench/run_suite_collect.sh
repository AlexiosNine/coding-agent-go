#!/usr/bin/env bash
# Run the local SWE-bench golden suite and keep per-case artifacts.
#
# Usage:
#   ./swebench/run_suite_collect.sh
#   ./swebench/run_suite_collect.sh --suite swebench/suites/golden_cases.txt --max-turns 18 --budget 12
#
# Unknown options are forwarded to run_case_collect.sh.

set -u
set -o pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
GOAGENT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

SUITE_FILE="$GOAGENT_ROOT/swebench/suites/golden_cases.txt"
SUITE_NAME="golden"
SUITE_OUTPUT_ROOT="$GOAGENT_ROOT/swebench/suite_runs"
CASE_ARGS=()

while [ "$#" -gt 0 ]; do
  case "$1" in
    --suite)
      SUITE_FILE="$2"
      shift 2
      ;;
    --suite-name)
      SUITE_NAME="$2"
      shift 2
      ;;
    --suite-output-root)
      SUITE_OUTPUT_ROOT="$2"
      shift 2
      ;;
    *)
      CASE_ARGS+=("$1")
      shift
      ;;
  esac
done

if [ ! -f "$SUITE_FILE" ]; then
  echo "Suite file not found: $SUITE_FILE"
  exit 2
fi

RUN_ID="$(date +%Y%m%d_%H%M%S)_${SUITE_NAME}"
SUITE_DIR="$SUITE_OUTPUT_ROOT/$RUN_ID"
CASE_OUTPUT_ROOT="$SUITE_DIR/cases"
SUMMARY_FILE="$SUITE_DIR/summary.tsv"
README_FILE="$SUITE_DIR/README.md"
mkdir -p "$CASE_OUTPUT_ROOT"

{
  echo "# SWE-bench Suite Run"
  echo
  echo "- Suite: \`$SUITE_NAME\`"
  echo "- Suite file: \`$SUITE_FILE\`"
  echo "- Case output root: \`$CASE_OUTPUT_ROOT\`"
  echo "- Started at: \`$(date -Iseconds)\`"
  echo
  echo "Each case is executed through \`swebench/run_case_collect.sh\` and keeps its own artifacts."
} > "$README_FILE"

printf "instance_id\texit_code\trun_result\tverify_status\trun_dir\n" > "$SUMMARY_FILE"

total=0
passed=0
failed=0

echo "=== SWE-bench suite collection ==="
echo "Suite:     $SUITE_NAME"
echo "Suite dir: $SUITE_DIR"
echo "Cases:     $SUITE_FILE"
echo

while IFS= read -r raw_line || [ -n "$raw_line" ]; do
  line="${raw_line%%#*}"
  line="$(printf '%s' "$line" | xargs)"
  if [ -z "$line" ]; then
    continue
  fi

  total=$((total + 1))
  instance_id="$line"

  echo "=== [$total] $instance_id ==="
  "$GOAGENT_ROOT/swebench/run_case_collect.sh" "$instance_id" \
    --output-root "$CASE_OUTPUT_ROOT" \
    "${CASE_ARGS[@]}"
  exit_code=$?

  run_dir="$(ls -td "$CASE_OUTPUT_ROOT"/*_"$instance_id" 2>/dev/null | head -1)"
  run_result="unknown"
  verify_status="unknown"

  if [ -n "$run_dir" ] && [ -f "$run_dir/status.txt" ]; then
    run_result="$(sed -n 's/^run_result=//p' "$run_dir/status.txt" | tail -1)"
  fi
  if [ -n "$run_dir" ] && [ -f "$run_dir/metrics.json" ]; then
    metrics_result="$(sed -n 's/.*"run_result": *"\([^"]*\)".*/\1/p' "$run_dir/metrics.json" | head -1)"
    metrics_verify="$(sed -n 's/.*"verify_status": *"\([^"]*\)".*/\1/p' "$run_dir/metrics.json" | head -1)"
    [ -n "$metrics_result" ] && run_result="$metrics_result"
    [ -n "$metrics_verify" ] && verify_status="$metrics_verify"
  fi

  if [ "$run_result" = "case_success" ]; then
    passed=$((passed + 1))
  else
    failed=$((failed + 1))
  fi

  printf "%s\t%s\t%s\t%s\t%s\n" "$instance_id" "$exit_code" "$run_result" "$verify_status" "$run_dir" >> "$SUMMARY_FILE"
  echo "Result: $run_result verify=$verify_status exit=$exit_code"
  echo
done < "$SUITE_FILE"

{
  echo
  echo "## Result"
  echo
  echo "- Total: \`$total\`"
  echo "- Passed: \`$passed\`"
  echo "- Failed: \`$failed\`"
  echo "- Finished at: \`$(date -Iseconds)\`"
  echo
  echo "Summary: \`summary.tsv\`"
} >> "$README_FILE"

echo "=== Suite complete ==="
echo "Total:   $total"
echo "Passed:  $passed"
echo "Failed:  $failed"
echo "Summary: $SUMMARY_FILE"

if [ "$failed" -gt 0 ]; then
  exit 1
fi
exit 0
