package contrast

import (
	"math"
	"strconv"
	"strings"
)

// Role is the name of a semantic color token within a theme
// (e.g. "text/primary", "surface/background").
type Role string

// Color is an sRGB-encoded color with a straight (non-premultiplied)
// alpha channel. R/G/B are nominally in [0,1]; values outside that range
// are deliberately accepted here and are normalized deterministically by
// the gamut-mapping stage before any accessibility judgment. A is
// normalized to [0,1] at parse time.
type Color struct {
	R float64
	G float64
	B float64
	A float64
}

// Layer associates a resolved color with the semantic role it came from,
// so compositing traces stay explainable.
type Layer struct {
	Role  Role
	Color Color
}

// CompositeStep records one deterministic compositing operation.
type CompositeStep struct {
	// Role is the semantic token applied at this step.
	Stage string
	// Role is duplicated below for clarity in traces; Role names the token.
	Role Role
	// Source is the (possibly translucent) token color.
	Source Color
	// Backdrop is the color it was composited over.
	Backdrop Color
	// Result is the output of this step.
	Result Color
}

// CompositeTrace explains, step by step, how translucent tokens were
// flattened onto the supplied backdrop.
type CompositeTrace struct {
	// Base is the caller-supplied backdrop (never silently assumed).
	Base Color
	// Steps are in application order (background first, then foreground).
	Steps []CompositeStep
	// FinalBackground and FinalForeground are the opaque colors that
	// were actually compared.
	FinalBackground Color
	FinalForeground Color
}

// ParseHex parses #rgb, #rgba, #rrggbb and #rrggbbaa forms. Hex digits are
// scaled to [0,1]; syntactically invalid input yields InvalidColorError.
func ParseHex(s string) (Color, error) {
	in := s
	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return Color{}, &InvalidColorError{Input: in, Reason: "empty input"}
	}
	if s[0] == '#' {
		s = s[1:]
	}
	var nibbles []byte
	switch len(s) {
	case 3, 4:
		nibbles = make([]byte, 0, len(s))
		for i := 0; i < len(s); i++ {
			v, ok := hexNibble(s[i])
			if !ok {
				return Color{}, &InvalidColorError{Input: in, Reason: "non-hex digit"}
			}
			nibbles = append(nibbles, v, v)
		}
	case 6, 8:
		nibbles = make([]byte, 0, len(s))
		for i := 0; i < len(s); i++ {
			v, ok := hexNibble(s[i])
			if !ok {
				return Color{}, &InvalidColorError{Input: in, Reason: "non-hex digit"}
			}
			nibbles = append(nibbles, v)
		}
	default:
		return Color{}, &InvalidColorError{Input: in, Reason: "expected 3, 4, 6 or 8 hex digits"}
	}
	r := float64(nibbles[0]<<4|nibbles[1]) / 255
	g := float64(nibbles[2]<<4|nibbles[3]) / 255
	b := float64(nibbles[4]<<4|nibbles[5]) / 255
	a := 1.0
	if len(nibbles) == 8 {
		a = float64(nibbles[6]<<4|nibbles[7]) / 255
	}
	return Color{R: r, G: g, B: b, A: a}, nil
}

func hexNibble(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// ParseColor accepts the hex forms of ParseHex plus CSS-like
// rgb(r g b / a), rgb(r,g,b), rgba(r,g,b,a) with numbers in 0..255 and
// alpha as a 0..1 fraction or percentage.
func ParseColor(s string) (Color, error) {
	in := s
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "#") {
		return ParseHex(in)
	}
	lower := strings.ToLower(s)
	isRGB := strings.HasPrefix(lower, "rgb(") || strings.HasPrefix(lower, "rgba(")
	if !isRGB {
		return Color{}, &InvalidColorError{Input: in, Reason: "not a hex color or rgb()/rgba() function"}
	}
	open := strings.IndexByte(s, '(')
	close := strings.LastIndexByte(s, ')')
	if close < open {
		return Color{}, &InvalidColorError{Input: in, Reason: "missing closing parenthesis"}
	}
	body := strings.NewReplacer("/", ",", " ", ",").Replace(s[open+1 : close])
	parts := strings.Split(body, ",")
	cleaned := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			cleaned = append(cleaned, p)
		}
	}
	if len(cleaned) != 3 && len(cleaned) != 4 {
		return Color{}, &InvalidColorError{Input: in, Reason: "expected 3 or 4 channel values"}
	}
	vals := make([]float64, 0, 4)
	pct := make([]bool, 0, 4)
	for _, p := range cleaned {
		v, isPct, err := parseChannel(p)
		if err != nil {
			return Color{}, &InvalidColorError{Input: in, Reason: err.Error()}
		}
		vals = append(vals, v)
		pct = append(pct, isPct)
	}
	scale := func(i int) float64 {
		if pct[i] {
			return clamp01(vals[i] / 100)
		}
		return vals[i] / 255
	}
	c := Color{R: scale(0), G: scale(1), B: scale(2), A: 1}
	if len(vals) == 4 {
		if pct[3] {
			c.A = clamp01(vals[3] / 100)
		} else {
			c.A = clamp01(vals[3])
		}
	}
	return c, nil
}

func parseChannel(p string) (v float64, isPct bool, err error) {
	if strings.HasSuffix(p, "%") {
		isPct = true
		p = p[:len(p)-1]
	}
	f, e := strconv.ParseFloat(p, 64)
	if e != nil {
		return 0, false, errBadChannel
	}
	return f, isPct, nil
}

var errBadChannel = &plainErr{"bad channel value"}

type plainErr struct{ s string }

func (e *plainErr) Error() string { return e.s }

// Hex renders the color as #rrggbbaa (alpha omitted when it equals 1).
// Channels are rounded to 8-bit values; out-of-[0,1] channels are clamped
// so any Color can be rendered after gamut mapping.
func (c Color) Hex() string {
	r := toByte(c.R)
	g := toByte(c.G)
	b := toByte(c.B)
	a := toByte(c.A)
	if c.A >= 1-1e-9 {
		return "#" + hex2(r) + hex2(g) + hex2(b)
	}
	return "#" + hex2(r) + hex2(g) + hex2(b) + hex2(a)
}

func hex2(v byte) string {
	const h = "0123456789abcdef"
	return string([]byte{h[v>>4], h[v&0xf]})
}

func toByte(v float64) byte {
	v = clamp01(v)
	n := int(v*255 + 0.5)
	if n > 255 {
		n = 255
	}
	return byte(n)
}

// IsTranslucent reports whether alpha is below 1.
func (c Color) IsTranslucent() bool { return c.A < 1-1e-9 }

// CompositeOver performs straight-alpha "source over" compositing of fg
// over bg. Channel math is performed in linear-light sRGB (the encoded
// values are linearized first and re-encoded after), which is the
// deterministic documented rule; alpha is combined algebraically.
func CompositeOver(fg, bg Color) Color {
	fa := clamp01(fg.A)
	ba := clamp01(bg.A)
	outA := fa + ba*(1-fa)
	if outA <= 1e-12 {
		return Color{R: 0, G: 0, B: 0, A: 0}
	}
	fr, fgC, fb := linearize(fg.R), linearize(fg.G), linearize(fg.B)
	br, bgc, bb := linearize(bg.R), linearize(bg.G), linearize(bg.B)
	mix := func(f, b float64) float64 {
		return (f*fa + b*ba*(1-fa)) / outA
	}
	return Color{
		R: encodize(mix(fr, br)),
		G: encodize(mix(fgC, bgc)),
		B: encodize(mix(fb, bb)),
		A: outA,
	}
}

// ResolveTranslucency flattens a possibly-translucent foreground/background
// pair in the mandatory fixed order: first background over base, then
// foreground over the composited background. The returned trace records
// every operation so the result is fully explainable.
//
// base must be supplied by the caller when translucency is involved; this
// function never substitutes a color. The caller (Evaluate) enforces that
// rule and returns ErrMissingBase.
func ResolveTranslucency(fg, bg, base Color) (Color, Color, *CompositeTrace) {
	tr := &CompositeTrace{Base: base}

	bgOver := CompositeOver(bg, base)
	tr.Steps = append(tr.Steps, CompositeStep{
		Stage:    "background",
		Source:   bg,
		Backdrop: base,
		Result:   bgOver,
	})
	tr.FinalBackground = bgOver

	fgOver := CompositeOver(fg, bgOver)
	tr.Steps = append(tr.Steps, CompositeStep{
		Stage:    "foreground",
		Source:   fg,
		Backdrop: bgOver,
		Result:   fgOver,
	})
	tr.FinalForeground = fgOver
	return fgOver, bgOver, tr
}

// linearize converts an sRGB-encoded channel to linear light (IEC 61966).
func linearize(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return powPos((c+0.055)/1.055, 2.4)
}

// encodize converts a linear-light channel back to sRGB encoding.
func encodize(c float64) float64 {
	if c <= 0.0031308 {
		return 12.92 * c
	}
	return 1.055*powPos(c, 1/2.4) - 0.055
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func powPos(x, y float64) float64 {
	if x <= 0 {
		return 0
	}
	return math.Pow(x, y)
}
