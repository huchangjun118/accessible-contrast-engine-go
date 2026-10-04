package contrast

import (
	"errors"
	"testing"
)

func TestDeriveHighContrastSucceeds(t *testing.T) {
	// Low-contrast brand colors: dark-ish text on a dark surface.
	theme := NewTheme("brand", map[Role]Color{
		"text":    mustHex("#5a5a5a"),
		"surface": mustHex("#4d4d4d"),
	})
	combos := []Combo{{
		Name: "body", Foreground: "text", Background: "surface",
		Usage: TextUsage{Text: true}, Font: FontSize{Pixels: 16},
	}}
	before, err := Evaluate(theme, combos, EvalConfig{BaseSet: true})
	if err != nil {
		t.Fatal(err)
	}
	if before.AllPass() {
		t.Fatal("precondition: source theme should fail")
	}

	derived, err := DeriveHighContrast(theme, combos, DeriveOptions{
		BaseSet:    true,
		LightStep:  0.02,
		ChromaStep: 0.02,
	})
	if err != nil {
		t.Fatalf("derivation failed: %v", err)
	}
	after, err := Evaluate(derived.Theme, combos, EvalConfig{BaseSet: true})
	if err != nil {
		t.Fatal(err)
	}
	if !after.PassUnder(ModelWCAG) {
		t.Fatalf("derived theme must pass WCAG, scores: %+v", after.Combos[0].Verdicts[ModelWCAG])
	}
	// Hue preserved for each adjusted role.
	for role, adj := range derived.Adjustments {
		_, _, h0 := srgbToOKLCH(mustMapped(theme.Tokens[role]))
		if !approx(angleDiff(adj.Hue, h0), 0, 0.01) {
			t.Fatalf("role %s hue drifted: %.4f vs %.4f", role, adj.Hue, h0)
		}
	}
	// Source theme untouched.
	if got := theme.Tokens["text"].Hex(); got != "#5a5a5a" {
		t.Fatalf("source theme mutated: %s", got)
	}
}

func TestDeriveImpossiblePinpointsCombos(t *testing.T) {
	// Force impossibility with tight bounds that forbid moving either role
	// apart: both pinned to essentially the same lightness.
	theme := NewTheme("pinned", map[Role]Color{
		"fg": {0.5, 0.5, 0.5, 1},
		"bg": {0.5, 0.5, 0.5, 1},
	})
	combos := []Combo{{
		Name: "locked", Foreground: "fg", Background: "bg",
		Usage: TextUsage{Text: true},
	}}
	_, err := DeriveHighContrast(theme, combos, DeriveOptions{
		BaseSet: true,
		Bounds: map[Role]RoleBound{
			"fg": {MinL: 0.59, MaxL: 0.61},
			"bg": {MinL: 0.59, MaxL: 0.61},
		},
		MaxIterations: 10,
	})
	var df *DerivationFailure
	if !errors.As(err, &df) || !errors.Is(err, ErrDerivationImpossible) {
		t.Fatalf("want DerivationFailure, got %v", err)
	}
	if len(df.Unresolved) != 1 {
		t.Fatalf("want 1 unresolved combo, got %d", len(df.Unresolved))
	}
	fc := df.Unresolved[0]
	if fc.Foreground != "fg" || fc.Background != "bg" {
		t.Fatalf("failure must name roles: %+v", fc)
	}
	if _, ok := fc.Score[ModelWCAG]; !ok {
		t.Fatal("failure must carry measured WCAG score")
	}
	if _, ok := fc.Required[ModelAPCA]; !ok {
		t.Fatal("failure must carry required APCA score")
	}
	t.Logf("derivation failure: %v", err)
}

func TestDerivePreservesRoleOrdering(t *testing.T) {
	// Three grayscale roles with a required dark->light order. Derivation
	// for fg/surface must not invert surface vs elevated ordering.
	theme := NewTheme("layers", map[Role]Color{
		"fg":       mustHex("#6a6a6a"),
		"surface":  mustHex("#555555"),
		"elevated": mustHex("#606060"),
	})
	combos := []Combo{{
		Foreground: "fg", Background: "surface",
		Usage: TextUsage{Text: true},
	}}
	derived, err := DeriveHighContrast(theme, combos, DeriveOptions{
		BaseSet:             true,
		PreserveDarkToLight: [][]Role{{"surface", "elevated", "fg"}},
		LightStep:           0.02,
	})
	if err != nil {
		t.Fatal(err)
	}
	l := func(r Role) float64 {
		ll, _, _ := srgbToOKLCH(derived.Theme.Tokens[r])
		return ll
	}
	if !(l("surface") <= l("elevated")+1e-9 && l("elevated") <= l("fg")+1e-9) {
		t.Fatalf("hierarchy inverted: surface=%.3f elevated=%.3f fg=%.3f",
			l("surface"), l("elevated"), l("fg"))
	}
}

func mustHex(s string) Color {
	c, err := ParseHex(s)
	if err != nil {
		panic(err)
	}
	return c
}

func mustMapped(c Color) Color {
	out, _ := MapToGamut(c)
	return out
}

func TestDerivePreservesLightOnDarkPolarity(t *testing.T) {
	// Brand starts light text on dark surface; derivation must keep that
	// polarity and widen it, never invert it.
	theme := NewTheme("dark", map[Role]Color{
		"text":    mustHex("#9a9a9a"),
		"surface": mustHex("#8c8c8c"),
	})
	combos := []Combo{{
		Foreground: "text", Background: "surface",
		Usage: TextUsage{Text: true}, Font: FontSize{Pixels: 16},
	}}
	derived, err := DeriveHighContrast(theme, combos, DeriveOptions{BaseSet: true, LightStep: 0.02})
	if err != nil {
		t.Fatal(err)
	}
	lF, _, _ := srgbToOKLCH(derived.Theme.Tokens["text"])
	lB, _, _ := srgbToOKLCH(derived.Theme.Tokens["surface"])
	if !(lF > lB) {
		t.Fatalf("light-on-dark polarity inverted: fg L=%.3f bg L=%.3f", lF, lB)
	}
	after, err := Evaluate(derived.Theme, combos, EvalConfig{BaseSet: true})
	if err != nil {
		t.Fatal(err)
	}
	if !after.PassUnder(ModelWCAG) || !after.PassUnder(ModelAPCA) {
		t.Fatalf("derived theme must pass both models: %+v", after.Combos[0].Verdicts)
	}
}
