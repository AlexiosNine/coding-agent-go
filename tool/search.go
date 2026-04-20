package tool

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	cc "github.com/alexioschen/cc-connect/goagent"
	"github.com/yanyiwu/gojieba"
)

var jieba *gojieba.Jieba

func init() {
	jieba = gojieba.NewJieba()
}

type searchInput struct {
	Query      string `json:"query" desc:"Search query (natural language or keywords)"`
	Path       string `json:"path" desc:"Directory to search in (default: current directory)"`
	Pattern    string `json:"pattern,omitempty" desc:"File glob pattern filter (e.g. *.go, *.py)"`
	MaxResults int    `json:"max_results,omitempty" desc:"Maximum results to return (default 20)"`
	Offset     int    `json:"offset,omitempty" desc:"Result offset for pagination"`
	Limit      int    `json:"limit,omitempty" desc:"Results per page (default 20)"`
}

type docInfo struct {
	path      string
	termFreqs map[string]int
	docLength int
	lines     []string
}

type searchResult struct {
	path    string
	score   float64
	snippet string
}

// Search returns a BM25-based semantic code search tool.
func Search() cc.Tool {
	return cc.NewFuncTool(
		"search",
		"Search codebase using BM25 semantic ranking. Returns relevant files with scores and code snippets.",
		func(ctx context.Context, input searchInput) (string, error) {
			if input.Query == "" {
				return "", fmt.Errorf("query is required")
			}

			path := input.Path
			if path == "" {
				path = "."
			}

			maxResults := input.MaxResults
			if maxResults <= 0 {
				maxResults = 20
			}

			limit := input.Limit
			if limit <= 0 {
				limit = 20
			}

			offset := input.Offset
			if offset < 0 {
				offset = 0
			}

			cacheID := "search:" + input.Query + ":" + path + ":" + input.Pattern

			if offset > 0 {
				buf := cc.GetOutputBuffer(ctx)
				if buf != nil {
					page, total, exists := buf.TryGetPage(cacheID, offset, limit)
					if exists {
						if offset >= total {
							return fmt.Sprintf("No results at offset %d (total: %d)", offset, total), nil
						}
						end := offset + limit
						if end > total {
							end = total
						}
						body := decodeResultPage(page)
						return fmt.Sprintf("Found %d relevant files for %q (showing %d-%d):\n\n%s\n---\nTotal: %d files. Next offset: %d", total, input.Query, offset+1, end, body, total, end), nil
					}
				}
			}

			absPath, err := filepath.Abs(path)
			if err != nil {
				return "", fmt.Errorf("invalid path: %w", err)
			}

			info, err := os.Stat(absPath)
			if err != nil {
				return "", fmt.Errorf("path not found: %w", err)
			}

			queryTerms := tokenize(input.Query)
			if len(queryTerms) == 0 {
				return "", fmt.Errorf("query has no searchable terms")
			}

			var docs []docInfo
			basePath := absPath
			if !info.IsDir() {
				basePath = filepath.Dir(absPath)
			}

			collectDoc := func(filePath string, entry os.DirEntry) error {
				if input.Pattern != "" {
					rel, relErr := filepath.Rel(basePath, filePath)
					if relErr != nil {
						rel = filePath
					}
					baseMatch, err := filepath.Match(input.Pattern, filepath.Base(filePath))
					if err != nil {
						return fmt.Errorf("invalid pattern: %w", err)
					}
					relMatch, _ := filepath.Match(input.Pattern, rel)
					if !baseMatch && !relMatch {
						return nil
					}
				}

				fileInfo, err := entry.Info()
				if err != nil {
					return nil
				}
				if fileInfo.Size() > (1 << 20) {
					return nil
				}
				if isBinaryFile(filePath) {
					return nil
				}

				data, err := os.ReadFile(filePath)
				if err != nil {
					return nil
				}

				termFreqs := tokenize(string(data))
				if len(termFreqs) == 0 {
					return nil
				}

				docLen := 0
				for _, c := range termFreqs {
					docLen += c
				}

				rel, relErr := filepath.Rel(basePath, filePath)
				if relErr != nil {
					rel = filePath
				}

				docs = append(docs, docInfo{
					path:      rel,
					termFreqs: termFreqs,
					docLength: docLen,
					lines:     strings.Split(string(data), "\n"),
				})
				return nil
			}

			if info.IsDir() {
				err = filepath.WalkDir(absPath, func(currPath string, d os.DirEntry, walkErr error) error {
					if walkErr != nil {
						return walkErr
					}

					if d.IsDir() {
						name := d.Name()
						if name == ".git" || name == "node_modules" || name == "__pycache__" ||
							name == ".tox" || name == ".eggs" || name == "build" || name == "dist" {
							return filepath.SkipDir
						}
						return nil
					}

					return collectDoc(currPath, d)
				})
				if err != nil {
					return "", err
				}
			} else {
				entryInfo, err := os.Stat(absPath)
				if err != nil {
					return "", err
				}
				dirEntry := fileInfoDirEntry{info: entryInfo, name: filepath.Base(absPath)}
				if err := collectDoc(absPath, dirEntry); err != nil {
					return "", err
				}
			}

			if len(docs) == 0 {
				return fmt.Sprintf("No relevant files found for %q in %s", input.Query, absPath), nil
			}

			scored := calculateBM25(docs, queryTerms)
			docByPath := make(map[string]docInfo, len(docs))
			for _, d := range docs {
				docByPath[d.path] = d
			}

			var results []searchResult
			for _, r := range scored {
				if r.score < 0.1 {
					continue
				}
				doc := docByPath[r.path]
				r.snippet = extractSnippet(doc.lines, queryTerms)
				results = append(results, r)
			}

			if len(results) == 0 {
				return fmt.Sprintf("No relevant files found for %q in %s", input.Query, absPath), nil
			}

			if len(results) > maxResults {
				results = results[:maxResults]
			}

			encoded := make([]string, 0, len(results))
			for i, r := range results {
				block := fmt.Sprintf("[%d] %s (score: %.2f)\n%s", i+1, r.path, r.score, r.snippet)
				encoded = append(encoded, base64.StdEncoding.EncodeToString([]byte(block)))
			}

			buf := cc.GetOutputBuffer(ctx)
			if buf != nil {
				buf.Store(cacheID, strings.Join(encoded, "\n"))
			}

			total := len(encoded)
			if offset >= total {
				return fmt.Sprintf("No results at offset %d (total: %d)", offset, total), nil
			}
			end := offset + limit
			if end > total {
				end = total
			}

			body := decodeResultPage(strings.Join(encoded[offset:end], "\n"))
			return fmt.Sprintf("Found %d relevant files for %q (showing %d-%d):\n\n%s\n---\nTotal: %d files. Next offset: %d", total, input.Query, offset+1, end, body, total, end), nil
		},
	)
}

func tokenize(text string) map[string]int {
	stopWords := map[string]struct{}{
		"the": {}, "is": {}, "a": {}, "an": {}, "in": {}, "on": {}, "at": {}, "to": {},
		"for": {}, "of": {}, "and": {}, "or": {}, "not": {}, "it": {}, "be": {}, "as": {},
		"do": {}, "by": {}, "if": {}, "no": {}, "up": {}, "so": {}, "he": {}, "we": {},
		"my": {}, "me": {}, "us": {},
		"的": {}, "了": {}, "在": {}, "是": {}, "我": {}, "有": {}, "和": {}, "就": {},
		"不": {}, "人": {}, "都": {}, "一": {}, "一个": {}, "上": {}, "也": {}, "很": {},
		"到": {}, "说": {}, "要": {}, "去": {}, "你": {}, "会": {}, "着": {}, "没有": {},
		"看": {}, "好": {}, "自己": {}, "这": {}, "那": {}, "里": {}, "为": {}, "能": {},
	}

	freqs := make(map[string]int)

	hasChinese := false
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			hasChinese = true
			break
		}
	}

	if hasChinese {
		terms := jieba.Cut(text, true)
		for _, t := range terms {
			t = strings.ToLower(strings.TrimSpace(t))
			if t == "" {
				continue
			}
			if _, stop := stopWords[t]; stop {
				continue
			}
			freqs[t]++
		}
		return freqs
	}

	var token []rune
	flush := func() {
		if len(token) == 0 {
			return
		}
		parts := splitCamel(string(token))
		for _, p := range parts {
			p = strings.ToLower(strings.TrimSpace(p))
			if p == "" {
				continue
			}
			if _, stop := stopWords[p]; stop {
				continue
			}
			freqs[p]++
		}
		token = token[:0]
	}

	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			token = append(token, r)
		} else {
			flush()
		}
	}
	flush()

	return freqs
}

func splitCamel(s string) []string {
	if s == "" {
		return nil
	}

	runes := []rune(s)
	start := 0
	var out []string
	for i := 1; i < len(runes); i++ {
		prev := runes[i-1]
		curr := runes[i]
		if unicode.IsLower(prev) && unicode.IsUpper(curr) {
			out = append(out, string(runes[start:i]))
			start = i
		}
	}
	out = append(out, string(runes[start:]))
	return out
}

func calculateBM25(docs []docInfo, queryTerms map[string]int) []searchResult {
	if len(docs) == 0 || len(queryTerms) == 0 {
		return nil
	}

	const (
		k1 = 1.2
		b  = 0.75
	)

	n := float64(len(docs))
	totalLen := 0.0
	for _, d := range docs {
		totalLen += float64(d.docLength)
	}
	avgLen := totalLen / n
	if avgLen <= 0 {
		avgLen = 1
	}

	df := make(map[string]float64)
	for term := range queryTerms {
		for _, d := range docs {
			if d.termFreqs[term] > 0 {
				df[term]++
			}
		}
	}

	results := make([]searchResult, 0, len(docs))
	for _, d := range docs {
		score := 0.0
		for term, qtf := range queryTerms {
			tf := float64(d.termFreqs[term])
			if tf <= 0 {
				continue
			}

			idf := math.Log((n-df[term]+0.5)/(df[term]+0.5) + 1.0)
			denom := tf + k1*(1-b+b*(float64(d.docLength)/avgLen))
			score += idf * ((tf * (k1 + 1)) / denom) * float64(qtf)
		}

		if score > 0 {
			fileNameTerms := tokenize(filepath.Base(d.path))
			for term := range queryTerms {
				if fileNameTerms[term] > 0 {
					score *= 1.2
					break
				}
			}
		}

		results = append(results, searchResult{path: d.path, score: score})
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].score == results[j].score {
			return results[i].path < results[j].path
		}
		return results[i].score > results[j].score
	})

	return results
}

func extractSnippet(lines []string, queryTerms map[string]int) string {
	if len(lines) == 0 {
		return "  1: "
	}

	bestStart := 0
	bestDensity := -1.0
	windowSize := 3

	for start := 0; start < len(lines); start++ {
		end := start + windowSize
		if end > len(lines) {
			end = len(lines)
		}

		hits := 0
		for i := start; i < end; i++ {
			lineTerms := tokenize(lines[i])
			for term := range queryTerms {
				hits += lineTerms[term]
			}
		}

		density := float64(hits) / float64(end-start)
		if density > bestDensity {
			bestDensity = density
			bestStart = start
		}
	}

	end := bestStart + windowSize
	if end > len(lines) {
		end = len(lines)
	}

	var b strings.Builder
	for i := bestStart; i < end; i++ {
		if i > bestStart {
			b.WriteByte('\n')
		}
		b.WriteString(fmt.Sprintf("  %d: %s", i+1, strings.TrimSpace(lines[i])))
	}
	return b.String()
}

func isBinaryFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return true
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return false
	}
	return bytes.IndexByte(buf[:n], 0) >= 0
}

func decodeResultPage(page string) string {
	if strings.TrimSpace(page) == "" {
		return ""
	}

	parts := strings.Split(page, "\n")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(p)
		if err != nil {
			out = append(out, p)
			continue
		}
		out = append(out, string(decoded))
	}
	return strings.Join(out, "\n\n")
}

type fileInfoDirEntry struct {
	info os.FileInfo
	name string
}

func (e fileInfoDirEntry) Name() string               { return e.name }
func (e fileInfoDirEntry) IsDir() bool                { return false }
func (e fileInfoDirEntry) Type() os.FileMode          { return e.info.Mode().Type() }
func (e fileInfoDirEntry) Info() (os.FileInfo, error) { return e.info, nil }
