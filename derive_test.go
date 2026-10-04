package contrast

import (
	"context"
	"math"
	"strings"
	"testing"
)

func hueOf(t *testing.T, c RGB) float64 {
	t.Helper()
	L, a, b := linearSRGBToOKLab(srgbToLinear(c))
	_ = L
	return math.Atan2(b, a)
}

func TestDeriveHighContrastSucceeds(t *testing.T) {
	roles := RoleMapping{
		"text":       MustHex("#9aa0a6"), // low contrast gray
		"muted":      MustHex("#b9bec4"),
		"background": MustHex("#ffffff"),
		"surface":    MustHex("#f8f9fa"),
	}
	pairs := []RolePair{
		{Name: "body", Foreground: "text", Background: "background", Usage: UsageText},
		{Name: "muted-body", Foreground: "muted", Background: "surface", Usage: UsageText},
	}
	res, err := DeriveHighContrast(context.Background(), roles, pairs, DeriveConfig{Model: ModelWCAG21})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || len(res.Failures) != 0 {
		t.Fatalf("expected success, failures=%+v", res.Failures)
	}

	// Every pair meets WCAG text threshold in the derived tokens.
	for _, p := range pairs {
		ratio := wcagRatio(res.Tokens[p.Foreground], res.Tokens[p.Background])
		if ratio < 4.5 {
			t.Errorf("pair %q ratio %.3f < 4.5", p.Name, ratio)
		}
	}

	// Brand hue preserved per role.
	for name, orig := range roles {
		dh := math.Abs(hueOf(t, orig.RGB) - hueOf(t, res.Tokens[name]))
		for dh > math.Pi {
			dh -= 2 * math.Pi
		}
		if dh > 2*math.Pi/180 {
			t.Errorf("role %q hue shifted %.2f degrees", name, dh*180/math.Pi)
		}
	}

	// Hierarchy/polarity preserved: text darker than background etc.
	for _, p := range pairs {
		if RelativeLuminance(res.Tokens[p.Foreground]) >= RelativeLuminance(res.Tokens[p.Background]) {
			t.Errorf("pair %q polarity reversed", p.Name)
		}
	}

	// Adjustments explain the changes in OKLCh with constant hue.
	if len(res.Adjustments) != len(roles) {
		t.Errorf("want %d adjustments, got %d", len(roles), len(res.Adjustments))
	}
}

func TestDerivePreservesLightThemeOrdering(t *testing.T) {
	roles := RoleMapping{
		"text": MustHex("#8a8f98"),
		"bg":   MustHex("#f2f3f5"),
		"card": MustHex("#e9ebef"),
	}
	pairs := []RolePair{
		{Name: "text-bg", Foreground: "text", Background: "bg"},
		{Name: "text-card", Foreground: "text", Background: "card"},
	}
	res, err := DeriveHighContrast(context.Background(), roles, pairs, DeriveConfig{Model: ModelWCAG21})
	if err != nil {
		t.Fatal(err)
	}
	// Original bg lighter than card; that ordering must survive.
	if RelativeLuminance(res.Tokens["card"]) >= RelativeLuminance(res.Tokens["bg"]) {
		// card must remain darker than bg (allowing equality tolerance)
		if math.Abs(RelativeLuminance(res.Tokens["card"])-RelativeLuminance(res.Tokens["bg"])) > 1e-3 {
			t.Errorf("surface ordering between bg and card was destroyed")
		}
	}
}

func TestDeriveInfeasibleReportsPairFailures(t *testing.T) {
	// Two roles constrained by hierarchy cannot both meet an extreme
	// enhanced margin: request AAA 7:1 plus a huge margin.
	roles := RoleMapping{
		"text": MustHex("#777777"),
		"bg":   MustHex("#f5f5f5"),
	}
	pairs := []RolePair{{Name: "impossible", Foreground: "text", Background: "bg", Usage: UsageText}}
	res, err := DeriveHighContrast(context.Background(), roles, pairs,
		DeriveConfig{Model: ModelWCAG21, Usage: UsageEnhanced, Margin: 50, MaxIterations: 80})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatalf("expected failure for threshold beyond 21:1")
	}
	if len(res.Failures) != 1 {
		t.Fatalf("want 1 failure, got %d", len(res.Failures))
	}
	f := res.Failures[0]
	if f.Pair.Name != "impossible" {
		t.Errorf("failure pair name = %q", f.Pair.Name)
	}
	if f.Required <= f.Achieved {
		t.Errorf("failure required %.2f must exceed achieved %.2f", f.Required, f.Achieved)
	}
	if strings.TrimSpace(f.Reason) == "" {
		t.Errorf("failure reason must be non-empty and locatable")
	}
	// Tokens are still returned, in gamut.
	for n, c := range res.Tokens {
		if !inGamut(c) {
			t.Errorf("role %q derived token out of gamut: %+v", n, c)
		}
	}
}

func TestDeriveCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := DeriveHighContrast(ctx,
		RoleMapping{"text": MustHex("#888888"), "bg": MustHex("#ffffff")},
		[]RolePair{{Name: "p", Foreground: "text", Background: "bg"}},
		DeriveConfig{})
	if err == nil {
		t.Errorf("canceled derivation must return an error")
	}
}

func TestDeriveTranslucentNeedsBase(t *testing.T) {
	_, err := DeriveHighContrast(context.Background(),
		RoleMapping{"text": {RGB: RGB{0, 0, 0}, Alpha: 0.5}, "bg": MustHex("#fff")},
		[]RolePair{{Name: "p", Foreground: "text", Background: "bg"}},
		DeriveConfig{})
	if err != ErrMissingBase {
		t.Errorf("got %v want ErrMissingBase", err)
	}
}
