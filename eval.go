package contrast

import "fmt"

// RoleMapping binds semantic role names to (possibly translucent,
// possibly out-of-gamut) colors.
type RoleMapping map[string]Color

// RolePair declares a key role combination: Foreground role seen against
// Background role, used in a way that selects a threshold.
type RolePair struct {
	Name       string
	Foreground string
	Background string
	Usage      Usage
}

// PairResult is the per-model evaluation of one RolePair. Scores for the
// two models are stored separately and never merge or overwrite.
type PairResult struct {
	Pair        RolePair
	EffectiveFG RGB
	EffectiveBG RGB
	GamutMapped bool
	Scores      []Score
	// Pass reports that every evaluated model passed.
	Pass bool
}

// Report is the full evaluation result for a set of role pairs.
type Report struct {
	Results []PairResult
	Pass    bool
}

// EvalConfig configures evaluation.
type EvalConfig struct {
	// Base is the mandatory surface when any pair uses translucency.
	Base *RGB
	// Thresholds defaults to DefaultThresholds() when nil.
	Thresholds map[Model]ModelThresholds
	// GamutMethod defaults to GamutMapOKLCh.
	GamutMethod GamutMappingMethod
}

// Evaluate composites, gamut-maps and scores every pair under every
// model, returning independent per-model verdicts.
func Evaluate(roles RoleMapping, pairs []RolePair, cfg EvalConfig) (Report, error) {
	thresholds := cfg.Thresholds
	if thresholds == nil {
		thresholds = DefaultThresholds()
	}

	report := Report{Results: make([]PairResult, 0, len(pairs)), Pass: true}
	for _, pair := range pairs {
		fg, okF := roles[pair.Foreground]
		bg, okB := roles[pair.Background]
		if !okF {
			return Report{}, fmt.Errorf("contrast: pair %q references unknown foreground role %q", pair.Name, pair.Foreground)
		}
		if !okB {
			return Report{}, fmt.Errorf("contrast: pair %q references unknown background role %q", pair.Name, pair.Background)
		}

		// Ordered compositing on the explicit base; translucent inputs
		// without a base are rejected here, never assumed on black.
		if cfg.Base == nil && (fg.IsTranslucent() || bg.IsTranslucent()) {
			return Report{}, fmt.Errorf("contrast: pair %q: %w", pair.Name, ErrMissingBase)
		}
		bgSurface := bg
		if cfg.Base != nil {
			bgSurface = sourceOver(bg, Color{RGB: *cfg.Base, Alpha: 1})
		}
		fgSurface := sourceOver(fg, bgSurface)

		gmBG := mapToGamutWith(bgSurface.RGB, cfg.GamutMethod)
		gmFG := mapToGamutWith(fgSurface.RGB, cfg.GamutMethod)

		scores := ContrastScores(gmFG.Mapped, gmBG.Mapped, pair.Usage, thresholds)
		pass := true
		for _, s := range scores {
			if !s.Pass {
				pass = false
				break
			}
		}
		if !pass {
			report.Pass = false
		}
		report.Results = append(report.Results, PairResult{
			Pair:        pair,
			EffectiveFG: gmFG.Mapped,
			EffectiveBG: gmBG.Mapped,
			GamutMapped: !gmBG.InGamut || !gmFG.InGamut,
			Scores:      scores,
			Pass:        pass,
		})
	}
	return report, nil
}
