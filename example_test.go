package contrast_test

import (
	"errors"
	"fmt"

	contrast "github.com/huchangjun118/accessible-contrast-engine-go"
)

func mustColor(s string) contrast.Color {
	c, err := contrast.ParseHex(s)
	if err != nil {
		panic(err)
	}
	return c
}

func Example() {
	theme := contrast.NewTheme("brand", map[contrast.Role]contrast.Color{
		"text/primary": mustColor("#767676"),
		"surface/bg":   mustColor("#ffffff"),
	})
	combos := []contrast.Combo{{
		Name:       "body",
		Foreground: "text/primary",
		Background: "surface/bg",
		Usage:      contrast.TextUsage{Text: true},
		Font:       contrast.FontSize{Pixels: 16, Weight: 400},
	}}

	rep, err := contrast.Evaluate(theme, combos, contrast.EvalConfig{
		Base:    mustColor("#ffffff"),
		BaseSet: true,
	})
	if err != nil {
		panic(err)
	}
	cr := rep.Combos[0]
	fmt.Printf("wcag pass=%v (%.2f)\n", cr.Verdicts[contrast.ModelWCAG].Pass,
		cr.Verdicts[contrast.ModelWCAG].Score)
	fmt.Printf("apca pass=%v (%.1f)\n", cr.Verdicts[contrast.ModelAPCA].Pass,
		cr.Verdicts[contrast.ModelAPCA].Score)

	// Translucent token without a declared base is rejected, never black.
	translucent := contrast.NewTheme("t", map[contrast.Role]contrast.Color{
		"text": {R: 0, G: 0, B: 0, A: 0.4},
		"bg":   mustColor("#ffffff"),
	})
	_, err = contrast.Evaluate(translucent, []contrast.Combo{
		{Foreground: "text", Background: "bg", Usage: contrast.TextUsage{Text: true}},
	}, contrast.EvalConfig{})
	fmt.Println("missing base rejected:", errors.Is(err, contrast.ErrMissingBase))
	// Output:
	// wcag pass=true (4.54)
	// apca pass=false (68.2)
	// missing base rejected: true
}
