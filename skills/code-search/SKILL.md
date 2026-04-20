---
name: code-search
description: "Semantic code search using BM25 ranking with Chinese/English tokenization"
auto_match: true
keywords: ["search", "find", "locate", "where is", "哪里", "搜索", "查找"]
priority: 5
---

## Instructions

When the user asks to find code, locate implementations, or search the codebase:

1. Use the `search` tool with a natural language query
2. The search tool uses BM25 ranking with:
   - Chinese text: jieba segmentation
   - English text: camelCase splitting + word tokenization
   - File name boosting (1.2x score if query matches filename)
3. Review the top results and their code snippets
4. If needed, use `read_file` to examine the most relevant files in detail
5. Summarize findings for the user

### Tips for effective queries:
- Use specific function/class names when known
- Include the programming language or file type in the query
- For Chinese codebases, use Chinese keywords
- Use `pattern` parameter to filter by file type (e.g., `*.go`, `*.py`)

### Example workflow:
```
search(query="user authentication handler", pattern="*.go")
→ Review top 3 results
→ read_file the most relevant one
→ Explain to user
```
