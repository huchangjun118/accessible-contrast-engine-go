# Accessible Contrast Engine (Go)

A local, in-process color accessibility engine for design systems. It takes a
mapping of **semantic roles → colors**, produces independently verifiable
contrast conclusions under multiple models, and can **derive a high-contrast
theme variant** that keeps brand hue and the role hierarchy intact while
reaching accessibility targets.

There are **no external services and no real design assets**: everything is
computed in-process with deterministic math.

## Feature map

| Requirement | Where |
| --- | --- |
| Multiple contrast models, independent thresholds | `contrast.go` (`ModelWCAG`, `ModelAPCA`) |
| Alpha compositing in fixed order on an explicit base; missing base rejected | `color.go`, `theme.go` (`ErrMissingBase`) |
| Out-of-gamut colors mapped back reproducibly, hue preserved | `gamut.go` (OKLCH chroma reduction) |
| High-contrast derivation preserving hue + hierarchy, localized failures | `derive.go` (`DerivationFailure`) |
| Versioned replacement + incremental recompute, no torn reads | `store.go` (atomic snapshots) |
| Non-blocking reads; canceled/failed recompute never commits | `store.go` (`ApplyChange`, `ReplaceAll`) |
| Tests for all of the above | `*_test.go` |

## Color model

`Color` is an **sRGB-encoded** value with a **straight (non-premultiplied)
alpha** channel:

```go
type Color struct{ R, G, B, A float64 } // channels nominally in [0,1]
```

* Inputs accept hex (`#rgb`, `#rgba`, `#rrggbb`, `#rrggbbaa`) and CSS-like
  `rgb()/rgba()` (`ParseHex`, `ParseColor`).
* `R/G/B` outside `[0,1]` are **not** rejected at the value type — they are
  normalized deterministically by the gamut stage before any judgment (see
  below). `A` is clamped to `[0,1]`.
* Internal compositing and the WCAG/APCA luminance work in **linear-light
  sRGB** (channels are linearized with the IEC 61966 transfer function and
  re-encoded afterward).

### Translucent compositing

A translucent pair is flattened in a **mandatory, fixed order**:

1. `background` composited **over the caller-supplied base**;
2. `foreground` composited **over the result of step 1**.

Compositing is straight-alpha *source-over* performed in linear light. Every
operation is recorded in a `CompositeTrace` (`Stage`, `Source`, `Backdrop`,
`Result`) so the flattened colors that were actually scored are fully
explainable.

> **No implicit black.** If any token is translucent and no base was supplied
> (`EvalConfig{BaseSet: false}`), evaluation fails with
> `ErrMissingBase` (`*MissingBaseError` names the role and stage). The engine
> never silently flattens onto black.

For example, 50% black over a white base composites to linear `0.5`, which
encodes to sRGB `#bcbcbc`.

### Gamut mapping

Out-of-gamut colors are mapped with one fixed, total rule
(`gamutMapMethod = "oklch-chroma-reduction-bsearch-v1"`):

`sRGB → linear sRGB → XYZ(D65) → OKLab → OKLCH`, then **chroma is reduced by
binary search (64 steps) while hue H and lightness L are held constant** until
the color fits the sRGB cube (channels within a fixed epsilon of `[0,1]`).

* The same input always maps to the same output (reproducible).
* **Brand hue and lightness are preserved**; only the chroma needed to produce
  a displayable value changes.
* The mapping changes only the numeric value used for the accessibility
  judgment — it never changes which semantic role a token is bound to.
* Every mapping is reported (`GamutMapping`: `InitialChroma`, `FinalChroma`,
  `Hue`, `Lightness`, `Changed`) and collected per combo in
  `ComboReport.Gamut`.

## Contrast models and threshold semantics

The two models are always evaluated **independently**; each produces its own
`Verdict`, and one model's conclusion never overwrites the other's.

### 1. WCAG 2.x relative luminance (`ModelWCAG` = `wcag21-relative-luminance`)

`Y = 0.2126 Rlin + 0.7152 Glin + 0.0722 Blin`; the score is the symmetric
contrast ratio `(L_lighter+0.05)/(L_darker+0.05)` in **[1, 21]**.

| Key | Min ratio | Use |
| --- | --- | --- |
| `AA` | **4.5** | normal body text |
| `AA-large` | **3** | large text (≥24px, or ≥18.66px bold) |
| `AAA` | **7** | enhanced normal text |
| `AAA-large` | **4.5** | enhanced large text |
| `ui` | **3** | non-text/UI components |

### 2. APCA perceptual contrast (`ModelAPCA` = `apca-0.0.98g-4g`)

The Accessible Perceptual Contrast Algorithm (W3 draft, 0.0.98G-4g). The score
is **Lc**, on a roughly `0..108` perceptual scale — it is **not a ratio** and
must never be compared to WCAG numbers.

* **Sign is polarity**: `Lc > 0` = dark foreground on lighter background;
  `Lc < 0` = light foreground on darker background. `Lc == 0` means the pair is
  below the 0.1 clamp / indistinguishable.
* Luminance uses the sRGB/Rec709 primaries; near-black inputs get the soft-knee
  `Y + (0.022 − Y)^1.414`; normal/reverse polarity use different exponents
  (`0.56/0.57` and `0.65/0.62`); scale `1.14`, offset `0.027`, ×100.

| Key | Min \|Lc\| | Use |
| --- | --- | --- |
| `normal` | **90** | body text (<24px) |
| `large` | **75** | large text (≥24px, or ≥18px bold) |
| `ui` | **60** | UI components / non-text |

Passing compares **|Lc|** against the requirement; polarity is preserved on
the score and explained in `Verdict.Note`.

### Aggregates

`Report.AllPass()` requires every combo to pass **every** model.
`Report.PassUnder(modelID)` reports one model independently — e.g. a pair can
pass WCAG AA while failing APCA, and both verdicts remain visible.

## Usage

```go
theme := contrast.NewTheme("brand", map[contrast.Role]contrast.Color{
    "text/primary": must("#1a1a1a"),
    "surface/bg":   must("#ffffff"),
})
combos := []contrast.Combo{{
    Name: "body",
    Foreground: "text/primary",
    Background: "surface/bg",
    Usage: contrast.TextUsage{Text: true},
    Font:  contrast.FontSize{Pixels: 16, Weight: 400},
}}

// BaseSet is REQUIRED whenever any token may be translucent.
rep, err := contrast.Evaluate(theme, combos, contrast.EvalConfig{
    Models:  contrast.DefaultModels(), // WCAG + APCA
    Base:    must("#ffffff"),
    BaseSet: true,
})
fmt.Println(rep.Combos[0].Verdicts[contrast.ModelWCAG].Pass)
fmt.Println(rep.Combos[0].Verdicts[contrast.ModelAPCA].Score)
```

### Deriving a high-contrast variant

```go
derived, err := contrast.DeriveHighContrast(theme, combos, contrast.DeriveOptions{
    BaseSet: true,
    // keep surface no lighter than elevated, and both below text:
    PreserveDarkToLight: [][]contrast.Role{{"surface/bg", "surface/elev", "text/primary"}},
    Bounds: map[contrast.Role]contrast.RoleBound{
        "surface/bg": {MaxL: 0.25},
    },
    LightStep: 0.02,
})
```

Derivation works in OKLCH:

* **hue is locked per role** (brand hue never moves);
* the foreground/background **lightness gap is widened in the existing
  polarity** (light-on-dark stays light-on-dark and vice-versa);
* when lightness is blocked by a bound or an ordering group, **chroma is
  reduced** at fixed hue/L;
* per-role `Bounds` and `PreserveDarkToLight` groups keep the role hierarchy.

If it cannot satisfy a combo after `MaxIterations`, the error wraps
`ErrDerivationImpossible` and is a `*DerivationFailure` that **names each
unresolved role combination** with measured vs required score per model.

## Versioned store, incremental recompute, concurrency

```go
store, v1, err := contrast.NewStore(theme, combos, cfg)

// Local token change: only combos touching the changed role(s) recompute.
h := store.ApplyChange(ctx, map[contrast.Role]contrast.Color{"text/primary": c})
<-h.Done()

snap := store.Current() // never blocks; either v1 or the new version
```

* `Current()` is an **atomic pointer load** of an immutable `Snapshot`
  (`Theme` + `Report` + version). Readers observe either the old or the new
  version — **never a mixed/interleaved state**.
* `ApplyChange` (incremental; unchanged combos are reused verbatim) and
  `ReplaceAll` (full replacement) run recomputation **off the read path** in a
  goroutine. Evaluation cost is paid without holding the write lock; a single
  atomic swap publishes the candidate, and successful commits raise the version
  by exactly one (gapless under concurrency).
* A candidate is rechecked against `ctx` after preparation and immediately
  before commit. **Cancellation or evaluation error discards the candidate**;
  the committed snapshot is untouched.

## Configuration reference

| Field | Default | Meaning |
| --- | --- | --- |
| `EvalConfig.Models` | WCAG + APCA | models evaluated independently |
| `EvalConfig.Base` / `BaseSet` | unset | mandatory backdrop for translucent tokens; missing → `ErrMissingBase` |
| `WCAGModel.AA* / AAA* / UINonText` | 4.5/3/7/4.5/3 | WCAG threshold overrides |
| `APCAModel.NormalText/LargeText/UIComponent` | 90/75/60 | APCA \|Lc\| threshold overrides |
| `Combo.ModelLevel` | per model default | override the level key for one model on one combo |
| `Combo.Font.Pixels/Weight` | 0/400 | drives large-text thresholds (APCA/WCAG) |
| `DeriveOptions.Bounds` | none | per-role OKLCH lightness limits |
| `DeriveOptions.PreserveDarkToLight` | none | role groups whose L-ordering is locked |
| `DeriveOptions.MaxIterations` | 256 | adjustment budget |
| `DeriveOptions.LightStep / ChromaStep` | 0.01 / 0.01 | per-iteration increments |

## Errors

* `ErrMissingBase` (`*MissingBaseError`) — translucency without an explicit
  backdrop; the role/stage are named.
* `ErrInvalidColor` (`*InvalidColorError`) — unparseable color string.
* `ErrUnknownRole` (`*UnknownRoleError`) — combo references an absent role.
* `ErrUnknownModel` — unregistered model id.
* `ErrDerivationImpossible` (`*DerivationFailure`) — derivation converged with
  unresolved combos; each carries role names and per-model scores.

## Testing

```sh
go test -race ./...
```

Covered: multi-model independent verdicts (a pair passing WCAG AA but failing
APCA), fixed-order translucent compositing + explicit-base rejection,
deterministic OKLCH gamut mapping with hue preservation, derivation success,
hierarchy/polarity preservation, the localized impossible case, and
incremental/atomic store replacement under concurrent reads plus cancellation
and failure non-pollution.
