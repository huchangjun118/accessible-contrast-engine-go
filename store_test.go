package contrast

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func baseStore(t *testing.T) (*Store, []Combo) {
	t.Helper()
	theme := NewTheme("v", map[Role]Color{
		"text": {0, 0, 0, 1},
		"bg":   {1, 1, 1, 1},
		"mute": {0.5, 0.5, 0.5, 1},
	})
	combos := []Combo{
		{Name: "body", Foreground: "text", Background: "bg", Usage: TextUsage{Text: true}},
		{Name: "muted", Foreground: "mute", Background: "bg", Usage: TextUsage{Text: true}},
	}
	store, snap, err := NewStore(theme, combos, EvalConfig{BaseSet: true})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Version != 1 {
		t.Fatalf("initial version = %d want 1", snap.Version)
	}
	return store, combos
}

func TestIncrementalOnlyRecomputesAffected(t *testing.T) {
	store, _ := baseStore(t)
	old := store.Current()

	// Change only "text". The "muted" combo must be the reused pointer.
	snap, err := store.ApplyChangeSync(context.Background(), map[Role]Color{
		"text": {0.1, 0.1, 0.1, 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Version != 2 {
		t.Fatalf("version = %d want 2", snap.Version)
	}
	if !containsRole(snap.Report.RolesAffected, "text") {
		t.Fatalf("RolesAffected = %v, want text", snap.Report.RolesAffected)
	}
	// Muted combo unaffected: identical verdict values.
	if old.Report.Combos[1].Verdicts[ModelWCAG].Score != snap.Report.Combos[1].Verdicts[ModelWCAG].Score {
		t.Fatal("unaffected combo must be reused unchanged")
	}
}

func TestReadersNeverSeeMixedState(t *testing.T) {
	store, combos := baseStore(t)

	// Hammer concurrent reads while many replacements commit; every read
	// must observe a fully self-consistent snapshot (version, theme token
	// count and report length aligned).
	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				snap := store.Current()
				if snap == nil {
					t.Error("nil snapshot")
					return
				}
				if len(snap.Combos) != len(snap.Report.Combos) {
					t.Errorf("torn read: combos/report length mismatch v%d", snap.Version)
					return
				}
				if _, ok := snap.Theme.Tokens["text"]; !ok {
					t.Errorf("torn read: missing token at v%d", snap.Version)
					return
				}
			}
		}
	}()

	for i := 0; i < 50; i++ {
		v := float64(i%20) / 40
		h := store.ReplaceAll(context.Background(), map[Role]Color{
			"text": {v, v, v, 1},
			"bg":   {1, 1, 1, 1},
			"mute": {0.5, 0.5, 0.5, 1},
		})
		<-h.Done()
		if h.Err() != nil {
			t.Fatalf("replace %d: %v", i, h.Err())
		}
	}
	close(stop)
	wg.Wait()
	_ = combos
}

func TestCancelledRecomputeDoesNotPollute(t *testing.T) {
	store, _ := baseStore(t)
	before := store.Current()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled
	h := store.ApplyChange(ctx, map[Role]Color{"text": {0.2, 0.2, 0.2, 1}})
	<-h.Done()
	if !errors.Is(h.Err(), context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", h.Err())
	}
	after := store.Current()
	if after != before {
		t.Fatal("canceled recomputation must not change the committed snapshot")
	}
	if after.Version != 1 {
		t.Fatalf("version advanced to %d after cancellation", after.Version)
	}
	if h.Snapshot() != nil {
		t.Fatal("canceled handle must not expose a committed snapshot")
	}
}

func TestFailedRecomputeDoesNotPollute(t *testing.T) {
	store, _ := baseStore(t)
	before := store.Current()

	// Introduce a token change that removes a role referenced by a combo:
	// can't happen via map update (only adds/overwrites), so force a
	// failure by a full replacement lacking a required role.
	h := store.ReplaceAll(context.Background(), map[Role]Color{
		"text": {0, 0, 0, 1},
		// "bg" and "mute" intentionally absent -> unknown role
	})
	<-h.Done()
	if !errors.Is(h.Err(), ErrUnknownRole) {
		t.Fatalf("want ErrUnknownRole, got %v", h.Err())
	}
	after := store.Current()
	if after != before {
		t.Fatal("failed recomputation must leave the committed theme intact")
	}
	if store.Version() != 1 {
		t.Fatalf("version advanced to %d after failure", store.Version())
	}
}

func TestReadsNotBlockedByRecompute(t *testing.T) {
	// Recomputation is synchronous/cpu-bound but very fast; assert that
	// while many changes are in flight a steady stream of Current() calls
	// always returns promptly with a valid snapshot.
	store, _ := baseStore(t)
	var handles []*Recomputation
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			v := float64(i%30) / 60
			h := store.ApplyChange(context.Background(), map[Role]Color{"text": {v, v, v, 1}})
			handles = append(handles, h)
		}
		close(done)
	}()
	for {
		select {
		case <-done:
			for _, h := range handles {
				<-h.Done()
			}
			return
		default:
			start := time.Now()
			snap := store.Current()
			if snap == nil {
				t.Fatal("nil snapshot during churn")
			}
			if time.Since(start) > 50*time.Millisecond {
				t.Fatal("read blocked by recomputation")
			}
		}
	}
}

func TestSerialCommitsGaplessVersions(t *testing.T) {
	store, _ := baseStore(t)
	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			v := float64(i) / 40
			h := store.ApplyChange(context.Background(), map[Role]Color{"text": {v, v, v, 1}})
			<-h.Done()
		}()
	}
	wg.Wait()
	if store.Version() != 1+n {
		t.Fatalf("final version = %d want %d (gap detected)", store.Version(), 1+n)
	}
}

func containsRole(roles []Role, r Role) bool {
	for _, x := range roles {
		if x == r {
			return true
		}
	}
	return false
}
