// Package contrast implements a fully local, dependency-free color
// accessibility engine for design systems.
//
// It evaluates semantic-role to color mappings against multiple contrast
// models (WCAG 2.x relative luminance and the perceptual APCA model),
// performs ordered alpha compositing on an explicit base, maps
// out-of-gamut inputs back to sRGB deterministically, derives high
// contrast themes while preserving brand hue and role hierarchy, and
// serves versioned, atomically replaceable themes with incremental
// recomputation.
package contrast

import (
	"errors"
	"strings"
)

// RGB is a non-premultiplied gamma-encoded sRGB color.
//
// Channels are not constrained to [0,1]: inputs may describe colors
// outside the sRGB gamut (e.g. wide-gamut sources expressed naively).
// Out-of-range values are only mapped back into the displayable gamut at
// evaluation/display time (see MapToGamut); the original semantics of an
// input token are never rewritten.
type RGB struct {
	R, G, B float64
}

// Color is an sRGB color with a straight (non-premultiplied) alpha value
// in [0,1], where 1 is fully opaque.
type Color struct {
	RGB   RGB
	Alpha float64
}

var errHexFormat = errors.New("contrast: invalid hex color, want #rgb, #rgba, #rrggbb or #rrggbbaa")

// ParseHex parses sRGB hex notation: #rgb, #rgba, #rrggbb or #rrggbbaa.
// Channel bytes are mapped to [0,1]; such inputs are always in gamut.
// Missing alpha defaults to 1 (fully opaque).
func ParseHex(s string) (Color, error) {
	h := strings.TrimPrefix(s, "#")
	if len(h) != 3 && len(h) != 4 && len(h) != 6 && len(h) != 8 {
		return Color{}, errHexFormat
	}
	// Expand short form (#rgb / #rgba) to long form.
	if len(h) <= 4 {
		expanded := make([]byte, 0, len(h)*2)
		for i := 0; i < len(h); i++ {
			expanded = append(expanded, h[i], h[i])
		}
		h = string(expanded)
	}
	v, err := parseHexBytes(h)
	if err != nil {
		return Color{}, err
	}
	c := Color{RGB: RGB{R: v[0] / 255, G: v[1] / 255, B: v[2] / 255}, Alpha: 1}
	if len(v) == 4 {
		c.Alpha = v[3] / 255
	}
	return c, nil
}

func parseHexBytes(h string) ([]float64, error) {
	out := make([]float64, len(h)/2)
	for i := 0; i < len(h); i += 2 {
		hi, ok1 := hexNibble(h[i])
		lo, ok2 := hexNibble(h[i+1])
		if !ok1 || !ok2 {
			return nil, errHexFormat
		}
		out[i/2] = float64(hi<<4 | lo)
	}
	return out, nil
}

func hexNibble(b byte) (int, bool) {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0'), true
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10, true
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10, true
	}
	return 0, false
}

// MustHex parses s and panics if it is not valid hex notation.
func MustHex(s string) Color {
	c, err := ParseHex(s)
	if err != nil {
		panic(err)
	}
	return c
}

// IsOpaque reports whether the color is fully opaque (alpha == 1).
func (c Color) IsOpaque() bool { return c.Alpha >= 1 }

// IsTranslucent reports whether the color has an alpha below 1.
// Alpha 0 (fully transparent) is also considered translucent: it still
// needs a base to produce a definite displayed color.
func (c Color) IsTranslucent() bool { return c.Alpha < 1 }
