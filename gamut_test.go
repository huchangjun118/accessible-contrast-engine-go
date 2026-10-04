package contrast

import (
	"math"
	"testing"
)

func TestInGamut(t *testing.T) {
	if !inGamut(RGB{0, 0.5, 1}) {
		t.Errorf("[0,0.5,1] must be in gamut")
	}
	if inGamut(RGB{1.2, 0, 0}) || inGamut(RGB{-0.1, 0, 0}) {
		t.Errorf("out-of-range channels must be out of gamut")
	}
	if inGamut(RGB{math.NaN(), 0, 0}) || inGamut(RGB{math.Inf(1), 0, 0}) {
		t.Errorf("non-finite channels must be out of gamut")
	}
}

func TestClipGamutDeterministic(t *testing.T) {
	in := RGB{1.4, -0.2, 0.5}
	got := mapToGamutWith(in, GamutMapClip)
	if got.Mapped != (RGB{1, 0, 0.5}) {
		t.Errorf("clip = %+v want {1 0 0.5}", got.Mapped)
	}
	if got.InGamut {
		t.Errorf("InGamut flag wrong")
	}
}

func TestOKLChMappingHuePreservingAndReproducible(t *testing.T) {
	// A strongly saturated, over-bright red is outside sRGB.
	in := RGB{1.6, -0.1, -0.05}
	r1 := MapToGamut(in)
	r2 := MapToGamut(in)
	if !inGamut(r1.Mapped) {
		t.Fatalf("mapped color out of gamut: %+v", r1.Mapped)
	}
	if r1.Mapped != r2.Mapped {
		t.Fatalf("gamut mapping is not reproducible")
	}
	if r1.InGamut || r1.Iterations == 0 {
		t.Errorf("expected reported out-of-gamut projection with iterations")
	}

	// Hue must be preserved: compare OKLCh hue before/after.
	lin0 := srgbToLinear(in)
	_, a0, b0 := linearSRGBToOKLab(lin0)
	lin1 := srgbToLinear(r1.Mapped)
	_, a1, b1 := linearSRGBToOKLab(lin1)
	hue0 := math.Atan2(b0, a0)
	hue1 := math.Atan2(b1, a1)
	dh := math.Abs(hue0 - hue1)
	for dh > math.Pi {
		dh -= 2 * math.Pi
	}
	if dh > math.Pi/180 { // within 1 degree
		t.Errorf("hue shifted by %.2f degrees during gamut mapping", dh*180/math.Pi)
	}
	// Chroma must be reduced, never increased.
	c0, c1 := math.Hypot(a0, b0), math.Hypot(a1, b1)
	if c1 > c0+1e-9 {
		t.Errorf("chroma increased: %.4f -> %.4f", c0, c1)
	}
}

func TestGamutMappingInGamutIdentity(t *testing.T) {
	in := MustHex("#3366cc").RGB
	r := MapToGamut(in)
	if !r.InGamut || r.Mapped != in {
		t.Errorf("in-gamut color should pass through unchanged, got %+v", r)
	}
}

func TestNonFiniteMappingSanitized(t *testing.T) {
	for _, in := range []RGB{
		{math.NaN(), math.NaN(), math.NaN()},
		{math.Inf(1), 0, 0},
		{math.Inf(-1), 1, 1},
	} {
		r := MapToGamut(in)
		if !inGamut(r.Mapped) {
			t.Errorf("non-finite input %+v did not map into gamut: %+v", in, r.Mapped)
		}
	}
}
