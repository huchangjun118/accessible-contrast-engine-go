package contrast

import "testing"

func TestEvaluateReportsModelsIndependently(t *testing.T) {
	// Pick a gray whose WCAG ratio passes 4.5 on white while |Lc| fails
	// the APCA 75 text threshold, demonstrating independent verdicts.
	roles := RoleMapping{
		"text": MustHex("#595959"), // WCAG ~7:1; APCA |Lc| roughly 74
		"bg":   MustHex("#ffffff"),
	}
	pairs := []RolePair{{Name: "body", Foreground: "text", Background: "bg", Usage: UsageText}}
	rep, err := Evaluate(roles, pairs, EvalConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Results) != 1 || len(rep.Results[0].Scores) != 2 {
		t.Fatalf("want 1 pair x 2 models, got %+v", rep.Results)
	}
	byModel := map[Model]Score{}
	for _, s := range rep.Results[0].Scores {
		byModel[s.Model] = s
		if s.Required != 4.5 && s.Model == ModelWCAG21 {
			t.Errorf("WCAG text required = %.2f want 4.5", s.Required)
		}
	}
	// WCAG should pass comfortably; APCA independently evaluates and may
	// differ. Critically the two flags are stored separately.
	if !byModel[ModelWCAG21].Pass {
		t.Errorf("WCAG expected to pass, ratio %.2f", byModel[ModelWCAG21].Value)
	}
	apca := byModel[ModelAPCA]
	if apca.Value < 60 || apca.Value > 90 {
		t.Errorf("unexpected |Lc| %.2f for #595959 on white", apca.Value)
	}
	// If APCA fails, the aggregate report must fail even though WCAG
	// passed; the two never overwrite each other.
	if apca.Pass != rep.Pass {
		t.Errorf("aggregate pass=%v must reflect APCA pass=%v independently", rep.Pass, apca.Pass)
	}
}

func TestEvaluateRejectsUnknownRole(t *testing.T) {
	_, err := Evaluate(RoleMapping{"bg": MustHex("#fff")},
		[]RolePair{{Name: "x", Foreground: "missing", Background: "bg"}}, EvalConfig{})
	if err == nil {
		t.Errorf("expected error for unknown role")
	}
}

func TestEvaluateGamutMappingFlag(t *testing.T) {
	roles := RoleMapping{
		"text": Color{RGB: RGB{0, 0, 0}, Alpha: 1},
		"bg":   Color{RGB: RGB{1.6, 1.6, 1.6}, Alpha: 1},
	}
	rep, err := Evaluate(roles, []RolePair{{Name: "p", Foreground: "text", Background: "bg"}}, EvalConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Results[0].GamutMapped {
		t.Errorf("out-of-gamut token must flag GamutMapped")
	}
	if !inGamut(rep.Results[0].EffectiveBG) {
		t.Errorf("effective background must be in gamut")
	}
	// Mapping does not alter the stored semantic token.
	if roles["bg"].RGB.R != 1.6 {
		t.Errorf("input token semantics were rewritten")
	}
}

func TestDeriveWithAPCAModel(t *testing.T) {
	roles := RoleMapping{
		"text": MustHex("#8a8a8a"),
		"bg":   MustHex("#ffffff"),
	}
	pairs := []RolePair{{Name: "body", Foreground: "text", Background: "bg", Usage: UsageText}}
	res, err := DeriveHighContrast(nilCtx(), roles, pairs, DeriveConfig{Model: ModelAPCA})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("expected APCA derivation to converge: %+v", res.Failures)
	}
	if got := apcaContrast(res.Tokens["text"], res.Tokens["bg"]); absF(got) < 75 {
		t.Errorf("derived |Lc| = %.2f < 75", got)
	}
}
