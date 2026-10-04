package contrast

import "errors"

// ErrMissingBase is returned when a foreground/background pair contains
// translucency but no explicit base surface was supplied. The engine
// never substitutes an implicit black (or white) base.
var ErrMissingBase = errors.New("contrast: translucent foreground/background requires an explicit base color")

// CompositeStep records one ordered source-over compositing operation so
// that the resulting displayed color is fully explainable.
type CompositeStep struct {
	Label  string
	Source Color
	Dest   Color
	Output Color
}

// CompositedColor is the definite, opaque displayed color together with
// the ordered compositing trace that produced it.
type CompositedColor struct {
	Color Color
	Steps []CompositeStep
}

// sourceOver composites src over dst using the straight-alpha
// source-over formula:
//
//	outA = srcA + dstA*(1-srcA)
//	outC = (srcC*srcA + dstC*dstA*(1-srcA)) / outA
//
// When dst is fully opaque outA is 1 and the division collapses away.
func sourceOver(src, dst Color) Color {
	if src.Alpha >= 1 {
		return Color{RGB: src.RGB, Alpha: 1}
	}
	outA := src.Alpha + dst.Alpha*(1-src.Alpha)
	if outA <= 0 {
		return Color{RGB: RGB{}, Alpha: 0}
	}
	w := dst.Alpha * (1 - src.Alpha)
	return Color{
		RGB: RGB{
			R: (src.RGB.R*src.Alpha + dst.RGB.R*w) / outA,
			G: (src.RGB.G*src.Alpha + dst.RGB.G*w) / outA,
			B: (src.RGB.B*src.Alpha + dst.RGB.B*w) / outA,
		},
		Alpha: outA,
	}
}

// CompositeOver composites fg over bg over the explicit, opaque base,
// applying the standard source-over formula in that fixed order.
//
// If either fg or bg is translucent and base is nil, ErrMissingBase is
// returned (the engine never assumes an implicit black base). The base
// itself, when provided, is treated as an opaque surface.
func CompositeOver(fg, bg Color, base *RGB) (CompositedColor, error) {
	if base == nil && (fg.IsTranslucent() || bg.IsTranslucent()) {
		return CompositedColor{}, ErrMissingBase
	}

	var steps []CompositeStep
	var surface Color

	if base != nil {
		// Step 1: background laid over the explicit base.
		bgOver := sourceOver(bg, Color{RGB: *base, Alpha: 1})
		steps = append(steps, CompositeStep{
			Label: "background over base", Source: bg,
			Dest: Color{RGB: *base, Alpha: 1}, Output: bgOver,
		})
		surface = bgOver
	} else {
		// Both layers are opaque; no base is required.
		surface = bg
	}

	// Step 2: foreground laid over the resolved background surface.
	final := sourceOver(fg, surface)
	steps = append(steps, CompositeStep{
		Label: "foreground over background", Source: fg,
		Dest: surface, Output: final,
	})

	return CompositedColor{Color: final, Steps: steps}, nil
}
