package contrast

import (
	"context"
	"sort"
)

// Snapshot is one immutable theme version. Reads always observe a whole
// Snapshot: a replacement can never expose a mix of old and new tokens.
type Snapshot struct {
	Version int64
	Tokens  RoleMapping
	Report  Report
}

// Engine is a versioned, concurrency-safe theme store.
//
// Reads (Current) are lock-free against the committed snapshot: they
// load a pointer atomically. Writes (Replace, UpdateTokens) are
// serialized with wmu, recompute on a private copy, and publish the
// result with a single atomic pointer swap, so an in-flight query sees
// either the complete old version or the complete new version, never a
// mixed intermediate state. A canceled or failed recomputation is simply
// discarded and never reaches the committed snapshot.
type Engine struct {
	snap  atomicSnapshot
	pairs []RolePair    // read-only after NewEngine
	cfg   EvalConfig    // read-only after NewEngine
	wmu   chan struct{} // writer mutex (single-token channel)
}

// NewEngine builds an engine with an initial committed version (version 1).
func NewEngine(roles RoleMapping, pairs []RolePair, cfg EvalConfig) (*Engine, error) {
	normalizedPairs := clonePairs(pairs)
	if err := validatePairs(normalizedPairs); err != nil {
		return nil, err
	}
	report, err := Evaluate(roles, normalizedPairs, cfg)
	if err != nil {
		return nil, err
	}
	e := &Engine{
		pairs: normalizedPairs,
		cfg:   cfg,
		wmu:   make(chan struct{}, 1),
	}
	e.wmu <- struct{}{} // writer token available
	e.snap.p.Store(&Snapshot{Version: 1, Tokens: cloneRoles(roles), Report: report})
	return e, nil
}

// Current returns the committed snapshot (never nil after NewEngine).
// It never blocks on a recomputation in progress.
func (e *Engine) Current() *Snapshot { return e.snap.p.Load() }

// Pairs returns the configured key role combinations (a copy).
func (e *Engine) Pairs() []RolePair { return clonePairs(e.pairs) }

// Replace atomically replaces all tokens and recomputes the whole report.
// On ctx cancellation or evaluation failure the committed version is
// left completely untouched and the zero version is returned.
func (e *Engine) Replace(ctx context.Context, roles RoleMapping) (int64, error) {
	unlock := e.lockWrite()
	defer unlock()

	cur := e.snap.p.Load()
	report, err := Evaluate(roles, e.pairs, e.cfg)
	if err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	next := &Snapshot{
		Version: cur.Version + 1,
		Tokens:  cloneRoles(roles),
		Report:  report,
	}
	e.snap.p.Store(next) // single atomic publication
	return next.Version, nil
}

// UpdateTokens applies a partial change and recomputes only pairs that
// depend on changed roles; unaffected results are reused verbatim.
// Publication is atomic; cancellation/failure leaves the current version
// intact and returns the unchanged report.
func (e *Engine) UpdateTokens(ctx context.Context, changes map[string]Color) (int64, Report, error) {
	unlock := e.lockWrite()
	defer unlock()

	cur := e.snap.p.Load()
	if err := ctx.Err(); err != nil {
		return 0, cur.Report, err
	}

	// Private working copy: the committed snapshot is never mutated.
	nextTokens := cloneRoles(cur.Tokens)
	names := make([]string, 0, len(changes))
	for name, col := range changes {
		if _, ok := nextTokens[name]; !ok {
			return 0, cur.Report, errUnknownRole(name)
		}
		nextTokens[name] = col
		names = append(names, name)
	}
	sort.Strings(names)

	changed := make(map[string]struct{}, len(names))
	for _, n := range names {
		if !colorsEqual(cur.Tokens[n], nextTokens[n]) {
			changed[n] = struct{}{}
		}
	}
	if len(changed) == 0 {
		// Nothing actually changed; no new version is produced.
		return cur.Version, cur.Report, nil
	}

	affected := ChangedPairs(e.pairs, changed)
	partial, err := Evaluate(nextTokens, affected, e.cfg)
	if err != nil {
		return 0, cur.Report, err
	}
	if err := ctx.Err(); err != nil {
		return 0, cur.Report, err
	}

	// Merge recomputed pairs into reused results by stable pair index.
	index := make(map[string]PairResult, len(partial.Results))
	for _, r := range partial.Results {
		index[r.Pair.Name] = r
	}
	merged := Report{Results: make([]PairResult, 0, len(cur.Report.Results)), Pass: true}
	for _, r := range cur.Report.Results {
		if nr, ok := index[r.Pair.Name]; ok {
			r = nr
		}
		if !r.Pass {
			merged.Pass = false
		}
		merged.Results = append(merged.Results, r)
	}

	next := &Snapshot{
		Version: cur.Version + 1,
		Tokens:  nextTokens,
		Report:  merged,
	}
	e.snap.p.Store(next)
	return next.Version, merged, nil
}

func (e *Engine) lockWrite() func() {
	tok := <-e.wmu
	return func() { e.wmu <- tok }
}

// ChangedPairs reports which configured pairs depend on any of roles.
func ChangedPairs(pairs []RolePair, roles map[string]struct{}) []RolePair {
	out := make([]RolePair, 0)
	for _, p := range pairs {
		if _, ok := roles[p.Foreground]; ok {
			out = append(out, p)
			continue
		}
		if _, ok := roles[p.Background]; ok {
			out = append(out, p)
		}
	}
	return out
}

func colorsEqual(a, b Color) bool {
	return a.Alpha == b.Alpha && a.RGB == b.RGB
}
