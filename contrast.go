package contrast

import (
	"math"
	"strings"
)

// ModelID identifies a contrast evaluation model. Both models are always
// evaluated independently; a verdict from one never overwrites the other.
type ModelID string

const (
	// ModelWCAG is the classic relative-luminance contrast ratio
	// (WCAG 2.x): ratio in [1,21], pass thresholds typically 4.5/3.
	ModelWCAG ModelID = "wcag21-relative-luminance"

	// ModelAPCA is the perceptual contrast model
	// (Accessible Perceptual Contrast Algorithm, W3 draft 0.0.98G-4g):
	// a lightness-perception based Lc value (sign depends on polarity),
	// with its own font-size-aware thresholds.
	ModelAPCA ModelID = "apca-0.0.98g-4g"
)

// FontSize classifies text size for models (APCA) whose required score
// depends on it.
type FontSize struct {
	// Pixels is the computed font size in CSS px (0 if not a text pair).
	Pixels float64
	// Weight is the CSS font weight (400 normal, 700 bold).
	Weight int
}

// TextUsage describes how a foreground/background pair is used.
type TextUsage struct {
	// Text is true for text-bearing pairs; false for non-text UI.
	Text bool
	// Level is a model-defined requirement key, e.g. for WCAG:
	// "AA"/"AAA"; for APCA: "normal"/"large"/"ui".
	// Empty means the model's default body-text level.
	Level string
}

// Level describes one named pass threshold for documentation/UI display.
type Level struct {
	Key       string
	MinScore  float64
	FontMinPx float64
	FontMaxPx float64
}

// Model evaluates contrast for one perceptual/mathematical model.
type Model interface {
	ID() ModelID
	Score(fg, bg Color) float64
	RequiredScore(text TextUsage, fs FontSize) float64
	Passes(score float64, text TextUsage, fs FontSize) bool
	Levels() []Level
}

// Verdict is one model's independent conclusion for one combo.
type Verdict struct {
	Model ModelID
	// Score is the raw model score (WCAG ratio, or APCA Lc).
	Score float64
	// Required is the score the usage demanded (positive magnitude).
	Required float64
	// LevelKey is the named threshold that applied.
	LevelKey string
	// Pass is the independent conclusion under this model.
	Pass bool
	// Note carries model-specific interpretation (e.g. APCA polarity).
	Note string
}

// ModelByID finds a registered model by ID (false when absent).
func ModelByID(models []Model, id ModelID) (Model, bool) {
	for _, m := range models {
		if m.ID() == id {
			return m, true
		}
	}
	return nil, false
}

// ------------------------------------------------------------------------
// WCAG 2.x relative luminance
// ------------------------------------------------------------------------

// WCAGModel is the classic relative-luminance ratio.
type WCAGModel struct {
	AANormal  float64
	AALarge   float64
	AAANormal float64
	AAALarge  float64
	UINonText float64
}

func (m *WCAGModel) withDefaults() *WCAGModel {
	n := *m
	if n.AANormal == 0 {
		n.AANormal = 4.5
	}
	if n.AALarge == 0 {
		n.AALarge = 3
	}
	if n.AAANormal == 0 {
		n.AAANormal = 7
	}
	if n.AAALarge == 0 {
		n.AAALarge = 4.5
	}
	if n.UINonText == 0 {
		n.UINonText = 3
	}
	return &n
}

func (m *WCAGModel) ID() ModelID { return ModelWCAG }

func (m *WCAGModel) Score(fg, bg Color) float64 { return WCAGContrast(fg, bg) }

func (m *WCAGModel) Levels() []Level {
	d := m.withDefaults()
	return []Level{
		{Key: "AA", MinScore: d.AANormal, FontMaxPx: 23.99},
		{Key: "AA-large", MinScore: d.AALarge, FontMinPx: 24},
		{Key: "AAA", MinScore: d.AAANormal, FontMaxPx: 23.99},
		{Key: "AAA-large", MinScore: d.AAALarge, FontMinPx: 24},
		{Key: "ui", MinScore: d.UINonText},
	}
}

func (m *WCAGModel) RequiredScore(t TextUsage, fs FontSize) float64 {
	d := m.withDefaults()
	if !t.Text {
		return d.UINonText
	}
	large := fs.Pixels >= 24 || (fs.Pixels >= 18.66 && fs.Weight >= 700)
	switch strings.ToLower(strings.ReplaceAll(t.Level, "-", "")) {
	case "aaa", "aaanormal":
		if large {
			return d.AAALarge
		}
		return d.AAANormal
	case "aaalarge":
		return d.AAALarge
	case "aalarge":
		return d.AALarge
	case "aa", "":
		if large {
			return d.AALarge
		}
		return d.AANormal
	}
	return d.AANormal
}

func (m *WCAGModel) Passes(score float64, t TextUsage, fs FontSize) bool {
	return score+1e-9 >= m.RequiredScore(t, fs)
}

// RelativeLuminance computes WCAG relative luminance Y (0..1) from an
// sRGB-encoded opaque color.
func RelativeLuminance(c Color) float64 {
	return 0.2126*linearize(c.R) + 0.7152*linearize(c.G) + 0.0722*linearize(c.B)
}

// WCAGContrast computes the WCAG 2.x contrast ratio in [1,21]:
//
//	(L_lighter + 0.05) / (L_darker + 0.05)
func WCAGContrast(fg, bg Color) float64 {
	yl, yd := RelativeLuminance(fg), RelativeLuminance(bg)
	lighter, darker := yl, yd
	if darker > lighter {
		lighter, darker = darker, lighter
	}
	return (lighter + 0.05) / (darker + 0.05)
}

// ------------------------------------------------------------------------
// APCA 0.0.98G-4g (W3 perceptual contrast)
//
// Reference behavior of the 0.0.98G-4g draft: luminance Y uses the
// sRGB/Rec709 primaries; near-black inputs are soft-clamped at 0.022;
// normal and reverse polarity use different perceptual exponents; a
// scale of 1.14 and a 0.027 offset are applied before the x100 output.
// ------------------------------------------------------------------------

// APCAModel is the perceptual-contrast model.
type APCAModel struct {
	// Required |Lc| magnitudes per usage; the W3 defaults are applied
	// when zero: NormalText 90, LargeText 75, UIComponent 60.
	NormalText  float64
	LargeText   float64
	UIComponent float64
}

func (m *APCAModel) withDefaults() *APCAModel {
	n := *m
	if n.NormalText == 0 {
		n.NormalText = 90
	}
	if n.LargeText == 0 {
		n.LargeText = 75
	}
	if n.UIComponent == 0 {
		n.UIComponent = 60
	}
	return &n
}

func (m *APCAModel) ID() ModelID { return ModelAPCA }

func (m *APCAModel) Score(fg, bg Color) float64 { return APCAContrast(fg, bg) }

func (m *APCAModel) Levels() []Level {
	d := m.withDefaults()
	return []Level{
		{Key: "normal", MinScore: d.NormalText, FontMaxPx: 23.99},
		{Key: "large", MinScore: d.LargeText, FontMinPx: 24},
		{Key: "ui", MinScore: d.UIComponent},
	}
}

func (m *APCAModel) RequiredScore(t TextUsage, fs FontSize) float64 {
	d := m.withDefaults()
	if !t.Text {
		return d.UIComponent
	}
	switch strings.ToLower(t.Level) {
	case "large":
		return d.LargeText
	case "ui":
		return d.UIComponent
	case "normal", "":
		if fs.Pixels >= 24 || (fs.Pixels >= 18 && fs.Weight >= 700) {
			return d.LargeText
		}
		return d.NormalText
	}
	return d.NormalText
}

// Passes compares the magnitude |Lc| with the required magnitude. Polarity
// (sign of Lc) is preserved in the score and reported, but passing depends
// on magnitude, per the model.
func (m *APCAModel) Passes(score float64, t TextUsage, fs FontSize) bool {
	return math.Abs(score)+1e-9 >= m.RequiredScore(t, fs)
}

const (
	apcBlkThrs  = 0.022
	apcBlkClmp  = 1.414 // soft-knee exponent applied below the black threshold
	apcScale    = 1.14
	apcOffset   = 0.027
	apcLoClip   = 0.1
	apcDeltaMin = 0.0005

	apcExpBG   = 0.56 // normal polarity: background exponent
	apcExpTXT  = 0.57 // normal polarity: text exponent
	apcExpRBG  = 0.65 // reverse polarity: background exponent
	apcExpRTXT = 0.62 // reverse polarity: text exponent
)

// apcaY computes APCA luminance Y from an encoded sRGB color using the
// sRGB/Rec709 primaries.
func apcaY(c Color) float64 {
	return 0.2126729*linearize(c.R) +
		0.7151522*linearize(c.G) +
		0.0721750*linearize(c.B)
}

// apcaClampBlack applies the draft's deterministic soft-knee: luminance
// below blkThrs is lifted by (blkThrs - Y)^blkClmp so deep blacks are
// perceptualized smoothly instead of hard-clipped.
func apcaClampBlack(y float64) float64 {
	if y < apcBlkThrs {
		return y + math.Pow(apcBlkThrs-y, apcBlkClmp)
	}
	return y
}

// APCAContrast computes the perceptual Lc value per the 0.0.98G-4g draft.
//   - Lc > 0 : dark foreground on lighter background (normal polarity)
//   - Lc < 0 : light foreground on darker background (reverse polarity)
//   - Lc == 0: contrast below the clamp / indistinguishable pair
//
// Magnitudes are roughly on a 0..108 perceptual scale; they are NOT a
// ratio and must not be compared to WCAG thresholds.
func APCAContrast(fg, bg Color) float64 {
	txtY := apcaY(fg)
	bgY := apcaY(bg)

	if math.Abs(bgY-txtY) < apcDeltaMin {
		return 0
	}

	var sapc float64
	if bgY > txtY {
		// dark text on lighter background
		sapc = (math.Pow(bgY, apcExpBG) -
			math.Pow(apcaClampBlack(txtY), apcExpTXT)) * apcScale
		if sapc < apcLoClip {
			return 0
		}
		lc := (sapc - apcOffset) * 100
		if lc < 0 {
			return 0
		}
		return lc
	}

	// light text on darker background
	sapc = (math.Pow(apcaClampBlack(bgY), apcExpRBG) -
		math.Pow(txtY, apcExpRTXT)) * apcScale
	if sapc > -apcLoClip {
		return 0
	}
	lc := (sapc + apcOffset) * 100
	if lc > 0 {
		return 0
	}
	return lc
}

// DefaultModels returns the two built-in models with standard thresholds.
func DefaultModels() []Model {
	return []Model{&WCAGModel{}, &APCAModel{}}
}
