package contrast

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Snapshot is one committed, immutable theme version with its report.
// Never mutate Theme or Report obtained from a snapshot: a replacement
// builds entirely new values and swaps them in atomically.
type Snapshot struct {
	Version   int64
	CreatedAt time.Time
	Theme     *Theme
	Report    *Report
	// Combos and Config are retained so incremental recomputations stay
	// structurally compatible with the committed version.
	Combos []Combo
	Config EvalConfig
}

// Recomputation is a handle on an in-flight background recomputation.
type Recomputation struct {
	done chan struct{}
	mu   sync.RWMutex
	err  error
	snap *Snapshot
}

// Done returns a channel closed when the recomputation finished.
func (h *Recomputation) Done() <-chan struct{} { return h.done }

// Err returns nil after a successful commit, otherwise the failure cause.
// A canceled or failed recomputation never changes the committed theme.
func (h *Recomputation) Err() error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.err
}

// Snapshot returns the snapshot produced by a successful recomputation
// (nil before completion or after failure).
func (h *Recomputation) Snapshot() *Snapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.snap
}

// Store serializes versioned theme replacement while never blocking
// reads: queries observe either the old snapshot or the new one, never a
// torn mix. Recomputation runs off the read path and only an atomic
// pointer swap publishes results.
type Store struct {
	cur atomic.Pointer[Snapshot]

	// wmu ensures recomputations are prepared/committed serially and each
	// successful commit raises the version by exactly one. It is held only
	// across bookkeeping and the final swap, never across the evaluation,
	// so reads are not blocked.
	wmu sync.Mutex
}

// NewStore builds a store and evaluates the initial theme (version 1).
func NewStore(theme *Theme, combos []Combo, cfg EvalConfig) (*Store, *Snapshot, error) {
	rep, err := Evaluate(theme, combos, cfg)
	if err != nil {
		return nil, nil, err
	}
	snap := &Snapshot{
		Version:   1,
		CreatedAt: time.Now(),
		Theme:     theme,
		Report:    rep,
		Combos:    cloneCombos(combos),
		Config:    cfg,
	}
	s := &Store{}
	s.cur.Store(snap)
	return s, snap, nil
}

// Current returns the latest committed snapshot; it never blocks on a
// recomputation in progress.
func (s *Store) Current() *Snapshot { return s.cur.Load() }

// Version returns the current committed version number.
func (s *Store) Version() int64 {
	snap := s.cur.Load()
	if snap == nil {
		return 0
	}
	return snap.Version
}

// recomputeRequest describes a candidate waiting in the serial queue.
type recomputeRequest struct {
	ctx     context.Context
	tokens  map[Role]Color
	partial bool
	handle  *Recomputation
}

func (s *Store) launch(req recomputeRequest) *Recomputation {
	go func() {
		defer close(req.handle.done)

		// Observe the committed version the candidate is based on BEFORE
		// any heavy work. Cancellation is checked after preparation and
		// right before commit; an aborted candidate never reaches swap.
		base := s.cur.Load()

		// Prepare the candidate theme off the read path. Both full and
		// partial changes build a brand-new map; the committed theme is
		// never mutated, so concurrent readers keep seeing a consistent
		// old version.
		candTokens := make(map[Role]Color, len(base.Theme.Tokens))
		if req.partial {
			// Incremental change: start from committed tokens and overlay
			// only the changed roles.
			for k, v := range base.Theme.Tokens {
				candTokens[k] = v
			}
			for k, v := range req.tokens {
				candTokens[k] = v
			}
		} else {
			// Full replacement: the supplied token set is the new theme.
			for k, v := range req.tokens {
				candTokens[k] = v
			}
		}
		candTheme := NewTheme(base.Theme.Name, candTokens)

		if err := req.ctx.Err(); err != nil {
			s.fail(req.handle, err)
			return
		}

		var rep *Report
		var err error
		if req.partial {
			changed := make([]Role, 0, len(req.tokens))
			for r := range req.tokens {
				changed = append(changed, r)
			}
			rep, err = EvaluateAffected(candTheme, base.Combos, base.Config, base.Report, changed)
		} else {
			rep, err = Evaluate(candTheme, base.Combos, base.Config)
		}
		if err != nil {
			s.fail(req.handle, err)
			return
		}
		if err := req.ctx.Err(); err != nil {
			s.fail(req.handle, err)
			return
		}

		// Serialize commits: version numbers stay gapless and only one
		// swap happens at a time. Evaluation cost was already paid
		// without the lock, so reads never waited on it.
		s.wmu.Lock()
		cur := s.cur.Load()
		newSnap := &Snapshot{
			Version:   cur.Version + 1,
			CreatedAt: time.Now(),
			Theme:     candTheme,
			Report:    rep,
			Combos:    cloneCombos(base.Combos),
			Config:    base.Config,
		}
		// Single atomic publish: readers get either the old pointer or
		// the new pointer, never an interleaved state.
		s.cur.Store(newSnap)
		s.wmu.Unlock()

		req.handle.mu.Lock()
		req.handle.snap = newSnap
		req.handle.mu.Unlock()
	}()
	return req.handle
}

func (s *Store) fail(h *Recomputation, err error) {
	h.mu.Lock()
	h.err = err
	h.mu.Unlock()
}

// ApplyChange starts a background incremental recomputation for the given
// token changes; only combos referencing changed roles are recomputed.
// The returned handle resolves when the candidate either commits
// atomically or is discarded (on ctx cancellation or error).
func (s *Store) ApplyChange(ctx context.Context, changed map[Role]Color) *Recomputation {
	return s.launch(recomputeRequest{
		ctx:     ctx,
		tokens:  cloneTokenChanges(changed),
		partial: true,
		handle:  &Recomputation{done: make(chan struct{})},
	})
}

// ReplaceAll starts a background full replacement of every token.
func (s *Store) ReplaceAll(ctx context.Context, tokens map[Role]Color) *Recomputation {
	return s.launch(recomputeRequest{
		ctx:     ctx,
		tokens:  cloneTokenChanges(tokens),
		partial: false,
		handle:  &Recomputation{done: make(chan struct{})},
	})
}

// ApplyChangeSync is a convenience wrapper that waits for completion.
func (s *Store) ApplyChangeSync(ctx context.Context, changed map[Role]Color) (*Snapshot, error) {
	h := s.ApplyChange(ctx, changed)
	select {
	case <-h.Done():
		return h.Snapshot(), h.Err()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func cloneCombos(in []Combo) []Combo {
	out := make([]Combo, len(in))
	for i, c := range in {
		out[i] = c
		if c.ModelLevel != nil {
			ml := make(map[ModelID]string, len(c.ModelLevel))
			for k, v := range c.ModelLevel {
				ml[k] = v
			}
			out[i].ModelLevel = ml
		}
	}
	return out
}

func cloneTokenChanges(in map[Role]Color) map[Role]Color {
	out := make(map[Role]Color, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
