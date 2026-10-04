package contrast

// RoleBound optionally pins a role's OKLCH lightness to a range so
// derivation cannot break its place in the visual hierarchy.
type RoleBound struct {
	MinL float64
	MaxL float64
}

// DeriveOptions controls high-contrast theme derivation.
type DeriveOptions struct {
	// Models judged while adjusting; defaults to DefaultModels().
	Models []Model
	// Base/BaseSet backdrop, identical semantics to EvalConfig.
	Base    Color
	BaseSet bool
	// Bounds optionally constrain per-role lightness.
	Bounds map[Role]RoleBound
	// PreserveDarkToLight lists groups of roles whose relative lightness
	// ordering must be preserved (earlier roles stay no lighter than later
	// roles); the brand hue and the role hierarchy are therefore never
	// broken.
	PreserveDarkToLight [][]Role
	// MaxIterations caps the adjustment loop (default 256).
	MaxIterations int
	// LightStep / ChromaStep are the per-iteration adjustment increments.
	LightStep  float64
	ChromaStep float64
}

// Adjustment records how one role's color was changed in OKLCH terms.
type Adjustment struct {
	From   Color
	To     Color
	Hue    float64
	DeltaL float64
	DeltaC float64
}

// DerivedTheme is a high-contrast variant plus an audit trail.
type DerivedTheme struct {
	Theme       *Theme
	Iterations  int
	Adjustments map[Role]Adjustment
}

// DeriveHighContrast derives a variant by adjusting OKLCH lightness and
// chroma (hue held fixed per role) until every combo passes every model.
//
// The loop works in perceptual OKLCH: hue never moves (brand hue is
// preserved), the foreground/background lightness gap is widened in the
// polarity that fails, and chroma is reduced when lightness alone stalls.
// Per-role lightness bounds and explicit dark-to-light orderings keep the
// role hierarchy intact. When convergence is impossible, the returned
// error wraps ErrDerivationImpossible and is a *DerivationFailure that
// pinpoints each unresolved combo with its measured vs required score.
func DeriveHighContrast(base *Theme, combos []Combo, opts DeriveOptions) (*DerivedTheme, error) {
	if base == nil {
		return nil, &InvalidColorError{Input: "", Reason: "nil base theme"}
	}
	if opts.MaxIterations <= 0 {
		opts.MaxIterations = 256
	}
	if opts.LightStep <= 0 {
		opts.LightStep = 0.01
	}
	if opts.ChromaStep <= 0 {
		opts.ChromaStep = 0.01
	}
	models := opts.Models
	if len(models) == 0 {
		models = DefaultModels()
	}
	cfg := EvalConfig{Models: models, Base: opts.Base, BaseSet: opts.BaseSet}

	// Work on a private copy so the source theme is never mutated.
	work := NewTheme(base.Name, base.Tokens)

	// Keep the starting OKLCH state per role; hue is locked for each role
	// involved in a combo.
	orig := map[Role]Color{}
	origL := map[Role]float64{}
	origC := map[Role]float64{}
	stateL := map[Role]float64{}
	stateC := map[Role]float64{}
	stateH := map[Role]float64{}
	stateAlpha := map[Role]float64{}
	lock := map[Role]struct{}{}
	for _, cb := range combos {
		for _, r := range [2]Role{cb.Foreground, cb.Background} {
			if _, seen := lock[r]; seen {
				continue
			}
			col, ok := work.Get(r)
			if !ok {
				return nil, &UnknownRoleError{Role: r}
			}
			mapped, _ := MapToGamut(col)
			l, C, h := srgbToOKLCH(mapped)
			orig[r] = mapped
			origL[r], origC[r] = l, C
			stateL[r], stateC[r], stateH[r] = l, C, h
			stateAlpha[r] = col.A
			lock[r] = struct{}{}
			work.Tokens[r] = mapped
		}
	}

	audit := map[Role]Adjustment{}
	iter := 0
	for ; iter < opts.MaxIterations; iter++ {
		rep, err := Evaluate(work, combos, cfg)
		if err != nil {
			return nil, err
		}
		failed := failingCombos(rep, models)
		if len(failed) == 0 {
			return &DerivedTheme{
				Theme:       work,
				Iterations:  iter,
				Adjustments: audit,
			}, nil
		}

		moved := false
		for _, ci := range failed {
			cr := rep.Combos[ci]
			cb := cr.Combo
			fgL, bgL := stateL[cb.Foreground], stateL[cb.Background]

			// Respect the brand's established polarity: if the foreground
			// is already lighter, widen toward light-on-dark; if darker,
			// widen toward dark-on-light. Only when the two are nearly
			// equal do we pick the direction with more reachable room, so
			// the role hierarchy and visual polarity are never inverted.
			stepL := opts.LightStep
			switch {
			case fgL >= bgL+1e-9:
				// light foreground on dark background: push fg up / bg down
				if nudgeLightness(cb.Foreground, +stepL, stateL, stateC, stateH, stateAlpha, opts, work, audit, orig, origL, origC) {
					moved = true
					continue
				}
				if nudgeLightness(cb.Background, -stepL, stateL, stateC, stateH, stateAlpha, opts, work, audit, orig, origL, origC) {
					moved = true
					continue
				}
			case fgL <= bgL-1e-9:
				// dark foreground on light background: push fg down / bg up
				if nudgeLightness(cb.Foreground, -stepL, stateL, stateC, stateH, stateAlpha, opts, work, audit, orig, origL, origC) {
					moved = true
					continue
				}
				if nudgeLightness(cb.Background, +stepL, stateL, stateC, stateH, stateAlpha, opts, work, audit, orig, origL, origC) {
					moved = true
					continue
				}
			default:
				// near-equal: choose the polarity with the most room
				roomUp := roleRoomUp(cb.Foreground, stateL, opts, work)
				roomDown := roleRoomDown(cb.Background, stateL, opts, work)
				if roomUp >= roomDown {
					if nudgeLightness(cb.Foreground, +stepL, stateL, stateC, stateH, stateAlpha, opts, work, audit, orig, origL, origC) {
						moved = true
						continue
					}
					if nudgeLightness(cb.Background, -stepL, stateL, stateC, stateH, stateAlpha, opts, work, audit, orig, origL, origC) {
						moved = true
						continue
					}
				} else {
					if nudgeLightness(cb.Background, -stepL, stateL, stateC, stateH, stateAlpha, opts, work, audit, orig, origL, origC) {
						moved = true
						continue
					}
					if nudgeLightness(cb.Foreground, +stepL, stateL, stateC, stateH, stateAlpha, opts, work, audit, orig, origL, origC) {
						moved = true
						continue
					}
				}
			}

			// Lightness is blocked by bounds/order: reduce chroma, which
			// typically raises perceptual contrast at fixed hue/L.
			stepC := opts.ChromaStep
			if nudgeChroma(cb.Foreground, -stepC, stateL, stateC, stateH, stateAlpha, work, audit, orig, origL, origC) {
				moved = true
				continue
			}
			if nudgeChroma(cb.Background, -stepC, stateL, stateC, stateH, stateAlpha, work, audit, orig, origL, origC) {
				moved = true
			}
		}
		if !moved {
			// Nothing can move; converge and report the failure precisely.
			iter++
			break
		}
	}

	rep, err := Evaluate(work, combos, cfg)
	if err != nil {
		return nil, err
	}
	failed := failingCombos(rep, models)
	fc := make([]FailedCombo, 0, len(failed))
	for _, ci := range failed {
		cr := rep.Combos[ci]
		f := FailedCombo{
			Name:       cr.Combo.Name,
			Foreground: cr.Combo.Foreground,
			Background: cr.Combo.Background,
			UsageKey:   cr.Combo.Usage.Level,
			Score:      map[ModelID]float64{},
			Required:   map[ModelID]float64{},
		}
		for _, mdl := range models {
			v := cr.Verdicts[mdl.ID()]
			f.Score[mdl.ID()] = v.Score
			f.Required[mdl.ID()] = v.Required
		}
		fc = append(fc, f)
	}
	return &DerivedTheme{
			Theme:       work,
			Iterations:  iter,
			Adjustments: audit,
		}, &DerivationFailure{
			IterationsReached: true,
			Unresolved:        fc,
		}
}

// failingCombos returns indices of combos failing at least one model.
func failingCombos(rep *Report, models []Model) []int {
	var out []int
	for i := range rep.Combos {
		for _, m := range models {
			if !rep.Combos[i].Pass(m.ID()) {
				out = append(out, i)
				break
			}
		}
	}
	return out
}

// roleRoomUp/RoomDown estimate remaining permissible lightness movement.
func roleRoomUp(r Role, l map[Role]float64, opts DeriveOptions, work *Theme) float64 {
	max := 1.0
	if b, ok := opts.Bounds[r]; ok && b.MaxL > 0 {
		max = b.MaxL
	}
	return max - l[r]
}

func roleRoomDown(r Role, l map[Role]float64, opts DeriveOptions, work *Theme) float64 {
	min := 0.0
	if b, ok := opts.Bounds[r]; ok {
		min = b.MinL
	}
	return l[r] - min
}

// nudgeLightness applies a lightness step when bounds and dark-to-light
// ordering remain satisfied, materializing the new sRGB color into work.
func nudgeLightness(
	r Role, d float64,
	stateL, stateC map[Role]float64, stateH map[Role]float64, stateAlpha map[Role]float64,
	opts DeriveOptions, work *Theme,
	audit map[Role]Adjustment, orig map[Role]Color, origL, origC map[Role]float64,
) bool {
	newL := stateL[r] + d
	if newL < 0 || newL > 1 {
		return false
	}
	if b, ok := opts.Bounds[r]; ok {
		if newL < b.MinL || (b.MaxL > 0 && newL > b.MaxL) {
			return false
		}
	}
	if !orderingHolds(r, newL, stateL, opts) {
		return false
	}
	C := stateC[r]
	cand := oklchToSrgb(newL, C, stateH[r], stateAlpha[r])
	mapped, _ := MapToGamut(cand)
	stateL[r] = newL
	work.Tokens[r] = mapped
	recordAdjustment(audit, r, orig[r], mapped, stateH[r], origL, origC, stateL, stateC)
	return true
}

// orderingHolds verifies that assigning role r the candidate lightness
// preserves every configured dark-to-light group.
func orderingHolds(r Role, candidate float64, stateL map[Role]float64, opts DeriveOptions) bool {
	for _, group := range opts.PreserveDarkToLight {
		for i, g := range group {
			if g != r {
				continue
			}
			// Earlier roles must be <= this role (darker); later >=.
			for j := 0; j < i; j++ {
				if ov, ok := stateL[group[j]]; ok && ov-candidate > 1e-9 {
					return false
				}
			}
			for j := i + 1; j < len(group); j++ {
				if ov, ok := stateL[group[j]]; ok && candidate-ov > 1e-9 {
					return false
				}
			}
		}
	}
	return true
}

// nudgeChroma reduces chroma (never below 0); hue and lightness are fixed.
func nudgeChroma(
	r Role, d float64,
	stateL, stateC map[Role]float64, stateH map[Role]float64, stateAlpha map[Role]float64,
	work *Theme, audit map[Role]Adjustment, orig map[Role]Color, origL, origC map[Role]float64,
) bool {
	newC := stateC[r] + d
	if newC < 0 {
		newC = 0
	}
	if newC >= stateC[r]-1e-12 {
		return false
	}
	cand := oklchToSrgb(stateL[r], newC, stateH[r], stateAlpha[r])
	mapped, _ := MapToGamut(cand)
	stateC[r] = newC
	work.Tokens[r] = mapped
	recordAdjustment(audit, r, orig[r], mapped, stateH[r], origL, origC, stateL, stateC)
	return true
}

func recordAdjustment(
	audit map[Role]Adjustment, r Role, from, to Color, h float64,
	origL, origC, stateL, stateC map[Role]float64,
) {
	a := audit[r]
	if _, set := audit[r]; !set {
		a.From = from
	}
	a.To = to
	a.Hue = h
	a.DeltaL = stateL[r] - origL[r]
	a.DeltaC = stateC[r] - origC[r]
	audit[r] = a
}
