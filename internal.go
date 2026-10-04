package contrast

import "sync/atomic"

// atomicSnapshot wraps the single atomically-swapped committed snapshot.
// Reads load a *Snapshot pointer without taking locks; a replacement
// publishes a brand-new immutable Snapshot with one Store, so in-flight
// queries see either the old or the new version, never a mixture.
type atomicSnapshot struct {
	p atomic.Pointer[Snapshot]
}

// cloneRoles returns a shallow-copied role map. Color/RGB values are
// value types so the copy is fully independent.
func cloneRoles(roles RoleMapping) RoleMapping {
	out := make(RoleMapping, len(roles))
	for k, v := range roles {
		out[k] = v
	}
	return out
}

// clonePairs returns an independent copy of the pair list.
func clonePairs(pairs []RolePair) []RolePair {
	out := make([]RolePair, len(pairs))
	copy(out, pairs)
	return out
}

// validatePairs rejects empty role references and duplicate pair names so
// reports/merges stay unambiguous. Nameless pairs are given synthesized
// stable names rather than rejected.
func validatePairs(pairs []RolePair) error {
	seen := make(map[string]struct{}, len(pairs))
	for i := range pairs {
		p := &pairs[i]
		if p.Foreground == "" || p.Background == "" {
			return errEmptyPairRoles(p)
		}
		if p.Name == "" {
			p.Name = p.Foreground + "@" + p.Background
		}
		if _, dup := seen[p.Name]; dup {
			return errDuplicatePair(p.Name)
		}
		seen[p.Name] = struct{}{}
	}
	return nil
}
