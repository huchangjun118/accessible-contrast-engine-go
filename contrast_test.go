package contrast

import (
	"math"
	"testing"
)

func TestParseHex(t *testing.T) {
	cases := []struct {
		in   string
		rgba [4]float64
	}{
		{"#000000", [4]float64{0, 0, 0, 1}},
		{"#ffffff", [4]float64{1, 1, 1, 1}},
		{"#f00", [4]float64{1, 0, 0, 1}},
		{"#00ff0080", [4]float64{0, 1, 0, 128.0 / 255.0}},
		{"fff", [4]float64{1, 1, 1, 1}},
		{"1234", [4]float64{0x11 / 255.0, 0x22 / 255.0, 0x33 / 255.0, 0x44 / 255.0}},
	}
	for _, tc := range cases {
		c, err := ParseHex(tc.in)
		if err != nil {
			t.Fatalf("ParseHex(%q) error: %v", tc.in, err)
		}
		got := [4]float64{c.RGB.R, c.RGB.G, c.RGB.B, c.Alpha}
		for i := range got {
			if math.Abs(got[i]-tc.rgba[i]) > 1e-12 {
				t.Errorf("ParseHex(%q) channel %d = %v want %v", tc.in, i, got[i], tc.rgba[i])
			}
		}
	}

	for _, bad := range []string{"#12", "#12345", "#xyzxyz", "#0000000000"} {
		if _, err := ParseHex(bad); err == nil {
			t.Errorf("ParseHex(%q) expected error", bad)
		}
	}
}

func TestWCAGKnownRatios(t *testing.T) {
	black := MustHex("#000000").RGB
	white := MustHex("#ffffff").RGB
	if got := wcagRatio(black, white); math.Abs(got-21) > 1e-9 {
		t.Errorf("black/white ratio = %.6f want 21", got)
	}
	// WCAG reference: #767676 on #ffffff is 4.54:1.
	gray := MustHex("#767676").RGB
	got := wcagRatio(gray, white)
	if got < 4.53 || got > 4.55 {
		t.Errorf("#767676/#fff ratio = %.3f want ~4.54", got)
	}
	// Symmetry (unordered pair).
	if math.Abs(wcagRatio(white, black)-wcagRatio(black, white)) > 1e-9 {
		t.Errorf("wcag ratio must be polarity-independent")
	}
}

func TestRelativeLuminance(t *testing.T) {
	if got := RelativeLuminance(MustHex("#000000").RGB); math.Abs(got) > 1e-12 {
		t.Errorf("black luminance = %v want 0", got)
	}
	if got := RelativeLuminance(MustHex("#ffffff").RGB); math.Abs(got-1) > 1e-9 {
		t.Errorf("white luminance = %v want 1", got)
	}
}

func TestAPCAPolarityAndZero(t *testing.T) {
	black := MustHex("#000000").RGB
	white := MustHex("#ffffff").RGB
	darkOnLight := apcaContrast(black, white)
	lightOnDark := apcaContrast(white, black)
	if darkOnLight <= 0 {
		t.Errorf("black-on-white Lc must be positive, got %.2f", darkOnLight)
	}
	if lightOnDark >= 0 {
		t.Errorf("white-on-black Lc must be negative, got %.2f", lightOnDark)
	}
	if math.Abs(math.Abs(darkOnLight)-math.Abs(lightOnDark)) > 1.0 {
		t.Errorf("expected symmetric |Lc|, got %.2f vs %.2f", darkOnLight, lightOnDark)
	}
	if got := apcaContrast(white, white); got != 0 {
		t.Errorf("identical colors Lc = %.2f want 0", got)
	}
}

func TestScoresIndependentPerModel(t *testing.T) {
	// #777777 on white: WCAG ~4.48 (fails 4.5 text), APCA |Lc| ~64
	// (also fails its 75 text threshold) but verdicts are independent.
	fg := MustHex("#777777").RGB
	bg := MustHex("#ffffff").RGB
	scores := ContrastScores(fg, bg, UsageText, nil)
	if len(scores) != 2 {
		t.Fatalf("got %d scores, want 2", len(scores))
	}
	if scores[0].Model == scores[1].Model {
		t.Fatalf("models must be distinct")
	}
	for _, s := range scores {
		if s.Model == ModelWCAG21 {
			if s.Signed != s.Value {
				t.Errorf("WCAG signed value should equal magnitude")
			}
			if s.Polarity != PolarityDarkOnLight {
				t.Errorf("polarity = %q", s.Polarity)
			}
		}
		if s.Model == ModelAPCA {
			if s.Signed <= 0 {
				t.Errorf("dark-on-light APCA signed value should be positive, got %.2f", s.Signed)
			}
			if math.Abs(s.Signed) != s.Value {
				t.Errorf("Value should be |Signed|")
			}
		}
	}
}

func TestCustomThresholdsPerModel(t *testing.T) {
	fg := MustHex("#0000ff").RGB
	bg := MustHex("#ffffff").RGB
	lo := map[Model]ModelThresholds{
		ModelWCAG21: {Model: ModelWCAG21, Text: 1},
		ModelAPCA:   {Model: ModelAPCA, Text: 1},
	}
	for _, s := range ContrastScores(fg, bg, UsageText, lo) {
		if !s.Pass {
			t.Errorf("%s should pass custom low threshold %.2f (got %.2f)", s.Model, s.Required, s.Value)
		}
	}
}
