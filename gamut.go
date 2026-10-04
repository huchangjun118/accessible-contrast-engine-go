package contrast

import "math"

// GamutMapping is the reproducible record of one out-of-gamut correction.
// Hue and lightness are preserved; chroma is reduced (in OKLCH) until the
// color fits inside the sRGB display gamut.
type GamutMapping struct {
	// Input is the raw color (possibly with channels outside [0,1]).
	Input Color
	// Mapped is the in-gamut sRGB color used for accessibility judgments.
	Mapped Color
	// Method identifies the deterministic mapping rule.
	Method string
	// Hue is the preserved OKLCH hue in radians.
	Hue float64
	// Lightness is the preserved OKLCH lightness in [0,1].
	Lightness float64
	// InitialChroma / FinalChroma bracket the chroma reduction.
	InitialChroma float64
	FinalChroma   float64
	// Changed reports whether any mapping was actually needed.
	Changed bool
}

// gamutMapMethod is the stable name recorded in every mapping.
const gamutMapMethod = "oklch-chroma-reduction-bsearch-v1"

// MapToGamut maps any color (including R/G/B outside [0,1], or alpha
// outside [0,1]) back into the sRGB gamut using one fixed rule:
//
//  1. alpha is clamped to [0,1] (alpha is not a colorimetric channel);
//  2. the sRGB color is converted to linear sRGB -> XYZ(D65) -> OKLab ->
//     OKLCH;
//  3. if the sRGB encoding already falls in [0,1] for all channels the
//     color passes through unchanged;
//  4. otherwise chroma C is reduced by binary search (up to 64 steps)
//     while hue H and lightness L are held constant, until the sRGB
//     encoding is within a fixed epsilon of the gamut cube.
//
// The rule is total and deterministic so the same out-of-gamut input
// always maps to the same output. The color's semantic role and hue are
// untouched; only the light-perceptual chroma needed for a displayable
// value changes.
func MapToGamut(c Color) (Color, GamutMapping) {
	in := Color{R: c.R, G: c.G, B: c.B, A: clamp01(c.A)}
	m := GamutMapping{
		Input:  Color{R: c.R, G: c.G, B: c.B, A: c.A},
		Method: gamutMapMethod,
	}

	l, a, b := srgbToOKLab(in)
	h := math.Atan2(b, a)
	if h < 0 {
		h += 2 * math.Pi
	}
	C0 := math.Sqrt(a*a + b*b)
	m.Hue = h
	m.Lightness = l
	m.InitialChroma = C0

	if inGamut(in) {
		m.Mapped = in
		m.FinalChroma = C0
		m.Changed = false
		return in, m
	}

	// Binary search the largest chroma in [0, C0] that stays in gamut.
	lo, hi := 0.0, C0
	var mapped Color
	for i := 0; i < 64; i++ {
		mid := (lo + hi) / 2
		cand := oklchToSrgb(l, mid, h, in.A)
		if inGamut(cand) {
			lo = mid
			mapped = cand
		} else {
			hi = mid
		}
	}
	if mapped.A == 0 && lo == 0 {
		// lo==0 guarantees achromatic in-gamut color.
		mapped = oklchToSrgb(l, 0, h, in.A)
	}
	// Final safety: snap any residual epsilon excursion.
	mapped = snapToGamut(mapped)
	m.Mapped = mapped
	m.FinalChroma = lo
	m.Changed = true
	return mapped, m
}

// inGamut reports whether the encoded sRGB channels are inside [0,1].
func inGamut(c Color) bool {
	const eps = 1e-7
	return c.R >= -eps && c.R <= 1+eps &&
		c.G >= -eps && c.G <= 1+eps &&
		c.B >= -eps && c.B <= 1+eps
}

// snapToGamut clamps residual epsilon out-of-range values onto the cube.
func snapToGamut(c Color) Color {
	return Color{R: clamp01(c.R), G: clamp01(c.G), B: clamp01(c.B), A: clamp01(c.A)}
}

// ---- color space conversions ------------------------------------------
// All matrix constants follow the published sRGB / OKLab references and
// are shared by gamut mapping and high-contrast derivation so the two
// stages cannot disagree about what a color "is".

func srgbLinToXYZ(r, g, b float64) (x, y, z float64) {
	// linear sRGB -> XYZ D65.
	x = 0.4124564*r + 0.3575761*g + 0.1804375*b
	y = 0.2126729*r + 0.7151522*g + 0.0721750*b
	z = 0.0193339*r + 0.1191920*g + 0.9503041*b
	return
}

func xyzToSrgbLin(x, y, z float64) (r, g, b float64) {
	r = 3.2404542*x + -1.5371385*y + -0.4985314*z
	g = -0.9692660*x + 1.8760108*y + 0.0415560*z
	b = 0.0556434*x + -0.2040259*y + 1.0572252*z
	return
}

// srgbToOKLab converts an encoded sRGB Color to OKLab (L in [0,1] approx).
func srgbToOKLab(c Color) (l, a, b float64) {
	r, g, bl := linearize(c.R), linearize(c.G), linearize(c.B)
	x, y, z := srgbLinToXYZ(r, g, bl)
	l_ := 0.8189330101*x + 0.3618667424*y - 0.1288597137*z
	m_ := 0.0329845436*x + 0.9293118715*y + 0.0361456387*z
	s_ := 0.0482003018*x + 0.2643662691*y + 0.6338517070*z
	lc := math.Cbrt(l_)
	mc := math.Cbrt(m_)
	sc := math.Cbrt(s_)
	l = 0.2104542553*lc + 0.7936177850*mc - 0.0040720468*sc
	a = 1.9779984951*lc - 2.4285922050*mc + 0.4505937099*sc
	b = 0.0259040371*lc + 0.7827717662*mc - 0.8086757660*sc
	return
}

// oklabToSrgb converts OKLab back to encoded sRGB.
func oklabToSrgb(L, a, b float64) Color {
	lc := L + 0.3963377774*a + 0.2158037573*b
	mc := L - 0.1055613458*a - 0.0638541728*b
	sc := L - 0.0894841775*a - 1.2914855480*b
	l3 := lc * lc * lc
	m3 := mc * mc * mc
	s3 := sc * sc * sc
	x := 1.2270138511*l3 - 0.5577999807*m3 + 0.2812561490*s3
	y := -0.0405801784*l3 + 1.1122568696*m3 - 0.0716766787*s3
	z := -0.0763812845*l3 - 0.4214819784*m3 + 1.5861632204*s3
	r, g, b2 := xyzToSrgbLin(x, y, z)
	return Color{R: encodize(r), G: encodize(g), B: encodize(b2), A: 1}
}

// srgbToOKLCH convenience: returns lightness, chroma, hue(rad).
func srgbToOKLCH(c Color) (l, C, h float64) {
	l, a, b := srgbToOKLab(c)
	C = math.Hypot(a, b)
	h = math.Atan2(b, a)
	if h < 0 {
		h += 2 * math.Pi
	}
	return
}

// oklchToSrgb converts OKLCH to encoded sRGB with the given alpha.
func oklchToSrgb(L, C, h, alpha float64) Color {
	a := C * math.Cos(h)
	b := C * math.Sin(h)
	c := oklabToSrgb(L, a, b)
	c.A = alpha
	return c
}
