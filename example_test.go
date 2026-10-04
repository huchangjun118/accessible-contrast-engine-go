package contrast_test

import (
	"context"
	"errors"
	"fmt"

	contrast "github.com/huchangjun118/accessible-contrast-engine-go"
)

func Example() {
	roles := contrast.RoleMapping{
		"text":       contrast.MustHex("#767676"),
		"background": contrast.MustHex("#ffffff"),
	}
	pairs := []contrast.RolePair{
		{Name: "body", Foreground: "text", Background: "background", Usage: contrast.UsageText},
	}

	report, err := contrast.Evaluate(roles, pairs, contrast.EvalConfig{})
	if err != nil {
		fmt.Println("eval error:", err)
		return
	}
	for _, r := range report.Results {
		for _, s := range r.Scores {
			fmt.Printf("%s/%s value=%.2f required=%.2f pass=%t\n",
				s.Model, s.Polarity, s.Value, s.Required, s.Pass)
		}
	}

	engine, err := contrast.NewEngine(roles, pairs, contrast.EvalConfig{})
	if err != nil {
		fmt.Println("new engine error:", err)
		return
	}
	fmt.Println("committed version:", engine.Current().Version)

	// Derive a compliant high-contrast theme while preserving brand hue.
	derived, err := contrast.DeriveHighContrast(context.Background(), roles, pairs,
		contrast.DeriveConfig{Model: contrast.ModelWCAG21})
	if err != nil {
		fmt.Println("derive error:", err)
		return
	}
	fmt.Println("derived ok:", derived.OK)

	// Translucent input without an explicit base is rejected, not
	// silently composited on black.
	_, err = contrast.CompositeOver(
		contrast.Color{RGB: contrast.RGB{R: 1}, Alpha: 0.5},
		contrast.MustHex("#000000"), nil)
	fmt.Println("missing base rejected:", errors.Is(err, contrast.ErrMissingBase))
	// Output:
	// wcag21/dark-on-light value=4.54 required=4.50 pass=true
	// apca0.0.98G/dark-on-light value=68.25 required=75.00 pass=false
	// committed version: 1
	// derived ok: true
	// missing base rejected: true
}
