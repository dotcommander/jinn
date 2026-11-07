package webfetch

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

type gitTreeResponse struct {
	Tree []struct {
		Path string `json:"path"`
		Type string `json:"type"`
		Size int    `json:"size"`
	} `json:"tree"`
	Truncated bool `json:"truncated"`
}

func (g *githubInterceptor) getTreeJSON(ctx context.Context, apiURL string) (gitTreeResponse, error) {
	body, err := g.getJSONBody(ctx, apiURL)
	if err != nil {
		return gitTreeResponse{}, err
	}
	var tree gitTreeResponse
	if err := decodeJSON(body, &tree, "github tree"); err != nil {
		return gitTreeResponse{}, fmt.Errorf("github tree decode: %w", err)
	}
	return tree, nil
}

func (g *githubInterceptor) getJSONBody(ctx context.Context, apiURL string) ([]byte, error) {
	headers := make(http.Header)
	headers.Set("Accept", "application/vnd.github+json")
	headers.Set("X-GitHub-Api-Version", "2022-11-28")
	if g.token != "" {
		headers.Set(headerAuthorization, "Bearer "+g.token)
	}
	resp, err := g.client.Get(ctx, apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("github fetch: %w", err)
	}
	return resp.Body, nil
}

var binaryExtensions = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".ico": true,
	".webp": true, ".svg.gz": true, ".zip": true, ".tar": true, ".gz": true,
	".mp3": true, ".mp4": true, ".exe": true, ".bin": true,
}

// renderGitHubTree renders the recursive tree capped at
// maxGitHubTreeEntries. A non-empty prefix scopes the listing to entries
// under that repository path (tree URLs).
func renderGitHubTree(tree gitTreeResponse, prefix string) (string, int) {
	entries := tree.Tree
	if prefix != "" {
		prefix = strings.Trim(prefix, "/") + "/"
		scoped := entries[:0]
		for _, entry := range entries {
			if strings.HasPrefix(entry.Path, prefix) {
				scoped = append(scoped, entry)
			}
		}
		entries = scoped
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	if len(entries) > maxGitHubTreeEntries {
		entries = entries[:maxGitHubTreeEntries]
	}
	var builder strings.Builder
	if prefix != "" {
		fmt.Fprintf(&builder, "# Directory %s (%d tracked files)\n\n", strings.TrimSuffix(prefix, "/"), len(entries))
	} else {
		fmt.Fprintf(&builder, "# Repository (%d tracked files)\n\n", len(tree.Tree))
	}
	inline := 0
	for _, entry := range entries {
		fmt.Fprintf(&builder, "- %s %s", entry.Type, entry.Path)
		if entry.Size > 0 {
			fmt.Fprintf(&builder, " (%d bytes)", entry.Size)
		}
		builder.WriteString("\n")
		if entry.Type == string(githubURLBlob) && !isBinaryPath(entry.Path) {
			inline++
		}
	}
	if len(tree.Tree) > maxGitHubTreeEntries {
		fmt.Fprintf(&builder, "(tree truncated at %d entries)\n", maxGitHubTreeEntries)
	}
	return builder.String(), inline
}

func isBinaryPath(path string) bool {
	lower := strings.ToLower(path)
	for ext := range binaryExtensions {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// githubAPIEndpoint resolves the GitHub API base: explicit config value >
// descriptor default (the environment is covered via config wiring).
