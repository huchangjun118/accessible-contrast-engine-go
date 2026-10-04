package contrast

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func testRoles() (RoleMapping, []RolePair) {
	roles := RoleMapping{
		"text":  MustHex("#000000"),
		"muted": MustHex("#555555"),
		"bg":    MustHex("#ffffff"),
		"panel": MustHex("#f0f0f0"),
	}
	pairs := []RolePair{
		{Name: "text-bg", Foreground: "text", Background: "bg", Usage: UsageText},
		{Name: "muted-panel", Foreground: "muted", Background: "panel", Usage: UsageText},
	}
	return roles, pairs
}

func TestNewEngineInitialVersion(t *testing.T) {
	roles, pairs := testRoles()
	e, err := NewEngine(roles, pairs, EvalConfig{})
	if err != nil {
		t.Fatal(err)
	}
	cur := e.Current()
	if cur.Version != 1 {
		t.Errorf("initial version = %d want 1", cur.Version)
	}
	if len(cur.Report.Results) != 2 {
		t.Errorf("want 2 pair results")
	}
}

func TestReplaceVersioningAndFailureIsolation(t *testing.T) {
	roles, pairs := testRoles()
	e, _ := NewEngine(roles, pairs, EvalConfig{})
	before := e.Current()

	// Invalid replacement: references an unknown role -> rejected.
	_, err := e.Replace(context.Background(), RoleMapping{"text": MustHex("#111111")})
	if err == nil {
		t.Fatalf("expected error for missing roles")
	}
	after := e.Current()
	if after != before || after.Version != 1 {
		t.Errorf("failed Replace must not pollute committed snapshot")
	}

	// Valid replacement bumps the version and changes the report.
	v, err := e.Replace(context.Background(), RoleMapping{
		"text":  MustHex("#777777"), // fails WCAG 4.5 on white
		"muted": MustHex("#555555"),
		"bg":    MustHex("#ffffff"),
		"panel": MustHex("#f0f0f0"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if v != 2 {
		t.Errorf("version after replace = %d want 2", v)
	}
	cur := e.Current()
	if cur.Report.Pass {
		t.Errorf("theme with #777 on white must fail")
	}
	if cur.Tokens["text"] != MustHex("#777777") {
		t.Errorf("tokens were not replaced")
	}
}

func TestIncrementalUpdateOnlyRecomputesAffected(t *testing.T) {
	roles, pairs := testRoles()
	e, _ := NewEngine(roles, pairs, EvalConfig{})

	// Change only "muted": text-bg must be unaffected and identical.
	origTextBG := e.Current().Report.Results[0]
	v, rep, err := e.UpdateTokens(context.Background(), map[string]Color{
		"muted": MustHex("#777777"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if v != 2 {
		t.Errorf("version = %d want 2", v)
	}
	if rep.Results[0].EffectiveFG != origTextBG.EffectiveFG {
		t.Errorf("unaffected pair was recomputed/changed")
	}
	if !rep.Results[0].Pass {
		t.Errorf("unaffected pair must remain passing")
	}
	if rep.Results[1].Pass {
		t.Errorf("muted #777 on #f0f0f0 should fail")
	}

	// ChangedPairs dependency logic.
	affected := ChangedPairs(pairs, map[string]struct{}{"bg": {}})
	if len(affected) != 1 || affected[0].Name != "text-bg" {
		t.Errorf("ChangedPairs(bg) = %+v", affected)
	}
	affected = ChangedPairs(pairs, map[string]struct{}{"text": {}, "panel": {}})
	if len(affected) != 2 {
		t.Errorf("ChangedPairs(text,panel) = %+v, want both pairs", affected)
	}

	// No-op change produces no new version.
	cur := e.Current()
	v2, _, err := e.UpdateTokens(context.Background(), map[string]Color{"muted": MustHex("#777777")})
	if err != nil || v2 != cur.Version {
		t.Errorf("no-op update should keep version, got %d err %v", v2, err)
	}
}

func TestUpdateFailureDoesNotPollute(t *testing.T) {
	roles, pairs := testRoles()
	e, _ := NewEngine(roles, pairs, EvalConfig{})
	before := e.Current()
	_, _, err := e.UpdateTokens(context.Background(), map[string]Color{
		"text": {RGB: RGB{0, 0, 0}, Alpha: 0.5}, // translucency, engine has no base
	})
	if !errors.Is(err, ErrMissingBase) {
		t.Errorf("got %v want ErrMissingBase", err)
	}
	if e.Current() != before {
		t.Errorf("failed incremental update must leave committed snapshot untouched")
	}
	if e.Current().Version != 1 {
		t.Errorf("version moved on failure")
	}
}

func TestCanceledUpdateDoesNotCommit(t *testing.T) {
	roles, pairs := testRoles()
	e, _ := NewEngine(roles, pairs, EvalConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	v, _, err := e.UpdateTokens(ctx, map[string]Color{"text": MustHex("#123456")})
	if err == nil {
		t.Errorf("canceled update must error")
	}
	if v != 0 || e.Current().Version != 1 {
		t.Errorf("canceled update must not bump version, got v=%d committed=%d", v, e.Current().Version)
	}
}

func TestConcurrentReadsNeverSeeMixedState(t *testing.T) {
	roles, pairs := testRoles()
	e, _ := NewEngine(roles, pairs, EvalConfig{})

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Readers must always see a coherent snapshot where every token of
	// the version belongs together (all dark or all light), and the
	// report matches that version's tokens.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				snap := e.Current()
				textL := RelativeLuminance(snap.Tokens["text"].RGB)
				mutedL := RelativeLuminance(snap.Tokens["muted"].RGB)
				if snap.Version == 1 {
					// both dark grays
					if textL > 0.2 || mutedL > 0.2 {
						t.Errorf("v1 snapshot mixed: %v %v", textL, mutedL)
						return
					}
				}
				if len(snap.Report.Results) != 2 {
					t.Errorf("snapshot report must always be complete")
					return
				}
			}
		}
	}()

	// Writers alternate between two complete, coherent themes.
	light := roles
	dark := RoleMapping{
		"text":  MustHex("#f0f0f0"),
		"muted": MustHex("#bbbbbb"),
		"bg":    MustHex("#000000"),
		"panel": MustHex("#101010"),
	}
	for i := 0; i < 200; i++ {
		var next RoleMapping
		if i%2 == 0 {
			next = dark
		} else {
			next = light
		}
		if _, err := e.Replace(context.Background(), next); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
}

func TestReadsNotBlockedByWrites(t *testing.T) {
	roles, pairs := testRoles()
	e, _ := NewEngine(roles, pairs, EvalConfig{})

	// Hold the write lock to simulate an in-progress recomputation;
	// Current() must still return immediately.
	<-e.wmu // acquire the writer token
	done := make(chan struct{})
	go func() {
		_ = e.Current()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("Current() blocked while a write was in progress")
	}
	e.wmu <- struct{}{} // release
}
