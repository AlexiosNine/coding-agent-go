Tool workflow:
- Prefer the smallest tool set needed for the current task.
- All file paths must be repository-relative, such as `sympy/printing/ccode.py`; never use a leading slash or host absolute path.
- Use read-only tools to establish the edit target, then switch to mutation tools.
- After a successful mutation, stop exploring and produce the final response unless the mutation tool reported an error.
