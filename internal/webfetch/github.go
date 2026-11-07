package webfetch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
)

const (
	hostGitHubCom            = "github.com"
	defaultGitHubAPIEndpoint = "https://api.github.com"
	githubTokenEnvVar        = "GITHUB_TOKEN" //nolint:gosec // env var name, not a credential
	maxGitHubTreeEntries     = 200
)

// urlInterceptor is the generic fetch specialist contract: intercept inspects
// a fetch target and either claims it with a complete Document or returns
// ok=false so the next interceptor (and ultimately the generic fetch) owns it.
type urlInterceptor interface {
	intercept(ctx context.Context, target *url.URL) (Document, bool, error)
}

// githubInterceptor claims github.com repository URLs and returns structured
// repository content via the GitHub API, authenticated by GITHUB_TOKEN when
// present. Public repo URLs work without a token; private repos require one.
// Repository roots and tree URLs render a capped file tree scoped to the
// URL's ref and path. Blob URLs are not claimed here: validateTarget already
// rewrites them to raw.githubusercontent.com, which the generic fetch owns.
// Non-code URLs (issues, pull requests, wiki, …) are not claimed either.
type githubInterceptor struct {
	client      *Client
	apiEndpoint string
	token       string
}

func (g *githubInterceptor) intercept(ctx context.Context, target *url.URL) (Document, bool, error) {
	info, ok := parseGitHubURL(target)
	if !ok || info.kind == githubURLBlob {
		// Blob URLs never reach this interceptor through Fetch: validateTarget
		// rewrites them to raw.githubusercontent.com and the generic fetch
		// owns raw file content. Declining here keeps direct intercept calls
		// consistent with that ownership.
		return Document{}, false, nil
	}
	return g.interceptTree(ctx, target, info)
}

func (g *githubInterceptor) interceptTree(ctx context.Context, target *url.URL, info githubURLInfo) (Document, bool, error) {
	candidates := info.refCandidates()
	var lastErr error
	for _, ref := range candidates {
		apiURL := fmt.Sprintf("%s/repos/%s/%s/git/trees/%s?recursive=1",
			strings.TrimRight(g.apiEndpoint, "/"), url.PathEscape(info.owner), url.PathEscape(info.repo), url.PathEscape(ref.ref))
		tree, err := g.getTreeJSON(ctx, apiURL)
		if err != nil {
			if isGitHubNotFound(err) {
				lastErr = err
				continue
			}
			return Document{}, false, err
		}
		content, _ := renderGitHubTree(tree, ref.path)
		title := info.owner + "/" + info.repo
		if ref.path != "" {
			title += " - " + ref.path
		}
		return g.githubDocument(target, title, contentTypeMarkdown, content), true, nil
	}
	if lastErr != nil {
		return Document{}, false, lastErr
	}
	return Document{}, false, nil
}

// refCandidate is one (ref, path) split of the URL segments after the
// action. Branch names may contain slashes, so resolution tries the
// longest ref first and shortens on 404 until the tree endpoint answers.
type refCandidate struct {
	ref  string
	path string
}

// refCandidates returns the (ref, path) splits to try, longest ref first.
// Repository roots and full-SHA refs have a single unambiguous split.
func (info githubURLInfo) refCandidates() []refCandidate {
	if info.kind == githubURLRoot {
		return []refCandidate{{ref: "HEAD"}}
	}
	if info.refIsFullSHA {
		return []refCandidate{{ref: info.ref, path: info.path}}
	}
	candidates := make([]refCandidate, 0, len(info.refSegments))
	for i := len(info.refSegments); i >= 1; i-- {
		candidates = append(candidates, refCandidate{
			ref:  strings.Join(info.refSegments[:i], "/"),
			path: strings.Join(info.refSegments[i:], "/"),
		})
	}
	return candidates
}

// isGitHubNotFound reports whether err is a GitHub API 404 (unknown ref),
// the only tree-fetch failure that justifies trying a shorter ref.
func isGitHubNotFound(err error) bool {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == http.StatusNotFound
	}
	return false
}

func (g *githubInterceptor) githubDocument(target *url.URL, title, contentType, content string) Document {
	return Document{
		URL:         target.String(),
		FinalURL:    target.String(),
		StatusCode:  http.StatusOK,
		ContentType: contentType,
		Title:       title,
		Domain:      "github.com",
		Source:      "github",
		Content:     content,
		WordCount:   len(strings.Fields(content)),
	}
}

type githubURLKind string

const (
	githubURLRoot githubURLKind = "root"
	githubURLBlob githubURLKind = "blob"
	githubURLTree githubURLKind = "tree"
)

// githubURLInfo is the parsed shape of a github.com repository URL. ref is
// empty for repository roots; refIsFullSHA marks a 40-hex commit ref.
// refSegments holds every segment after the action (ref + path, unsplit);
// refCandidates derives the candidate splits from it.
type githubURLInfo struct {
	owner        string
	repo         string
	ref          string
	refIsFullSHA bool
	path         string
	kind         githubURLKind
	refSegments  []string
}

// gitHubNonCodeSegments are repository URL segments that are not browsable
// code (issues, pull requests, wiki, …); URLs under them are not claimed.
var gitHubNonCodeSegments = map[string]bool{
	"issues": true, "pull": true, "pulls": true, "discussions": true,
	"releases": true, "wiki": true, "actions": true, "settings": true,
	"security": true, "projects": true, "graphs": true, "compare": true,
	"commits": true, "tags": true, "branches": true, "stargazers": true,
	"watchers": true, "network": true, "forks": true, "milestone": true,
	"labels": true, "packages": true, "codespaces": true, "contribute": true,
	"community": true, "sponsors": true, "invitations": true,
	"notifications": true, "insights": true,
}

// parseGitHubURL parses a github.com owner/repo URL into its code shape.
// It returns ok=false for other hosts, non-repository paths, and non-code
// segments so the generic fetch owns those URLs.
func parseGitHubURL(target *url.URL) (githubURLInfo, bool) {
	host := strings.ToLower(target.Hostname())
	if host != hostGitHubCom && host != "www.github.com" {
		return githubURLInfo{}, false
	}
	var segments []string
	for _, raw := range strings.Split(target.EscapedPath(), "/") {
		if raw == "" {
			continue
		}
		if decoded, err := url.PathUnescape(raw); err == nil {
			raw = decoded
		}
		segments = append(segments, raw)
	}
	if len(segments) < 2 {
		return githubURLInfo{}, false
	}
	info := githubURLInfo{
		owner: segments[0],
		repo:  strings.TrimSuffix(segments[1], ".git"),
		kind:  githubURLRoot,
	}
	if len(segments) == 2 {
		return info, true
	}
	if gitHubNonCodeSegments[strings.ToLower(segments[2])] {
		return githubURLInfo{}, false
	}
	action := segments[2]
	if action != string(githubURLBlob) && action != "tree" {
		return githubURLInfo{}, false
	}
	if len(segments) < 4 {
		return githubURLInfo{}, false
	}
	info.ref = segments[3]
	info.refIsFullSHA = isFullGitSHA(info.ref)
	info.refSegments = segments[3:]
	if len(segments) > 4 {
		info.path = strings.Join(segments[4:], "/")
	}
	if action == string(githubURLBlob) {
		info.kind = githubURLBlob
	} else {
		info.kind = githubURLTree
	}
	return info, true
}

// isFullGitSHA reports whether ref is a 40-character lowercase hex SHA.
func isFullGitSHA(ref string) bool {
	if len(ref) != 40 {
		return false
	}
	for _, r := range ref {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func githubAPIEndpoint(cfg Config) string {
	if strings.TrimSpace(cfg.GitHubAPIEndpoint) != "" {
		return strings.TrimSpace(cfg.GitHubAPIEndpoint)
	}
	return defaultGitHubAPIEndpoint
}

// resolveGitHubToken implements the token precedence for the interceptor:
// explicit config value first, else the GITHUB_TOKEN environment variable.
func resolveGitHubToken(cfg Config) string {
	if strings.TrimSpace(cfg.GitHubToken) != "" {
		return strings.TrimSpace(cfg.GitHubToken)
	}
	return strings.TrimSpace(os.Getenv(githubTokenEnvVar))
}
