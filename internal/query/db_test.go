package query

import (
	"crypto/sha256"
	"encoding/binary"
	"strings"
	"testing"
)

// ---- Shared helpers ----

func hashBytes(b []byte) [32]byte { return sha256.Sum256(b) }

func hashInt(n int) [32]byte {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(n))
	return sha256.Sum256(buf[:])
}

func hashString(s string) [32]byte { return sha256.Sum256([]byte(s)) }

// ---- Input mechanics ----

func TestInputSetBumpsRevisionOnlyOnContentChange(t *testing.T) {
	db := NewDatabase(nil, nil)
	src := RegisterInput[string, []byte](db, "Src", hashBytes)

	if got := db.Revision(); got != 0 {
		t.Fatalf("fresh db should have rev 0, got %d", got)
	}

	src.Set(db, "a", []byte("hello"))
	if got := db.Revision(); got != 1 {
		t.Fatalf("first set should bump rev to 1, got %d", got)
	}

	src.Set(db, "a", []byte("hello")) // identical
	if got := db.Revision(); got != 1 {
		t.Fatalf("same-content set should not bump rev, got %d", got)
	}

	src.Set(db, "a", []byte("world"))
	if got := db.Revision(); got != 2 {
		t.Fatalf("diff-content set should bump rev, got %d", got)
	}

	// different key, different content
	src.Set(db, "b", []byte("other"))
	if got := db.Revision(); got != 3 {
		t.Fatalf("new key should bump rev, got %d", got)
	}
}

func TestInputGetReturnsLatest(t *testing.T) {
	db := NewDatabase(nil, nil)
	src := RegisterInput[string, []byte](db, "Src", hashBytes)

	src.Set(db, "a", []byte("v1"))
	if got := src.Get(db, "a"); string(got) != "v1" {
		t.Fatalf("want v1, got %s", got)
	}

	src.Set(db, "a", []byte("v2"))
	if got := src.Get(db, "a"); string(got) != "v2" {
		t.Fatalf("want v2, got %s", got)
	}
}

func TestInputGetUnsetPanics(t *testing.T) {
	db := NewDatabase(nil, nil)
	src := RegisterInput[string, []byte](db, "Src", hashBytes)

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic, got none")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "Src") {
			t.Fatalf("want panic mentioning Src, got %v", r)
		}
	}()
	_ = src.Get(db, "missing")
}

// ---- Basic derived query ----

func TestDerivedQueryMissThenHit(t *testing.T) {
	db := NewDatabase(nil, nil)
	src := RegisterInput[string, []byte](db, "Src", hashBytes)

	var bodyCount int
	parse := Register(db, "Parse",
		func(ctx *Ctx, key string) int {
			bodyCount++
			v := src.Fetch(ctx, key)
			return len(v)
		},
		hashInt,
	)

	src.Set(db, "a", []byte("12345"))

	before := db.Metrics()
	got := parse.Get(db, "a")
	if got != 5 {
		t.Fatalf("want 5, got %d", got)
	}
	after := db.Metrics().Sub(before)
	if after.Misses != 1 || after.Hits != 1 /* input Fetch inside body counts a hit */ {
		t.Fatalf("miss+input-hit expected, got %+v", after)
	}
	if bodyCount != 1 {
		t.Fatalf("body ran %d times, want 1", bodyCount)
	}

	// Second call should be a cached hit: no body run, verifiedAt
	// already at current rev.
	before = db.Metrics()
	got = parse.Get(db, "a")
	after = db.Metrics().Sub(before)
	if got != 5 || bodyCount != 1 {
		t.Fatalf("want cached; body=%d got=%d", bodyCount, got)
	}
	if after.Hits != 1 || after.Misses != 0 || after.Reruns != 0 {
		t.Fatalf("expected 1 hit, got %+v", after)
	}
}

// ---- Input change → derived rerun ----

func TestDerivedRerunsOnInputChange(t *testing.T) {
	db := NewDatabase(nil, nil)
	src := RegisterInput[string, []byte](db, "Src", hashBytes)

	var bodyCount int
	length := Register(db, "Length",
		func(ctx *Ctx, key string) int {
			bodyCount++
			return len(src.Fetch(ctx, key))
		},
		hashInt,
	)

	src.Set(db, "a", []byte("hello"))
	_ = length.Get(db, "a")
	if bodyCount != 1 {
		t.Fatalf("initial run: body=%d", bodyCount)
	}

	// Content-identical Set: no rerun even though we pulled after.
	src.Set(db, "a", []byte("hello"))
	_ = length.Get(db, "a")
	if bodyCount != 1 {
		t.Fatalf("same-content Set should not trigger rerun, body=%d", bodyCount)
	}

	// Content change → rerun.
	src.Set(db, "a", []byte("world!"))
	before := db.Metrics()
	got := length.Get(db, "a")
	after := db.Metrics().Sub(before)
	if got != 6 {
		t.Fatalf("len changed: got %d", got)
	}
	if bodyCount != 2 {
		t.Fatalf("body should run again, got %d", bodyCount)
	}
	if after.Reruns != 1 {
		t.Fatalf("expected 1 rerun, got %+v", after)
	}
}

// ---- Early cutoff for derived queries ----

func TestDerivedEarlyCutoff(t *testing.T) {
	db := NewDatabase(nil, nil)
	src := RegisterInput[string, []byte](db, "Src", hashBytes)

	// Mid-layer query: returns the length of its input.
	lenBodyCount := 0
	length := Register(db, "Length",
		func(ctx *Ctx, key string) int {
			lenBodyCount++
			return len(src.Fetch(ctx, key))
		},
		hashInt,
	)

	// Downstream query: returns length times 10. If Length's output
	// hash didn't change, this downstream body must NOT re-run.
	mulBodyCount := 0
	mul := Register(db, "Mul10",
		func(ctx *Ctx, key string) int {
			mulBodyCount++
			return length.Fetch(ctx, key) * 10
		},
		hashInt,
	)

	src.Set(db, "a", []byte("abcde")) // length 5
	if got := mul.Get(db, "a"); got != 50 {
		t.Fatalf("want 50, got %d", got)
	}
	if lenBodyCount != 1 || mulBodyCount != 1 {
		t.Fatalf("initial: len=%d mul=%d", lenBodyCount, mulBodyCount)
	}

	// Change input to a value with the SAME LENGTH. Length.fn will
	// re-run but produce the same output 5 → same hash → cutoff.
	// Mul10 depends on Length; its dep-walk sees Length's computedAt
	// NOT advanced, so Mul10 stays cached and its body does not run.
	before := db.Metrics()
	src.Set(db, "a", []byte("vwxyz"))
	if got := mul.Get(db, "a"); got != 50 {
		t.Fatalf("same length, want 50, got %d", got)
	}
	after := db.Metrics().Sub(before)
	if lenBodyCount != 2 {
		t.Fatalf("Length body should re-run, got %d", lenBodyCount)
	}
	if mulBodyCount != 1 {
		t.Fatalf("Mul10 body should be spared by cutoff, got %d", mulBodyCount)
	}
	if after.Cutoffs != 1 {
		t.Fatalf("expected 1 cutoff, got %+v", after)
	}

	// Change input to different length. Length's hash changes.
	// Mul10 must re-run.
	src.Set(db, "a", []byte("longer string"))
	if got := mul.Get(db, "a"); got != 130 {
		t.Fatalf("want 130, got %d", got)
	}
	if lenBodyCount != 3 || mulBodyCount != 2 {
		t.Fatalf("after diff-len change: len=%d mul=%d", lenBodyCount, mulBodyCount)
	}
}

// ---- Transitive dep validation ----

func TestTransitiveCacheHitWhenUnrelatedInputChanges(t *testing.T) {
	db := NewDatabase(nil, nil)
	src := RegisterInput[string, []byte](db, "Src", hashBytes)

	lenBody := 0
	length := Register(db, "Length",
		func(ctx *Ctx, key string) int {
			lenBody++
			return len(src.Fetch(ctx, key))
		},
		hashInt,
	)

	src.Set(db, "a", []byte("one"))
	src.Set(db, "b", []byte("two2"))

	_ = length.Get(db, "a")
	_ = length.Get(db, "b")
	if lenBody != 2 {
		t.Fatalf("initial: body=%d", lenBody)
	}

	// Change only "a"; querying "b" should hit cache.
	src.Set(db, "a", []byte("changed"))
	before := db.Metrics()
	_ = length.Get(db, "b")
	after := db.Metrics().Sub(before)
	if lenBody != 2 {
		t.Fatalf("b's body should not re-run, got %d", lenBody)
	}
	// Cache hit: one hit for length("b"), plus one hit for
	// src.Fetch("b") happens ONLY if body re-ran, which it didn't.
	if after.Hits != 1 || after.Reruns != 0 {
		t.Fatalf("b should be pure cache hit, got %+v", after)
	}
}

// ---- Cycle detection ----

func TestCycleDetection(t *testing.T) {
	db := NewDatabase(nil, nil)

	// Two mutually-recursive queries.
	var a, b *Query[string, int]
	a = Register(db, "A",
		func(ctx *Ctx, key string) int {
			return b.Fetch(ctx, key)
		},
		hashInt,
	)
	b = Register(db, "B",
		func(ctx *Ctx, key string) int {
			return a.Fetch(ctx, key)
		},
		hashInt,
	)

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected cycle panic")
		}
		err, ok := r.(*CycleError)
		if !ok {
			t.Fatalf("want *CycleError, got %T: %v", r, r)
		}
		if len(err.Chain) < 3 {
			t.Fatalf("chain too short: %v", err.Chain)
		}
	}()
	_ = a.Get(db, "x")
}

// ---- Dep tracking correctness ----

func TestDepEdgesRecordedFromFetch(t *testing.T) {
	db := NewDatabase(nil, nil)
	src := RegisterInput[string, []byte](db, "Src", hashBytes)

	twoKeysBody := 0
	sum := Register(db, "Sum",
		func(ctx *Ctx, _ struct{}) int {
			twoKeysBody++
			a := src.Fetch(ctx, "a")
			b := src.Fetch(ctx, "b")
			return len(a) + len(b)
		},
		hashInt,
	)

	src.Set(db, "a", []byte("aa"))
	src.Set(db, "b", []byte("bbb"))

	if got := sum.Get(db, struct{}{}); got != 5 {
		t.Fatalf("want 5, got %d", got)
	}
	if twoKeysBody != 1 {
		t.Fatalf("initial body=%d", twoKeysBody)
	}

	// Change only "a" → Sum should re-run.
	src.Set(db, "a", []byte("aaaa"))
	if got := sum.Get(db, struct{}{}); got != 7 {
		t.Fatalf("want 7, got %d", got)
	}
	if twoKeysBody != 2 {
		t.Fatalf("after a-change body=%d", twoKeysBody)
	}

	// Change only "b" → Sum should re-run.
	src.Set(db, "b", []byte("bbbbb"))
	if got := sum.Get(db, struct{}{}); got != 9 {
		t.Fatalf("want 9, got %d", got)
	}
	if twoKeysBody != 3 {
		t.Fatalf("after b-change body=%d", twoKeysBody)
	}

	// No change → no re-run.
	if got := sum.Get(db, struct{}{}); got != 9 {
		t.Fatalf("cached: got %d", got)
	}
	if twoKeysBody != 3 {
		t.Fatalf("cached body=%d", twoKeysBody)
	}
}

// ---- Cutoff cascade across layers ----

func TestCutoffCascadesAcrossMultipleLayers(t *testing.T) {
	db := NewDatabase(nil, nil)
	src := RegisterInput[string, []byte](db, "Src", hashBytes)

	aCount, bCount, cCount := 0, 0, 0
	layerA := Register(db, "A",
		func(ctx *Ctx, k string) int {
			aCount++
			return len(src.Fetch(ctx, k))
		},
		hashInt,
	)
	layerB := Register(db, "B",
		func(ctx *Ctx, k string) int {
			bCount++
			return layerA.Fetch(ctx, k) + 1
		},
		hashInt,
	)
	layerC := Register(db, "C",
		func(ctx *Ctx, k string) int {
			cCount++
			return layerB.Fetch(ctx, k) * 2
		},
		hashInt,
	)

	src.Set(db, "k", []byte("abc")) // A=3, B=4, C=8
	if got := layerC.Get(db, "k"); got != 8 {
		t.Fatalf("want 8, got %d", got)
	}
	if aCount != 1 || bCount != 1 || cCount != 1 {
		t.Fatalf("initial: a=%d b=%d c=%d", aCount, bCount, cCount)
	}

	// Change input to same-length bytes. A re-runs, hash same, cutoff.
	// B and C should not re-run their bodies.
	src.Set(db, "k", []byte("xyz"))
	if got := layerC.Get(db, "k"); got != 8 {
		t.Fatalf("want 8, got %d", got)
	}
	if aCount != 2 {
		t.Fatalf("A should re-run, got %d", aCount)
	}
	if bCount != 1 {
		t.Fatalf("B should be cut off, got %d", bCount)
	}
	if cCount != 1 {
		t.Fatalf("C should be cut off (transitively), got %d", cCount)
	}
}

// ---- Input.Has ----

func TestInputHas(t *testing.T) {
	db := NewDatabase(nil, nil)
	src := RegisterInput[string, []byte](db, "Src", hashBytes)

	if src.Has(db, "x") {
		t.Fatal("unset key should not Has")
	}
	src.Set(db, "x", []byte("v"))
	if !src.Has(db, "x") {
		t.Fatal("set key should Has")
	}
	src.Clear(db, "x")
	if src.Has(db, "x") {
		t.Fatal("cleared key should not Has")
	}
}

// ---- Metrics sanity ----

func TestMetricsSubtraction(t *testing.T) {
	a := MetricsSnapshot{Hits: 10, Misses: 5, Reruns: 2, Cutoffs: 1, InputSet: 8}
	b := MetricsSnapshot{Hits: 3, Misses: 1, Reruns: 0, Cutoffs: 0, InputSet: 2}
	d := a.Sub(b)
	if d != (MetricsSnapshot{Hits: 7, Misses: 4, Reruns: 2, Cutoffs: 1, InputSet: 6}) {
		t.Fatalf("sub: %+v", d)
	}
}
