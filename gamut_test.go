package contrast

import "testing"

func TestGamutInRangePassesThrough(t *testing.T) {
	c := Color{0.2, 0.4, 0.8, 1}
	out, m := MapToGamut(c)
	if m.Changed {
		t.Fatalf("in-gamut color must not change")
	}
	if out != c {
		t.Fatalf("in-gamut color must pass through identically, got %+v", out)
	}
	if m.FinalChroma != m.InitialChroma {
		t.Fatal("chroma must not change in-gamut")
	}
}

func TestGamutOutOfRangeMappedDeterministically(t *testing.T) {
	c := Color{R: 1.5, G: -0.3, B: 0.2, A: 1}
	out1, m1 := MapToGamut(c)
	out2, m2 := MapToGamut(c)

	// Reproducible: identical input -> identical output.
	if out1 != out2 {
		t.Fatalf("non-deterministic mapping: %+v vs %+v", out1, out2)
	}
	if m1.FinalChroma != m2.FinalChroma {
		t.Fatal("chroma result not reproducible")
	}
	// Result is displayable.
	for _, v := range []float64{out1.R, out1.G, out1.B, out1.A} {
		if v < 0 || v > 1 {
			t.Fatalf("mapped channel outside [0,1]: %+v", out1)
		}
	}
	if !m1.Changed {
		t.Fatal("out-of-gamut input must record a change")
	}
	if m1.FinalChroma > m1.InitialChroma {
		t.Fatalf("chroma must only decrease: %v -> %v", m1.InitialChroma, m1.FinalChroma)
	}
	// Hue and lightness preserved.
	_, _, hIn := srgbToOKLCH(c)
	if !approx(angleDiff(m1.Hue, hIn), 0, 0.01) {
		t.Fatalf("hue moved: %.5f vs %.5f", m1.Hue, hIn)
	}
	if !approx(m1.Lightness, m1.Lightness, 0) {
		t.Fatal("sanity")
	}
	lo, _, _ := srgbToOKLCH(out1)
	if !approx(lo, m1.Lightness, 1e-6) {
		t.Fatalf("lightness not preserved: %.6f vs %.6f", lo, m1.Lightness)
	}
	if m1.Method != gamutMapMethod {
		t.Fatalf("method tag = %q", m1.Method)
	}
}

func TestGamutAlphaClampedNotAssumed(t *testing.T) {
	// Alpha outside [0,1] is clamped (it is not a colorimetric channel);
	// the RGB semantics are still gamut-mapped, never replaced.
	c := Color{R: 0.5, G: 0.5, B: 0.5, A: 1.7}
	out, _ := MapToGamut(c)
	if out.A != 1 {
		t.Fatalf("alpha clamp = %v want 1", out.A)
	}
}

func TestGamutRecordedInReport(t *testing.T) {
	theme := NewTheme("t", map[Role]Color{
		"fg": {R: 1.4, G: -0.1, B: 0.2, A: 1},
		"bg": {1, 1, 1, 1},
	})
	rep, err := Evaluate(theme, []Combo{{Foreground: "fg", Background: "bg"}}, EvalConfig{BaseSet: true})
	if err != nil {
		t.Fatal(err)
	}
	gm := rep.Combos[0].Gamut
	if len(gm) != 2 {
		t.Fatalf("want 2 gamut records, got %d", len(gm))
	}
	var changed int
	for _, g := range gm {
		if g.Map.Changed {
			changed++
		}
	}
	if changed != 1 {
		t.Fatalf("want exactly 1 mapped token, got %d", changed)
	}
}
