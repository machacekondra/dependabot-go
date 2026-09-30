package dependabot

import "testing"

func satisfied(t *testing.T, requirement, version string) bool {
	t.Helper()
	constraints, err := ParseOperatorConstraints(requirement, parseTestVersion)
	if err != nil {
		t.Fatalf("ParseOperatorConstraints(%q): %v", requirement, err)
	}
	v := mustTestVersion(version)
	for _, c := range constraints {
		if !c.SatisfiedBy(v) {
			return false
		}
	}
	return true
}

func TestParseOperatorConstraints(t *testing.T) {
	cases := []struct {
		requirement, version string
		want                 bool
	}{
		{">= 1.2.0", "1.2.0", true},
		{">= 1.2.0", "1.1.9", false},
		{"> 1.2.0", "1.2.0", false},
		{"<= 1.2.0", "1.2.0", true},
		{"< 1.2.0", "1.2.0", false},
		{"!= 1.2.0", "1.2.0", false},
		{"!= 1.2.0", "1.2.1", true},
		// A bare version is an equality constraint.
		{"1.2.0", "1.2.0", true},
		{"1.2.0", "1.2.1", false},
		{"= 1.2.0", "1.2.0", true},
		{"== 1.2.0", "1.2.0", true},
		// Comma-separated parts all have to hold.
		{">= 1.1.0, < 2.0.0", "1.5.0", true},
		{">= 1.1.0, < 2.0.0", "2.0.0", false},
		{">= 1.1.0, < 2.0.0", "1.0.0", false},
		// Whitespace between operator and operand is optional.
		{">=1.2.0", "1.2.0", true},
	}
	for _, tc := range cases {
		if got := satisfied(t, tc.requirement, tc.version); got != tc.want {
			t.Errorf("%q satisfied by %q = %v, want %v", tc.requirement, tc.version, got, tc.want)
		}
	}
}

// "~> 1.2.3" allows patch updates; "~> 1.2" allows minor ones.
func TestParsePessimisticConstraint(t *testing.T) {
	cases := []struct {
		requirement, version string
		want                 bool
	}{
		{"~> 1.2.3", "1.2.3", true},
		{"~> 1.2.3", "1.2.9", true},
		{"~> 1.2.3", "1.3.0", false},
		{"~> 1.2.3", "1.2.2", false},
		{"~> 1.2", "1.9.0", true},
		{"~> 1.2", "2.0.0", false},
		{"~> 1.2", "1.1.0", false},
	}
	for _, tc := range cases {
		if got := satisfied(t, tc.requirement, tc.version); got != tc.want {
			t.Errorf("%q satisfied by %q = %v, want %v", tc.requirement, tc.version, got, tc.want)
		}
	}
}

// A single segment gives no series to bound, so it is rejected rather than
// silently treated as an exact match.
func TestParsePessimisticNeedsTwoSegments(t *testing.T) {
	if _, err := ParseOperatorConstraints("~> 1", parseTestVersion); err == nil {
		t.Error("expected an error for a single-segment pessimistic constraint")
	}
}

// An empty or wildcard requirement constrains nothing.
func TestParseEmptyConstraint(t *testing.T) {
	for _, requirement := range []string{"", "   ", "*"} {
		constraints, err := ParseOperatorConstraints(requirement, parseTestVersion)
		if err != nil {
			t.Fatalf("ParseOperatorConstraints(%q): %v", requirement, err)
		}
		if len(constraints) != 0 {
			t.Errorf("%q gave %d constraints, want none", requirement, len(constraints))
		}
	}
}

func TestParseInvalidConstraint(t *testing.T) {
	if _, err := ParseOperatorConstraints(">= not-a-version", parseTestVersion); err == nil {
		t.Error("expected an error for an unparseable operand")
	}
}

// The text is what ends up in a pull request body, so it has to survive
// parsing.
func TestConstraintString(t *testing.T) {
	constraints, err := ParseOperatorConstraints(">= 1.1.0, < 2.0.0", parseTestVersion)
	if err != nil {
		t.Fatalf("ParseOperatorConstraints: %v", err)
	}
	if len(constraints) != 1 {
		t.Fatalf("got %d constraints, want the parts joined into one", len(constraints))
	}
	if constraints[0].String() != ">= 1.1.0, < 2.0.0" {
		t.Errorf("String() = %q, want the requirement as written", constraints[0].String())
	}
}
