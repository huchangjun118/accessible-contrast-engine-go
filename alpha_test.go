package contrast

import (
	"errors"
	"testing"
)

func TestTranslucentRequiresBase(t *testing.T) {
	theme := NewTheme("t", map[Role]Color{
		"fg": {0, 0, 0, 0.4},
		"bg": {1, 1, 1, 0.8}, // translucent surface so the base leaks through
	})
	combos := []Combo{{Foreground: "fg", Background: "bg", Usage: TextUsage{Text: true}}}

	// BaseSet=false -> hard error, never implicit black.
	_, err := Evaluate(theme, combos, EvalConfig{})
	var mb *MissingBaseError
	if !errors.As(err, &mb) || !errors.Is(err, ErrMissingBase) {
		t.Fatalf("want MissingBaseError, got %v", err)
	}
	if mb.Role != "bg" || mb.Stage != "background" {
		t.Fatalf("unexpected error detail: %+v", mb)
	}

	// Same colors over an explicitly black base must differ from over a
	// white base, proving the base actually drives the result.
	repBlack, err := Evaluate(theme, combos, EvalConfig{BaseSet: true, Base: Color{0, 0, 0, 1}})
	if err != nil {
		t.Fatal(err)
	}
	repWhite, err := Evaluate(theme, combos, EvalConfig{BaseSet: true, Base: Color{1, 1, 1, 1}})
	if err != nil {
		t.Fatal(err)
	}
	b := repBlack.Combos[0].ComparedFG
	w := repWhite.Combos[0].ComparedFG
	if b.Hex() == w.Hex() {
		t.Fatalf("base had no effect: both %s", b.Hex())
	}
}

func TestTranslucentBackgroundOrder(t *testing.T) {
	// Translucent BACKGROUND composited over the base first, then an
	// opaque foreground over that. Fixed order must be traceable.
	red := Color{1, 0, 0, 1}
	bgAlpha := Color{0, 0, 0, 0.5} // 50% black surface
	base := Color{1, 1, 1, 1}      // white page
	theme := NewTheme("t", map[Role]Color{"fg": red, "bg": bgAlpha})
	rep, err := Evaluate(theme,
		[]Combo{{Foreground: "fg", Background: "bg", Usage: TextUsage{Text: true}}},
		EvalConfig{BaseSet: true, Base: base})
	if err != nil {
		t.Fatal(err)
	}
	cr := rep.Combos[0]
	if cr.Trace == nil {
		t.Fatal("expected compositing trace")
	}
	if len(cr.Trace.Steps) != 2 {
		t.Fatalf("want 2 steps, got %d", len(cr.Trace.Steps))
	}
	if cr.Trace.Steps[0].Stage != "background" || cr.Trace.Steps[1].Stage != "foreground" {
		t.Fatal("compositing order must be background then foreground")
	}
	// Background step backdrop must be the supplied base, foreground step
	// backdrop must be the composited background (deterministic chaining).
	if cr.Trace.Steps[0].Backdrop != base {
		t.Fatal("first step must composite over the caller base")
	}
	if cr.Trace.Steps[1].Backdrop != cr.Trace.Steps[0].Result {
		t.Fatal("second step must composite over the first step result")
	}
	// Background compared color is linear 0.5 -> sRGB #bcbcbc.
	if got := cr.ComparedBG.Hex(); got != "#bcbcbc" {
		t.Fatalf("background flatten = %s, want #bcbcbc", got)
	}
}

func TestFullyOpaqueNoTrace(t *testing.T) {
	theme := NewTheme("t", map[Role]Color{"fg": {0, 0, 0, 1}, "bg": {1, 1, 1, 1}})
	rep, err := Evaluate(theme, []Combo{{Foreground: "fg", Background: "bg"}}, EvalConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Combos[0].Trace != nil {
		t.Fatal("opaque pair should not produce a compositing trace")
	}
}

func TestParseColorForms(t *testing.T) {
	c, err := ParseHex("#abc")
	if err != nil || c.Hex() != "#aabbcc" {
		t.Fatalf("#abc -> %s err=%v", c.Hex(), err)
	}
	c, err = ParseHex("#aabbcc80")
	if err != nil || !approx(c.A, 128/255.0, 1e-6) {
		t.Fatalf("alpha hex: %+v err=%v", c, err)
	}
	c, err = ParseColor("rgba(255, 0, 0, 0.5)")
	if err != nil || c.R != 1 || c.G != 0 || !approx(c.A, 0.5, 1e-9) {
		t.Fatalf("rgba parse: %+v err=%v", c, err)
	}
	c, err = ParseColor("rgb(100% 0% 0% / 25%)")
	if err != nil || c.R != 1 || c.A != 0.25 {
		t.Fatalf("rgb slash percent: %+v err=%v", c, err)
	}
	if _, err := ParseColor("not-a-color"); !errors.Is(err, ErrInvalidColor) {
		t.Fatalf("want ErrInvalidColor, got %v", err)
	}
	if _, err := ParseHex("#12"); !errors.Is(err, ErrInvalidColor) {
		t.Fatalf("want ErrInvalidColor, got %v", err)
	}
}
