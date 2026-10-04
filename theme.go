package contrast

import "fmt"

// Theme is a mapping of semantic roles to colors. Treat values as
// immutable after construction; the Store manages replacement versions.
type Theme struct {
	Name   string
	Tokens map[Role]Color
}

// Combo is one foreground/background role pairing that must be judged.
type Combo struct {
	// Name is an optional human-readable identifier.
	Name string
	// Foreground and Background are semantic roles in the theme.
	Foreground Role
	Background Role
	// Usage selects the requirement (text vs UI, level key).
	Usage TextUsage
	// Font is used by font-aware models (APCA); zero for UI pairs.
	Font FontSize
	// ModelLevel optionally overrides the level key per model.
	ModelLevel map[ModelID]string
}

// RoleGamut records the gamut mapping of one token involved in a combo.
type RoleGamut struct {
	Role Role
	Map  GamutMapping
}

// ComboReport is the independent per-combo conclusion.
type ComboReport struct {
	Combo Combo
	// Trace explains translucent compositing; nil when fully opaque.
	Trace *CompositeTrace
	// Gamut records every token mapping applied to this combo.
	Gamut []RoleGamut
	// ComparedFG / ComparedBG are the exact opaque sRGB colors scored.
	ComparedFG Color
	ComparedBG Color
	// Verdicts holds one independent verdict per model, keyed by model ID.
	Verdicts map[ModelID]Verdict
}

// Pass reports this combo's conclusion under one specific model.
func (r *ComboReport) Pass(id ModelID) bool {
	if r == nil {
		return false
	}
	v, ok := r.Verdicts[id]
	return ok && v.Pass
}

// Report is the full evaluation result for a theme version.
type Report struct {
	// Base is the backdrop used to flatten translucent tokens.
	Base Color
	// HasBase distinguishes "base supplied" from the zero value.
	HasBase bool
	// Models are the model IDs evaluated, in order.
	Models []ModelID
	// Combos holds results in the requested order.
	Combos []ComboReport
	// RolesAffected lists roles whose combos were (re)computed; when nil
	// the whole theme was evaluated.
	RolesAffected []Role
}

// AllPass reports whether every combo passes under every model.
func (r *Report) AllPass() bool {
	if r == nil {
		return false
	}
	for _, c := range r.Combos {
		for _, v := range c.Verdicts {
			if !v.Pass {
				return false
			}
		}
	}
	return true
}

// PassUnder reports whether every combo passes under one specific model.
func (r *Report) PassUnder(id ModelID) bool {
	if r == nil {
		return false
	}
	for _, c := range r.Combos {
		v, ok := c.Verdicts[id]
		if !ok || !v.Pass {
			return false
		}
	}
	return true
}

// EvalConfig configures an evaluation run.
type EvalConfig struct {
	// Models are evaluated independently; defaults to DefaultModels().
	Models []Model
	// Base / BaseSet: the mandatory backdrop for translucent tokens.
	// The caller must set BaseSet=true whenever translucent colors may
	// occur; otherwise evaluation fails with ErrMissingBase rather than
	// silently flattening onto black.
	Base    Color
	BaseSet bool
}

// NewTheme constructs a theme from a token map (copied defensively).
func NewTheme(name string, tokens map[Role]Color) *Theme {
	cp := make(map[Role]Color, len(tokens))
	for k, v := range tokens {
		cp[k] = v
	}
	return &Theme{Name: name, Tokens: cp}
}

// Get returns a token and reports presence.
func (t *Theme) Get(r Role) (Color, bool) {
	c, ok := t.Tokens[r]
	return c, ok
}

func (cfg EvalConfig) models() []Model {
	if len(cfg.Models) > 0 {
		return cfg.Models
	}
	return DefaultModels()
}

// Evaluate evaluates every combo under every model.
//
// Deterministic pipeline per combo:
//  1. resolve both roles (ErrUnknownRole otherwise);
//  2. reject translucency when no backdrop was supplied
//     (ErrMissingBase, never an implicit black);
//  3. composite background-over-base then foreground-over-background;
//  4. gamut-map each token used and the two flattened colors;
//  5. score every model independently and record a separate Verdict.
func Evaluate(theme *Theme, combos []Combo, cfg EvalConfig) (*Report, error) {
	if theme == nil {
		return nil, fmt.Errorf("contrast: nil theme")
	}
	models := cfg.models()
	rep := &Report{
		Base:    cfg.Base,
		HasBase: cfg.BaseSet,
		Models:  make([]ModelID, 0, len(models)),
		Combos:  make([]ComboReport, 0, len(combos)),
	}
	for _, m := range models {
		rep.Models = append(rep.Models, m.ID())
	}
	for _, cb := range combos {
		cr, err := evalCombo(theme, cb, cfg, models)
		if err != nil {
			return nil, err
		}
		rep.Combos = append(rep.Combos, cr)
	}
	return rep, nil
}

func evalCombo(theme *Theme, cb Combo, cfg EvalConfig, models []Model) (ComboReport, error) {
	fgCol, ok := theme.Get(cb.Foreground)
	if !ok {
		return ComboReport{}, &UnknownRoleError{Role: cb.Foreground}
	}
	bgCol, ok := theme.Get(cb.Background)
	if !ok {
		return ComboReport{}, &UnknownRoleError{Role: cb.Background}
	}

	cr := ComboReport{Combo: cb, Verdicts: map[ModelID]Verdict{}}

	// Mandatory backdrop check: translucency without a declared base is a
	// hard rejection. Black is never assumed.
	if (fgCol.IsTranslucent() || bgCol.IsTranslucent()) && !cfg.BaseSet {
		stage := "foreground"
		role := cb.Foreground
		if bgCol.IsTranslucent() {
			stage, role = "background", cb.Background
		}
		return cr, &MissingBaseError{Role: role, Stage: stage}
	}

	var finalFG, finalBG Color
	var maps []RoleGamut
	if fgCol.IsTranslucent() || bgCol.IsTranslucent() {
		f, b, tr := ResolveTranslucency(fgCol, bgCol, cfg.Base)
		tr.Steps[0].Role = cb.Background
		tr.Steps[1].Role = cb.Foreground
		cr.Trace = tr
		finalFG, finalBG = f, b
	} else {
		finalFG, finalBG = fgCol, bgCol
	}

	// Record gamut mapping for the raw tokens and map the colors that are
	// actually scored, so out-of-gamut input never changes semantics.
	mappedFG, mgFG := MapToGamut(finalFG)
	mappedBG, mgBG := MapToGamut(finalBG)
	maps = append(maps,
		RoleGamut{Role: cb.Foreground, Map: mgFG},
		RoleGamut{Role: cb.Background, Map: mgBG},
	)
	cr.Gamut = maps
	cr.ComparedFG = mappedFG
	cr.ComparedBG = mappedBG

	for _, mdl := range models {
		usage := cb.Usage
		if lv, ok := cb.ModelLevel[mdl.ID()]; ok {
			usage = TextUsage{Text: usage.Text, Level: lv}
		}
		score := mdl.Score(mappedFG, mappedBG)
		req := mdl.RequiredScore(usage, cb.Font)
		v := Verdict{
			Model:    mdl.ID(),
			Score:    score,
			Required: req,
			Pass:     mdl.Passes(score, usage, cb.Font),
		}
		switch mdl.ID() {
		case ModelAPCA:
			switch {
			case score == 0:
				v.LevelKey = "below-clamp"
				v.Note = "|Lc| below 0.1 clamp; pair treated as indistinguishable"
			case score > 0:
				v.LevelKey = usage.Level
				v.Note = "normal polarity: dark foreground on lighter background"
			default:
				v.LevelKey = usage.Level
				v.Note = "reverse polarity: light foreground on darker background"
			}
			if v.LevelKey == "" {
				if !usage.Text {
					v.LevelKey = "ui"
				} else if cb.Font.Pixels >= 24 {
					v.LevelKey = "large"
				} else {
					v.LevelKey = "normal"
				}
			}
		default:
			v.LevelKey = usage.Level
			if v.LevelKey == "" {
				if !usage.Text {
					v.LevelKey = "ui"
				} else {
					v.LevelKey = "AA"
				}
			}
		}
		cr.Verdicts[mdl.ID()] = v
	}
	return cr, nil
}

// EvaluateAffected incrementally (re)evaluates only combos touching the
// changed roles, reusing unaffected results from the previous report. The
// previous report must have been produced with the same combo set, models
// and base; structural mismatches cause a full evaluation instead.
func EvaluateAffected(theme *Theme, combos []Combo, cfg EvalConfig, prev *Report, changed []Role) (*Report, error) {
	changedSet := map[Role]struct{}{}
	for _, r := range changed {
		changedSet[r] = struct{}{}
	}
	models := cfg.models()

	// Fall back to full evaluation when there is nothing reusable.
	if prev == nil || !reportsCompatible(prev, combos, cfg, models) || len(changed) == 0 {
		rep, err := Evaluate(theme, combos, cfg)
		if err != nil {
			return nil, err
		}
		if len(changed) > 0 {
			rep.RolesAffected = append(rep.RolesAffected, changed...)
		}
		return rep, nil
	}

	rep := &Report{
		Base:          cfg.Base,
		HasBase:       cfg.BaseSet,
		Models:        append([]ModelID(nil), prev.Models...),
		Combos:        make([]ComboReport, len(combos)),
		RolesAffected: append([]Role(nil), changed...),
	}
	for i, cb := range combos {
		if _, f := changedSet[cb.Foreground]; !f {
			if _, b := changedSet[cb.Background]; !b {
				// Unaffected: reuse the previously computed verdict verbatim.
				rep.Combos[i] = prev.Combos[i]
				continue
			}
		}
		cr, err := evalCombo(theme, cb, cfg, models)
		if err != nil {
			return nil, err
		}
		rep.Combos[i] = cr
	}
	return rep, nil
}

func reportsCompatible(prev *Report, combos []Combo, cfg EvalConfig, models []Model) bool {
	if len(prev.Combos) != len(combos) {
		return false
	}
	if prev.HasBase != cfg.BaseSet {
		return false
	}
	if len(prev.Models) != len(models) {
		return false
	}
	for i, m := range models {
		if prev.Models[i] != m.ID() {
			return false
		}
	}
	for i, cb := range combos {
		p := prev.Combos[i].Combo
		if p.Foreground != cb.Foreground || p.Background != cb.Background || p.Name != cb.Name {
			return false
		}
	}
	return true
}
