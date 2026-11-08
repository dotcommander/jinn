package webfetch

import (
	"errors"
	"strings"
)

// Fetch outcome taxonomy (slice 5): standardized labels distinguishing
// real content from challenge pages, empty extraction, and unsupported
// media. Authentication and rate-limit outcomes map from coded error
// codes on failure paths; the labels below cover success paths.
const (
	OutcomeContent              = "content"
	OutcomeSuspectedChallenge   = "suspected_challenge"
	OutcomeAuthenticationNeeded = "authentication_required"
	OutcomeRateLimited          = "rate_limited"
	OutcomeEmptyExtraction      = "empty_extraction"
	OutcomeUnsupportedMedia     = "unsupported_media"
)

// OutcomeForDocument classifies a successfully fetched document.
func OutcomeForDocument(doc Document) string {
	if doc.Source == sourceImage {
		return OutcomeContent
	}
	if strings.TrimSpace(doc.Content) == "" {
		return OutcomeEmptyExtraction
	}
	if QualityOf(doc.Content) == "chrome_only" {
		return OutcomeSuspectedChallenge
	}
	return OutcomeContent
}

// OutcomeForError maps a fetch failure's coded error to its outcome
// label, or "" when the failure has no taxonomy label.
func OutcomeForError(err error) string {
	var coded *CodedError
	if !errors.As(err, &coded) {
		return ""
	}
	switch coded.Code {
	case ErrorCodeAuthentication:
		return OutcomeAuthenticationNeeded
	case ErrorCodeRateLimited:
		return OutcomeRateLimited
	case ErrorCodeNotHTML:
		return OutcomeUnsupportedMedia
	default:
		return ""
	}
}
