package jinn

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	RouteDefaultMaxTools = 5
	RouteMaxTools        = 8
)

var routeMutatingTools = map[string]bool{
	"write_file":     true,
	"edit_file":      true,
	"multi_edit":     true,
	"apply_patch":    true,
	"search_replace": true,
	"memory":         true,
	"undo":           true,
	"run_shell":      true,
}

type RouteRequest struct {
	Need            string `json:"need"`
	MaxTools        int    `json:"max_tools,omitempty"`
	IncludeSchema   bool   `json:"include_schema,omitempty"`
	IncludeMutating *bool  `json:"include_mutating,omitempty"`
}

type RouteResponse struct {
	Query   string       `json:"query"`
	Matches []RouteMatch `json:"matches"`
	Notes   []string     `json:"notes"`
}

type RouteMatch struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Reason      string   `json:"reason"`
	Mutating    bool     `json:"mutating"`
	Risk        string   `json:"risk"`
	Features    []string `json:"features,omitempty"`
	Schema      any      `json:"schema,omitempty"`
}

type schemaTool struct {
	Type     string             `json:"type"`
	Function schemaToolFunction `json:"function"`
}

type schemaToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type routeCandidate struct {
	tool        schemaTool
	score       int
	reasonParts []string
}

// RouteTools recommends existing jinn tools for a natural-language need. It
// never dispatches or executes a tool.
func RouteTools(req RouteRequest) (RouteResponse, error) {
	need := strings.TrimSpace(req.Need)
	resp := RouteResponse{Query: req.Need, Matches: []RouteMatch{}}
	if need == "" {
		resp.Notes = []string{"No need was provided; pass a concrete task to get recommendations."}
		return resp, nil
	}

	maxTools := req.MaxTools
	if maxTools <= 0 {
		maxTools = RouteDefaultMaxTools
	}
	if maxTools > RouteMaxTools {
		maxTools = RouteMaxTools
	}

	tools, err := parseSchemaTools()
	if err != nil {
		return resp, err
	}

	queryTokens := tokenSet(need)
	candidates := make([]routeCandidate, 0, len(tools))
	for _, tool := range tools {
		name := tool.Function.Name
		mutating := routeMutatingTools[name]
		if mutating && !req.allowMutating() {
			continue
		}
		c := scoreRouteCandidate(tool, queryTokens, strings.ToLower(need))
		if c.score >= 8 {
			candidates = append(candidates, c)
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].tool.Function.Name < candidates[j].tool.Function.Name
		}
		return candidates[i].score > candidates[j].score
	})
	if len(candidates) > maxTools {
		candidates = candidates[:maxTools]
	}

	for _, c := range candidates {
		name := c.tool.Function.Name
		match := RouteMatch{
			Name:        name,
			Description: c.tool.Function.Description,
			Reason:      routeReason(c.reasonParts),
			Mutating:    routeMutatingTools[name],
			Risk:        routeRisk(name),
			Features:    toolFeatures[name],
		}
		if req.IncludeSchema {
			match.Schema = leanSchemaForTool(c.tool)
		}
		resp.Matches = append(resp.Matches, match)
	}

	resp.Notes = routeNotes(resp.Matches)
	return resp, nil
}

func (r RouteRequest) allowMutating() bool {
	return r.IncludeMutating == nil || *r.IncludeMutating
}

func DecodeRouteRequest(data []byte) (RouteRequest, error) {
	var raw struct {
		Need            string `json:"need"`
		MaxTools        int    `json:"max_tools"`
		IncludeSchema   bool   `json:"include_schema"`
		IncludeMutating *bool  `json:"include_mutating"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return RouteRequest{}, err
	}
	req := RouteRequest{
		Need:            raw.Need,
		MaxTools:        raw.MaxTools,
		IncludeSchema:   raw.IncludeSchema,
		IncludeMutating: raw.IncludeMutating,
	}
	return req, nil
}

func parseSchemaTools() ([]schemaTool, error) {
	var tools []schemaTool
	if err := json.Unmarshal([]byte(Schema), &tools); err != nil {
		return nil, fmt.Errorf("parse schema tools: %w", err)
	}
	return tools, nil
}

func scoreRouteCandidate(tool schemaTool, queryTokens map[string]bool, queryLower string) routeCandidate {
	fn := tool.Function
	c := routeCandidate{tool: tool}
	nameLower := strings.ToLower(fn.Name)
	nameWords := strings.ReplaceAll(nameLower, "_", " ")
	if strings.Contains(queryLower, nameLower) || strings.Contains(queryLower, nameWords) {
		c.score += 10
		c.reasonParts = append(c.reasonParts, "tool name match")
	}

	descTokens := tokenSet(fn.Description)
	paramTokens, enumTokens := parameterTokens(fn.Parameters)
	featureTokens := tokenSet(strings.Join(toolFeatures[fn.Name], " "))
	nameTokens := tokenSet(fn.Name)

	c.score += weightedOverlap(queryTokens, nameTokens, 5, &c.reasonParts, "name tokens")
	c.score += weightedOverlap(queryTokens, descTokens, 2, &c.reasonParts, "description")
	c.score += weightedOverlap(queryTokens, paramTokens, 3, &c.reasonParts, "parameters")
	c.score += weightedOverlap(queryTokens, enumTokens, 4, &c.reasonParts, "enum values")
	c.score += weightedOverlap(queryTokens, featureTokens, 3, &c.reasonParts, "features")
	c.score += intentBoost(fn.Name, queryTokens, queryLower, &c.reasonParts)
	return c
}

func routeReason(parts []string) string {
	if len(parts) == 0 {
		return "Matched related tool metadata."
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return "Matched " + strings.Join(out, ", ") + "."
}

func routeRisk(name string) string {
	if name == "run_shell" {
		return "shell"
	}
	if routeMutatingTools[name] {
		return "mutating"
	}
	return "read_only"
}

func routeNotes(matches []RouteMatch) []string {
	if len(matches) == 0 {
		return []string{"No confident route found. Try a more concrete task, object, or operation name."}
	}
	notes := []string{"Recommendation only: jinn_route does not execute tools."}
	for _, m := range matches {
		if m.Risk == "shell" {
			notes = append(notes, "run_shell is classified as shell risk; inspect command risk/classification and prefer dry_run for uncertain commands.")
			break
		}
	}
	for _, m := range matches {
		if m.Mutating && m.Risk != "shell" {
			notes = append(notes, "Mutating recommendations can change files or persistent state; use dry_run where supported.")
			break
		}
	}
	return notes
}

func leanSchemaForTool(tool schemaTool) any {
	node := map[string]any{
		"type": tool.Type,
		"function": map[string]any{
			"name":        tool.Function.Name,
			"description": tool.Function.Description,
			"parameters":  cloneJSONValue(tool.Function.Parameters),
		},
	}
	stripParameterDescriptions(node)
	return node
}

func cloneJSONValue(v any) any {
	data, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(data, &out)
	return out
}

func weightedOverlap(query, target map[string]bool, weight int, reasons *[]string, label string) int {
	score := 0
	for tok := range query {
		if target[tok] {
			score += weight
		}
	}
	if score > 0 {
		*reasons = append(*reasons, label)
	}
	return score
}

func parameterTokens(params map[string]any) (map[string]bool, map[string]bool) {
	param := map[string]bool{}
	enum := map[string]bool{}
	var walk func(any, bool)
	walk = func(v any, inEnum bool) {
		switch x := v.(type) {
		case map[string]any:
			for k, child := range x {
				for tok := range tokenSet(k) {
					param[tok] = true
				}
				walk(child, k == "enum")
			}
		case []any:
			for _, child := range x {
				walk(child, inEnum)
			}
		case string:
			for tok := range tokenSet(x) {
				if inEnum {
					enum[tok] = true
				} else {
					param[tok] = true
				}
			}
		}
	}
	walk(params, false)
	return param, enum
}

var tokenRE = regexp.MustCompile(`[a-z0-9]+`)

func tokenSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, tok := range tokenRE.FindAllString(strings.ToLower(strings.ReplaceAll(s, "_", " ")), -1) {
		if len(tok) < 2 {
			continue
		}
		out[singularize(tok)] = true
	}
	return out
}

func singularize(tok string) string {
	if len(tok) > 3 && strings.HasSuffix(tok, "ies") {
		return strings.TrimSuffix(tok, "ies") + "y"
	}
	if len(tok) > 3 && strings.HasSuffix(tok, "s") {
		return strings.TrimSuffix(tok, "s")
	}
	return tok
}

// intentRule boosts a tool when the query signals its task intent.
// anyOf: at least one must match; also (when non-empty): at least one must also match.
// A tool may have multiple rows; the highest matching boost wins (max, not sum).
type intentRule struct {
	tool  string
	anyOf []string
	also  []string
	boost int
}

var intentRules = []intentRule{
	{"read_file", []string{"read", "open", "show", "cat"}, []string{"file", "files"}, 7},
	{"multi_read", []string{"read", "open", "show", "cat"}, []string{"file", "files"}, 7},
	{"search_files", []string{"search", "grep", "find text", "text"}, []string{"repo", "file", "files", "code"}, 8},
	{"find_files", []string{"find", "locate", "glob"}, []string{"file", "filename", "path"}, 7},
	{"apply_patch", []string{"patch", "apply patch"}, nil, 12},
	{"run_shell", []string{"test", "build", "command", "shell", "run", "exec"}, nil, 8},
	{"lsp_query", []string{"rename", "symbol", "definition", "reference", "diagnostic", "hover"}, nil, 10},
	{"search_replace", []string{"replace", "regex", "rename"}, []string{"across", "bulk", "many", "repo", "files"}, 8},
	{"list_dir", []string{"list", "directory", "dir", "folder"}, nil, 8},
	{"stat_file", []string{"stat", "metadata", "size", "encoding"}, nil, 8},
}

func intentBoost(name string, query map[string]bool, lower string, reasons *[]string) int {
	has := func(words ...string) bool {
		for _, w := range words {
			if query[singularize(w)] || strings.Contains(lower, w) {
				return true
			}
		}
		return false
	}
	best := 0
	for _, r := range intentRules {
		if r.tool != name || !has(r.anyOf...) {
			continue
		}
		if len(r.also) > 0 && !has(r.also...) {
			continue
		}
		if r.boost > best {
			best = r.boost
		}
	}
	if best > 0 {
		*reasons = append(*reasons, "task intent")
	}
	return best
}
