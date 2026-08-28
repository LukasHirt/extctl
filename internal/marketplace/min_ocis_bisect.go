package marketplace

import "fmt"

// BisectMinOCIS finds the lowest version in versions (sorted ascending) at
// which check reports the release under test as compatible, assuming
// compatibility is monotonic in oCIS version — once check(v) succeeds,
// every later version in versions also succeeds. This is the same
// "extension-point APIs are additive" assumption InferMinOCISFromHistory
// already relied on as an unverified guess; BisectMinOCIS instead confirms
// it empirically, one real oCIS version at a time, via check.
//
// If seed is non-empty, the nearest version in versions >= seed is checked
// FIRST as a fast path: the common case across an extension's own release
// history is that its floor hasn't moved since the previous release, so
// this avoids a full O(log n) bisection on every publish and costs exactly
// one check() call. Bisection only runs — and only ABOVE the seed, never
// below it — when the seed check fails. Searching below a passing prior
// value would risk reporting a lower minOCIS than a human already approved
// for an earlier release, which should never happen for the same
// extension's version history moving forward.
//
// check must be deterministic — BisectMinOCIS does not retry a flaky
// result, since a failing check could as easily mean "genuinely
// incompatible" as "environment hiccup". A caller whose check wraps a real,
// flaky harness (Docker/Playwright) is responsible for retrying transient
// failures itself before reporting a definitive pass/fail here.
//
// Returns ok=false (not an error) if check never passes for any candidate
// version — a legitimate outcome (this build genuinely doesn't work against
// anything available), not something callers should treat as missing data.
func BisectMinOCIS(versions []string, seed string, check func(version string) (bool, error)) (result string, ok bool, err error) {
	if len(versions) == 0 {
		return "", false, fmt.Errorf("no candidate oCIS versions to check")
	}

	lo, hi := 0, len(versions)
	if seed != "" {
		seedIdx := lowerBoundVersion(versions, seed)
		if seedIdx == len(versions) {
			// seed is newer than every available oCIS image — nothing to
			// verify it against, and searching below seed would risk
			// reporting a lower minOCIS than a previous release already
			// claimed.
			return "", false, nil
		}
		passed, err := check(versions[seedIdx])
		if err != nil {
			return "", false, err
		}
		if passed {
			return versions[seedIdx], true, nil
		}
		lo = seedIdx + 1 // the floor moved past the seed; never search below it
	}

	for lo < hi {
		mid := (lo + hi) / 2
		passed, err := check(versions[mid])
		if err != nil {
			return "", false, err
		}
		if passed {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	if lo == len(versions) {
		return "", false, nil
	}
	return versions[lo], true, nil
}

// lowerBoundVersion returns the index of the first entry in versions
// (sorted ascending) that is >= seed, or len(versions) if every entry is
// lower.
func lowerBoundVersion(versions []string, seed string) int {
	lo, hi := 0, len(versions)
	for lo < hi {
		mid := (lo + hi) / 2
		if compareVersions(versions[mid], seed) < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}
