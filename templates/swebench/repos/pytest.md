Pytest repository notes:
- Assertion rewrite issues usually live in `src/_pytest/assertion/rewrite.py`.
- In assertion rewrite docstring handling, do not treat every leading `ast.Expr(ast.Constant(...))` as a docstring. A real docstring must be a string constant.
- If the issue mentions a numeric first expression being mistaken for a docstring, the likely minimal edit is to add a string-value check around the existing docstring branch, not to keep rereading helper methods.
- Once you have read `AssertionRewriter.run()` and `is_rewrite_disabled()`, edit the condition in `run()` immediately.
