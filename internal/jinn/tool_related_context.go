package jinn

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	relatedContextIndexVersion = 3
	relatedContextDefaultLimit = 5
	relatedContextMaxLimit     = 20
	relatedContextClientNone   = "none"
)

type relatedContextIndex struct {
	Version      int                   `json:"version"`
	BuildTime    time.Time             `json:"build_time"`
	SourceMTime  time.Time             `json:"source_mtime"`
	FileCount    int                   `json:"file_count"`
	SourceCount  int                   `json:"source_count"`
	SourceClient string                `json:"source_client,omitempty"`
	SourcePaths  []string              `json:"source_paths,omitempty"`
	Entries      []relatedContextEntry `json:"entries"`
	Warnings     []string              `json:"warnings,omitempty"`
}

type relatedContextEntry struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	Path        string         `json:"path"`
	Source      string         `json:"source,omitempty"`
	Category    string         `json:"category,omitempty"`
	Keywords    map[string]int `json:"keywords"`
}

type relatedContextResult struct {
	Type        string   `json:"type"`
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Path        string   `json:"path"`
	Score       int      `json:"score"`
	Matched     []string `json:"matched"`
	Source      string   `json:"source,omitempty"`
}

type relatedContextResponse struct {
	Results  []relatedContextResult `json:"results"`
	Index    relatedContextMeta     `json:"index"`
	Warnings []string               `json:"warnings,omitempty"`
}

type relatedContextMeta struct {
	EntryCount  int       `json:"entry_count"`
	Rebuilt     bool      `json:"rebuilt"`
	BuildTime   time.Time `json:"build_time"`
	SourceCount int       `json:"source_count"`
	Client      string    `json:"client,omitempty"`
}

type relatedFrontmatter struct {
	Name        string
	Description string
	Tags        []string
	Aliases     []string
	Source      string
	SourceFile  string
	SourceTask  string
}

func (e *Engine) relatedContext(ctx context.Context, args map[string]interface{}) (string, error) {
	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("query is required"),
			Suggestion: `pass the user prompt or failed tool output as "query"`,
			Code:       ErrCodeInvalidArgs,
		}
	}

	allowedTypes, err := relatedContextTypes(args["types"])
	if err != nil {
		return "", err
	}
	limit := intArg(args, "limit", relatedContextDefaultLimit)
	if limit > relatedContextMaxLimit {
		limit = relatedContextMaxLimit
	}
	rebuild, _ := args["rebuild"].(bool)
	client, clientWarnings, err := relatedContextClient(args["client"])
	if err != nil {
		return "", err
	}

	idx, rebuilt, err := e.loadRelatedContextIndex(ctx, rebuild, client, clientWarnings)
	if err != nil {
		return "", err
	}

	tokens := relatedContextTokenize(query)
	results := scoreRelatedContext(idx.Entries, tokens, allowedTypes, limit)
	resp := relatedContextResponse{
		Results: results,
		Index: relatedContextMeta{
			EntryCount:  len(idx.Entries),
			Rebuilt:     rebuilt,
			BuildTime:   idx.BuildTime,
			SourceCount: idx.SourceCount,
			Client:      idx.SourceClient,
		},
		Warnings: idx.Warnings,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return "", fmt.Errorf("related_context: marshal response: %w", err)
	}
	return string(data), nil
}

func relatedContextTypes(raw any) (map[string]bool, error) {
	if raw == nil {
		return nil, nil
	}
	valid := map[string]bool{"kb": true, "skill": true, "agent": true, "command": true}
	out := make(map[string]bool)
	items, ok := raw.([]interface{})
	if !ok {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("types must be an array"),
			Suggestion: `use types:["kb","skill"] or omit types to search all context`,
			Code:       ErrCodeInvalidArgs,
		}
	}
	for _, item := range items {
		t, _ := item.(string)
		if !valid[t] {
			return nil, &ErrWithSuggestion{
				Err:        fmt.Errorf("invalid related_context type: %q", t),
				Suggestion: `allowed types are "kb", "skill", "agent", and "command"`,
				Code:       ErrCodeInvalidArgs,
			}
		}
		out[t] = true
	}
	return out, nil
}

func relatedContextClient(raw any) (string, []string, error) {
	if raw != nil {
		client, _ := raw.(string)
		return validateRelatedContextClient(client)
	}
	return relatedContextClientNone, []string{"client not provided; pass top-level client=claude|codex|pi to include client-specific skills"}, nil
}

func validateRelatedContextClient(client string) (string, []string, error) {
	client = strings.ToLower(strings.TrimSpace(client))
	if client == "" {
		client = relatedContextClientNone
	}
	switch client {
	case relatedContextClientNone:
		return client, []string{"client not provided; pass top-level client=claude|codex|pi to include client-specific skills"}, nil
	case "claude", "codex", "pi", "all":
		return client, nil, nil
	default:
		return "", nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("invalid related_context client: %q", client),
			Suggestion: `use top-level client "claude", "codex", or "pi"; use "all" only for cross-client audits`,
			Code:       ErrCodeInvalidArgs,
		}
	}
}

func (e *Engine) loadRelatedContextIndex(ctx context.Context, force bool, client string, warnings []string) (relatedContextIndex, bool, error) {
	sources, sourceWarnings := relatedContextSourceDirs(client)
	warnings = append(warnings, sourceWarnings...)
	if len(sources) == 0 {
		return relatedContextIndex{
			Version:      relatedContextIndexVersion,
			BuildTime:    time.Now().UTC(),
			SourceClient: client,
			Warnings:     append(warnings, "no related context source directories found"),
		}, false, nil
	}

	cached, loadErr := readRelatedContextIndex(client)
	if !force && loadErr == nil && !relatedContextNeedsRebuild(cached, client, sources) {
		return cached, false, nil
	}

	idx, err := buildRelatedContextIndex(ctx, sources, client, warnings)
	if err != nil {
		return relatedContextIndex{}, false, err
	}
	if err := writeRelatedContextIndex(client, idx); err != nil {
		idx.Warnings = append(idx.Warnings, "index cache write failed: "+err.Error())
	}
	return idx, true, nil
}

func relatedContextIndexPath(client string) (string, error) {
	base := os.Getenv("JINN_CONFIG_DIR")
	if base == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("related_context: resolve config dir: %w", err)
		}
		base = dir
	}
	client = relatedContextCacheClient(client)
	return filepath.Join(base, "jinn", "context-index-"+client+".json"), nil
}

func relatedContextCacheClient(client string) string {
	switch client {
	case relatedContextClientNone, "claude", "codex", "pi", "all":
		return client
	default:
		return relatedContextClientNone
	}
}

func readRelatedContextIndex(client string) (relatedContextIndex, error) {
	path, err := relatedContextIndexPath(client)
	if err != nil {
		return relatedContextIndex{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return relatedContextIndex{}, err
	}
	var idx relatedContextIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return relatedContextIndex{}, fmt.Errorf("related_context: unmarshal index: %w", err)
	}
	return idx, nil
}

func writeRelatedContextIndex(client string, idx relatedContextIndex) error {
	path, err := relatedContextIndexPath(client)
	if err != nil {
		return err
	}
	return atomicWriteJSON(path, idx, 0o600)
}

func relatedContextNeedsRebuild(idx relatedContextIndex, client string, sources []string) bool {
	if idx.Version != relatedContextIndexVersion {
		return true
	}
	fileCount, maxMTime, err := relatedContextSourceStats(sources)
	if err != nil {
		return true
	}
	return fileCount != idx.FileCount ||
		!maxMTime.Equal(idx.SourceMTime) ||
		len(sources) != idx.SourceCount ||
		client != idx.SourceClient ||
		!slices.Equal(sources, idx.SourcePaths)
}

func buildRelatedContextIndex(ctx context.Context, sources []string, client string, warnings []string) (relatedContextIndex, error) {
	idx := relatedContextIndex{
		Version:      relatedContextIndexVersion,
		BuildTime:    time.Now().UTC(),
		SourceCount:  len(sources),
		SourceClient: client,
		SourcePaths:  append([]string(nil), sources...),
		Warnings:     warnings,
	}
	for _, source := range sources {
		sourceType := relatedContextSourceType(source)
		err := filepath.WalkDir(source, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				idx.Warnings = append(idx.Warnings, fmt.Sprintf("skip %s: %v", path, err))
				return nil
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if d.IsDir() || filepath.Ext(path) != ".md" {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				idx.Warnings = append(idx.Warnings, fmt.Sprintf("stat %s: %v", path, err))
				return nil
			}
			idx.FileCount++
			if info.ModTime().After(idx.SourceMTime) {
				idx.SourceMTime = info.ModTime()
			}
			entry, ok := parseRelatedContextEntry(path, source, sourceType)
			if ok {
				idx.Entries = append(idx.Entries, entry)
			}
			return nil
		})
		if err != nil {
			return relatedContextIndex{}, fmt.Errorf("related_context: walk %s: %w", source, err)
		}
	}
	sort.Slice(idx.Entries, func(i, j int) bool {
		if idx.Entries[i].Type != idx.Entries[j].Type {
			return idx.Entries[i].Type < idx.Entries[j].Type
		}
		if idx.Entries[i].Name != idx.Entries[j].Name {
			return idx.Entries[i].Name < idx.Entries[j].Name
		}
		return idx.Entries[i].Path < idx.Entries[j].Path
	})
	return idx, nil
}

func relatedContextSourceStats(sources []string) (int, time.Time, error) {
	var count int
	var maxMTime time.Time
	for _, source := range sources {
		err := filepath.WalkDir(source, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || filepath.Ext(path) != ".md" {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			count++
			if info.ModTime().After(maxMTime) {
				maxMTime = info.ModTime()
			}
			return nil
		})
		if err != nil {
			return 0, time.Time{}, err
		}
	}
	return count, maxMTime, nil
}

func relatedContextSourceDirs(client string) ([]string, []string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, []string{"home directory unavailable: " + err.Error()}
	}
	candidates := []string{
		filepath.Join(home, ".claude", "kb"),
	}
	switch client {
	case "claude":
		candidates = append(candidates, relatedContextClaudeDirs(home)...)
	case "codex":
		candidates = append(candidates, filepath.Join(home, ".codex", "skills"))
	case "pi":
		candidates = append(candidates, relatedContextPiSourceDirs(home)...)
	case "all":
		candidates = append(candidates, relatedContextClaudeDirs(home)...)
		candidates = append(candidates, filepath.Join(home, ".codex", "skills"))
		candidates = append(candidates, relatedContextPiSourceDirs(home)...)
	}
	configDirs, configWarnings := relatedContextConfigDirs()
	candidates = append(candidates, configDirs...)

	seen := make(map[string]bool, len(candidates))
	out := make([]string, 0, len(candidates))
	for _, dir := range candidates {
		real, err := filepath.EvalSymlinks(dir)
		if err == nil {
			dir = real
		}
		if dir == "" || seen[dir] {
			continue
		}
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		seen[dir] = true
		out = append(out, dir)
	}
	return out, configWarnings
}

func relatedContextPiSourceDirs(home string) []string {
	dirs := []string{
		filepath.Join(home, ".pi", "agent", "skills"),
		filepath.Join(home, ".pi", "docs"),
		filepath.Join(home, ".pi", "agent", "kb"),
	}
	dirs = append(dirs, relatedContextPiSkillPackageDirs(home)...)
	return dirs
}

func relatedContextClaudeDirs(home string) []string {
	dirs := []string{
		filepath.Join(home, ".claude", "skills"),
		filepath.Join(home, ".claude", "agents"),
		filepath.Join(home, ".claude", "commands"),
	}
	return append(dirs, relatedContextPluginDirs(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"))...)
}

func relatedContextConfigDirs() ([]string, []string) {
	cfg, configPath, err := loadConfigFile()
	if err != nil {
		return nil, []string{"config read failed: " + err.Error()}
	}
	var dirs []string
	var warnings []string
	for _, raw := range cfg.RelatedContext.Paths {
		dir, err := expandConfigPath(raw, configPath)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skip configured related_context path %q: %v", raw, err))
			continue
		}
		real, err := filepath.EvalSymlinks(dir)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skip configured related_context path %q: %v", raw, err))
			continue
		}
		if hasSensitivePathSegment(real) {
			warnings = append(warnings, fmt.Sprintf("skip configured related_context path %q: sensitive path", raw))
			continue
		}
		info, err := os.Stat(real)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skip configured related_context path %q: %v", raw, err))
			continue
		}
		if !info.IsDir() {
			warnings = append(warnings, fmt.Sprintf("skip configured related_context path %q: not a directory", raw))
			continue
		}
		dirs = append(dirs, real)
	}
	return dirs, warnings
}

func relatedContextPluginDirs(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var installed struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(data, &installed); err != nil {
		return nil
	}
	var dirs []string
	for _, installs := range installed.Plugins {
		for _, inst := range installs {
			if inst.InstallPath == "" {
				continue
			}
			dirs = append(dirs, relatedContextPluginComponentDirs(inst.InstallPath)...)
		}
	}
	return dirs
}

func relatedContextPluginComponentDirs(installPath string) []string {
	manifestPath := filepath.Join(installPath, ".claude-plugin", "plugin.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return []string{
			filepath.Join(installPath, "skills"),
			filepath.Join(installPath, "agents"),
			filepath.Join(installPath, "commands"),
		}
	}
	var manifest struct {
		Skills   json.RawMessage `json:"skills"`
		Commands json.RawMessage `json:"commands"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return []string{
			filepath.Join(installPath, "skills"),
			filepath.Join(installPath, "agents"),
			filepath.Join(installPath, "commands"),
		}
	}
	var dirs []string
	dirs = append(dirs, relatedContextManifestPaths(installPath, manifest.Skills)...)
	dirs = append(dirs, filepath.Join(installPath, "agents"))
	dirs = append(dirs, relatedContextManifestPaths(installPath, manifest.Commands)...)
	return dirs
}

func relatedContextPiSkillPackageDirs(home string) []string {
	settingsPath := filepath.Join(home, ".pi", "agent", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return nil
	}

	var payload struct {
		Packages []json.RawMessage `json:"packages"`
	}
	if err := json.Unmarshal(data, &payload); err != nil || len(payload.Packages) == 0 {
		return nil
	}

	var dirs []string
	for _, raw := range payload.Packages {
		var pkgSource string
		if err := json.Unmarshal(raw, &pkgSource); err == nil {
			pkgSource = strings.TrimSpace(pkgSource)
		} else {
			var obj struct {
				Source string `json:"source"`
			}
			if err := json.Unmarshal(raw, &obj); err != nil {
				continue
			}
			pkgSource = strings.TrimSpace(obj.Source)
		}
		root := relatedContextResolvePiPackageRoot(home, pkgSource)
		if root == "" {
			continue
		}
		skillsRoot := filepath.Join(root, "skills")
		dirs = append(dirs, skillsRoot)
	}
	return dirs
}

func relatedContextResolvePiPackageRoot(home, source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return ""
	}

	if strings.HasPrefix(source, "npm:") || strings.HasPrefix(source, "http:") || strings.HasPrefix(source, "https:") || strings.HasPrefix(source, "ssh:") {
		return ""
	}

	if strings.HasPrefix(source, "git:") || strings.HasPrefix(source, "github:") {
		rest := strings.TrimPrefix(strings.TrimPrefix(source, "git:"), "github:")
		if strings.HasPrefix(rest, "git@") {
			parts := strings.SplitN(strings.TrimPrefix(rest, "git@"), ":", 2)
			if len(parts) == 2 {
				rest = filepath.Join(parts[0], parts[1])
			}
		}
		if at := strings.LastIndex(rest, "@"); at >= 0 {
			rest = rest[:at]
		}
		return filepath.Join(home, ".pi", "agent", "git", filepath.FromSlash(rest))
	}

	target := source
	if target == "~" {
		target = home
	} else if strings.HasPrefix(target, "~"+string(filepath.Separator)) {
		target = filepath.Join(home, strings.TrimPrefix(target, "~/"))
	}
	if !filepath.IsAbs(target) {
		target, _ = filepath.Abs(target)
	}
	return target
}

func relatedContextManifestPaths(installPath string, raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var paths []string
	if err := json.Unmarshal(raw, &paths); err != nil {
		var single string
		if err := json.Unmarshal(raw, &single); err != nil {
			return nil
		}
		paths = []string{single}
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimPrefix(strings.TrimSuffix(p, "/"), "./")
		out = append(out, filepath.Join(installPath, p))
	}
	return out
}

func relatedContextSourceType(source string) string {
	base := filepath.Base(source)
	switch {
	case base == "skills":
		return "skill"
	case base == "agents":
		return "agent"
	case base == "commands" || strings.HasSuffix(base, "-commands"):
		return "command"
	default:
		return "kb"
	}
}

func parseRelatedContextEntry(path, source, sourceType string) (relatedContextEntry, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return relatedContextEntry{}, false
	}
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	fm := parseRelatedFrontmatter(data)
	body := stripRelatedFrontmatter(data)

	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if fm.Name != "" {
		name = fm.Name
	}
	title := firstMarkdownTitle(body)
	if title == "" {
		title = firstNonBlankLine(body)
	}
	if title == "" {
		title = name
	}
	description := fm.Description
	if description == "" {
		description = firstParagraph(body)
	}

	entry := relatedContextEntry{
		Type:        sourceType,
		Name:        name,
		Title:       title,
		Description: truncateRunes(description, 240),
		Path:        path,
		Source:      firstNonEmpty(fm.SourceFile, fm.SourceTask, fm.Source),
		Category:    relatedContextCategory(path, source, sourceType),
		Keywords:    make(map[string]int),
	}
	addWeightedTokens(entry.Keywords, entry.Name, 3)
	addWeightedTokens(entry.Keywords, entry.Title, 2)
	addWeightedTokens(entry.Keywords, entry.Category, 2)
	addWeightedTokens(entry.Keywords, entry.Description, 1)
	for _, tag := range fm.Tags {
		addWeightedTokens(entry.Keywords, tag, 2)
	}
	for _, alias := range fm.Aliases {
		addWeightedTokens(entry.Keywords, alias, 1)
	}
	addWeightedTokens(entry.Keywords, truncateRunes(string(body), 600), 1)
	return entry, true
}

func relatedContextCategory(path, source, sourceType string) string {
	if sourceType != "kb" {
		return sourceType
	}
	rel, err := filepath.Rel(source, path)
	if err != nil {
		return "kb"
	}
	dir := filepath.Dir(rel)
	if dir == "." {
		return "kb"
	}
	return filepath.Base(dir)
}

func parseRelatedFrontmatter(data []byte) relatedFrontmatter {
	block, ok := relatedFrontmatterBlock(data)
	if !ok {
		return relatedFrontmatter{}
	}
	lines := strings.Split(string(block), "\n")
	var fm relatedFrontmatter
	var currentList string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") && currentList != "" {
			value := trimYAMLScalar(strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
			switch currentList {
			case "tags":
				fm.Tags = append(fm.Tags, value)
			case "aliases":
				fm.Aliases = append(fm.Aliases, value)
			}
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			currentList = ""
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		currentList = ""
		switch key {
		case "name":
			fm.Name = trimYAMLScalar(value)
		case "description":
			fm.Description = trimYAMLScalar(value)
		case "source":
			fm.Source = trimYAMLScalar(value)
		case "source_file":
			fm.SourceFile = trimYAMLScalar(value)
		case "source_task":
			fm.SourceTask = trimYAMLScalar(value)
		case "tags", "aliases":
			values := parseYAMLListValue(value)
			if len(values) == 0 && value == "" {
				currentList = key
				continue
			}
			if key == "tags" {
				fm.Tags = append(fm.Tags, values...)
			} else {
				fm.Aliases = append(fm.Aliases, values...)
			}
		}
	}
	return fm
}

func relatedFrontmatterBlock(data []byte) ([]byte, bool) {
	if !bytes.HasPrefix(data, []byte("---\n")) {
		return nil, false
	}
	rest := data[len("---\n"):]
	end := bytes.Index(rest, []byte("---\n"))
	if end < 0 {
		return nil, false
	}
	return rest[:end], true
}

func stripRelatedFrontmatter(data []byte) []byte {
	_, ok := relatedFrontmatterBlock(data)
	if !ok {
		return data
	}
	rest := data[len("---\n"):]
	end := bytes.Index(rest, []byte("---\n"))
	return rest[end+len("---\n"):]
}

func parseYAMLListValue(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, "["), "]"))
		if inner == "" {
			return nil
		}
		parts := strings.Split(inner, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			out = append(out, trimYAMLScalar(strings.TrimSpace(part)))
		}
		return out
	}
	return []string{trimYAMLScalar(value)}
}

func trimYAMLScalar(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			value = value[1 : len(value)-1]
		}
	}
	value = strings.ReplaceAll(value, `\"`, `"`)
	return value
}

func scoreRelatedContext(entries []relatedContextEntry, tokens []string, allowedTypes map[string]bool, limit int) []relatedContextResult {
	if len(tokens) == 0 {
		return nil
	}
	results := make([]relatedContextResult, 0)
	for _, entry := range entries {
		if len(allowedTypes) > 0 && !allowedTypes[entry.Type] {
			continue
		}
		score, matched := scoreRelatedEntry(entry, tokens)
		if score <= 0 {
			continue
		}
		results = append(results, relatedContextResult{
			Type:        entry.Type,
			Name:        entry.Name,
			Title:       entry.Title,
			Description: entry.Description,
			Path:        entry.Path,
			Score:       score,
			Matched:     matched,
			Source:      entry.Source,
		})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		if results[i].Type != results[j].Type {
			return results[i].Type < results[j].Type
		}
		if results[i].Name != results[j].Name {
			return results[i].Name < results[j].Name
		}
		return results[i].Path < results[j].Path
	})
	return limitRelatedContextResults(results, allowedTypes, limit)
}

func limitRelatedContextResults(results []relatedContextResult, allowedTypes map[string]bool, limit int) []relatedContextResult {
	if limit <= 0 || len(results) <= limit {
		return results
	}
	if len(allowedTypes) <= 1 {
		return results[:limit]
	}

	counts := make(map[string]int, len(allowedTypes))
	out := make([]relatedContextResult, 0, min(len(results), limit*len(allowedTypes)))
	for _, result := range results {
		if counts[result.Type] >= limit {
			continue
		}
		counts[result.Type]++
		out = append(out, result)
	}
	return out
}

func scoreRelatedEntry(entry relatedContextEntry, tokens []string) (int, []string) {
	score := 0
	seen := make(map[string]bool)
	for _, tok := range tokens {
		delta := 0
		if weight, ok := entry.Keywords[tok]; ok {
			delta += weight
		} else if weight, ok := entry.Keywords[pluralVariant(tok)]; ok {
			delta += max(1, weight-1)
		}
		if aliasCategory(tok) == entry.Category {
			delta += 2
		}
		if delta > 0 {
			score += delta
			seen[tok] = true
		}
	}
	matched := make([]string, 0, len(seen))
	for tok := range seen {
		matched = append(matched, tok)
	}
	sort.Strings(matched)
	return int(math.Round(float64(score))), matched
}

func aliasCategory(tok string) string {
	switch tok {
	case "golang", "goroutine", "goroutines":
		return "go"
	case "shell", "zsh":
		return "bash"
	case "postgresql", "pgx":
		return "postgres"
	case "llm", "openai", "anthropic", "gemini":
		return "llm"
	}
	return ""
}

func pluralVariant(tok string) string {
	if strings.HasSuffix(tok, "s") && len(tok) > 1 {
		return strings.TrimSuffix(tok, "s")
	}
	return tok + "s"
}

func addWeightedTokens(dst map[string]int, text string, weight int) {
	for _, tok := range relatedContextTokenize(text) {
		if weight > dst[tok] {
			dst[tok] = weight
		}
	}
}

func relatedContextTokenize(text string) []string {
	stop := map[string]bool{
		"a": true, "an": true, "and": true, "are": true, "as": true, "at": true, "be": true,
		"for": true, "from": true, "how": true, "i": true, "in": true, "is": true, "it": true,
		"of": true, "on": true, "or": true, "the": true, "this": true, "to": true, "with": true,
	}
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-')
	})
	seen := make(map[string]bool, len(fields))
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.Trim(field, "-")
		if field == "" || stop[field] || seen[field] {
			continue
		}
		seen[field] = true
		out = append(out, field)
	}
	return out
}

func firstMarkdownTitle(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

func firstNonBlankLine(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			return s
		}
	}
	return ""
}

func firstParagraph(data []byte) string {
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if len(lines) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		lines = append(lines, trimmed)
		if len(lines) == 3 {
			break
		}
	}
	return strings.Join(lines, " ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func truncateRunes(s string, limit int) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit])
}
