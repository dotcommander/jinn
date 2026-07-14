package jinn

import "testing"

func TestRouteToolsCoreCases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		need string
		want string
		risk string
	}{
		{"read file", "read a file", "read_file", "read_only"},
		{"search text", "search text in repo", "search_files", "read_only"},
		{"apply patch", "apply patch", "apply_patch", "mutating"},
		{"run tests", "run tests", "run_shell", "shell"},
		{"rename symbol", "rename symbol", "lsp_query", "read_only"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp, err := RouteTools(RouteRequest{Need: tt.need, MaxTools: 3, IncludeMutating: boolPtr(true)})
			if err != nil {
				t.Fatalf("RouteTools: %v", err)
			}
			if len(resp.Matches) == 0 {
				t.Fatalf("no matches for %q", tt.need)
			}
			if resp.Matches[0].Name != tt.want {
				t.Fatalf("top match = %s, want %s; matches=%v", resp.Matches[0].Name, tt.want, resp.Matches)
			}
			if resp.Matches[0].Risk != tt.risk {
				t.Fatalf("risk = %s, want %s", resp.Matches[0].Risk, tt.risk)
			}
		})
	}
}

func TestRouteToolsLowSignal(t *testing.T) {
	t.Parallel()
	resp, err := RouteTools(RouteRequest{Need: "please help", IncludeMutating: boolPtr(true)})
	if err != nil {
		t.Fatalf("RouteTools: %v", err)
	}
	if len(resp.Matches) != 0 {
		t.Fatalf("matches = %v, want none", resp.Matches)
	}
	if len(resp.Notes) == 0 {
		t.Fatal("expected explanatory note")
	}
}

func TestRouteToolsIncludeMutatingFalse(t *testing.T) {
	t.Parallel()
	req := RouteRequest{Need: "apply patch", IncludeMutating: boolPtr(false)}
	resp, err := RouteTools(req)
	if err != nil {
		t.Fatalf("RouteTools: %v", err)
	}
	for _, m := range resp.Matches {
		if m.Mutating {
			t.Fatalf("mutating match returned with include_mutating=false: %+v", m)
		}
	}
}

func TestRouteToolsRunPlanClassification(t *testing.T) {
	t.Parallel()
	resp, err := RouteTools(RouteRequest{Need: "run plan", MaxTools: RouteMaxTools, IncludeMutating: boolPtr(true)})
	if err != nil {
		t.Fatalf("RouteTools: %v", err)
	}
	match, ok := routeMatchNamed(resp.Matches, "run_plan")
	if !ok {
		t.Fatalf("run_plan missing from matches: %+v", resp.Matches)
	}
	if !match.Mutating || match.Risk != "mutating" {
		t.Fatalf("run_plan classification = mutating:%v risk:%q", match.Mutating, match.Risk)
	}
}

func TestRouteToolsRunPlanExcludedWhenMutatingDisabled(t *testing.T) {
	t.Parallel()
	resp, err := RouteTools(RouteRequest{Need: "run plan", MaxTools: RouteMaxTools, IncludeMutating: boolPtr(false)})
	if err != nil {
		t.Fatalf("RouteTools: %v", err)
	}
	if _, ok := routeMatchNamed(resp.Matches, "run_plan"); ok {
		t.Fatalf("run_plan returned with include_mutating=false: %+v", resp.Matches)
	}
}

func routeMatchNamed(matches []RouteMatch, name string) (RouteMatch, bool) {
	for _, match := range matches {
		if match.Name == name {
			return match, true
		}
	}
	return RouteMatch{}, false
}

func TestRouteToolsIncludeSchemaOnlyMatches(t *testing.T) {
	t.Parallel()
	resp, err := RouteTools(RouteRequest{Need: "read a file", MaxTools: 2, IncludeSchema: true, IncludeMutating: boolPtr(true)})
	if err != nil {
		t.Fatalf("RouteTools: %v", err)
	}
	if len(resp.Matches) == 0 {
		t.Fatal("expected matches")
	}
	for _, m := range resp.Matches {
		if m.Schema == nil {
			t.Fatalf("match %s missing schema", m.Name)
		}
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func TestRouteToolsCorpus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		need       string
		wantAny    []string // acceptable top matches; nil (with noMutating false) = expect zero matches + note
		noMutating bool     // include_mutating=false; assert no mutating matches only
	}{
		// Direct phrasings — one per tool.
		{"direct read_file", "read file main.go", []string{"read_file"}, false},
		{"direct multi_read", "multi read several files", []string{"multi_read"}, false},
		{"direct write_file", "write file with this content", []string{"write_file"}, false},
		{"direct edit_file", "edit file replacing exact text", []string{"edit_file"}, false},
		{"direct multi_edit", "multi edit across files", []string{"multi_edit"}, false},
		{"direct apply_patch", "apply patch to the repo", []string{"apply_patch"}, false},
		{"direct search_files", "search files for a regex pattern", []string{"search_files"}, false},
		{"direct find_files", "find files by glob pattern", []string{"find_files"}, false},
		{"direct search_replace", "search replace across the repo", []string{"search_replace"}, false},
		{"direct stat_file", "stat file metadata", []string{"stat_file"}, false},
		{"direct list_dir", "list dir contents", []string{"list_dir"}, false},
		{"direct diff_files", "diff files and show changes", []string{"diff_files"}, false},
		{"direct run_shell", "run shell command", []string{"run_shell"}, false},
		{"direct lsp_query", "lsp query for symbol references", []string{"lsp_query"}, false},
		{"direct memory", "memory store a key value pair", []string{"memory"}, false},
		{"direct undo", "undo the last file change", []string{"undo"}, false},
		{"direct detect_project", "detect project language and framework", []string{"detect_project"}, false},
		{"direct list_tools", "list tools and capabilities", []string{"list_tools"}, false},

		// Paraphrases — no tool-name words.
		{"paraphrase read_file", "show me the contents of that source file", []string{"read_file"}, false},
		{"paraphrase multi_read", "open these three files at once", []string{"multi_read", "read_file"}, false},
		{"paraphrase write_file", "save this content to a new file", []string{"write_file"}, false},
		{"paraphrase edit_file", "fix a typo on one line of this file", []string{"edit_file"}, false},
		{"paraphrase multi_edit", "change the same thing in many files at once", []string{"multi_edit", "edit_file"}, false},
		{"paraphrase diff_files", "compare these two files and show the difference", []string{"diff_files"}, false},
		{"paraphrase stat_file", "get the size and encoding of a file without reading it", []string{"stat_file"}, false},
		{"paraphrase list_dir", "what's inside this folder", []string{"list_dir"}, false},
		{"paraphrase find_files", "locate a file by its name pattern", []string{"find_files"}, false},
		{"paraphrase search_files", "look for this text in the codebase", []string{"search_files"}, false},
		{"paraphrase search_replace", "replace every occurrence across many files", []string{"search_replace"}, false},
		{"paraphrase apply_patch", "apply this patch to the working tree", []string{"apply_patch"}, false},
		{"paraphrase run_shell", "execute the build", []string{"run_shell"}, false},
		{"paraphrase lsp_query", "where is this symbol defined", []string{"lsp_query"}, false},
		{"paraphrase memory", "remember this value for the next session", []string{"memory"}, false},
		{"paraphrase undo", "revert my last change", []string{"undo"}, false},
		{"paraphrase detect_project", "what framework and language is this codebase", []string{"detect_project"}, false},
		{"paraphrase list_tools", "what tools are available here", []string{"list_tools"}, false},

		// Confusable pairs.
		{"confusable edit vs replace", "replace one exact string in a single file", []string{"edit_file"}, false},
		{"confusable replace across repo", "regex replace across many files", []string{"search_replace"}, false},
		{"confusable batch edit", "edit several files in one batch", []string{"multi_edit"}, false},
		{"confusable stat vs read", "get file metadata without reading the contents", []string{"stat_file"}, false},
		{"confusable find by name", "find a file by its filename", []string{"find_files"}, false},
		{"confusable search contents", "search file contents for a pattern", []string{"search_files"}, false},
		{"confusable list directory", "list the files in a directory", []string{"list_dir"}, false},
		{"confusable revert patch", "revert the last patch", []string{"undo", "apply_patch"}, false},
		{"confusable rollback edit", "roll back my last edit", []string{"undo"}, false},

		// Low signal — expect zero matches plus an explanatory note.
		{"low signal do the thing", "do the thing", nil, false},
		{"low signal make it better", "make it better", nil, false},
		{"low signal hello", "hello", nil, false},

		// Mutating disabled — no mutating tools may appear.
		{"no mutating typo fix", "fix a typo on one line of this file", nil, true},
		{"no mutating save file", "save this content to a new file", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp, err := RouteTools(RouteRequest{Need: tt.need, MaxTools: 3, IncludeMutating: boolPtr(!tt.noMutating)})
			if err != nil {
				t.Fatalf("RouteTools(%q): %v", tt.need, err)
			}
			if tt.noMutating {
				for _, m := range resp.Matches {
					if m.Mutating {
						t.Fatalf("mutating tool %s returned for %q with include_mutating=false", m.Name, tt.need)
					}
				}
				return
			}
			if tt.wantAny == nil {
				if len(resp.Matches) != 0 {
					t.Fatalf("expected zero matches for %q, got %s", tt.need, resp.Matches[0].Name)
				}
				if len(resp.Notes) == 0 {
					t.Fatalf("expected explanatory note for %q", tt.need)
				}
				return
			}
			if len(resp.Matches) == 0 {
				t.Fatalf("no matches for %q, want one of %v", tt.need, tt.wantAny)
			}
			got := resp.Matches[0].Name
			for _, w := range tt.wantAny {
				if got == w {
					return
				}
			}
			t.Fatalf("top match for %q = %s, want one of %v", tt.need, got, tt.wantAny)
		})
	}
}
