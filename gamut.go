package contrast

import (
	"math"
)

// GamutMappingMethod selects how an out-of-gamut RGB is projected back
// into the displayable sRGB cube.
type GamutMappingMethod string

const (
	// GamutMapOKLCh reduces chroma in OKLCh at constant hue and lightness
	// until the color falls inside sRGB. This is the default: it preserves
	// hue better than naive channel clipping.
	GamutMapOKLCh GamutMappingMethod = "oklch-chroma-reduction"

	// GamutMapClip naively clamps each channel into [0,1]. Deterministic
	// but hue-shifting; offered mainly for comparison.
	GamutMapClip GamutMappingMethod = "channel-clip"
)

// GamutMapResult explains one gamut projection.
type GamutMapResult struct {
	Original RGB
	Mapped   RGB
	// InGamut reports whether Original was already displayable as-is.
	InGamut bool
	Method  GamutMappingMethod
	// Iterations is the number of search steps taken (0 when InGamut).
	Iterations int
}

// epsilon/gamut boundary tolerance used while searching.
const gamutEps = 1e-7

// MapToGamut projects c into the sRGB gamut using the default method
// (OKLCh chroma reduction). The projection is deterministic: the same
// input always yields the same mapped value, and the mapping never
// rewrites the input token's semantics outside accessibility
// evaluation/display.
func MapToGamut(c RGB) GamutMapResult {
	return mapToGamutWith(c, GamutMapOKLCh)
}

// mapToGamutWith projects c using the given method.
func mapToGamutWith(c RGB, method GamutMappingMethod) GamutMapResult {
	switch method {
	case GamutMapClip:
		return GamutMapResult{Original: c, Mapped: clipGamut(c), InGamut: inGamut(c), Method: method}
	case GamutMapOKLCh, "":
		return mapOKLCh(c)
	default:
		// Unknown methods fall back deterministically to the default.
		return mapOKLCh(c)
	}
}

// inGamut reports whether every channel lies in [0,1] and is finite.
func inGamut(c RGB) bool {
	return finiteInUnit(c.R) && finiteInUnit(c.G) && finiteInUnit(c.B)
}

func finiteInUnit(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) && x >= 0 && x <= 1 }

// clipGamut clamps channels to [0,1]. Non-finite values follow the fixed
// rule NaN -> 0, +Inf -> 1, -Inf -> 0.
func clipGamut(c RGB) RGB {
	return RGB{R: clampChannel(c.R), G: clampChannel(c.G), B: clampChannel(c.B)}
}

func clampChannel(x float64) float64 {
	switch {
	case math.IsNaN(x):
		return 0
	case math.IsInf(x, 1):
		return 1
	case math.IsInf(x, -1):
		return 0
	case x < 0:
		return 0
	case x > 1:
		return 1
	}
	return x
}

// mapOKLCh projects c by binary-searching the maximum chroma in OKLCh at
// constant hue/lightness that still yields an in-gamut sRGB color.
func mapOKLCh(c RGB) GamutMapResult {
	res := GamutMapResult{Original: c, Method: GamutMapOKLCh}

	// Non-finite channels cannot participate in color math. Apply the
	// fixed sanitization rule first, then continue the normal projection
	// so the outcome stays reproducible.
	src := sanitizeNonFinite(c)
	if inGamut(src) {
		res.Mapped = src
		res.InGamut = true
		return res
	}

	lin := srgbToLinear(src)
	L, a, b := linearSRGBToOKLab(lin)
	// Clamp lightness to [0,1]; with chroma 0 the gray ramp is in gamut.
	if L < 0 {
		L = 0
	} else if L > 1 {
		L = 1
	}
	hue := math.Atan2(b, a)
	chroma := math.Hypot(a, b)

	// Candidate with chroma k.
	candidate := func(k float64) RGB {
		aa := k * math.Cos(hue)
		bb := k * math.Sin(hue)
		return linearToSRGB(okLabToLinearSRGB(L, aa, bb))
	}

	// lo = 0 is guaranteed in gamut (the OKLab gray ramp maps to sRGB
	// r=g=b in [0,1]); hi = original chroma is out of gamut.
	lo, hi := 0.0, chroma
	iters := 0
	if inGamut(candidate(hi)) {
		// Can happen after lightness clamping: original chroma fits.
		m := candidate(hi)
		res.Mapped, res.Iterations = clipGamut(m), iters
		return res
	}
	for i := 0; i < 64; i++ {
		iters++
		mid := (lo + hi) / 2
		if inGamut(candidate(mid)) {
			lo = mid
		} else {
			hi = mid
		}
		if hi-lo < gamutEps {
			break
		}
	}
	mapped := candidate(lo)
	// Numeric guard: the search boundary can sit a hair outside [0,1].
	mapped = clipGamut(mapped)
	res.Mapped = mapped
	res.Iterations = iters
	return res
}

func sanitizeNonFinite(c RGB) RGB {
	return RGB{R: sanitizeChannel(c.R), G: sanitizeChannel(c.G), B: sanitizeChannel(c.B)}
}

func sanitizeChannel(x float64) float64 {
	switch {
	case math.IsNaN(x):
		return 0
	case math.IsInf(x, 1):
		return 1.5 // out of gamut but finite; normal mapping takes over
	case math.IsInf(x, -1):
		return -0.5
	}
	return x
}

// ---- sRGB transfer functions (defined for values outside [0,1] too) ----

func srgbChannelToLinear(x float64) float64 {
	if x <= 0.04045 {
		return x / 12.92
	}
	return math.Pow((x+0.055)/1.055, 2.4)
}

func linearChannelToSRGB(x float64) float64 {
	if x <= 0.0031308 {
		return 12.92 * x
	}
	return 1.055*math.Pow(x, 1/2.4) - 0.055
}

func srgbToLinear(c RGB) RGB {
	return RGB{R: srgbChannelToLinear(c.R), G: srgbChannelToLinear(c.G), B: srgbChannelToLinear(c.B)}
}

func linearToSRGB(c RGB) RGB {
	return RGB{R: linearChannelToSRGB(c.R), G: linearChannelToSRGB(c.G), B: linearChannelToSRGB(c.B)}
}

// ---- OKLab <-> linear sRGB (W3C CSS Color 4 matrices) ----

func linearSRGBToOKLab(c RGB) (L, a, b float64) {
	l := 0.4122214708*c.R + 0.5363325363*c.G + 0.0514459929*c.B
	m := 0.2119034982*c.R + 0.6806995451*c.G + 0.1073969566*c.B
	s := 0.0883024619*c.R + 0.2817188376*c.G + 0.6299787005*c.B
	lc, mc, sc := math.Cbrt(l), math.Cbrt(m), math.Cbrt(s)
	L = 0.2104542553*lc + 0.7936177850*mc - 0.0040720468*sc
	a = 1.9779984951*lc - 2.4285922050*mc + 0.4505937099*sc
	b = 0.0259040371*lc + 0.7827717662*mc - 0.8086757660*sc
	return
}

func okLabToLinearSRGB(L, a, b float64) RGB {
	lc := L + 0.3963377774*a + 0.2158037573*b
	mc := L - 0.1055613458*a - 0.0638541728*b
	sc := L - 0.0894841775*a - 1.2914855480*b
	l, m, s := lc*lc*lc, mc*mc*mc, sc*sc*sc
	return RGB{
		R: 4.0767416621*l - 3.3077115913*m + 0.2309699292*s,
		G: -1.2684380046*l + 2.6097574011*m - 0.3413193965*s,
		B: -0.0041960863*l - 0.7034186147*m + 1.7076147010*s,
	}
}

// okLabToOKLCh converts Cartesian OKLab to cylindrical OKLCh. Hue is in
// radians in (-pi, pi]; when chroma is 0 hue is 0.
func okLabToOKLCh(L, a, b float64) (l, c, h float64) {
	return L, math.Hypot(a, b), math.Atan2(b, a)
}

// oklchToOKLab is the inverse of okLabToOKLCh; h in radians.
func oklchToOKLab(L, c, h float64) (l, a, b float64) {
	return L, c * math.Cos(h), c * math.Sin(h)
}
