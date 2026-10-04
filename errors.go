package contrast

import (
	"errors"
	"strconv"
	"strings"
)

// Sentinel errors. Callers can use errors.Is to detect them.
var (
	// ErrMissingBase indicates that a semi-transparent color was evaluated
	// without the mandatory backdrop/base color. The engine never falls back
	// to an implicit black backdrop.
	ErrMissingBase = errors.New("contrast: backdrop base color is required for semi-transparent tokens")

	// ErrInvalidColor indicates a malformed or impossible color value.
	ErrInvalidColor = errors.New("contrast: invalid color value")

	// ErrUnknownRole indicates that a combo references a role absent from
	// the theme token map.
	ErrUnknownRole = errors.New("contrast: unknown semantic role")

	// ErrUnknownModel indicates a request for an unregistered contrast model.
	ErrUnknownModel = errors.New("contrast: unknown contrast model")

	// ErrDerivationImpossible indicates that high-contrast derivation could
	// not satisfy every required role/model combination.
	ErrDerivationImpossible = errors.New("contrast: high-contrast derivation impossible")
)

// MissingBaseError carries the role/stage that required a backdrop but
// had none, so the rejection is pinpointable.
type MissingBaseError struct {
	Role  Role
	Stage string
}

func (e *MissingBaseError) Error() string {
	var b strings.Builder
	b.WriteString("contrast: role ")
	b.WriteString(string(e.Role))
	if e.Stage != "" {
		b.WriteString(" (")
		b.WriteString(e.Stage)
		b.WriteByte(')')
	}
	b.WriteString(" is translucent but no backdrop base color was supplied; refusing to assume black")
	return b.String()
}

func (e *MissingBaseError) Unwrap() error { return ErrMissingBase }

// InvalidColorError describes why a color value was rejected.
type InvalidColorError struct {
	Input  string
	Reason string
}

func (e *InvalidColorError) Error() string {
	return "contrast: invalid color " + strconv.Quote(e.Input) + ": " + e.Reason
}

func (e *InvalidColorError) Unwrap() error { return ErrInvalidColor }

// UnknownRoleError identifies the missing role.
type UnknownRoleError struct {
	Role Role
}

func (e *UnknownRoleError) Error() string {
	return "contrast: semantic role " + string(e.Role) + " is not defined in the theme"
}

func (e *UnknownRoleError) Unwrap() error { return ErrUnknownRole }

// FailedCombo identifies one role combination that derivation could not
// bring to the required threshold, with the measured scores.
type FailedCombo struct {
	// Name echoes the combo identifier when present.
	Name string
	// Foreground and Background name the offending semantic roles.
	Foreground Role
	Background Role
	// UsageKey is the usage that demanded the unmet score.
	UsageKey string
	// Score is what each model measured after all adjustment attempts.
	Score map[ModelID]float64
	// Required is what each model demanded.
	Required map[ModelID]float64
}

func (f FailedCombo) label() string {
	if f.Name != "" {
		return f.Name
	}
	return string(f.Foreground) + " on " + string(f.Background)
}

// DerivationFailure implements error and pinpoints every unresolved combo.
type DerivationFailure struct {
	// IterationsReached is true when the iteration budget was exhausted
	// with one or more combos still failing.
	IterationsReached bool
	Unresolved        []FailedCombo
}

func (e *DerivationFailure) Error() string {
	var b strings.Builder
	b.WriteString("contrast: high-contrast derivation cannot satisfy ")
	n := len(e.Unresolved)
	if n == 1 {
		b.WriteString("1 combo: ")
	} else {
		b.WriteString(itoa(n))
		b.WriteString(" combos: ")
	}
	for i, f := range e.Unresolved {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(f.label())
		b.WriteString(" measured")
		first := true
		for id, sc := range f.Score {
			if !first {
				b.WriteByte(',')
			}
			first = false
			b.WriteByte(' ')
			b.WriteString(string(id))
			b.WriteByte('=')
			b.WriteString(formatFloat(sc))
			b.WriteString(" need>=")
			b.WriteString(formatFloat(f.Required[id]))
		}
	}
	return b.String()
}

func (e *DerivationFailure) Unwrap() error { return ErrDerivationImpossible }
