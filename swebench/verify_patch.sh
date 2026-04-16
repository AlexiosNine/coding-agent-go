#!/bin/bash
# SWE-bench Patch Verification for sympy__sympy-11400
# Validates that ccode(sinc(x)) produces valid C code after patching
set -e

REPO_PATH="/tmp/swebench/sympy__sympy-11400"
PATCH_FILE="${1:-/tmp/test_patch.diff}"

echo "=== SWE-bench Patch Verification ==="
echo "Instance: sympy__sympy-11400"
echo ""

# Step 1: Check patch file
if [ ! -f "$PATCH_FILE" ]; then
    # Try to get patch from git diff
    cd "$REPO_PATH"
    PATCH_FILE="/tmp/test_patch.diff"
    git diff HEAD > "$PATCH_FILE"
fi

if [ ! -s "$PATCH_FILE" ]; then
    echo "FAIL: No patch to verify"
    exit 1
fi
echo "[1/4] Patch: $(wc -l < "$PATCH_FILE") lines"

# Step 2: Reset and apply
cd "$REPO_PATH"
git checkout . 2>/dev/null
echo "[2/4] Clean checkout"

if ! git apply --check "$PATCH_FILE" 2>/dev/null; then
    echo "FAIL: Patch cannot be applied"
    exit 1
fi
git apply "$PATCH_FILE"
echo "[3/4] Patch applied"

# Step 3: Run verification
echo "[4/4] Running tests..."
PYTHON=$(command -v python3.13 || command -v python3.11 || command -v python3.10 || command -v python3)

$PYTHON -W ignore::SyntaxWarning -c "
import collections, collections.abc
for attr in ['Mapping', 'MutableMapping', 'Iterable', 'Callable', 'Iterator', 'Sequence', 'MutableSequence', 'Set', 'MutableSet']:
    if not hasattr(collections, attr) and hasattr(collections.abc, attr):
        setattr(collections, attr, getattr(collections.abc, attr))

import sys, os
sys.path.insert(0, '$REPO_PATH')
os.chdir('$REPO_PATH')

from sympy import symbols, sinc, sin, Piecewise, Ne, S
from sympy.printing.ccode import ccode

x = symbols('x')
theta = symbols('theta')

# Test 1: ccode(sinc(x)) should not be 'Not supported'
result = ccode(sinc(x))
print(f'Test 1: ccode(sinc(x)) = {result}')
assert '// Not supported' not in result, f'FAIL: sinc still not supported: {result}'
print('  PASS')

# Test 2: Result should contain conditional (Piecewise) or sin/x
assert '?' in result or 'sin' in result, f'FAIL: unexpected format: {result}'
print('Test 2: Contains conditional or sin expression')
print('  PASS')

# Test 3: Should handle sinc(0) → 1 case
assert '1' in result, f'FAIL: missing sinc(0)=1 case: {result}'
print('Test 3: Contains sinc(0)=1 fallback')
print('  PASS')

# Test 4: Compare with expected Piecewise output
expected = ccode(Piecewise((sin(x)/x, Ne(x, 0)), (1, True)))
print(f'Test 4: Expected = {expected}')
assert result == expected, f'FAIL: result mismatch.\n  Got:      {result}\n  Expected: {expected}'
print('  PASS')

# Test 5: Works with different symbol
result2 = ccode(sinc(theta))
print(f'Test 5: ccode(sinc(theta)) = {result2}')
assert '// Not supported' not in result2, f'FAIL: sinc(theta) not supported'
print('  PASS')

print()
print('=== ALL 5 TESTS PASSED ===')
"

TEST_EXIT=$?
echo ""
if [ $TEST_EXIT -eq 0 ]; then
    echo "RESULT: PASS - Patch correctly fixes ccode(sinc(x))"
else
    echo "RESULT: FAIL"
fi
exit $TEST_EXIT
