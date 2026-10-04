package contrast

import (
	"fmt"
	"sort"
)

// errUnknownModel reports a model the engine does not implement.
func errUnknownModel(m Model) error {
	return fmt.Errorf("contrast: unknown contrast model %q (supported: %q, %q)", m, ModelWCAG21, ModelAPCA)
}

// errUnknownRole reports a role name absent from the mapping.
func errUnknownRole(name string) error {
	return fmt.Errorf("contrast: unknown role %q", name)
}

// sortStrings sorts a string slice in place (deterministic iteration).
func sortStrings(s []string) { sort.Strings(s) }

// sortSliceBy sorts s using the given less function.
func sortSliceBy(s []string, less func(i, j int) bool) { sort.Slice(s, less) }

// errEmptyPairRoles reports a pair missing a role reference.
func errEmptyPairRoles(p *RolePair) error {
	return fmt.Errorf("contrast: pair %q must declare both foreground and background roles", p.Name)
}

// errDuplicatePair reports a non-unique pair name.
func errDuplicatePair(name string) error {
	return fmt.Errorf("contrast: duplicate pair name %q", name)
}
