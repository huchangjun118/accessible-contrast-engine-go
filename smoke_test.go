package contrast

import (
	"errors"
	"math"
	"testing"
)

func TestSmokeCoreValues(t *testing.T) {
	black := Color{0, 0, 0, 1}
	white := Color{1, 1, 1, 1}

	if got := WCAGContrast(black, white); math.Abs(got-21) > 1e-9 {
		t.Fatalf("wcag black/white = %v, want 21", got)
	}
	if l := RelativeLuminance(white); math.Abs(l-1) > 1e-12 {
		t.Fatalf("Y(white)=%v want 1", l)
	}
	if l := RelativeLuminance(black); math.Abs(l) > 1e-12 {
		t.Fatalf("Y(black)=%v want 0", l)
	}
	lc := APCAContrast(black, white)
	if lc < 100 || lc > 108 {
		t.Fatalf("APCA black-on-white Lc=%v, want ~106", lc)
	}
	lc2 := APCAContrast(white, black)
	if lc2 > 0 {
		t.Fatalf("APCA white-on-black Lc=%v, want negative polarity", lc2)
	}
	t.Logf("APCA black-on-white=%.2f white-on-black=%.2f", lc, lc2)

	// translucent black over white base -> mid gray, explainable trace
	fg, _, tr := ResolveTranslucency(Color{0, 0, 0, 0.5}, white, white)
	if tr == nil || len(tr.Steps) != 2 {
		t.Fatalf("want 2 trace steps, got %+v", tr)
	}
	if tr.Steps[0].Stage != "background" || tr.Steps[1].Stage != "foreground" {
		t.Fatalf("wrong stage order: %v %v", tr.Steps[0].Stage, tr.Steps[1].Stage)
	}
	t.Logf("50%% black over white -> %s (linear composite)", fg.Hex())
	// Linear-space composite of 50% black over white is linear 0.5, which
	// encodes to sRGB ~0.7353 (#bcbcbc). Assert against the encoded value.
	if math.Abs(fg.R-0.7353569830524495) > 1e-6 {
		t.Fatalf("expected sRGB 0.7354 mid gray, got %v", fg.R)
	}
	if fg.Hex() != "#bcbcbc" {
		t.Fatalf("expected #bcbcbc, got %s", fg.Hex())
	}

	// missing base must be rejected, not silently black
	theme := NewTheme("x", map[Role]Color{
		"text": {0, 0, 0, 0.5},
		"bg":   white,
	})
	_, err := Evaluate(theme, []Combo{{Foreground: "text", Background: "bg", Usage: TextUsage{Text: true}}}, EvalConfig{})
	if !errors.Is(err, ErrMissingBase) {
		t.Fatalf("want ErrMissingBase, got %v", err)
	}

	// out of gamut maps in, hue preserved
	oob := Color{R: 1.4, G: -0.2, B: 0.3, A: 1}
	mapped, gm := MapToGamut(oob)
	if !gm.Changed {
		t.Fatalf("expected mapping to be recorded")
	}
	if mapped.R < 0 || mapped.R > 1 || mapped.G < 0 || mapped.G > 1 || mapped.B < 0 || mapped.B > 1 {
		t.Fatalf("mapped color out of gamut: %+v", mapped)
	}
	_, _, hIn := srgbToOKLCH(oob)
	if math.Abs(angleDiff(gm.Hue, hIn)) > 0.02 {
		t.Fatalf("hue changed: %v vs %v", gm.Hue, hIn)
	}
	t.Logf("OOB %+v -> %s chroma %.4f->%.4f", oob, mapped.Hex(), gm.InitialChroma, gm.FinalChroma)
}

func angleDiff(a, b float64) float64 {
	d := a - b
	for d > math.Pi {
		d -= 2 * math.Pi
	}
	for d < -math.Pi {
		d += 2 * math.Pi
	}
	return d
}
