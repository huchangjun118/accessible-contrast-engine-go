package contrast

import (
	"context"
	"math"
)

// DeriveConfig configures high-contrast theme derivation.
type DeriveConfig struct {
	// Model selects the target contrast model (default ModelWCAG21).
	Model Model
	// Usage, when non-empty, overrides the Usage declared on each pair.
	Usage Usage
	// Margin is added on top of the required threshold.
	Margin float64
	// MaxIterations bounds the adjustment search (default 200).
	MaxIterations int
	// Base is mandatory when the input roles contain translucency.
	Base *RGB
	// GamutMethod defaults to GamutMapOKLCh.
	GamutMethod GamutMappingMethod
}

// Adjustment records what derivation changed for one role, expressed in
// OKLCh so it is clear the hue was preserved.
type Adjustment struct {
	Role              string
	HueDegrees        float64
	OriginalLightness float64
	FinalLightness    float64
	OriginalChroma    float64
	FinalChroma       float64
}

// PairFailure locates a role combination that could not be brought to
// compliance and explains why.
type PairFailure struct {
	Pair     RolePair
	Achieved float64
	Required float64
	Reason   string
}

// DeriveResult is the outcome of a derivation attempt.
type DeriveResult struct {
	// Tokens maps every input role to its derived opaque, in-gamut color.
	Tokens      map[string]RGB
	Adjustments []Adjustment
	Failures    []PairFailure
	OK          bool
}

type deriveRole struct {
	name         string
	l, c, h      float64 // OKLCh; h is held constant
	origL, origC float64
	rgb          RGB
}

// DeriveHighContrast adjusts lightness and chroma (never hue) of the
// given roles in OKLCh until every pair satisfies the target under the
// chosen model, while preserving each role's brand hue and the relative
// lightness ordering of the roles. Honors ctx cancellation.
func DeriveHighContrast(ctx context.Context, roles RoleMapping, pairs []RolePair, cfg DeriveConfig) (DeriveResult, error) {
	if cfg.Model == "" {
		cfg.Model = ModelWCAG21
	}
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = 200
	}
	thresholds := DefaultThresholds()
	th, ok := thresholds[cfg.Model]
	if !ok {
		return DeriveResult{}, errUnknownModel(cfg.Model)
	}

	// Resolve each role to an opaque in-gamut starting color, honoring
	// the same ordered-compositing and base rules as Evaluate.
	dr := make(map[string]*deriveRole, len(roles))
	names := make([]string, 0, len(roles))
	for name, col := range roles {
		names = append(names, name)
		if cfg.Base == nil && col.IsTranslucent() {
			return DeriveResult{}, ErrMissingBase
		}
		surface := col
		if cfg.Base != nil {
			surface = sourceOver(col, Color{RGB: *cfg.Base, Alpha: 1})
		}
		mapped := mapToGamutWith(surface.RGB, cfg.GamutMethod).Mapped
		lin := srgbToLinear(mapped)
		L, a, b := linearSRGBToOKLab(lin)
		c, hue := math.Hypot(a, b), math.Atan2(b, a)
		dr[name] = &deriveRole{name: name, l: L, c: c, h: hue, origL: L, origC: c, rgb: mapped}
	}
	// Stable role order for reproducible iteration/output.
	sortStrings(names)

	// Validate pairs up front.
	for _, p := range pairs {
		if _, ok := dr[p.Foreground]; !ok {
			return DeriveResult{}, errUnknownRole(p.Foreground)
		}
		if _, ok := dr[p.Background]; !ok {
			return DeriveResult{}, errUnknownRole(p.Background)
		}
	}

	// Sorted by original lightness for hierarchy projection.
	byL := append([]string(nil), names...)
	sortSliceBy(byL, func(i, j int) bool {
		ai, bi := dr[byL[i]], dr[byL[j]]
		if ai.origL != bi.origL {
			return ai.origL < bi.origL
		}
		return ai.name < bi.name
	})

	const (
		lMin    = 0.005
		lMax    = 0.995
		lEps    = 1e-4 // roles closer than this have no strict ordering
		stopEps = 1e-4 // no meaningful lightness movement left
	)

	// render converts the current OKLCh state to gamut-mapped sRGB.
	render := func() {
		for _, n := range names {
			st := dr[n]
			lin := okLabToLinearSRGB(oklchToOKLab(st.l, st.c, st.h))
			st.rgb = mapToGamutWith(linearToSRGB(lin), cfg.GamutMethod).Mapped
		}
	}

	// magnitude returns the unsigned contrast magnitude of one pair.
	magnitude := func(p RolePair) float64 {
		fg, bg := dr[p.Foreground].rgb, dr[p.Background].rgb
		switch cfg.Model {
		case ModelWCAG21:
			return wcagRatio(fg, bg)
		case ModelAPCA:
			return math.Abs(apcaContrast(fg, bg))
		default:
			return 0
		}
	}
	required := func(p RolePair) float64 {
		u := p.Usage
		if cfg.Usage != "" {
			u = cfg.Usage
		}
		return th.thresholdFor(u) + cfg.Margin
	}

	// projectHierarchy enforces origL order among roles that had a
	// strict original ordering; originally equal roles may separate.
	projectHierarchy := func() {
		for pass := 0; pass < 32; pass++ {
			changed := false
			for i := 0; i+1 < len(byL); i++ {
				a, b := dr[byL[i]], dr[byL[i+1]]
				if a.origL < b.origL-lEps && a.l > b.l {
					avg := (a.l + b.l) / 2
					a.l, b.l = avg, avg
					changed = true
				}
			}
			if !changed {
				break
			}
		}
	}

	stalledIterations := 0
	var lastMinMargin float64
	for iter := 0; iter < cfg.MaxIterations; iter++ {
		if err := ctx.Err(); err != nil {
			return DeriveResult{}, err
		}
		render()

		allPass := true
		type deficit struct {
			p   RolePair
			req float64
			ach float64
		}
		var failing []deficit
		for _, p := range pairs {
			req := required(p)
			ach := magnitude(p)
			if ach < req {
				allPass = false
				failing = append(failing, deficit{p, req, ach})
			}
		}
		if allPass {
			return buildDeriveResult(dr, names, nil), nil
		}

		// Track whether lightness moves can still help.
		minMargin := math.MaxFloat64
		for _, d := range failing {
			if m := d.req - d.ach; m < minMargin {
				minMargin = m
			}
		}
		if math.Abs(minMargin-lastMinMargin) < 1e-5 {
			stalledIterations++
		} else {
			stalledIterations = 0
		}
		lastMinMargin = minMargin

		// Accumulate lightness moves per role. Move the endpoints of each
		// failing pair apart along their original polarity.
		moves := make(map[string]float64)
		for _, d := range failing {
			fg, bg := dr[d.p.Foreground], dr[d.p.Background]
			gap := (d.req - d.ach) / d.req
			if gap < 0 {
				gap = 0
			}
			step := 0.015 + 0.05*gap
			if step > 0.08 {
				step = 0.08
			}
			if fg.origL <= bg.origL { // dark foreground on light background
				moves[fg.name] -= step
				moves[bg.name] += step
			} else { // light foreground on dark background
				moves[fg.name] += step
				moves[bg.name] -= step
			}
		}
		moved := false
		for n, mv := range moves {
			st := dr[n]
			nl := st.l + mv
			if nl < lMin {
				nl = lMin
			}
			if nl > lMax {
				nl = lMax
			}
			if math.Abs(nl-st.l) > stopEps {
				moved = true
			}
			st.l = nl
		}
		projectHierarchy()

		// Secondary lever: when lightness progress stalls, monotonically
		// reduce chroma of endpoints of still-failing pairs.
		if stalledIterations >= 3 || !moved {
			for _, d := range failing {
				for _, n := range []string{d.p.Foreground, d.p.Background} {
					st := dr[n]
					st.c *= 0.8
					if st.c < 1e-4 {
						st.c = 0
					}
				}
			}
		}
	}

	// Exhausted the iteration budget without satisfying every pair:
	// report precisely which combinations fail and why.
	render()
	failures := make([]PairFailure, 0)
	for _, p := range pairs {
		req := required(p)
		ach := magnitude(p)
		if ach < req {
			reason := "max iterations reached; lightness bounds and role lightness hierarchy prevent further separation"
			if lightnessSaturated(dr, pairs) {
				reason = "endpoints are at the lightness limits while the role lightness hierarchy is preserved; the target threshold is infeasible"
			}
			failures = append(failures, PairFailure{Pair: p, Achieved: round2(ach), Required: round2(req), Reason: reason})
		}
	}
	return buildDeriveResult(dr, names, failures), nil
}

func lightnessSaturated(dr map[string]*deriveRole, pairs []RolePair) bool {
	const edge = 0.02
	for _, p := range pairs {
		fg, bg := dr[p.Foreground], dr[p.Background]
		if fg.origL <= bg.origL {
			if fg.l > 0.005+edge && bg.l < 0.995-edge {
				return false
			}
		} else {
			if fg.l < 0.995-edge && bg.l > 0.005+edge {
				return false
			}
		}
	}
	return true
}

func buildDeriveResult(dr map[string]*deriveRole, names []string, failures []PairFailure) DeriveResult {
	tokens := make(map[string]RGB, len(names))
	adjustments := make([]Adjustment, 0, len(names))
	for _, n := range names {
		st := dr[n]
		tokens[n] = st.rgb
		adjustments = append(adjustments, Adjustment{
			Role:              n,
			HueDegrees:        normalizeDeg(st.h * 180 / math.Pi),
			OriginalLightness: round4(st.origL),
			FinalLightness:    round4(st.l),
			OriginalChroma:    round4(st.origC),
			FinalChroma:       round4(st.c),
		})
	}
	return DeriveResult{Tokens: tokens, Adjustments: adjustments, Failures: failures, OK: len(failures) == 0}
}

func round2(x float64) float64 { return math.Round(x*100) / 100 }
func round4(x float64) float64 { return math.Round(x*10000) / 10000 }

func normalizeDeg(d float64) float64 {
	d = math.Mod(d, 360)
	if d < 0 {
		d += 360
	}
	return round2(d)
}
