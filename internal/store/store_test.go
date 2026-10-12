package store

import "testing"

func TestKey(t *testing.T) {
	if got := Key("linear", "sre-1"); got != "linear:SRE-1" {
		t.Errorf("Key linear = %q, want linear:SRE-1 (upper-cased)", got)
	}
	if got := Key("pr", "https://x/pull/1"); got != "pr:https://x/pull/1" {
		t.Errorf("Key pr = %q", got)
	}
}

func TestPRRoundTrip(t *testing.T) {
	s := New()
	if _, ok := s.PR("u"); ok {
		t.Fatal("empty store returned a PR")
	}
	s.PutPRs([]PR{{URL: "u", State: PRMerged, CI: CIPassing, Review: ReviewApproved}})
	got, ok := s.PR("u")
	if !ok || got.State != PRMerged || got.CI != CIPassing || got.Review != ReviewApproved {
		t.Errorf("PR = %+v ok=%v, want merged/passing/approved", got, ok)
	}
	// Upsert keeps unknown keys and overwrites known ones.
	s.PutPRs([]PR{{URL: "u", State: PRClosed}})
	if got, _ := s.PR("u"); got.State != PRClosed {
		t.Errorf("after upsert State = %q, want closed", got.State)
	}
}

func TestSessionMentions(t *testing.T) {
	s := New()
	s.SetSessionMentions(map[string][]SessionRef{
		Key("linear", "SRE-1"): {{Path: "/a", Snippet: "ctx"}},
	})
	got := s.SessionsMentioning(Key("linear", "sre-1")) // case-insensitive via Key
	if len(got) != 1 || got[0].Path != "/a" || got[0].Snippet != "ctx" {
		t.Errorf("SessionsMentioning = %+v, want one ref to /a", got)
	}
	if got := s.SessionsMentioning(Key("pr", "none")); got != nil {
		t.Errorf("unknown key = %+v, want nil", got)
	}
}

// An ABC-123 is shown as an issue when it is loaded, or from a team
// issues have come from and no lookup refused it; before any team is
// known everything is shown, since nothing can be ruled out yet.
func TestVouched(t *testing.T) {
	s := New()
	if !s.Vouched("WAVE-2") {
		t.Error("with no teams seen, nothing should be ruled out")
	}
	s.PutIssues([]Issue{{Identifier: "SRE-1"}})
	if !s.Vouched("SRE-1") || !s.Vouched("sre-99") {
		t.Error("a loaded issue, and its team's other ids, should be vouched")
	}
	if s.Vouched("WAVE-2") {
		t.Error("an id from a team never seen should not be vouched once teams are known")
	}
	s.MarkMissing("SRE-99")
	if s.Vouched("SRE-99") {
		t.Error("an id a lookup refused should not be vouched")
	}
	s.PutIssues([]Issue{{Identifier: "SRE-99"}})
	if !s.Vouched("SRE-99") {
		t.Error("a later load clears the refusal")
	}
}
