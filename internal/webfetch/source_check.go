package webfetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Source evidence (slice 3): reproducible, line-grounded term checks over
// an explicit set of URLs or document snapshots. This is a lexical tool:
// it reports whether quoted terms appear verbatim (exact) or under case
// normalization, never a semantic true/false verdict on a claim. A claim
// label is carried through for the caller's own review.

const (
	maxSourceCheckSources = 5
	maxSourceExcerpts     = 3
	maxSourceExcerptChars = 200
)

// SourceCheckRequest describes one evidence-gathering call.
type SourceCheckRequest struct {
	// Claim is an optional label for the caller's review; it is not
	// verified by this tool.
	Claim       string
	URLs        []string
	SnapshotIDs []string
	Terms       []string
}

// SourceCheckResponse is the per-source evidence outcome.
type SourceCheckResponse struct {
	Claim   string           `json:"claim,omitempty"`
	Sources []SourceEvidence `json:"sources"`
}

// SourceEvidence is one checked source.
type SourceEvidence struct {
	Source string             `json:"source"`
	Kind   string             `json:"kind"` // "url" or "snapshot"
	Digest string             `json:"digest,omitempty"`
	Terms  []SourceTermResult `json:"terms,omitempty"`
	// Error is set when the source could not be retrieved; successful
	// sources keep their evidence even when others fail.
	Error string `json:"error,omitempty"`
}

// SourceTermResult is one term's outcome within a source.
type SourceTermResult struct {
	Term     string   `json:"term"`
	Status   string   `json:"status"` // exact_match, normalized_match, not_found
	Excerpts []string `json:"excerpts,omitempty"`
	Lines    []int    `json:"lines,omitempty"` // 1-based source lines
}

// SourceCheck validates the request and checks each source in order.
// Partial retrieval failures are recorded per source, never fatal.
func (s *Service) SourceCheck(ctx context.Context, req SourceCheckRequest) (SourceCheckResponse, error) {
	if s == nil {
		return SourceCheckResponse{}, newCodedError(errors.New("webfetch service is nil"), ErrorCodeInvalidArgument, "initialize the webfetch service before calling SourceCheck")
	}
	terms, err := validateSourceTerms(req.Terms)
	if err != nil {
		return SourceCheckResponse{}, err
	}
	total := len(req.URLs) + len(req.SnapshotIDs)
	if total == 0 {
		return SourceCheckResponse{}, newCodedError(errors.New("source_check requires at least one URL or snapshot id"), ErrorCodeInvalidArgument, "pass urls or snapshot_ids to check")
	}
	if total > maxSourceCheckSources {
		return SourceCheckResponse{}, newCodedError(fmt.Errorf("source_check accepts at most %d sources per call", maxSourceCheckSources), ErrorCodeInvalidArgument, "split the sources across calls")
	}
	resp := SourceCheckResponse{Claim: strings.TrimSpace(req.Claim)}
	for _, rawURL := range req.URLs {
		resp.Sources = append(resp.Sources, s.checkURLSource(ctx, strings.TrimSpace(rawURL), terms))
	}
	for _, id := range req.SnapshotIDs {
		resp.Sources = append(resp.Sources, s.checkSnapshotSource(strings.TrimSpace(id), terms))
	}
	return resp, nil
}

func validateSourceTerms(terms []string) ([]string, error) {
	if len(terms) == 0 {
		return nil, newCodedError(errors.New("source_check requires at least one term"), ErrorCodeInvalidArgument, "pass terms to check for")
	}
	clean := make([]string, 0, len(terms))
	for _, term := range terms {
		trimmed := strings.TrimSpace(term)
		if trimmed == "" {
			return nil, newCodedError(errors.New("source_check: a term is empty"), ErrorCodeInvalidArgument, "provide non-empty terms")
		}
		if len([]rune(trimmed)) > maxMustContainChars {
			return nil, newCodedError(fmt.Errorf("source_check: a term is longer than %d chars", maxMustContainChars), ErrorCodeInvalidArgument, "shorten the term")
		}
		clean = append(clean, trimmed)
	}
	if len(clean) > maxMustContainTerms {
		return nil, newCodedError(fmt.Errorf("source_check: at most %d terms per call", maxMustContainTerms), ErrorCodeInvalidArgument, "split the terms across calls")
	}
	return clean, nil
}

func (s *Service) checkURLSource(ctx context.Context, rawURL string, terms []string) SourceEvidence {
	evidence := SourceEvidence{Source: rawURL, Kind: "url"}
	if rawURL == "" {
		evidence.Error = "missing URL"
		return evidence
	}
	doc, err := s.Fetch(ctx, FetchRequest{URL: rawURL})
	if err != nil {
		evidence.Error = err.Error()
		return evidence
	}
	evidence.Digest = contentDigest(doc.Content)
	evidence.Terms = checkTermsInContent(doc.Content, terms)
	return evidence
}

func (s *Service) checkSnapshotSource(id string, terms []string) SourceEvidence {
	evidence := SourceEvidence{Source: id, Kind: "snapshot"}
	snap, err := s.GetSnapshot(id)
	if err != nil {
		evidence.Error = err.Error()
		return evidence
	}
	if snap.Kind != snapshotKindDocument || snap.Document == nil {
		evidence.Error = "snapshot is not a document"
		return evidence
	}
	evidence.Digest = snap.Digest
	evidence.Terms = checkTermsInContent(snap.Document.Content, terms)
	return evidence
}

func contentDigest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// checkTermsInContent classifies each term against the content lines:
// exact_match (case-sensitive substring), normalized_match (case-folded
// substring), or not_found. Excerpts are bounded line texts with 1-based
// line numbers; full content is never returned.
func checkTermsInContent(content string, terms []string) []SourceTermResult {
	lines := strings.Split(content, "\n")
	results := make([]SourceTermResult, 0, len(terms))
	for _, term := range terms {
		results = append(results, checkTermInLines(term, lines))
	}
	return results
}

// checkTermInLines classifies one term against content lines: exact
// (case-sensitive), normalized (case-folded), or not found, with bounded
// excerpts and 1-based line numbers.
func checkTermInLines(term string, lines []string) SourceTermResult {
	result := SourceTermResult{Term: term, Status: "not_found"}
	lowerTerm := strings.ToLower(term)
	for lineNo, line := range lines {
		exact := strings.Contains(line, term)
		normalized := !exact && strings.Contains(strings.ToLower(line), lowerTerm)
		if !exact && !normalized {
			continue
		}
		if result.Status == "not_found" {
			if exact {
				result.Status = "exact_match"
			} else {
				result.Status = "normalized_match"
			}
		} else if result.Status == "normalized_match" && exact {
			result.Status = "exact_match"
		}
		if len(result.Excerpts) < maxSourceExcerpts {
			result.Excerpts = append(result.Excerpts, boundedExcerpt(line))
			result.Lines = append(result.Lines, lineNo+1)
		}
	}
	return result
}

func boundedExcerpt(line string) string {
	trimmed := strings.TrimSpace(line)
	if len([]rune(trimmed)) > maxSourceExcerptChars {
		return string([]rune(trimmed)[:maxSourceExcerptChars]) + "…"
	}
	return trimmed
}
