package marketplace

import (
	"errors"
	"testing"
)

// thresholdOracle returns a check func simulating a monotonic compatibility
// floor at threshold — passes for every version >= threshold — and a
// pointer to the number of times it was called, so tests can assert on the
// seed fast-path actually short-circuiting the full bisection.
func thresholdOracle(threshold string) (check func(string) (bool, error), calls *int) {
	calls = new(int)
	check = func(v string) (bool, error) {
		*calls++
		return compareVersions(v, threshold) >= 0, nil
	}
	return check, calls
}

func TestBisectMinOCIS_NoSeedFindsFloor(t *testing.T) {
	versions := []string{"1.0.0", "2.0.0", "3.0.0", "4.0.0", "5.0.0"}
	check, _ := thresholdOracle("3.0.0")

	got, ok, err := BisectMinOCIS(versions, "", check)
	if err != nil {
		t.Fatalf("BisectMinOCIS: %v", err)
	}
	if !ok || got != "3.0.0" {
		t.Errorf("got %q, ok=%v; want 3.0.0, ok=true", got, ok)
	}
}

func TestBisectMinOCIS_SeedFastPathShortCircuits(t *testing.T) {
	versions := []string{"1.0.0", "2.0.0", "3.0.0", "4.0.0", "5.0.0"}
	check, calls := thresholdOracle("3.0.0")

	got, ok, err := BisectMinOCIS(versions, "3.0.0", check)
	if err != nil {
		t.Fatalf("BisectMinOCIS: %v", err)
	}
	if !ok || got != "3.0.0" {
		t.Errorf("got %q, ok=%v; want 3.0.0, ok=true", got, ok)
	}
	if *calls != 1 {
		t.Errorf("check called %d times, want exactly 1 (seed fast path should short-circuit)", *calls)
	}
}

func TestBisectMinOCIS_SeedBelowFloor_SearchesOnlyAbove(t *testing.T) {
	versions := []string{"1.0.0", "2.0.0", "3.0.0", "4.0.0", "5.0.0"}
	check, _ := thresholdOracle("4.0.0")

	// seed (2.0.0) no longer passes — the real floor moved to 4.0.0.
	got, ok, err := BisectMinOCIS(versions, "2.0.0", check)
	if err != nil {
		t.Fatalf("BisectMinOCIS: %v", err)
	}
	if !ok || got != "4.0.0" {
		t.Errorf("got %q, ok=%v; want 4.0.0, ok=true", got, ok)
	}
}

func TestBisectMinOCIS_NeverReturnsBelowSeed(t *testing.T) {
	// Deliberately non-monotonic oracle: only 1.0.0 passes, everything
	// else (including versions above the seed) fails. Even though 1.0.0
	// would satisfy a naive "does anything pass" search, BisectMinOCIS
	// must never return a version below the seed once the seed itself has
	// failed — that would silently regress minOCIS below what a human
	// already approved for an earlier release.
	versions := []string{"1.0.0", "2.0.0", "3.0.0", "4.0.0", "5.0.0"}
	check := func(v string) (bool, error) { return v == "1.0.0", nil }

	got, ok, err := BisectMinOCIS(versions, "2.0.0", check)
	if err != nil {
		t.Fatalf("BisectMinOCIS: %v", err)
	}
	if ok {
		t.Errorf("got %q, ok=true; want ok=false (nothing above the seed passes, and below it must never be searched)", got)
	}
}

func TestBisectMinOCIS_SeedAboveEveryVersion(t *testing.T) {
	versions := []string{"1.0.0", "2.0.0", "3.0.0"}
	calls := 0
	check := func(v string) (bool, error) { calls++; return true, nil }

	got, ok, err := BisectMinOCIS(versions, "9.0.0", check)
	if err != nil {
		t.Fatalf("BisectMinOCIS: %v", err)
	}
	if ok {
		t.Errorf("got %q, ok=true; want ok=false (seed newer than every available image)", got)
	}
	if calls != 0 {
		t.Errorf("check called %d times, want 0 (nothing to verify seed against)", calls)
	}
}

func TestBisectMinOCIS_NothingPasses(t *testing.T) {
	versions := []string{"1.0.0", "2.0.0", "3.0.0"}
	check := func(v string) (bool, error) { return false, nil }

	got, ok, err := BisectMinOCIS(versions, "", check)
	if err != nil {
		t.Fatalf("BisectMinOCIS: %v", err)
	}
	if ok {
		t.Errorf("got %q, ok=true; want ok=false", got)
	}
}

func TestBisectMinOCIS_EmptyVersions(t *testing.T) {
	_, _, err := BisectMinOCIS(nil, "", func(string) (bool, error) { return true, nil })
	if err == nil {
		t.Error("want an error for an empty candidate list, got nil")
	}
}

func TestBisectMinOCIS_CheckErrorPropagates(t *testing.T) {
	versions := []string{"1.0.0", "2.0.0", "3.0.0"}
	sentinel := errors.New("docker daemon unreachable")
	check := func(v string) (bool, error) { return false, sentinel }

	_, _, err := BisectMinOCIS(versions, "", check)
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want it to wrap %v", err, sentinel)
	}
}

func TestBisectMinOCIS_HighestVersionIsFloor(t *testing.T) {
	versions := []string{"1.0.0", "2.0.0", "3.0.0"}
	check, _ := thresholdOracle("3.0.0")

	got, ok, err := BisectMinOCIS(versions, "", check)
	if err != nil {
		t.Fatalf("BisectMinOCIS: %v", err)
	}
	if !ok || got != "3.0.0" {
		t.Errorf("got %q, ok=%v; want 3.0.0, ok=true", got, ok)
	}
}

func TestBisectMinOCIS_LowestVersionIsFloor(t *testing.T) {
	versions := []string{"1.0.0", "2.0.0", "3.0.0"}
	check, _ := thresholdOracle("1.0.0")

	got, ok, err := BisectMinOCIS(versions, "", check)
	if err != nil {
		t.Fatalf("BisectMinOCIS: %v", err)
	}
	if !ok || got != "1.0.0" {
		t.Errorf("got %q, ok=%v; want 1.0.0, ok=true", got, ok)
	}
}
