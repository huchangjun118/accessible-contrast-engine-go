package contrast

import (
	"errors"
	"math"
	"testing"
)

func approx(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestMultiModelIndependentVerdicts(t *testing.T) {
	// A pair chosen to pass WCAG AA but be flagged by APCA's own scale,
	// demonstrating the two models never overwrite each other.
	// #767676 on white: WCAG ratio 4.54 (pass AA), APCA |Lc| ~ Lc 62
	// (below normal-text 90 requirement).
	gray := Color{0x76 / 255.0, 0x76 / 255.0, 0x76 / 255.0, 1}
	white := Color{1, 1, 1, 1}
	theme := NewTheme("t", map[Role]Color{"fg": gray, "bg": white})
	combos := []Combo{{
		Name: "body", Foreground: "fg", Background: "bg",
		Usage: TextUsage{Text: true}, Font: FontSize{Pixels: 16, Weight: 400},
	}}
	rep, err := Evaluate(theme, combos, EvalConfig{BaseSet: true, Base: white})
	if err != nil {
		t.Fatal(err)
	}
	cr := rep.Combos[0]
	wv, hasW := cr.Verdicts[ModelWCAG]
	av, hasA := cr.Verdicts[ModelAPCA]
	if !hasW || !hasA {
		t.Fatalf("missing verdicts: %+v", cr.Verdicts)
	}
	if !approx(wv.Score, 4.54, 0.05) {
		t.Fatalf("wcag score %v want ~4.54", wv.Score)
	}
	if !wv.Pass {
		t.Fatalf("WCAG AA should pass at 4.54")
	}
	if math.Abs(av.Score) >= 90 {
		t.Fatalf("APCA |Lc| %v expected below 90 normal-text", av.Score)
	}
	if av.Pass {
		t.Fatalf("APCA normal text should FAIL at |Lc|=%.1f < 90", math.Abs(av.Score))
	}
	// Global aggregates must reflect BOTH models independently.
	if rep.AllPass() {
		t.Fatalf("AllPass must be false when APCA fails")
	}
	if !rep.PassUnder(ModelWCAG) {
		t.Fatalf("PassUnder(WCAG) must still be true")
	}
	if rep.PassUnder(ModelAPCA) {
		t.Fatalf("PassUnder(APCA) must be false")
	}
}

func TestWCAGKnownPairs(t *testing.T) {
	cases := []struct {
		name      string
		fg, bg    Color
		wantRatio float64
	}{
		{"black-white", Color{0, 0, 0, 1}, Color{1, 1, 1, 1}, 21},
		{"white-black", Color{1, 1, 1, 1}, Color{0, 0, 0, 1}, 21},
		{"same", Color{0.3, 0.3, 0.3, 1}, Color{0.3, 0.3, 0.3, 1}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := WCAGContrast(tc.fg, tc.bg); !approx(got, tc.wantRatio, 1e-9) {
				t.Fatalf("ratio %v want %v", got, tc.wantRatio)
			}
		})
	}
}

func TestWCAGThresholds(t *testing.T) {
	m := &WCAGModel{}
	body := TextUsage{Text: true, Level: "AA"}
	large := FontSize{Pixels: 24}
	if m.RequiredScore(body, FontSize{Pixels: 16}) != 4.5 {
		t.Fatal("AA normal = 4.5")
	}
	if m.RequiredScore(body, large) != 3 {
		t.Fatal("AA large = 3")
	}
	if m.RequiredScore(TextUsage{Text: true, Level: "AAA"}, FontSize{Pixels: 16}) != 7 {
		t.Fatal("AAA normal = 7")
	}
	if m.RequiredScore(TextUsage{Text: false}, FontSize{}) != 3 {
		t.Fatal("UI non-text = 3")
	}
}

func TestAPCAPolarityAndClamp(t *testing.T) {
	black := Color{0, 0, 0, 1}
	white := Color{1, 1, 1, 1}
	if lc := APCAContrast(black, white); lc <= 0 {
		t.Fatalf("dark-on-light Lc must be positive, got %v", lc)
	}
	if lc := APCAContrast(white, black); lc >= 0 {
		t.Fatalf("light-on-dark Lc must be negative, got %v", lc)
	}
	same := Color{0.4, 0.4, 0.4, 1}
	if lc := APCAContrast(same, same); lc != 0 {
		t.Fatalf("identical colors Lc must be 0, got %v", lc)
	}
	// Two nearly-identical near colors fall below the delta/clamp.
	c1 := Color{0.5, 0.5, 0.5, 1}
	c2 := Color{0.501, 0.501, 0.501, 1}
	if lc := APCAContrast(c1, c2); lc != 0 {
		t.Fatalf("near-identical Lc must clamp to 0, got %v", lc)
	}
}

func TestUnknownRoleRejected(t *testing.T) {
	theme := NewTheme("t", map[Role]Color{"bg": {1, 1, 1, 1}})
	_, err := Evaluate(theme, []Combo{{Foreground: "missing", Background: "bg"}}, EvalConfig{BaseSet: true})
	var u *UnknownRoleError
	if !errors.As(err, &u) || !errors.Is(err, ErrUnknownRole) {
		t.Fatalf("want UnknownRoleError, got %v", err)
	}
	if u.Role != "missing" {
		t.Fatalf("role = %v", u.Role)
	}
}
