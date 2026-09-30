package dependabot

import "testing"

func advisory(t *testing.T, vulnerable, safe []string) *SecurityAdvisory {
	t.Helper()
	a, err := NewSecurityAdvisory("dep", testEcosystemName, vulnerable, safe)
	if err != nil {
		t.Fatalf("NewSecurityAdvisory: %v", err)
	}
	return a
}

func TestAdvisoryAffectsVersion(t *testing.T) {
	a := advisory(t, []string{">= 1.0.0, < 1.2.0"}, nil)

	cases := map[string]bool{
		"0.9.0": false,
		"1.0.0": true,
		"1.1.9": true,
		"1.2.0": false,
	}
	for version, want := range cases {
		if got := a.AffectsVersionString(version); got != want {
			t.Errorf("AffectsVersionString(%q) = %v, want %v", version, got, want)
		}
	}
}

// A safe range wins over a vulnerable one, which is how a backported fix is
// expressed.
func TestAdvisorySafeRangeWins(t *testing.T) {
	a := advisory(t, []string{"< 2.0.0"}, []string{"1.9.5"})

	if a.AffectsVersionString("1.9.5") {
		t.Error("a version listed as safe must not be reported as affected")
	}
	if !a.AffectsVersionString("1.9.4") {
		t.Error("a version in the vulnerable range should be affected")
	}
}

// With only safe ranges given, anything outside them is affected.
func TestAdvisoryOnlySafeRanges(t *testing.T) {
	a := advisory(t, nil, []string{">= 2.0.0"})

	if !a.AffectsVersionString("1.9.0") {
		t.Error("a version outside every safe range should be affected")
	}
	if a.AffectsVersionString("2.0.0") {
		t.Error("a version in a safe range should not be affected")
	}
}

// An advisory with no ranges at all cannot say anything.
func TestAdvisoryWithNoRanges(t *testing.T) {
	a := advisory(t, nil, nil)
	if a.AffectsVersionString("1.0.0") {
		t.Error("an advisory with no ranges should affect nothing")
	}
	if a.AffectsVersion(nil) {
		t.Error("a nil version should not be reported as affected")
	}
}

// A version the ecosystem cannot parse (a git SHA, say) is left alone rather
// than guessed at.
func TestAdvisoryUnparseableVersion(t *testing.T) {
	a := advisory(t, []string{"< 2.0.0"}, nil)
	for _, version := range []string{"", "0b84568fffcc1cbbf2b3dcdd9e7b4a6c5d8e9f0a"} {
		if a.AffectsVersionString(version) {
			t.Errorf("AffectsVersionString(%q) = true, want false", version)
		}
	}
}

func TestAdvisoryFixedBy(t *testing.T) {
	a := advisory(t, []string{"< 1.2.0"}, nil)

	fixed := MustNewDependency(Dependency{
		Name: "dep", PackageManager: testEcosystemName,
		Version: "1.2.0", PreviousVersion: "1.1.0",
	})
	if !a.FixedBy(fixed) {
		t.Error("moving out of the vulnerable range should fix the advisory")
	}

	// Still vulnerable after the update.
	notFixed := MustNewDependency(Dependency{
		Name: "dep", PackageManager: testEcosystemName,
		Version: "1.1.9", PreviousVersion: "1.1.0",
	})
	if a.FixedBy(notFixed) {
		t.Error("staying in the vulnerable range does not fix the advisory")
	}

	// Never vulnerable in the first place.
	wasSafe := MustNewDependency(Dependency{
		Name: "dep", PackageManager: testEcosystemName,
		Version: "1.3.0", PreviousVersion: "1.2.0",
	})
	if a.FixedBy(wasSafe) {
		t.Error("a dependency that was never vulnerable cannot be fixed")
	}

	// Without a previous version we cannot tell, so we do not claim a fix.
	noPrevious := MustNewDependency(Dependency{
		Name: "dep", PackageManager: testEcosystemName, Version: "1.2.0",
	})
	if a.FixedBy(noPrevious) {
		t.Error("a dependency with no previous version cannot be claimed as fixed")
	}

	// Removing the dependency removes the vulnerability.
	removed := MustNewDependency(Dependency{
		Name: "dep", PackageManager: testEcosystemName,
		PreviousVersion: "1.1.0", Removed: true,
	})
	if !a.FixedBy(removed) {
		t.Error("removing a vulnerable dependency should fix the advisory")
	}
}

// An advisory only applies to its own dependency, matched case-insensitively
// the way a registry would.
func TestAdvisoryFixedByMatchesDependency(t *testing.T) {
	a := advisory(t, []string{"< 1.2.0"}, nil)

	otherName := MustNewDependency(Dependency{
		Name: "other", PackageManager: testEcosystemName,
		Version: "1.2.0", PreviousVersion: "1.1.0",
	})
	if a.FixedBy(otherName) {
		t.Error("an advisory should not apply to a different dependency")
	}

	differentCase := MustNewDependency(Dependency{
		Name: "DEP", PackageManager: testEcosystemName,
		Version: "1.2.0", PreviousVersion: "1.1.0",
	})
	if !a.FixedBy(differentCase) {
		t.Error("the dependency name should match case-insensitively")
	}
}

func TestAnyAffect(t *testing.T) {
	advisories := []*SecurityAdvisory{
		advisory(t, []string{"< 1.0.0"}, nil),
		advisory(t, []string{">= 2.0.0, < 2.1.0"}, nil),
	}

	if !AnyAffect(advisories, mustTestVersion("2.0.5")) {
		t.Error("a version matching the second advisory should be affected")
	}
	if AnyAffect(advisories, mustTestVersion("1.5.0")) {
		t.Error("a version matching neither advisory should not be affected")
	}
}

func TestNewSecurityAdvisoryErrors(t *testing.T) {
	if _, err := NewSecurityAdvisory("dep", "nope", nil, nil); err == nil {
		t.Error("expected an error for an unregistered package manager")
	}
	if _, err := NewSecurityAdvisory("dep", testEcosystemName, []string{"< not-a-version"}, nil); err == nil {
		t.Error("expected an error for an unparseable range")
	}
}
