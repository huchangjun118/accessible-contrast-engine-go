package contrast

import (
	"errors"
	"math"
	"testing"
)

func approx(x, y, tol float64) bool { return math.Abs(x-y) <= tol }

func TestCompositeMissingBaseRejected(t *testing.T) {
	fg := Color{RGB: RGB{1, 1, 1}, Alpha: 0.5}
	bg := MustHex("#000000")
	if _, err := CompositeOver(fg, bg, nil); !errors.Is(err, ErrMissingBase) {
		t.Fatalf("translucent fg without base: got %v want ErrMissingBase", err)
	}
	bg2 := Color{RGB: RGB{0, 0, 0}, Alpha: 0.2}
	if _, err := CompositeOver(MustHex("#ffffff"), bg2, nil); !errors.Is(err, ErrMissingBase) {
		t.Fatalf("translucent bg without base: got %v want ErrMissingBase", err)
	}
}

func TestCompositeOpaqueNeedsNoBase(t *testing.T) {
	fg := MustHex("#888888")
	bg := MustHex("#ffffff")
	got, err := CompositeOver(fg, bg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Color.IsOpaque() {
		t.Errorf("opaque layers must composite opaque")
	}
	if got.Color.RGB != fg.RGB {
		t.Errorf("opaque fg over bg must equal fg")
	}
}

func TestCompositeOrderAndExplainability(t *testing.T) {
	// 50% black foreground over 50% white background over a black base.
	fg := Color{RGB: RGB{0, 0, 0}, Alpha: 0.5}
	bg := Color{RGB: RGB{1, 1, 1}, Alpha: 0.5}
	base := RGB{0, 0, 0}
	got, err := CompositeOver(fg, bg, &base)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Steps) != 2 {
		t.Fatalf("want 2 ordered steps, got %d", len(got.Steps))
	}
	if got.Steps[0].Label != "background over base" || got.Steps[1].Label != "foreground over background" {
		t.Errorf("unexpected step order: %q then %q", got.Steps[0].Label, got.Steps[1].Label)
	}
	// bg over black: 0.5 white => 0.5 gray (opaque). fg 50% black over 0.5
	// gray => 0.25 gray.
	want := 0.25
	for _, ch := range []float64{got.Color.RGB.R, got.Color.RGB.G, got.Color.RGB.B} {
		if !approx(ch, want, 1e-12) {
			t.Errorf("composited channel = %.4f want %.4f", ch, want)
		}
	}
	if !got.Color.IsOpaque() {
		t.Errorf("compositing over an opaque base must end opaque")
	}

	// Same translucent layers on a WHITE base must give a different
	// result — proving the base is required rather than assumed black.
	whiteBase := RGB{1, 1, 1}
	got2, err := CompositeOver(fg, bg, &whiteBase)
	if err != nil {
		t.Fatal(err)
	}
	if approx(got2.Color.RGB.R, got.Color.RGB.R, 1e-9) {
		t.Errorf("black-base and white-base results unexpectedly equal")
	}
	// Determinism.
	got3, _ := CompositeOver(fg, bg, &base)
	if got3.Color != got.Color {
		t.Errorf("compositing is not deterministic")
	}
}

func TestEvaluateTranslucentRequiresBase(t *testing.T) {
	roles := RoleMapping{
		"text": {RGB: RGB{1, 1, 1}, Alpha: 0.5},
		"bg":   MustHex("#000000"),
	}
	pairs := []RolePair{{Name: "t", Foreground: "text", Background: "bg", Usage: UsageText}}
	if _, err := Evaluate(roles, pairs, EvalConfig{}); !errors.Is(err, ErrMissingBase) {
		t.Fatalf("Evaluate without base: got %v want ErrMissingBase", err)
	}
	// With an explicit base it evaluates.
	rep, err := Evaluate(roles, pairs, EvalConfig{Base: &RGB{0, 0, 0}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Results) != 1 {
		t.Fatalf("want 1 result")
	}
}
