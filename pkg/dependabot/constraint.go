package dependabot

import (
	"fmt"
	"strconv"
	"strings"
)

// VersionParser turns an ecosystem's version string into a Version.
type VersionParser func(string) (Version, error)

// operatorConstraint is a single comparison such as ">= 1.2.0".
type operatorConstraint struct {
	op      string
	operand Version
	text    string
}

func (c operatorConstraint) String() string { return c.text }

func (c operatorConstraint) SatisfiedBy(v Version) bool {
	cmp := v.Compare(c.operand)
	switch c.op {
	case ">":
		return cmp > 0
	case ">=":
		return cmp >= 0
	case "<":
		return cmp < 0
	case "<=":
		return cmp <= 0
	case "!=":
		return cmp != 0
	default: // "=", "=="
		return cmp == 0
	}
}

// andConstraint requires every member to be satisfied, which is how a
// comma-separated requirement string such as ">= 1.1.0, < 2.0" behaves.
type andConstraint struct {
	parts []VersionConstraint
	text  string
}

func (c andConstraint) String() string { return c.text }

func (c andConstraint) SatisfiedBy(v Version) bool {
	for _, p := range c.parts {
		if !p.SatisfiedBy(v) {
			return false
		}
	}
	return true
}

// ParseOperatorConstraints parses a comma-separated requirement string using
// RubyGems-style operators (=, !=, >, >=, <, <=, ~>), returning one constraint
// that requires all of the comma-separated parts.
//
// Ruby's Gem::Requirement#requirements_array returns a list where each entry is
// an alternative; for the ecosystems ported so far every requirement string is
// a conjunction, so a one-element slice holding an AND is the faithful result.
func ParseOperatorConstraints(s string, parse VersionParser) ([]VersionConstraint, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "*" {
		return nil, nil
	}

	var parts []VersionConstraint
	for _, raw := range strings.Split(s, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		parsed, err := parseSingleConstraint(raw, parse)
		if err != nil {
			return nil, err
		}
		parts = append(parts, parsed...)
	}
	if len(parts) == 0 {
		return nil, nil
	}
	if len(parts) == 1 {
		return parts, nil
	}
	return []VersionConstraint{andConstraint{parts: parts, text: s}}, nil
}

func parseSingleConstraint(raw string, parse VersionParser) ([]VersionConstraint, error) {
	op, operand := splitOperator(raw)

	if op == "~>" {
		return parsePessimistic(raw, operand, parse)
	}

	v, err := parse(operand)
	if err != nil {
		return nil, fmt.Errorf("parsing constraint %q: %w", raw, err)
	}
	return []VersionConstraint{operatorConstraint{op: op, operand: v, text: raw}}, nil
}

// parsePessimistic expands "~> 1.2.3" into ">= 1.2.3, < 1.3". The upper bound
// is derived from the operand's own text: drop the final segment and increment
// the one before it.
func parsePessimistic(raw, operand string, parse VersionParser) ([]VersionConstraint, error) {
	lower, err := parse(operand)
	if err != nil {
		return nil, fmt.Errorf("parsing constraint %q: %w", raw, err)
	}

	// Only the release segments take part in the bound; a prerelease suffix on
	// the operand does not change which release series is allowed.
	release := operand
	if i := strings.IndexAny(release, "-+"); i >= 0 {
		release = release[:i]
	}
	segments := strings.Split(release, ".")
	if len(segments) < 2 {
		return nil, fmt.Errorf("constraint %q needs at least two version segments", raw)
	}
	bumpIdx := len(segments) - 2
	n, err := strconv.Atoi(strings.TrimPrefix(segments[bumpIdx], "v"))
	if err != nil {
		return nil, fmt.Errorf("constraint %q has a non-numeric segment", raw)
	}
	upperSegments := append([]string(nil), segments[:bumpIdx+1]...)
	prefix := ""
	if bumpIdx == 0 && strings.HasPrefix(segments[0], "v") {
		prefix = "v"
	}
	upperSegments[bumpIdx] = prefix + strconv.Itoa(n+1)

	upper, err := parse(strings.Join(upperSegments, "."))
	if err != nil {
		return nil, fmt.Errorf("deriving upper bound for %q: %w", raw, err)
	}
	return []VersionConstraint{
		operatorConstraint{op: ">=", operand: lower, text: ">= " + operand},
		operatorConstraint{op: "<", operand: upper, text: "< " + upper.String()},
	}, nil
}

func splitOperator(raw string) (op, operand string) {
	for _, candidate := range []string{">=", "<=", "==", "!=", "~>", ">", "<", "="} {
		if strings.HasPrefix(raw, candidate) {
			return normaliseOp(candidate), strings.TrimSpace(raw[len(candidate):])
		}
	}
	return "=", raw
}

func normaliseOp(op string) string {
	if op == "==" {
		return "="
	}
	return op
}
