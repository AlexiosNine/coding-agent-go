#!/bin/bash
# SWE-bench Patch Verification for supported local golden cases.
set -e

REPO_PATH="${SWE_REPO_PATH:-/tmp/swebench/sympy__sympy-11400}"
PATCH_FILE="${1:-/tmp/test_patch.diff}"
INSTANCE_ID="${SWE_INSTANCE_ID:-sympy__sympy-11400}"
if [ -n "$PATCH_FILE" ]; then
    PATCH_FILE="$(cd "$(dirname "$PATCH_FILE")" && pwd)/$(basename "$PATCH_FILE")"
fi

echo "=== SWE-bench Patch Verification ==="
echo "Instance: $INSTANCE_ID"
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

echo "[4/4] Running tests..."
PYTHON=$(command -v python3.13 || command -v python3.11 || command -v python3.10 || command -v python3)

case "$INSTANCE_ID" in
  sympy__sympy-11400)
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
    ;;
  django__django-11179)
    $PYTHON - <<'PY'
import os
import ast

repo = os.environ["SWE_REPO_PATH"]
target = os.path.join(repo, "django", "db", "models", "deletion.py")
with open(target, "r", encoding="utf-8") as f:
    tree = ast.parse(f.read(), filename=target)


def dotted_name(node):
    if isinstance(node, ast.Name):
        return node.id
    if isinstance(node, ast.Attribute):
        base = dotted_name(node.value)
        return f"{base}.{node.attr}" if base else node.attr
    return ""


def is_can_fast_delete_call(node):
    return (
        isinstance(node, ast.Call)
        and isinstance(node.func, ast.Attribute)
        and node.func.attr == "can_fast_delete"
        and len(node.args) == 1
        and isinstance(node.args[0], ast.Name)
        and node.args[0].id == "instance"
    )


def is_fast_delete_query(node):
    if not isinstance(node, ast.Assign) or not isinstance(node.value, ast.Call):
        return False
    call_target = node.value.func
    delete_query = call_target.value if isinstance(call_target, ast.Attribute) else None
    return (
        any(isinstance(target, ast.Name) and target.id == "count" for target in node.targets)
        and isinstance(call_target, ast.Attribute)
        and call_target.attr == "delete_batch"
        and isinstance(delete_query, ast.Call)
        and dotted_name(delete_query.func) == "sql.DeleteQuery"
        and len(delete_query.args) == 1
        and isinstance(delete_query.args[0], ast.Name)
        and delete_query.args[0].id == "model"
    )


def is_pk_clear(node):
    if not isinstance(node, ast.Expr) or not isinstance(node.value, ast.Call):
        return False
    call = node.value
    return (
        isinstance(call.func, ast.Name)
        and call.func.id == "setattr"
        and len(call.args) == 3
        and isinstance(call.args[0], ast.Name)
        and call.args[0].id == "instance"
        and dotted_name(call.args[1]) == "model._meta.pk.attname"
        and isinstance(call.args[2], ast.Constant)
        and call.args[2].value is None
    )


def has_fast_delete_pk_clear():
    for node in ast.walk(tree):
        if not isinstance(node, ast.FunctionDef) or node.name != "delete":
            continue
        for branch in ast.walk(node):
            if not isinstance(branch, ast.If) or not is_can_fast_delete_call(branch.test):
                continue
            body = branch.body
            for idx, stmt in enumerate(body):
                if isinstance(stmt, ast.Return):
                    before_return = body[:idx]
                    has_delete = any(
                        isinstance(inner, ast.With)
                        and any(is_fast_delete_query(with_stmt) for with_stmt in inner.body)
                        for inner in before_return
                    )
                    has_clear = any(is_pk_clear(prev) for prev in before_return)
                    if has_delete and has_clear:
                        return True
    return False


assert has_fast_delete_pk_clear(), (
    "Collector.delete() fast-delete branch must clear the instance primary key "
    "with setattr(instance, model._meta.pk.attname, None) before returning"
)
print("=== DJANGO FAST DELETE PK STATIC CHECK PASSED ===")
PY
    ;;
  pytest-dev__pytest-11143)
    $PYTHON - <<'PY'
import ast
import os
import sys

repo = os.environ["SWE_REPO_PATH"]
sys.path.insert(0, repo)
os.chdir(repo)

from _pytest.assertion.rewrite import AssertionRewriter

source = "1\nassert 1 == 1\n"
mod = ast.parse(source)
rewriter = AssertionRewriter("sample.py", None, source.encode())
rewriter.run(mod)

imports = [
    node for node in mod.body
    if isinstance(node, ast.Import) and any(alias.asname == "@py_builtins" for alias in node.names)
]
assert imports, "rewrite imports were not inserted after numeric leading expression"
assert any(
    isinstance(node, ast.Expr)
    and isinstance(node.value, ast.Constant)
    and node.value.value == 1
    for node in mod.body
), "numeric leading expression should remain in module body"
print("=== PYTEST REWRITE TEST PASSED ===")
PY
    ;;
  *)
    echo "FAIL: no verifier configured for $INSTANCE_ID"
    exit 1
    ;;
esac

echo ""
echo "RESULT: PASS"
