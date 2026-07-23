package marketplace

import "testing"

func TestPickPR(t *testing.T) {
	if got := pickPR(nil); got != nil {
		t.Errorf("pickPR(nil) = %+v, want nil", got)
	}

	closedOnly := []existingPR{{URL: "https://x/pull/1", State: "CLOSED"}}
	if got := pickPR(closedOnly); got == nil || got.State != "CLOSED" {
		t.Errorf("pickPR(closedOnly) = %+v, want the CLOSED PR reported", got)
	}

	// Newest-first with no OPEN entry falls back to the first (most recent).
	mixed := []existingPR{
		{URL: "https://x/pull/3", State: "CLOSED"},
		{URL: "https://x/pull/2", State: "MERGED"},
	}
	if got := pickPR(mixed); got == nil || got.URL != "https://x/pull/3" {
		t.Errorf("pickPR(mixed, no open) = %+v, want the newest (pull/3)", got)
	}

	// An OPEN PR always wins even if it isn't first in the list.
	withOpen := []existingPR{
		{URL: "https://x/pull/5", State: "CLOSED"},
		{URL: "https://x/pull/4", State: "OPEN"},
	}
	if got := pickPR(withOpen); got == nil || got.URL != "https://x/pull/4" {
		t.Errorf("pickPR(withOpen) = %+v, want the OPEN one (pull/4)", got)
	}
}
