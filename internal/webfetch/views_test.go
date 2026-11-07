package webfetch

import (
	"strings"
	"testing"
)

var viewSample = "# Title\nIntro text.\n## Pricing\nOur pricing starts at 10 dollars.\nMore pricing details here.\n## FAQ\nPricing questions live here.\n"

func TestDocumentOutlineAndSection(t *testing.T) {
	t.Parallel()
	sections := DocumentOutline(viewSample)
	if len(sections) != 3 {
		t.Fatalf("sections = %v, want 3", sections)
	}
	if sections[1].ID != "s2" || sections[1].Title != "Pricing" {
		t.Fatalf("section = %+v", sections[1])
	}
	start, end, found := SectionRange(sections, "s2", len(splitLinesSafe(viewSample)))
	if !found || start != 2 || end != 5 {
		t.Fatalf("SectionRange = %d,%d,%v, want 2,5,true", start, end, found)
	}
	if _, _, found := SectionRange(sections, "s9", 0); found {
		t.Fatal("unknown section found")
	}
}

func splitLinesSafe(text string) []string { return strings.Split(text, "\n") }

func TestProbeDocumentCountsAndExcerpts(t *testing.T) {
	t.Parallel()
	hit, err := ProbeDocument(viewSample, "pricing")
	if err != nil {
		t.Fatalf("ProbeDocument: %v", err)
	}
	if !hit.Found || hit.Count != 4 {
		t.Fatalf("hit = %+v, want 4 pricing matches (incl. heading)", hit)
	}
	if len(hit.Sections) == 0 || hit.Sections[0] != "s2" {
		t.Fatalf("sections = %v", hit.Sections)
	}
	if len(hit.Excerpts) == 0 || !strings.Contains(strings.ToLower(hit.Excerpts[0]), "pricing") {
		t.Fatalf("excerpts = %v", hit.Excerpts)
	}
	missing, err := ProbeDocument(viewSample, "nonexistent-term")
	if err != nil || missing.Found || missing.Count != 0 {
		t.Fatalf("missing = %+v err=%v", missing, err)
	}
}

func TestChromeOnlyCalibratedShapes(t *testing.T) {
	t.Parallel()
	shell := "# TIL that Pierce Brosnan was fired\nReddit Log In\n# TIL that Pierce Brosnan was fired.\nContinue with Phone Continue with Email\nJoin the most real place"
	if !ChromeOnly(shell) {
		t.Fatal("reddit shell shape not detected as chrome")
	}
	short := "Example Domain This domain is established to be used for illustrative examples in documents."
	if ChromeOnly(short) {
		t.Fatal("short real page flagged as chrome")
	}
	long := strings.Repeat("This is a long article sentence with more than eight words in it and enough length. ", 20)
	if ChromeOnly(long) {
		t.Fatal("long page flagged as chrome")
	}
}

func TestMustContainTermsValidation(t *testing.T) {
	t.Parallel()
	if _, err := MustContainTerms([]string{"", ""}); err == nil {
		t.Fatal("empty term accepted")
	}
	terms := make([]string, 9)
	for i := range terms {
		terms[i] = "x"
	}
	if _, err := MustContainTerms(terms); err == nil {
		t.Fatal("9 terms accepted, want cap at 8")
	}
}
