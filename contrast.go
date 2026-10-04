package contrast

import "math"

// Model identifies a contrast algorithm.
type Model string

const (
	// ModelWCAG21 is the WCAG 2.x relative-luminance contrast ratio,
	// ranging 1..21. Higher means more contrast; polarity is ignored.
	ModelWCAG21 Model = "wcag21"

	// ModelAPCA is the perceptual APCA 0.0.98G-W3 (lightness contrast).
	// Values are signed Lc in roughly [-108,106]; the sign encodes
	// polarity (positive = dark text on light background).
	ModelAPCA Model = "apca0.0.98G"
)

// modelOrder fixes evaluation/iteration order so results are reproducible.
var modelOrder = []Model{ModelWCAG21, ModelAPCA}

// Usage describes how a role combination is used, which selects the
// applicable threshold within every model.
type Usage string

const (
	UsageText     Usage = "text"     // body text
	UsageLarge    Usage = "large"    // large/bold text
	UsageEnhanced Usage = "enhanced" // AAA / enhanced contrast
	UsageUI       Usage = "ui"       // non-text UI affordances
)

// ModelThresholds holds the pass thresholds for one model keyed by usage.
// Values are compared against the model's unsigned contrast magnitude
// (ratio for WCAG, |Lc| for APCA).
type ModelThresholds struct {
	Model    Model
	Text     float64
	Large    float64
	Enhanced float64
	UI       float64
}

// DefaultThresholds returns the built-in thresholds:
//
//	WCAG 2.1: text 4.5, large 3.0, enhanced 7.0, UI 3.0
//	APCA |Lc|: text 75, large 60, enhanced 90, UI 60
//
// APCA thresholds compare the absolute Lc magnitude; Lc itself keeps its
// sign so polarity information is never lost.
func DefaultThresholds() map[Model]ModelThresholds {
	return map[Model]ModelThresholds{
		ModelWCAG21: {Model: ModelWCAG21, Text: 4.5, Large: 3.0, Enhanced: 7.0, UI: 3.0},
		ModelAPCA:   {Model: ModelAPCA, Text: 75, Large: 60, Enhanced: 90, UI: 60},
	}
}

// Polarity describes which side of a pair is lighter as displayed.
type Polarity string

const (
	PolarityNone        Polarity = ""
	PolarityDarkOnLight Polarity = "dark-on-light"
	PolarityLightOnDark Polarity = "light-on-dark"
)

// Score is the verdict of one model for one role combination. Each model
// is evaluated independently; scores never overwrite each other.
type Score struct {
	Model    Model
	Value    float64 // unsigned magnitude used against Required
	Signed   float64 // signed perceptual value (Lc); 0 for WCAG
	Polarity Polarity
	Usage    Usage
	Required float64
	Pass     bool
}

// thresholdFor selects the required magnitude for u.
func (t ModelThresholds) thresholdFor(u Usage) float64 {
	switch u {
	case UsageLarge:
		return t.Large
	case UsageEnhanced:
		return t.Enhanced
	case UsageUI:
		return t.UI
	case UsageText, "":
		return t.Text
	default:
		return t.Text
	}
}

// RelativeLuminance implements WCAG 2.x relative luminance Y for a
// gamma-encoded sRGB color, assumed already in gamut.
func RelativeLuminance(c RGB) float64 {
	r := srgbChannelToLinear(c.R)
	g := srgbChannelToLinear(c.G)
	b := srgbChannelToLinear(c.B)
	return 0.2126*r + 0.7152*g + 0.0722*b
}

// wcagRatio returns the WCAG contrast ratio (1..21) between two in-gamut
// colors.
func wcagRatio(fg, bg RGB) float64 {
	l1 := RelativeLuminance(fg)
	l2 := RelativeLuminance(bg)
	lighter, darker := l1, l2
	if darker > lighter {
		lighter, darker = darker, lighter
	}
	return (lighter + 0.05) / (darker + 0.05)
}

// apcaContrast returns the signed APCA 0.0.98G-W3 Lc value for text fg on
// bg. Positive Lc means a darker foreground on a lighter background.
func apcaContrast(fg, bg RGB) float64 {
	const (
		apcaBlkThresh = 0.022
		apcaScale     = 1.14
		apcaLoClamp   = 0.1
		apcaLoClip    = 0.035991
		apcaDelta     = 0.027
	)
	txtY := RelativeLuminance(fg)
	bgY := RelativeLuminance(bg)

	var lc float64
	if bgY > txtY { // normal polarity: dark text on light background
		if bgY >= apcaBlkThresh {
			bgY = math.Pow(bgY, apcaNormBg)
		} else {
			bgY = bgY + (apcaBlkThresh-bgY)*math.Pow(bgY, apcaNormBg)
		}
		if txtY >= apcaBlkThresh {
			txtY = math.Pow(txtY, apcaNormFg)
		} else {
			txtY = txtY + (apcaBlkThresh-txtY)*math.Pow(txtY, apcaNormFg)
		}
		lc = (bgY - txtY) * apcaScale * 100
	} else { // reverse polarity: light text on dark background
		if bgY >= apcaBlkThresh {
			bgY = math.Pow(bgY, apcaRevBg)
		} else {
			bgY = bgY + (apcaBlkThresh-bgY)*math.Pow(bgY, apcaRevBg)
		}
		if txtY >= apcaBlkThresh {
			txtY = math.Pow(txtY, apcaRevFg)
		} else {
			txtY = txtY + (apcaBlkThresh-txtY)*math.Pow(txtY, apcaRevFg)
		}
		lc = (bgY - txtY) * apcaScale * 100
	}

	if lc < -apcaLoClip {
		lc += apcaDelta * 100
	} else if lc > apcaLoClip {
		lc -= apcaDelta * 100
	} else {
		lc = 0
	}
	if math.Abs(lc) < apcaLoClamp*100 {
		lc = 0
	}
	return math.Round(lc*100) / 100
}

const (
	apcaNormBg = 0.56
	apcaNormFg = 0.57
	apcaRevBg  = 0.65
	apcaRevFg  = 0.62
)

// polarityFor infers display polarity from the effective luminances.
func polarityFor(fg, bg RGB) Polarity {
	lf, lb := RelativeLuminance(fg), RelativeLuminance(bg)
	const tol = 1e-9
	switch {
	case lf < lb-tol:
		return PolarityDarkOnLight
	case lf > lb+tol:
		return PolarityLightOnDark
	default:
		return PolarityNone
	}
}

// ContrastScores evaluates one effective color pair under every known
// model independently. Colors are expected to be in gamut already.
func ContrastScores(fg, bg RGB, usage Usage, thresholds map[Model]ModelThresholds) []Score {
	if thresholds == nil {
		thresholds = DefaultThresholds()
	}
	pol := polarityFor(fg, bg)
	scores := make([]Score, 0, len(modelOrder))
	for _, m := range modelOrder {
		t, ok := thresholds[m]
		if !ok {
			continue
		}
		req := t.thresholdFor(usage)
		var s Score
		switch m {
		case ModelWCAG21:
			v := wcagRatio(fg, bg)
			s = Score{Model: m, Value: v, Signed: v, Polarity: pol, Usage: usage, Required: req, Pass: v >= req}
		case ModelAPCA:
			lc := apcaContrast(fg, bg)
			s = Score{Model: m, Value: math.Abs(lc), Signed: lc, Polarity: pol, Usage: usage, Required: req, Pass: math.Abs(lc) >= req}
		default:
			continue
		}
		scores = append(scores, s)
	}
	return scores
}
