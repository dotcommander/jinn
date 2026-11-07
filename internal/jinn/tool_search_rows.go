package jinn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
)

// Ranked whole-record search over a JSONL or JSON-array file: BM25 over the
// union of query tokens, fixed-size pages, exact totals. Modeled on the GSI
// rows-search contract (search_rows.go); oversize corpora are a hard error,
// never silently truncated.
const (
	searchRowsSourceCap  = 32 << 20
	searchRowsPageSize   = 10
	searchRowsSchemaKeys = 12
	searchRowsBM25K1     = 1.2
	searchRowsBM25B      = 0.75
)

type searchRowRecord struct {
	Line   int     `json:"line"`
	Score  float64 `json:"score"`
	Record string  `json:"record"`
}

type searchRowsResult struct {
	TotalRows        int               `json:"total_rows"`
	SchemaKeys       []string          `json:"schema_keys"`
	MatchedRows      int               `json:"matched_rows"`
	Rows             []searchRowRecord `json:"rows"`
	Offset           int               `json:"offset"`
	Truncated        bool              `json:"truncated"`
	TruncationReason string            `json:"truncation_reason,omitempty"`
	Error            string            `json:"error,omitempty"`
}

// searchRows parses the file at args["path"] as JSONL or a JSON array of
// objects, ranks whole records by BM25 over the union of args["queries"]
// tokens, and pages the ranked list from args["offset"] (default 0).
func (e *Engine) searchRows(_ context.Context, args map[string]interface{}) (string, error) {
	result := searchRowsResult{Rows: []searchRowRecord{}, SchemaKeys: []string{}}
	//nolint:nilerr // rows-mode failures are reported in-band via result.Error, not as Go errors
	finish := func(err error) (string, error) {
		if err != nil {
			result.Error = err.Error()
			data, _ := json.Marshal(result)
			return string(data), nil
		}
		data, merr := json.Marshal(result)
		if merr != nil {
			return "", fmt.Errorf("marshal rows result: %w", merr)
		}
		return string(data), nil
	}
	pathArg := strArg(args, "path")
	if pathArg == "" {
		return finish(errors.New("rows mode requires path"))
	}
	queriesRaw, _ := args["queries"].([]interface{})
	var queries []string
	for _, q := range queriesRaw {
		if s, ok := q.(string); ok && strings.TrimSpace(s) != "" {
			queries = append(queries, s)
		}
	}
	if len(queries) == 0 {
		return finish(errors.New("rows mode requires a nonempty queries list"))
	}
	offset := max(0, intArg(args, "offset", 0))
	resolved, err := e.checkPath(pathArg)
	if err != nil {
		return finish(err)
	}
	data, _, readErr := e.rootedReadFile(resolved, searchRowsSourceCap)
	if readErr != nil {
		return finish(readErr)
	}
	parsed, parseErr := parseRowRecords(data)
	records, total, keys := parsed.rows, parsed.total, parsed.keys
	if parseErr != nil {
		return finish(parseErr)
	}
	result.TotalRows, result.SchemaKeys = total, keys
	scored := rankRowRecords(records, queries)
	result.MatchedRows = len(scored)
	if offset >= len(scored) {
		result.Offset = offset
		return finish(nil)
	}
	end := min(offset+searchRowsPageSize, len(scored))
	for _, s := range scored[offset:end] {
		result.Rows = append(result.Rows, searchRowRecord{Line: s.ordinal, Score: s.score, Record: s.text})
	}
	result.Offset = offset
	result.Truncated = end < len(scored)
	if result.Truncated {
		result.TruncationReason = "page"
	}
	return finish(nil)
}

type scoredRow struct {
	ordinal    int
	score      float64
	text       string
	searchText string // concatenated string values, filled during the single parse pass
}

// rowRecords bundles the parsed rows, total count, and top schema keys.
type rowRecords struct {
	rows  []scoredRow
	total int
	keys  []string
}

// parseRowRecords parses JSONL or a JSON array of objects, returning raw
// record texts with 1-based ordinals, the total count, and top schema keys.
func parseRowRecords(data []byte) (rowRecords, error) {
	trimmed := strings.TrimSpace(string(data))
	var lines []string
	if strings.HasPrefix(trimmed, "[") {
		var arr []json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
			return rowRecords{}, fmt.Errorf("rows source: invalid JSON array: %w", err)
		}
		for _, raw := range arr {
			lines = append(lines, string(raw))
		}
	} else {
		for _, line := range strings.Split(trimmed, "\n") {
			if strings.TrimSpace(line) != "" {
				lines = append(lines, line)
			}
		}
	}
	keyCounts := make(map[string]int)
	var rows []scoredRow
	for i, line := range lines {
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			return rowRecords{}, fmt.Errorf("rows source: record %d is not a JSON object: %w", i+1, err)
		}
		var searchText strings.Builder
		for k, v := range obj {
			keyCounts[k]++
			if s, ok := v.(string); ok {
				searchText.WriteString(s)
				searchText.WriteByte(' ')
			}
		}
		text := searchText.String()
		if text == "" {
			text = line
		}
		rows = append(rows, scoredRow{ordinal: i + 1, text: line, searchText: text})
	}
	keys := make([]string, 0, len(keyCounts))
	for k := range keyCounts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keyCounts[keys[i]] != keyCounts[keys[j]] {
			return keyCounts[keys[i]] > keyCounts[keys[j]]
		}
		return keys[i] < keys[j]
	})
	if len(keys) > searchRowsSchemaKeys {
		keys = keys[:searchRowsSchemaKeys]
	}
	return rowRecords{rows: rows, total: len(lines), keys: keys}, nil
}

// rankRowRecords scores each record with BM25 over the union of query tokens
// (all string values in the record) and returns matches sorted by score.
//
//nolint:gocognit,revive // branchy by design; splitting would scatter one sequential algorithm
func rankRowRecords(rows []scoredRow, queries []string) []scoredRow {
	queryTokens := make(map[string]bool)
	for _, q := range queries {
		for _, tok := range tokenizeRowText(q) {
			queryTokens[tok] = true
		}
	}
	if len(queryTokens) == 0 {
		return nil
	}
	type doc struct {
		ordinal int
		text    string
		tokens  []string
		counts  map[string]int
	}
	docs := make([]doc, 0, len(rows))
	docFreq := make(map[string]int)
	totalLen := 0
	for _, r := range rows {
		text := r.searchText
		if text == "" {
			text = rowStringValues(r.text)
		}
		tokens := tokenizeRowText(text)
		counts := make(map[string]int)
		for _, tok := range tokens {
			if counts[tok] == 0 {
				docFreq[tok]++
			}
			counts[tok]++
		}
		totalLen += len(tokens)
		docs = append(docs, doc{r.ordinal, r.text, tokens, counts})
	}
	avgLen := 1.0
	if len(docs) > 0 {
		avgLen = float64(totalLen) / float64(len(docs))
	}
	if avgLen <= 0 {
		avgLen = 1
	}
	N := float64(len(docs))
	var out []scoredRow
	for _, d := range docs {
		score := 0.0
		docLen := float64(len(d.tokens))
		for tok := range queryTokens {
			tf, ok := d.counts[tok]
			if !ok {
				continue
			}
			idf := math.Log(1 + (N-float64(docFreq[tok])+0.5)/(float64(docFreq[tok])+0.5))
			norm := 1 - searchRowsBM25B + searchRowsBM25B*docLen/avgLen
			score += idf * float64(tf) * (searchRowsBM25K1 + 1) / (float64(tf) + searchRowsBM25K1*norm)
		}
		if score > 0 {
			out = append(out, scoredRow{ordinal: d.ordinal, score: score, text: d.text})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].ordinal < out[j].ordinal
	})
	return out
}

func tokenizeRowText(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	return fields
}

// rowStringValues concatenates all string values in a JSON object record.
func rowStringValues(text string) string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(text), &obj); err != nil {
		return text
	}
	var b strings.Builder
	for _, v := range obj {
		if s, ok := v.(string); ok {
			b.WriteString(s)
			b.WriteByte(' ')
		}
	}
	if b.Len() == 0 {
		return text
	}
	return b.String()
}
