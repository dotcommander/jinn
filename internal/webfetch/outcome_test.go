package webfetch

import (
	"errors"
	"testing"
)

func TestOutcomeForDocumentTaxonomy(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		doc  Document
		want string
	}{
		{"content", Document{Content: "# Title\nA substantial body of real content follows here."}, OutcomeContent},
		{"empty", Document{Content: "   \n"}, OutcomeEmptyExtraction},
		{"challenge", Document{Content: "Log in to continue. Sign up to continue."}, OutcomeSuspectedChallenge},
		{"image", Document{Source: sourceImage}, OutcomeContent},
	}
	for _, tc := range cases {
		if got := OutcomeForDocument(tc.doc); got != tc.want {
			t.Errorf("%s: outcome = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestOutcomeForErrorTaxonomy(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err  error
		want string
	}{
		{newCodedError(errors.New("401"), ErrorCodeAuthentication, "auth"), OutcomeAuthenticationNeeded},
		{newCodedError(errors.New("429"), ErrorCodeRateLimited, "slow down"), OutcomeRateLimited},
		{newCodedError(errors.New("not html"), ErrorCodeNotHTML, "raw"), OutcomeUnsupportedMedia},
		{errors.New("plain"), ""},
	}
	for _, tc := range cases {
		if got := OutcomeForError(tc.err); got != tc.want {
			t.Errorf("outcome = %q, want %q (err %v)", got, tc.want, tc.err)
		}
	}
}
