/* Feature test macros enable the POSIX 2008 surface (nanosleep,
 * pthread_mutexattr_settype, sched_yield, PTHREAD_MUTEX_RECURSIVE)
 * across glibc/musl/Darwin regardless of compiler default. 2008
 * supersedes the earlier 199309L level previously used for nanosleep
 * alone. Windows skips the POSIX surface entirely and uses the Win32
 * synchronization primitives below.
 *
 * `_DARWIN_C_SOURCE` (macOS) and `_GNU_SOURCE` (Linux/glibc) expose
 * the BSD `MAP_ANON` / GNU `MAP_ANONYMOUS` mmap flag respectively —
 * needed by the GC's mmap'd allocation arena. POSIX 2008 alone hides
 * both. Setting these alongside `_POSIX_C_SOURCE` keeps the rest of
 * the POSIX surface available. */
#if !defined(_WIN32)
#ifndef _POSIX_C_SOURCE
#define _POSIX_C_SOURCE 200809L
#endif
#ifndef _XOPEN_SOURCE
#define _XOPEN_SOURCE 700
#endif
#if defined(__APPLE__) && !defined(_DARWIN_C_SOURCE)
#define _DARWIN_C_SOURCE 1
#endif
#if defined(__linux__) && !defined(_GNU_SOURCE)
#define _GNU_SOURCE 1
#endif
#endif

#include <stdbool.h>
#include <stdint.h>
#include <errno.h>
#include <limits.h>
#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#if !defined(_WIN32)
#include <zlib.h>
#endif

#if !defined(_WIN32)
#include <sys/mman.h>
/* `MAP_ANON` is the BSD/macOS spelling; `MAP_ANONYMOUS` is the GNU
 * spelling. Both flags are bit-equal aliases on every supported target.
 * Pick whichever is exposed at runtime-build time (macOS requires
 * `_DARWIN_C_SOURCE` for `MAP_ANONYMOUS`, which we don't request, so
 * fall back to `MAP_ANON`). */
#if defined(MAP_ANONYMOUS)
#  define OSTY_GC_MAP_ANON MAP_ANONYMOUS
#elif defined(MAP_ANON)
#  define OSTY_GC_MAP_ANON MAP_ANON
#else
#  error "no MAP_ANON / MAP_ANONYMOUS flag available"
#endif
#endif

/* OSTY_HOT_INLINE marks runtime primitives that osty-lowered IR calls
 * on hot paths (`xs[i]`, `xs[i] = v`, `xs.len()`). Without this
 * annotation ThinLTO weighs them against its own size budget and often
 * leaves them out-of-line, so every primitive List<Int> element access
 * in user code pays a real cross-TU function call. On List-heavy
 * benchmarks (quicksort, matmul, lane_route) this was measured at a
 * 10-50x slowdown vs. Go slice access. `always_inline` forces the
 * inlining regardless of the heuristic; external linkage is preserved
 * because .ll-side `declare`s bind to the ordinary runtime symbol. */
#if defined(__GNUC__) || defined(__clang__)
#  define OSTY_HOT_INLINE __attribute__((always_inline))
#else
#  define OSTY_HOT_INLINE
#endif

/* Snapshot / file-IO primitives — mkdir is spelled differently on Win32
 * vs. POSIX, so we funnel the one-directory-level call through
 * osty_rt_mkdir_one. Everything else (fopen/fread/fwrite/getenv) is
 * already cross-platform in the C standard library. */
#if defined(_WIN32)
#  include <direct.h>
#  define OSTY_RT_MKDIR_ONE(path) _mkdir(path)
#else
#  include <fcntl.h>
#  include <sys/stat.h>
#  include <sys/types.h>
#  include <sys/wait.h>
#  include <unistd.h>
#  if defined(__linux__)
#    include <sys/random.h>
#  endif
#  define OSTY_RT_MKDIR_ONE(path) mkdir((path), 0755)
#endif

/* ============================================================
 * Platform threading abstraction
 *
 * The runtime uses a narrow set of concurrency primitives (threads,
 * mutexes, condition variables, one-time init, thread-local storage,
 * a monotonic clock, sleep, yield). POSIX targets bind these to
 * pthread / POSIX 2008; Windows targets bind them to Win32 directly
 * (SRWLOCK / CONDITION_VARIABLE / CRITICAL_SECTION / INIT_ONCE /
 * QueryPerformanceCounter / Sleep / SwitchToThread).
 *
 * Thread bodies use the POSIX-style `void *(*)(void*)` signature in
 * both branches; the Win32 branch wraps that into an __stdcall
 * trampoline around _beginthreadex. The runtime never invokes
 * pthread_cancel / pthread_exit, so there is no cancellation surface
 * to emulate — workers always return normally through the body.
 * ============================================================ */

#if defined(_WIN32)
#  define OSTY_RT_PLATFORM_WIN32 1
#  ifndef WIN32_LEAN_AND_MEAN
#    define WIN32_LEAN_AND_MEAN
#  endif
#  include <windows.h>
#  include <process.h>
#  include <signal.h>         /* sig_atomic_t for SIGURG TLS flag */
#  define OSTY_RT_TLS __declspec(thread)
typedef HANDLE             osty_rt_thread_t;
typedef SRWLOCK            osty_rt_mu_t;
typedef CRITICAL_SECTION   osty_rt_rmu_t;
typedef CONDITION_VARIABLE osty_rt_cond_t;
typedef INIT_ONCE          osty_rt_once_t;
#  define OSTY_RT_ONCE_INIT INIT_ONCE_STATIC_INIT
#else
#  define OSTY_RT_PLATFORM_POSIX 1
#  include <pthread.h>
#  include <poll.h>
#  include <sched.h>
#  include <signal.h>
#  include <fcntl.h>
#  include <netdb.h>
#  include <arpa/inet.h>
#  include <netinet/in.h>
#  include <netinet/tcp.h>
#  include <sys/socket.h>
#  include <sys/time.h>
#  if defined(__APPLE__)
#    include <crt_externs.h>
#  endif
#  if defined(__STDC_NO_THREADS__) || defined(__APPLE__)
#    define OSTY_RT_TLS __thread
#  else
#    define OSTY_RT_TLS _Thread_local
#  endif
typedef pthread_t       osty_rt_thread_t;
typedef pthread_mutex_t osty_rt_mu_t;
typedef pthread_mutex_t osty_rt_rmu_t;
typedef pthread_cond_t  osty_rt_cond_t;
typedef pthread_once_t  osty_rt_once_t;
#  define OSTY_RT_ONCE_INIT PTHREAD_ONCE_INIT
#endif

/* ---- Thread start / join (POSIX-style signature in both branches). */

#if defined(OSTY_RT_PLATFORM_WIN32)
typedef struct osty_rt_win_thread_ctx {
    void *(*fn)(void *);
    void *arg;
} osty_rt_win_thread_ctx;

static unsigned __stdcall osty_rt_win_thread_entry(void *raw) {
    osty_rt_win_thread_ctx ctx = *(osty_rt_win_thread_ctx *)raw;
    free(raw);
    /* Return value is discarded — osty task handles propagate the body
     * result through the GC-managed handle, not via thread return. */
    ctx.fn(ctx.arg);
    return 0;
}

static int osty_rt_thread_start(osty_rt_thread_t *out,
                                void *(*fn)(void *), void *arg) {
    osty_rt_win_thread_ctx *ctx =
        (osty_rt_win_thread_ctx *)malloc(sizeof(*ctx));
    if (ctx == NULL) {
        return -1;
    }
    ctx->fn = fn;
    ctx->arg = arg;
    uintptr_t h = _beginthreadex(NULL, 0, osty_rt_win_thread_entry, ctx, 0,
                                 NULL);
    if (h == 0) {
        free(ctx);
        return -1;
    }
    *out = (HANDLE)h;
    return 0;
}

static int osty_rt_thread_join(osty_rt_thread_t t, void **retval) {
    if (WaitForSingleObject(t, INFINITE) != WAIT_OBJECT_0) {
        return -1;
    }
    if (retval != NULL) {
        *retval = NULL;
    }
    CloseHandle(t);
    return 0;
}
#else
static inline int osty_rt_thread_start(osty_rt_thread_t *out,
                                       void *(*fn)(void *), void *arg) {
    return pthread_create(out, NULL, fn, arg);
}

static inline int osty_rt_thread_join(osty_rt_thread_t t, void **retval) {
    return pthread_join(t, retval);
}
#endif

/* ---- Non-recursive mutex + condition variable. */

#if defined(OSTY_RT_PLATFORM_WIN32)
static inline int osty_rt_mu_init(osty_rt_mu_t *m)       { InitializeSRWLock(m); return 0; }
static inline void osty_rt_mu_destroy(osty_rt_mu_t *m)   { (void)m; }
static inline void osty_rt_mu_lock(osty_rt_mu_t *m)      { AcquireSRWLockExclusive(m); }
static inline void osty_rt_mu_unlock(osty_rt_mu_t *m)    { ReleaseSRWLockExclusive(m); }
static inline int osty_rt_cond_init(osty_rt_cond_t *c)   { InitializeConditionVariable(c); return 0; }
static inline void osty_rt_cond_destroy(osty_rt_cond_t *c)   { (void)c; }
static inline void osty_rt_cond_wait(osty_rt_cond_t *c, osty_rt_mu_t *m) {
    SleepConditionVariableSRW(c, m, INFINITE, 0);
}
static inline void osty_rt_cond_signal(osty_rt_cond_t *c)    { WakeConditionVariable(c); }
static inline void osty_rt_cond_broadcast(osty_rt_cond_t *c) { WakeAllConditionVariable(c); }
#else
static inline int osty_rt_mu_init(osty_rt_mu_t *m)       { return pthread_mutex_init(m, NULL); }
static inline void osty_rt_mu_destroy(osty_rt_mu_t *m)   { pthread_mutex_destroy(m); }
static inline void osty_rt_mu_lock(osty_rt_mu_t *m)      { pthread_mutex_lock(m); }
static inline void osty_rt_mu_unlock(osty_rt_mu_t *m)    { pthread_mutex_unlock(m); }
static inline int osty_rt_cond_init(osty_rt_cond_t *c)   { return pthread_cond_init(c, NULL); }
static inline void osty_rt_cond_destroy(osty_rt_cond_t *c)   { pthread_cond_destroy(c); }
static inline void osty_rt_cond_wait(osty_rt_cond_t *c, osty_rt_mu_t *m) {
    pthread_cond_wait(c, m);
}
static inline void osty_rt_cond_signal(osty_rt_cond_t *c)    { pthread_cond_signal(c); }
static inline void osty_rt_cond_broadcast(osty_rt_cond_t *c) { pthread_cond_broadcast(c); }
#endif

/* ---- Recursive mutex (GC lock). */

#if defined(OSTY_RT_PLATFORM_WIN32)
static inline int osty_rt_rmu_init(osty_rt_rmu_t *m)     { InitializeCriticalSection(m); return 0; }
static inline void osty_rt_rmu_destroy(osty_rt_rmu_t *m) { DeleteCriticalSection(m); }
static inline void osty_rt_rmu_lock(osty_rt_rmu_t *m)    { EnterCriticalSection(m); }
static inline void osty_rt_rmu_unlock(osty_rt_rmu_t *m)  { LeaveCriticalSection(m); }
#else
static inline int osty_rt_rmu_init(osty_rt_rmu_t *m) {
    pthread_mutexattr_t attr;
    int rc = pthread_mutexattr_init(&attr);
    if (rc != 0) {
        return rc;
    }
    rc = pthread_mutexattr_settype(&attr, PTHREAD_MUTEX_RECURSIVE);
    if (rc != 0) {
        pthread_mutexattr_destroy(&attr);
        return rc;
    }
    rc = pthread_mutex_init(m, &attr);
    pthread_mutexattr_destroy(&attr);
    return rc;
}
static inline void osty_rt_rmu_destroy(osty_rt_rmu_t *m) { pthread_mutex_destroy(m); }
static inline void osty_rt_rmu_lock(osty_rt_rmu_t *m)    { pthread_mutex_lock(m); }
static inline void osty_rt_rmu_unlock(osty_rt_rmu_t *m)  { pthread_mutex_unlock(m); }
#endif

/* ---- One-time initialization. */

#if defined(OSTY_RT_PLATFORM_WIN32)
static BOOL CALLBACK osty_rt_once_trampoline(PINIT_ONCE once, PVOID param,
                                             PVOID *ctx) {
    (void)once;
    (void)ctx;
    void (*fn)(void) = (void (*)(void))(uintptr_t)param;
    fn();
    return TRUE;
}
static inline void osty_rt_once(osty_rt_once_t *once, void (*init)(void)) {
    InitOnceExecuteOnce(once, osty_rt_once_trampoline,
                        (PVOID)(uintptr_t)init, NULL);
}
#else
static inline void osty_rt_once(osty_rt_once_t *once, void (*init)(void)) {
    pthread_once(once, init);
}
#endif

/* ---- Monotonic clock / sleep / yield. */

#if defined(OSTY_RT_PLATFORM_WIN32)
static uint64_t osty_rt_monotonic_ns(void) {
    static LARGE_INTEGER freq = {0};
    LARGE_INTEGER now;
    if (freq.QuadPart == 0) {
        QueryPerformanceFrequency(&freq);
    }
    QueryPerformanceCounter(&now);
    uint64_t hz = (uint64_t)freq.QuadPart;
    uint64_t ticks = (uint64_t)now.QuadPart;
    uint64_t sec = ticks / hz;
    uint64_t rem = ticks % hz;
    return sec * 1000000000ULL + (rem * 1000000000ULL) / hz;
}
static void osty_rt_sleep_ns(uint64_t ns) {
    if (ns == 0) {
        return;
    }
    /* Sleep granularity is milliseconds; round up so short naps still
     * yield the CPU at least once. */
    DWORD ms = (DWORD)((ns + 999999ULL) / 1000000ULL);
    if (ms == 0) {
        ms = 1;
    }
    Sleep(ms);
}
static inline void osty_rt_plat_yield(void) { SwitchToThread(); }
#else
static inline uint64_t osty_rt_monotonic_ns(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (uint64_t)ts.tv_sec * 1000000000ULL + (uint64_t)ts.tv_nsec;
}
static inline void osty_rt_sleep_ns(uint64_t ns) {
    struct timespec req;
    req.tv_sec = (time_t)(ns / 1000000000ULL);
    req.tv_nsec = (long)(ns % 1000000000ULL);
    struct timespec rem;
    while (nanosleep(&req, &rem) == -1) {
        req = rem;
    }
}
static inline void osty_rt_plat_yield(void) { sched_yield(); }
#endif

/* Active LLVM/native GC runtime path. See ../../../../RUNTIME_GC.md.
 *
 * Concurrency is pthread-based (see ../../../../RUNTIME_SCHEDULER.md):
 * task_spawn creates real OS threads, channels are mutex+cond ring
 * buffers, and GC allocation is serialized by osty_gc_lock so that
 * multi-threaded programs stay consistent. Automatic collection is
 * paused while worker threads are live (osty_concurrent_workers > 0)
 * because scanning only the current thread's stack would drop roots
 * held by siblings. Manual collection via osty_gc_debug_collect is
 * still available between phases. A concurrent collector lands in
 * Phase 3 per the scheduler roadmap.
 */

typedef void (*osty_gc_trace_fn)(void *payload);
typedef void (*osty_gc_destroy_fn)(void *payload);
typedef void (*osty_rt_trace_slot_fn)(void *slot_addr);

typedef enum osty_gc_trace_slot_mode {
    OSTY_GC_TRACE_SLOT_MODE_MARK = 0,
    OSTY_GC_TRACE_SLOT_MODE_REMAP = 1,
    /* Phase 6 step 2: tracer rewrites the slot to its to-space
     * forwarded address. Existing tracer fns (osty_rt_list_trace,
     * osty_rt_map_trace, ...) all funnel through `mark_slot_v1`, so
     * adding the third dispatch branch lights up every kind at once
     * without per-tracer modification. */
    OSTY_GC_TRACE_SLOT_MODE_CHENEY = 2,
} osty_gc_trace_slot_mode;

/* Phase C tri-colour marking (RUNTIME_GC_DELTA §4.1).
 *
 * Up through Phase B the mark pass used a single `marked` bit that
 * implicitly encoded grey + black: setting it meant "on mark stack or
 * already traced". Phase C splits the states explicitly so the
 * incremental collector can reason about them:
 *
 *   WHITE — not yet reached this cycle. Swept if the mark pass ends
 *           and this is still the colour.
 *   GREY  — reached but children not traced. On the mark stack.
 *   BLACK — reached AND children enqueued. Off the mark stack.
 *
 * Within a STW collection the transition is bounded: enqueue → GREY,
 * pop + trace → BLACK. Under the incremental collector, mutator
 * activity between step calls can create BLACK→WHITE edges (a freshly
 * stored value that would otherwise be swept). The SATB barrier in
 * `osty_gc_pre_write_v1` catches the old value and colours it GREY
 * before it can escape; this is what turns the Phase A passive
 * recording into a live collector input.
 */
enum {
    OSTY_GC_COLOR_WHITE = 0,
    OSTY_GC_COLOR_GREY = 1,
    OSTY_GC_COLOR_BLACK = 2,
};

/* `osty_gc_allocate_managed`'s hot path elides explicit zero-stores
 * to header->color and header->generation, relying on the fact that
 * the chunk is zero-initialized at this point and both default
 * values are zero. Lock the assumption with a build-time check so
 * a future enum-value reshuffle doesn't silently miscolour fresh
 * allocations. */
_Static_assert(OSTY_GC_COLOR_WHITE == 0,
               "alloc fast path assumes WHITE is the zero color");

/* Phase B generation tags (RUNTIME_GC_DELTA §5.1-5.5).
 *
 * Every managed allocation is born YOUNG. A minor collection promotes
 * any YOUNG survivor that has lived through `OSTY_GC_PROMOTE_AGE` minor
 * cycles into OLD; from then on the survivor participates only in full
 * collections. The promotion age is configurable via
 * `OSTY_GC_PROMOTE_AGE_ENV` so experiments can sweep without rebuilding.
 *
 * The distinction is purely bookkeeping today — OLD and YOUNG objects
 * share the same `calloc`-backed heap. Phase D compaction will split
 * the storage, but the filter logic (minor GC scans YOUNG only, uses
 * the remembered set for OLD→YOUNG edges) already depends on having a
 * stable tag on every header. */
enum {
    OSTY_GC_GEN_YOUNG = 0,
    OSTY_GC_GEN_OLD = 1,
};

_Static_assert(OSTY_GC_GEN_YOUNG == 0,
               "alloc fast path assumes YOUNG is the zero generation");

#define OSTY_GC_PROMOTE_AGE_DEFAULT 3
#define OSTY_GC_PROMOTE_AGE_ENV "OSTY_GC_PROMOTE_AGE"
#define OSTY_GC_NURSERY_LIMIT_ENV "OSTY_GC_NURSERY_BYTES"
/* 64 MiB nursery (was 1 MiB pre-#979 bump, 8 KiB pre-#977). #979's
 * 1 MiB default already unblocked big-map (1+ hour timeout → 9.2 s),
 * but a sweep across 1 / 8 / 16 / 64 MiB nurseries showed continued
 * speedup up to 64 MiB (12.4 / 2.7 / 2.0 / 1.1 s on big-map at the
 * time of measurement) where survivors per cycle become small enough
 * that further bumps add nothing. The young arena's virtual
 * reservation stays fixed at 256 MiB regardless — only when cheney
 * triggers changes. Actual page commit peaks around the cursor and
 * resets on swap. Tests that pin specific cheney behaviour set the
 * env var directly. */
#define OSTY_GC_NURSERY_LIMIT_DEFAULT (64 << 20)

typedef struct osty_gc_header {
    /* Global doubly-linked list — used by major collection, sweep, and
     * validate_heap walks. Every managed header lives here regardless
     * of generation. */
    struct osty_gc_header *next;
    struct osty_gc_header *prev;
    /* Per-generation doubly-linked list (Phase B2 depth). Lets minor
     * collection iterate exactly `|young|` headers instead of walking
     * the full heap and filtering on `generation`. Promotion moves a
     * header from the young list to the old list in-place; the global
     * list is untouched. */
    struct osty_gc_header *next_gen;
    struct osty_gc_header *prev_gen;
    int64_t object_kind;
    int64_t byte_size;
    /* root_count / pin_count are bounded by the number of active
     * `osty_gc_root_bind` / `_pin` handles. 32 bits (~2.1B) is far
     * past anything an Osty program would credibly hold simultaneously
     * — a real program would OOM long before. Shrinking these from
     * i64 to i32 saves 8 bytes per header. The INT*_MAX overflow
     * guards in the bind/pin paths are updated accordingly. */
    int32_t root_count;
    int32_t pin_count;
    /* Phase C: explicit tri-colour (`OSTY_GC_COLOR_*`). `marked` is
     * retained as a convenience alias — `marked == true` iff
     * `color != WHITE`. The sweep loop reads `color` directly; the
     * mark loop transitions GREY (on push) → BLACK (on drain). */
    uint8_t color;
    bool marked;
    /* Phase B: per-object generation and survival counter. `age` is a
     * u8 because promotion thresholds above ~8 defeat the point of
     * generational — the old-space ends up with the same pressure the
     * young-space was supposed to smooth. */
    uint8_t age;
    uint8_t generation;
    uint8_t storage_kind;
    /* Phase 4 (RUNTIME_GC_DELTA Phase E follow-up): per-object
     * generic-pattern selector. Indexes `osty_gc_generic_patterns[]`
     * to recover the (trace, destroy) pair for `OSTY_GC_KIND_GENERIC`
     * objects. Non-GENERIC kinds resolve through `osty_gc_kind_table`
     * and ignore this field. The slot reuses existing 8-byte
     * alignment padding (5 u8 fields → 6 u8 + 2 padding), so the
     * field is free; dropping the per-header `trace`/`destroy` fn
     * pointers is the actual win — net header shrink: 16 B per OLD
     * object (96 → 80). */
    uint8_t generic_pattern;
    /* Phase F card marking. The bit doubles as the dirty-OLD array's
     * membership flag — set on the 0→1 transition by
     * `osty_gc_post_write_v1`, cleared by major GC. Reuses existing
     * padding (6 u8 + 2 padding → 7 u8 + 1 padding); zero memory cost.
     * The bit must persist across minors because a still-young
     * reference inside the owner must keep the card dirty until the
     * next major; otherwise a later minor would skip the owner and the
     * value would be swept. */
    uint8_t card_dirty;
    /* Phase D groundwork: stable logical identity. Payload pointers are
     * still the operational handles today, but this id stays attached to
     * the object even once future compaction starts moving addresses. */
    uint64_t stable_id;
    void *payload;
} osty_gc_header;
/* `site` was historically a `const char *` debug label written by the
 * two `osty_gc_header_init*` paths but never read anywhere — `grep
 * '->site'` returned only the two writes. Dropping it shaves 8 bytes
 * per header (out of 112), which compounds on alloc-heavy benches:
 * markdown-stats allocates ~20k strings → 160 KB header memory
 * removed, lower L1/L2 pressure during the GC's `osty_gc_objects`
 * walk. The init functions still take the `site` parameter (unused)
 * so external callers — including the runtime's own
 * `osty_gc_allocate_managed` — keep their signatures and their
 * call sites stay readable as alloc-site labels in source. The
 * parameter is suppressed via `(void)site` to avoid the unused-arg
 * warning. */

/* Inline storage for short lists. The Split / Fields / `[lit]` /
 * cache-rebuild patterns that dominate the benchmark suite typically
 * produce 1-16 element lists; without enough inline capacity each
 * one paid a `realloc(NULL, 32)` data buffer alloc on first push
 * (initial `cap=4`, ptr elements) AND a doubling realloc as it grew
 * past 8 / 16 elements. That malloc + memcpy chain showed up as
 * **40% of lru-sim wall time** under `sample` (16 of 41 main-thread
 * samples in `_realloc`/`xzm_realloc`/`_xzm_free`), driven by the
 * eviction loop's "rebuild list excluding one index" pattern that
 * allocates a fresh `next: List<String>` and pushes 12 elements
 * into it per eviction (~14k evictions per 50k-op run).
 *
 * 128 bytes covers the 16-element ptr / i64 / f64 case (lru-sim's
 * 12-element ephemeral lists fit entirely inline now). Element
 * sizes 1/4 (i8/i32) get even more inline slots (128/32 elements);
 * the struct-element path (List<Record>) never fits inline because
 * record sizes are typically >= 16 bytes. Larger lists transparently
 * spill to a heap data buffer once `len * elem_size > 128`.
 *
 * Cost: list header grows from 88 → 184 bytes (~96 bytes / list).
 * For workloads with thousands of short-lived lists (lru-sim,
 * dep-resolver, log-aggregator) that's 1-5 MB extra in the young
 * generation — well under the nursery's default 8 MB budget, and
 * the existing GC sweep collects them at the same rate. The win
 * is one fewer malloc + memcpy per list spill, plus the inline
 * storage stays prefetched alongside the list header in the same
 * cache line stream so per-element access has no extra indirection. */
#define OSTY_RT_LIST_INLINE_BYTES 128

typedef struct osty_rt_list {
    int64_t len;
    int64_t cap;
    size_t elem_size;
    osty_rt_trace_slot_fn trace_elem;
    int64_t gc_offset_count;
    int64_t *gc_offsets;
    unsigned char *data;
    /* Inline storage: `data` points here while the list fits, and
     * gets repointed to a heap buffer once `osty_rt_list_reserve`
     * needs more bytes. `osty_rt_list_destroy` distinguishes the
     * two cases by `data == inline_storage` to avoid `free`-ing the
     * inline region. The trace callback walks `data` regardless,
     * so it just works for both layouts. */
    unsigned char inline_storage[OSTY_RT_LIST_INLINE_BYTES];
} osty_rt_list;

#define OSTY_RT_MAP_INLINE_INDEX_CAP 64

typedef struct osty_rt_map {
    int64_t len;
    int64_t cap;
    int64_t key_kind;
    int64_t value_kind;
    size_t value_size;
    osty_rt_trace_slot_fn value_trace;
    unsigned char *keys;
    unsigned char *values;
    // Lookup side index: open-addressed hash table storing slot+1 so
    // `Map` keeps its insertion-ordered key/value arrays while get /
    // contains / insert avoid an O(n) scan once the map grows past the
    // tiny-list regime.
    int64_t index_cap;
    int64_t index_len;
    int64_t *index_slots;
    uint32_t *index_hashes;
    int64_t index_inline[OSTY_RT_MAP_INLINE_INDEX_CAP];
    uint32_t index_hashes_inline[OSTY_RT_MAP_INLINE_INDEX_CAP];
    // Per-map recursive mutex. Public ops elide it while the runtime
    // is still single-threaded, then enable it permanently on first
    // task spawn so concurrent mutation can't realloc the slot arrays
    // mid-read, drop the len mid-walk, or interleave an insert's
    // memmove. Recursive so that `update` callbacks that touch the
    // same map from within the lock don't self-deadlock.
    osty_rt_rmu_t mu;
    int mu_init;
} osty_rt_map;

typedef struct osty_rt_set {
    int64_t len;
    int64_t cap;
    int64_t elem_kind;
    unsigned char *items;
} osty_rt_set;

typedef struct osty_rt_bytes {
    unsigned char *data;
    int64_t len;
} osty_rt_bytes;

typedef struct osty_rt_chan_impl {
    osty_rt_mu_t mu;
    osty_rt_cond_t not_full;
    osty_rt_cond_t not_empty;
    int64_t cap;
    int64_t head;
    int64_t tail;
    int64_t count;
    int64_t elem_kind;
    int closed;
    int sync_init;
    int64_t *slots;
} osty_rt_chan_impl;

/* Forward typedef for the task-handle struct; full definition lives
 * with the rest of the scheduler down below. The cheney_forward /
 * promote young paths only need the typedef name to cast the void*
 * payload — the safe-handoff body that touches struct fields lives
 * next to the full definition. */
typedef struct osty_rt_task_handle_impl_ osty_rt_task_handle_impl;

typedef struct osty_gc_free_chunk {
    struct osty_gc_free_chunk *next;
    size_t total_size;
    uint8_t storage_kind;
} osty_gc_free_chunk;

#define OSTY_GC_SIZE_CLASS_ALIGN 16
#define OSTY_GC_BUMP_BLOCK_BYTES 65536
#define OSTY_GC_HUMONGOUS_THRESHOLD_BYTES 1024
#define OSTY_GC_FREE_LIST_BIN_COUNT \
    (OSTY_GC_HUMONGOUS_THRESHOLD_BYTES / OSTY_GC_SIZE_CLASS_ALIGN)

typedef struct osty_gc_bump_block {
    struct osty_gc_bump_block *next;
    size_t size;
    size_t used;
    int64_t live_alloc_count;
    /* Aligned base of the payload region, used for bump allocation
     * within this block. */
    unsigned char *data;
    /* Original unaligned pointer that owns the heap-fallback payload
     * region. NULL when the data region was suballocated from the
     * arena (the arena retains ownership of its virtual range). */
    unsigned char *data_alloc;
    /* Reserved tail. Was the storage buffer in the pre-arena layout;
     * kept as a zero-length flexible array so existing sizeof / offset
     * computations stay stable for any external bridge that hard-codes
     * the struct layout. */
    unsigned char storage[];
} osty_gc_bump_block;

#if defined(__APPLE__)
#define OSTY_GC_SYMBOL(name) "_" name
#else
#define OSTY_GC_SYMBOL(name) name
#endif

enum {
    OSTY_GC_KIND_GENERIC = 1,
    OSTY_GC_KIND_LIST = 1024,
    OSTY_GC_KIND_STRING = 1025,
    OSTY_GC_KIND_MAP = 1026,
    OSTY_GC_KIND_SET = 1027,
    OSTY_GC_KIND_BYTES = 1028,
    /* Phase A4 (RUNTIME_GC_DELTA §2.4). Closure environment struct with
     * a self-describing capture array. The runtime registers a trace
     * callback (`osty_rt_closure_env_trace`) that walks the capture
     * slots so any managed pointer captured by a closure stays
     * reachable across GC cycles. Layout below in `osty_rt_closure_env`.
    * Allocation is dedicated: `osty.rt.closure_env_alloc_v2` rather
     * than the generic `osty.gc.alloc_v1`, so the trace is installed
     * at construction and not as a post-alloc mutation. Phase 1 still
     * emits envs with `capture_count = 0`, exercising the allocation
     * ABI while captures themselves are lowered in Phase 4. */
    OSTY_GC_KIND_CLOSURE_ENV = 1029,
    OSTY_GC_KIND_CHANNEL = 1030,
    /* Phase E follow-up: split out from OSTY_GC_KIND_GENERIC's
     * ENUM_PTR pattern so the per-instance trace fn is recoverable
     * via the kind-table descriptor (instead of the headerful-only
     * `header->generic_pattern` byte). Lets `osty_rt_enum_alloc_ptr_v1`
     * route through the no-header young arena: the trace
     * (`osty_rt_enum_ptr_payload_trace`) follows a single managed
     * pointer payload, and there's no destroy callback. */
    OSTY_GC_KIND_GENERIC_ENUM_PTR = 1031,
    /* Same split for TASK_HANDLE so cheney's young dispatch can find
     * the destroy + safe-handoff via the kind table. The destroy
     * tears down the embedded mutex + condvar, so the safe-clone
     * pattern (re-init dst's sync, destroy src's) is required at
     * cheney_forward / promote_young_to_old. The remaining GENERIC
     * pattern (NONE) keeps using OSTY_GC_KIND_GENERIC. */
    OSTY_GC_KIND_GENERIC_TASK_HANDLE = 1032,
};

/* Phase 1 of the tiny-tag young space landing: a per-kind descriptor table
 * collapses the (trace, destroy) pair currently stored on every header into
 * a single static record indexed by `object_kind`. STRING/BYTES carry no
 * callbacks; LIST/MAP/SET/CLOSURE_ENV/CHANNEL each have exactly one
 * fixed pair across all instances; only `OSTY_GC_KIND_GENERIC` is genuinely
 * per-instance and stays on the headerful path.
 *
 * The lookup result is the source of truth for these callbacks once Phase 2
 * routes the trace/destroy reads through it; today it runs alongside the
 * header fields and asserts they agree, catching any alloc site that
 * passes a mismatched callback. Getting this parity check landed first is
 * what unlocks dropping the per-header function pointers in Phase 4. */
static void osty_rt_list_trace(void *payload);
static void osty_rt_list_destroy(void *payload);
static void osty_rt_map_trace(void *payload);
static void osty_rt_map_destroy(void *payload);
static void osty_rt_set_trace(void *payload);
static void osty_rt_set_destroy(void *payload);
static void osty_rt_closure_env_trace(void *payload);
static void osty_rt_chan_trace(void *payload);
static void osty_rt_chan_destroy(void *payload);
static void osty_rt_enum_ptr_payload_trace(void *payload);
static void osty_rt_task_handle_destroy(void *payload);
/* Per-kind dispatch helpers wired into the descriptor — defined
 * with the rest of the cheney young-arena machinery further down. */
static void osty_gc_cleanup_young_dead_list(void *payload);
static void osty_gc_cleanup_young_dead_set(void *payload);
static void osty_gc_cleanup_young_dead_map(void *payload);
static void osty_gc_remap_list_payload(void *payload);
static void osty_gc_remap_map_payload(void *payload);
static void osty_gc_remap_set_payload(void *payload);
static void osty_gc_remap_closure_env_payload(void *payload);
static void osty_gc_remap_chan_payload(void *payload);
static void osty_gc_remap_slot(void *slot_addr);
static void osty_gc_cleanup_young_dead_chan(void *payload);
static void osty_gc_cleanup_young_dead_task_handle(void *payload);

typedef struct osty_gc_kind_descriptor {
    osty_gc_trace_fn trace;
    osty_gc_destroy_fn destroy;
    /* Whether the no-header young arena can carry this kind. Toggle
     * at the table row, not in `osty_gc_young_eligible`. */
    bool young_eligible;
    /* Phase 0h: whether the trace function is safe to invoke without
     * `osty_gc_lock` held. False for kinds whose payload backing
     * storage can be reallocated mid-trace by the mutator (list,
     * map, set, channel) — those would need deferred-realloc or
     * hazard-pointer reclamation, which is its own piece of work.
     * True for kinds whose tracer reads either fixed-layout fields
     * (closure_env captures, generic enum payloads) or single
     * pointer slots that cannot move. */
    bool trace_lockfree_safe;
    /* Per-kind cleanup for UNFORWARDED young objects swept by
     * `cheney_destroy_dead_from_space`. NULL when the payload has
     * no external buffers. */
    osty_gc_destroy_fn cleanup_young_dead;
    /* Per-kind pointer-rewrite invoked by both
     * `osty_gc_remap_header_payload` (OLD compaction) and
     * `osty_gc_remap_young_arena_payloads` (young arena). NULL
     * when the payload carries no managed pointers. */
    osty_gc_destroy_fn remap;
} osty_gc_kind_descriptor;

/* Phase 4 generic-pattern table (RUNTIME_GC_DELTA Phase E follow-up).
 *
 * `OSTY_GC_KIND_GENERIC` covers every alloc that doesn't have a
 * dedicated runtime kind — enum payloads, task handles, struct boxes,
 * hand-rolled copies. Pre-Phase-4 each instance carried its own
 * `trace` + `destroy` fn pointers on the header (16 B / object). In
 * practice only THREE patterns ever appeared, so Phase 4 collapses
 * them into a small enum + lookup table. The header gains a
 * `uint8_t generic_pattern` field that fits in existing alignment
 * padding (cost: 0 B), and drops the two fn-pointer fields
 * (saving: 16 B). Net header shrink: 16 B per OLD object — on top
 * of the 16 B saved by going from 112 → 96 in PR #910.
 *
 * New GENERIC users add a row here, not a new fn-pointer field. */
enum {
    OSTY_GC_GENERIC_NONE = 0,
    OSTY_GC_GENERIC_ENUM_PTR = 1,
    OSTY_GC_GENERIC_TASK_HANDLE = 2,
};

static const osty_gc_kind_descriptor osty_gc_generic_patterns[] = {
    /* NONE — opaque payload, cheney handles it as raw bytes. */
    [OSTY_GC_GENERIC_NONE] = {
        .young_eligible = true,
        /* Phase 0h: NONE has no trace fn, so the lockfree flag is
         * moot. Setting true keeps the predicate result consistent
         * (no trace = nothing to race on). */
        .trace_lockfree_safe = true,
    },
    /* ENUM_PTR routes through `OSTY_GC_KIND_GENERIC_ENUM_PTR`'s row
     * in the kind table; this row is only consulted on the headerful
     * fallback dispatch, so the young flag stays false. */
    [OSTY_GC_GENERIC_ENUM_PTR] = {
        .trace = osty_rt_enum_ptr_payload_trace,
        .trace_lockfree_safe = true,
    },
    /* Same shape as ENUM_PTR — TASK_HANDLE now routes through
     * `OSTY_GC_KIND_GENERIC_TASK_HANDLE`'s row in the kind table.
     * This row is parity-only for the headerful fallback dispatch. */
    [OSTY_GC_GENERIC_TASK_HANDLE] = {
        .destroy = osty_rt_task_handle_destroy,
        .trace_lockfree_safe = true,
    },
};

#define OSTY_GC_GENERIC_PATTERN_COUNT \
    (sizeof(osty_gc_generic_patterns) / sizeof(osty_gc_generic_patterns[0]))

/* `osty_gc_pattern_of` defined later (after `osty_rt_abort`). */
static uint8_t osty_gc_pattern_of(osty_gc_trace_fn trace,
                                  osty_gc_destroy_fn destroy);

/* Indexed by `kind - OSTY_GC_KIND_LIST`. Lookup is O(1) — Phase 2 routes
 * mark drain and every sweep through this table, so a linear scan over
 * the kinds would land in the hot path. The static assertions below pin
 * the layout: any new kind must extend the contiguous range and append a
 * row here in the same order. */
static const osty_gc_kind_descriptor osty_gc_kind_table[] = {
    [OSTY_GC_KIND_LIST - OSTY_GC_KIND_LIST] = {
        .trace = osty_rt_list_trace,
        .destroy = osty_rt_list_destroy,
        .young_eligible = true,
        .cleanup_young_dead = osty_gc_cleanup_young_dead_list,
        .remap = osty_gc_remap_list_payload,
    },
    [OSTY_GC_KIND_STRING - OSTY_GC_KIND_LIST] = {.young_eligible = true},
    [OSTY_GC_KIND_MAP - OSTY_GC_KIND_LIST] = {
        .trace = osty_rt_map_trace,
        .destroy = osty_rt_map_destroy,
        .young_eligible = true,
        .cleanup_young_dead = osty_gc_cleanup_young_dead_map,
        .remap = osty_gc_remap_map_payload,
    },
    [OSTY_GC_KIND_SET - OSTY_GC_KIND_LIST] = {
        .trace = osty_rt_set_trace,
        .destroy = osty_rt_set_destroy,
        .young_eligible = true,
        .cleanup_young_dead = osty_gc_cleanup_young_dead_set,
        .remap = osty_gc_remap_set_payload,
    },
    [OSTY_GC_KIND_BYTES - OSTY_GC_KIND_LIST] = {.young_eligible = true},
    /* CLOSURE_ENV's young safety relies on a mutator-side contract:
     * captures are populated synchronously at construction (single
     * basic block in LLVM emit) before the env can escape to a slot
     * that triggers root_bind. promote-on-bind copies a fully
     * populated env to OLD; nothing in cheney_forward / promote
     * special-cases this kind. */
    [OSTY_GC_KIND_CLOSURE_ENV - OSTY_GC_KIND_LIST] = {
        .trace = osty_rt_closure_env_trace,
        .young_eligible = true,
        /* Phase 0h: captures populated synchronously at creation, never
         * mutated, fixed-length array — safe to trace without the GC
         * lock once the rest of the lock-free invariants are in place. */
        .trace_lockfree_safe = true,
        .remap = osty_gc_remap_closure_env_payload,
    },
    /* CHANNEL young-safe through the same handoff pattern as Map:
     * `osty_gc_chan_safe_handoff` re-inits dst's mutex + 2 condvars
     * and destroys src's, then null-steals the slots pointer. The
     * cleanup helper delegates to `osty_rt_chan_destroy` to free
     * the slots buffer + tear down sync state on UNFORWARDED young
     * channels. */
    [OSTY_GC_KIND_CHANNEL - OSTY_GC_KIND_LIST] = {
        .trace = osty_rt_chan_trace,
        .destroy = osty_rt_chan_destroy,
        .young_eligible = true,
        /* Phase 0h: NOT lockfree-safe — the channel slot ring uses a
         * mutex+cond and the head/tail counters mutate on send/recv;
         * even reading the slots while a sender is mid-update could
         * race the slot allocation. Future lock-free path needs a
         * snapshot or hazard pointer. */
        .cleanup_young_dead = osty_gc_cleanup_young_dead_chan,
        .remap = osty_gc_remap_chan_payload,
    },
    [OSTY_GC_KIND_GENERIC_ENUM_PTR - OSTY_GC_KIND_LIST] = {
        .trace = osty_rt_enum_ptr_payload_trace,
        .young_eligible = true,
        /* Phase 0h: payload is a single managed pointer slot — read
         * is atomic on aligned platforms and the slot itself doesn't
         * move. SATB barrier on slot writes preserves the invariant. */
        .trace_lockfree_safe = true,
        .remap = osty_gc_remap_slot,
    },
    /* TASK_HANDLE young-safe through `osty_gc_task_handle_safe_handoff`
     * (mutex + 1 condvar). No managed pointers in the payload, so no
     * remap entry. */
    [OSTY_GC_KIND_GENERIC_TASK_HANDLE - OSTY_GC_KIND_LIST] = {
        .destroy = osty_rt_task_handle_destroy,
        .young_eligible = true,
        /* Phase 0h: no trace fn at all (no managed pointer payload),
         * so the lockfree flag is moot — but mark it true so a future
         * trace fn addition would have to opt out explicitly. */
        .trace_lockfree_safe = true,
        .cleanup_young_dead = osty_gc_cleanup_young_dead_task_handle,
    },
};

_Static_assert(OSTY_GC_KIND_LIST == 1024,
               "kind descriptor table indexed off OSTY_GC_KIND_LIST");
_Static_assert(OSTY_GC_KIND_GENERIC_TASK_HANDLE - OSTY_GC_KIND_LIST + 1 ==
                   sizeof(osty_gc_kind_table) /
                       sizeof(osty_gc_kind_table[0]),
               "kind descriptor table size mismatches enum range");

static inline const osty_gc_kind_descriptor *osty_gc_kind_descriptor_lookup(
    int64_t kind) {
    if (kind < OSTY_GC_KIND_LIST || kind > OSTY_GC_KIND_GENERIC_TASK_HANDLE) {
        return NULL;
    }
    return &osty_gc_kind_table[kind - OSTY_GC_KIND_LIST];
}

/* Phase 0h: predicate that decides whether `mark_drain_budget` may
 * release `osty_gc_lock` around the tracer call for this header.
 * GENERIC kinds fall through to their generic_pattern row, which
 * defaults to NOT-safe except for the patterns marked above —
 * conservative because GENERIC users that add a trace fn need to
 * audit it explicitly before flipping the flag. */
static inline bool osty_gc_trace_lockfree_safe(osty_gc_header *header) {
    const osty_gc_kind_descriptor *desc =
        osty_gc_kind_descriptor_lookup(header->object_kind);
    if (desc != NULL) {
        return desc->trace_lockfree_safe;
    }
    /* GENERIC fallback. Look at the pattern row. */
    uint8_t pat = header->generic_pattern;
    if (pat >= OSTY_GC_GENERIC_PATTERN_COUNT) {
        return false;
    }
    return osty_gc_generic_patterns[pat].trace_lockfree_safe;
}

/* Phase 2 dispatch (RUNTIME_GC_DELTA tiny-tag young plan).
 *
 * trace/destroy now resolve through the descriptor table for every
 * registered kind; only `OSTY_GC_KIND_GENERIC` falls back to the
 * per-instance pointer still stored on the header. Phase 4 will drop
 * those fields entirely once GENERIC moves to a sparse side table.
 *
 * The two counters exist so a test can prove the descriptor route
 * actually fires on registered kinds (rather than silently always
 * taking the header fallback) without poking at private state. */
static int64_t osty_gc_dispatch_via_descriptor_total = 0;
static int64_t osty_gc_dispatch_via_header_total = 0;

static inline void osty_gc_dispatch_trace(osty_gc_header *header) {
    const osty_gc_kind_descriptor *desc =
        osty_gc_kind_descriptor_lookup(header->object_kind);
    osty_gc_trace_fn fn;
    if (desc != NULL) {
        osty_gc_dispatch_via_descriptor_total += 1;
        fn = desc->trace;
    } else {
        /* GENERIC fallback: pattern lookup replaces the per-header
         * fn-pointer field that Phase 4 dropped. */
        osty_gc_dispatch_via_header_total += 1;
        fn = osty_gc_generic_patterns[header->generic_pattern].trace;
    }
    if (fn != NULL) {
        fn(header->payload);
    }
}

static inline void osty_gc_dispatch_destroy(osty_gc_header *header) {
    const osty_gc_kind_descriptor *desc =
        osty_gc_kind_descriptor_lookup(header->object_kind);
    osty_gc_destroy_fn fn;
    if (desc != NULL) {
        osty_gc_dispatch_via_descriptor_total += 1;
        fn = desc->destroy;
    } else {
        osty_gc_dispatch_via_header_total += 1;
        fn = osty_gc_generic_patterns[header->generic_pattern].destroy;
    }
    if (fn != NULL) {
        fn(header->payload);
    }
}

enum {
    OSTY_GC_STORAGE_DIRECT = 0,
    OSTY_GC_STORAGE_BUMP_YOUNG = 1,
    OSTY_GC_STORAGE_BUMP_SURVIVOR = 2,
    OSTY_GC_STORAGE_BUMP_OLD = 3,
    OSTY_GC_STORAGE_BUMP_PINNED = 4,
};

/* Small-string optimization (SSO).
 *
 * Strings of up to 7 bytes are encoded directly inside the
 * `const char *` pointer rather than allocated on the heap. Eliminates
 * the `osty_gc_allocate_managed` call entirely for the short-piece
 * case (split tokens, short interpolation fragments, LCG corpus tags
 * like "INFO" / "alice" / "login").
 *
 * The tag lives in bit 63 — the kernel/user split bit on every
 * supported 64-bit platform (macOS arm64 / Linux x86_64 / Linux arm64
 * all use 48-bit virtual addresses for user space, so bit 63 is
 * always 0 for valid pointers). Bit 0 cannot be used because string
 * literals from `.rodata` are byte-aligned, not pointer-aligned —
 * only arena slot allocations have bit 0 == 0.
 *
 * Layout:
 *     bit 63        — tag (always 1 for inline)
 *     bits 56..58   — length 0..7
 *     bits 48..55   — reserved (0)
 *     bits 0..55    — up to 7 bytes of content, byte i at bits (i*8)..(i*8+7)
 *
 * Hot paths check the tag and route inline strings through pack /
 * unpack helpers that operate on the pointer bits directly;
 * everything else (libc memcpy, strchr, hash cache, fputs) sees a
 * stack-decoded NUL-terminated buffer when needed. The decode is 7
 * byte writes plus a NUL — comparable to a strlen on the heap side
 * and amortized away by the alloc skip on every short-piece dup. */
#define OSTY_RT_SSO_TAG (((uintptr_t)1) << 63)
#define OSTY_RT_SSO_MAX_LEN ((size_t)7)
#define OSTY_RT_SSO_DECODE_BUF_BYTES ((size_t)8)
#define OSTY_RT_SSO_LEN_SHIFT 56

static inline bool osty_rt_string_is_inline(const char *value) {
    return ((uintptr_t)value & OSTY_RT_SSO_TAG) != 0;
}

static inline size_t osty_rt_string_inline_len(const char *value) {
    return ((uintptr_t)value >> OSTY_RT_SSO_LEN_SHIFT) & 0x7;
}

static inline unsigned char osty_rt_string_inline_byte(const char *value, size_t i) {
    /* Returns `unsigned char` for parity with `osty_rt_string_pack_inline`,
     * which round-trips through `(unsigned char)`. The `char` form would
     * sign-extend for bytes >= 0x80 in any caller doing arithmetic
     * promotion (e.g., `b >= 0x80` would always be false on signed-char
     * platforms because `b` would compare as a negative int). */
    return (unsigned char)(((uintptr_t)value >> (i * 8)) & 0xff);
}

static inline const char *osty_rt_string_pack_inline(const char *src, size_t len) {
    uintptr_t packed = OSTY_RT_SSO_TAG |
                       (((uintptr_t)len & 0x7) << OSTY_RT_SSO_LEN_SHIFT);
    size_t i;
    /* Defensive: if `src` is itself an inline-tagged pointer (the
     * caller spliced bytes from a Split piece without decoding) we
     * must read bytes from the pointer bits, not via `src[i]` which
     * would dereference the encoded content. The branch costs one
     * tag-bit test on the heap path. */
    if (osty_rt_string_is_inline(src)) {
        for (i = 0; i < len; i++) {
            packed |= ((uintptr_t)(unsigned char)osty_rt_string_inline_byte(src, i)) << (i * 8);
        }
    } else {
        for (i = 0; i < len; i++) {
            packed |= ((uintptr_t)(unsigned char)src[i]) << (i * 8);
        }
    }
    return (const char *)packed;
}

static inline void osty_rt_string_inline_decode(const char *value, char *out) {
    size_t len = osty_rt_string_inline_len(value);
    size_t i;
    for (i = 0; i < len; i++) {
        out[i] = (char)osty_rt_string_inline_byte(value, i);
    }
    out[len] = '\0';
}

static inline void osty_rt_string_decode_to_buf_if_inline(const char **value,
                                                          char *buf) {
    if (osty_rt_string_is_inline(*value)) {
        osty_rt_string_inline_decode(*value, buf);
        *value = buf;
    }
}

static inline void osty_rt_string_copy_bytes(char *dest, const char *src, size_t len) {
    if (osty_rt_string_is_inline(src)) {
        size_t i;
        for (i = 0; i < len; i++) {
            dest[i] = (char)osty_rt_string_inline_byte(src, i);
        }
    } else if (len != 0) {
        memcpy(dest, src, len);
    }
}

/* Closure environment payload. The thunk ABI is preserved — the LLVM
 * call site still does `load ptr, ptr %env` — because `fn_ptr` stays
 * at offset 0. `capture_count` and `pointer_bitmap` follow so the
 * trace callback can iterate without any side metadata.
 *
 * `pointer_bitmap` (RUNTIME_GC §2.4): bit i is 1 iff `captures[i]`
 * holds a managed pointer that the tracer must mark. Scalar captures
 * set bit i to 0 so the tracer skips them entirely — a structural
 * guarantee that a scalar bit pattern aliasing a live payload address
 * cannot false-retain that payload. Bitmap width caps capture count
 * at 64 per env (enforced at alloc time); closures in practice never
 * approach this limit.
 *
 * Capture slots hold `void *` bit patterns. For pointer slots the
 * value is a managed payload pointer; for scalar slots the 8-byte
 * pattern is the raw IEEE-754 double / sign-extended integer / bool
 * the frontend stored — the GC never dereferences it. */
typedef struct osty_rt_closure_env {
    void *fn_ptr;
    int64_t capture_count;
    uint64_t pointer_bitmap;
    void *captures[];
} osty_rt_closure_env;

enum {
    OSTY_RT_ABI_I64 = 1,
    OSTY_RT_ABI_I1 = 2,
    OSTY_RT_ABI_F64 = 3,
    OSTY_RT_ABI_PTR = 4,
    OSTY_RT_ABI_STRING = 5,
    OSTY_RT_ABI_INLINE = 6,
};

static osty_gc_header *osty_gc_objects = NULL;
static int64_t osty_gc_live_count = 0;
static int64_t osty_gc_live_bytes = 0;
static int64_t osty_gc_collection_count = 0;
static int64_t osty_gc_allocated_since_collect = 0;
static bool osty_gc_safepoint_stress_loaded = false;
static bool osty_gc_safepoint_stress_enabled = false;
static bool osty_gc_pressure_limit_loaded = false;
/* 128 MiB major-collect pressure threshold (was 16 MiB pre-bump,
 * 32 KiB pre-#977). With Map/List/Set/Closure-env all young-
 * eligible, most allocations turn over in the young arena and major
 * only needs to run when objects survive past the promote age and
 * pile up in OLD. STW major walks the full OLD generation, so the
 * threshold also caps worst-case pause: 128 MiB walk lands in the
 * tens of ms even on slow hardware. Bigger thresholds give marginal
 * throughput gains but stretch the STW window — 128 MiB is the
 * balance point between throughput and latency. Tests pin specific
 * values via `OSTY_GC_THRESHOLD_BYTES` directly. */
static int64_t osty_gc_pressure_limit_bytes = (128 << 20);
static bool osty_gc_collection_requested = false;
static int64_t osty_gc_pre_write_count = 0;
static int64_t osty_gc_pre_write_managed_count = 0;
static int64_t osty_gc_post_write_count = 0;
static int64_t osty_gc_post_write_managed_count = 0;
static int64_t osty_gc_load_count = 0;
static int64_t osty_gc_load_managed_count = 0;
static int64_t osty_gc_load_forwarded_count = 0;

/* Phase A2 cumulative counters (RUNTIME_GC_DELTA §9.3).
 *
 * `allocated_since_collect` only tracks pressure between collections; once
 * the collector runs it resets to zero. These cumulative totals preserve
 * the full lifetime signal so tests and `osty_gc_debug_stats` can observe
 * allocation throughput and sweep pressure across cycles. */
static int64_t osty_gc_allocated_bytes_total = 0;
static int64_t osty_gc_swept_count_total = 0;
static int64_t osty_gc_swept_bytes_total = 0;

/* Go-style mmap'd allocation arena.
 *
 * All bump-block storage is suballocated from a single contiguous
 * virtual region reserved up front via mmap. The advantage isn't
 * physical memory savings (we'd allocate the same RAM either way)
 * but two architectural wins:
 *
 *   1. `osty_gc_arena_contains(p)` is a 2-compare range check that
 *      tells us in O(1) whether a pointer might be a managed payload.
 *      The hash-table-based `osty_gc_index_lookup` was the dominant
 *      cost in alloc-heavy workloads after #878 / #879 / #880; this
 *      lets `osty_gc_find_header` short-circuit before the hash on
 *      the overwhelmingly common case.
 *
 *   2. Bump-allocated objects skip the per-allocation hash insert
 *      entirely; the range check + header self-reference suffices
 *      to identify them. The hash table now only carries
 *      humongous calloc'd payloads (outside the arena) and post-
 *      compaction relocations.
 *
 * Reservation strategy: 4 GiB of virtual address space on POSIX
 * (MAP_PRIVATE | MAP_ANON). On Linux/macOS the kernel only commits
 * physical pages on first touch, so the reservation is essentially
 * an address-space ledger entry. The Windows path is currently a
 * stub that fails arena init and falls through to the heap-backed
 * `osty_gc_bump_block_new` fallback below; a follow-up will wrap
 * VirtualAlloc(MEM_RESERVE) for the Win32 surface.
 *
 * Sub-allocator: bump pointer with no per-block reuse. Released bump
 * blocks leak their virtual range — fine for short-lived programs
 * (benchmarks, CLI tools) but not ideal for long-running services. A
 * follow-up will add a free-list of arena chunks for reuse.
 *
 * Concurrency: arena_init runs at most once. arena_alloc updates
 * `osty_gc_arena_cursor` non-atomically — safe because every caller
 * goes through `osty_gc_bump_block_new` which is reached from
 * `osty_gc_allocate_managed`'s locked critical section in the
 * multi-threaded path (post-#872's single-mutator skip preserves
 * the lock when `osty_concurrent_workers > 0`). */
#define OSTY_GC_ARENA_BYTES ((size_t)1 << 32)  /* 4 GiB virtual */

static unsigned char *osty_gc_arena_base = NULL;
static unsigned char *osty_gc_arena_end = NULL;
static unsigned char *osty_gc_arena_cursor = NULL;
static bool osty_gc_arena_init_failed = false;

static void osty_gc_arena_init(void) {
    if (osty_gc_arena_base != NULL || osty_gc_arena_init_failed) {
        return;
    }
#if defined(_WIN32)
    /* Reserve via VirtualAlloc; smaller default size since 32-bit
     * Windows has tighter address-space limits and the runtime's
     * concurrent collector hasn't been ported here yet. */
    void *base = NULL;  /* TODO: Win32 reservation */
    if (base == NULL) {
        osty_gc_arena_init_failed = true;
        return;
    }
#else
    void *base = mmap(NULL, OSTY_GC_ARENA_BYTES,
                      PROT_READ | PROT_WRITE,
                      MAP_PRIVATE | OSTY_GC_MAP_ANON, -1, 0);
    if (base == MAP_FAILED) {
        osty_gc_arena_init_failed = true;
        return;
    }
#endif
    osty_gc_arena_base = (unsigned char *)base;
    osty_gc_arena_cursor = osty_gc_arena_base;
    osty_gc_arena_end = osty_gc_arena_base + OSTY_GC_ARENA_BYTES;
}

static void *osty_gc_arena_alloc(size_t bytes, size_t align) {
    uintptr_t cur;
    uintptr_t aligned;
    uintptr_t next;

    if (osty_gc_arena_base == NULL) {
        osty_gc_arena_init();
        if (osty_gc_arena_base == NULL) {
            return NULL;
        }
    }
    cur = (uintptr_t)osty_gc_arena_cursor;
    aligned = (cur + align - 1) & ~(align - 1);
    next = aligned + bytes;
    if (next > (uintptr_t)osty_gc_arena_end) {
        return NULL;
    }
    osty_gc_arena_cursor = (unsigned char *)next;
    return (void *)aligned;
}

static OSTY_HOT_INLINE bool osty_gc_arena_contains(const void *p) {
    return (const unsigned char *)p >= osty_gc_arena_base &&
           (const unsigned char *)p < osty_gc_arena_cursor;
}
static osty_gc_free_chunk *osty_gc_free_list_bins[OSTY_GC_FREE_LIST_BIN_COUNT];
static int64_t osty_gc_free_list_count = 0;
static int64_t osty_gc_free_list_bytes = 0;
static int64_t osty_gc_free_list_reused_count_total = 0;
static int64_t osty_gc_free_list_reused_bytes_total = 0;
static osty_gc_bump_block *osty_gc_bump_blocks = NULL;
static osty_gc_bump_block *osty_gc_bump_current = NULL;
static int64_t osty_gc_bump_block_count = 0;
static int64_t osty_gc_bump_block_bytes_total = 0;
static int64_t osty_gc_bump_alloc_count_total = 0;
static int64_t osty_gc_bump_alloc_bytes_total = 0;
static int64_t osty_gc_tlab_refill_count_total = 0;
static int64_t osty_gc_bump_recycled_block_count_total = 0;
static int64_t osty_gc_bump_recycled_bytes_total = 0;
static osty_gc_bump_block *osty_gc_survivor_bump_blocks = NULL;
static osty_gc_bump_block *osty_gc_survivor_bump_current = NULL;
static int64_t osty_gc_survivor_bump_block_count = 0;
static int64_t osty_gc_survivor_bump_block_bytes_total = 0;
static int64_t osty_gc_survivor_bump_alloc_count_total = 0;
static int64_t osty_gc_survivor_bump_alloc_bytes_total = 0;
static int64_t osty_gc_survivor_bump_recycled_block_count_total = 0;
static int64_t osty_gc_survivor_bump_recycled_bytes_total = 0;
static int64_t osty_gc_survivor_tlab_refill_count_total = 0;
static osty_gc_bump_block *osty_gc_old_bump_blocks = NULL;
static osty_gc_bump_block *osty_gc_old_bump_current = NULL;
static int64_t osty_gc_old_bump_block_count = 0;
static int64_t osty_gc_old_bump_block_bytes_total = 0;
static int64_t osty_gc_old_bump_alloc_count_total = 0;
static int64_t osty_gc_old_bump_alloc_bytes_total = 0;
static int64_t osty_gc_old_bump_recycled_block_count_total = 0;
static int64_t osty_gc_old_bump_recycled_bytes_total = 0;
static int64_t osty_gc_old_tlab_refill_count_total = 0;
static osty_gc_bump_block *osty_gc_pinned_bump_blocks = NULL;
static osty_gc_bump_block *osty_gc_pinned_bump_current = NULL;
static int64_t osty_gc_pinned_bump_block_count = 0;
static int64_t osty_gc_pinned_bump_block_bytes_total = 0;
static int64_t osty_gc_pinned_bump_alloc_count_total = 0;
static int64_t osty_gc_pinned_bump_alloc_bytes_total = 0;
static int64_t osty_gc_pinned_bump_recycled_block_count_total = 0;
static int64_t osty_gc_pinned_bump_recycled_bytes_total = 0;
static int64_t osty_gc_pinned_tlab_refill_count_total = 0;
static int64_t osty_gc_humongous_alloc_count_total = 0;
static int64_t osty_gc_humongous_alloc_bytes_total = 0;
static int64_t osty_gc_humongous_swept_count_total = 0;
static int64_t osty_gc_humongous_swept_bytes_total = 0;

/* Phase B generational bookkeeping (RUNTIME_GC_DELTA §5, §9.1, §9.4).
 *
 * `young_count` / `young_bytes`: live population in the nursery. Minor
 * collection scans exactly this subset; survivors that cross
 * `promote_age` get moved to the OLD bucket without changing their
 * payload address (no compaction yet).
 *
 * `allocated_since_minor`: pressure counter reset after every minor.
 * Paired with `osty_gc_nursery_limit_bytes` so minor GC triggers on
 * nursery overflow independent of the major limit.
 *
 * `minor_count` / `major_count` / `promoted_*`: per-tier telemetry for
 * `osty_gc_stats`, exposed so tests and tuning scripts can assert on
 * the event mix the runtime is actually producing.
 */
static int64_t osty_gc_young_count = 0;
static int64_t osty_gc_young_bytes = 0;
static int64_t osty_gc_old_count = 0;
static int64_t osty_gc_old_bytes = 0;
static int64_t osty_gc_pinned_count = 0;
static int64_t osty_gc_pinned_bytes = 0;
/* Per-generation list heads (Phase B2 depth). Populated by link /
 * unlink / promote. The global `osty_gc_objects` list remains the
 * authoritative iteration order for major collection and heap
 * validation. */
static osty_gc_header *osty_gc_young_head = NULL;
static osty_gc_header *osty_gc_old_head = NULL;
static int64_t osty_gc_allocated_since_minor = 0;
static int64_t osty_gc_minor_count = 0;
static int64_t osty_gc_major_count = 0;
static int64_t osty_gc_promoted_count_total = 0;
static int64_t osty_gc_promoted_bytes_total = 0;
static int64_t osty_gc_minor_nanos_total = 0;
static int64_t osty_gc_major_nanos_total = 0;
static int64_t osty_gc_compaction_count_total = 0;
static int64_t osty_gc_forwarded_objects_last = 0;
static int64_t osty_gc_forwarded_bytes_last = 0;
static bool osty_gc_nursery_limit_loaded = false;
static int64_t osty_gc_nursery_limit_bytes = OSTY_GC_NURSERY_LIMIT_DEFAULT;
static bool osty_gc_promote_age_loaded = false;
static int64_t osty_gc_promote_age = OSTY_GC_PROMOTE_AGE_DEFAULT;
/* Phase F card marking. Replaces the per-edge remembered-set log with
 * an owner-granularity dirty bit on the GC header plus a flat array of
 * dirty OLD owners maintained across minors. The barrier becomes:
 *
 *     if (!owner_header->card_dirty) {
 *         owner_header->card_dirty = 1;
 *         dirty_old_array.push(owner_header);
 *     }
 *
 * — O(1) regardless of how many edges the owner already has, vs. the
 * legacy `osty_gc_remembered_edges_append` whose dedup loop ran linear
 * in the cumulative edge count. Minor GC walks the dirty array instead
 * of the full OLD list, so the scan is O(|dirty owners|) rather than
 * O(|OLD generation|). Major GC re-traces every reachable header, so
 * it both clears `card_dirty` on survivors and resets the dirty array.
 *
 * Default ON. Across the program-suite benchmarks (release builds, 30
 * runs each) ON is at parity or faster than OFF — big-map 1.23×,
 * log-aggregator 1.10×, dep-resolver 1.09×, dedup-stress 1.25×; the
 * remaining benches sit within ±5% noise. The OFF path stays available
 * via `OSTY_GC_CARD_MARKING=0` for A/B regressions. */
#define OSTY_GC_CARD_MARKING_ENV "OSTY_GC_CARD_MARKING"
static bool osty_gc_card_marking_loaded = false;
static bool osty_gc_card_marking_enabled = true;
static int64_t osty_gc_cards_dirtied_total = 0;
static int64_t osty_gc_cards_scanned_total = 0;
/* Dirty OLD owner array. Entries are header pointers; `card_dirty == 1`
 * iff the header is in the array (the bit doubles as the membership
 * flag). Cleared together with all `card_dirty` bits at major GC. */
static osty_gc_header **osty_gc_dirty_old_headers = NULL;
static int64_t osty_gc_dirty_old_count = 0;
static int64_t osty_gc_dirty_old_cap = 0;
/* Forward decls — bodies live further down but the promotion paths
 * above this point need to consult the env flag and push to the dirty
 * list. */
static void osty_gc_dirty_old_push(osty_gc_header *header);
static bool osty_gc_card_marking_now(void);
/* `osty_gc_minor_in_progress` lets trace callbacks short-circuit when a
 * minor collection is underway — children that are OLD must not be
 * enqueued (they are assumed live; the remembered set handles any
 * OLD→YOUNG edges into them). */
static bool osty_gc_minor_in_progress = false;
/* `osty_gc_collection_requested_major`: set when a minor finishes but
 * major-level pressure remains (young_bytes still above nursery limit,
 * or live_bytes above major threshold). The dispatcher at the next
 * safepoint turns this into an immediate major. */
static bool osty_gc_collection_requested_major = false;
static OSTY_RT_TLS osty_gc_bump_block *osty_gc_tlab_current = NULL;
static OSTY_RT_TLS osty_gc_bump_block *osty_gc_survivor_tlab_current = NULL;
static OSTY_RT_TLS osty_gc_bump_block *osty_gc_old_tlab_current = NULL;
static OSTY_RT_TLS osty_gc_bump_block *osty_gc_pinned_tlab_current = NULL;

/* Phase C incremental collection state (RUNTIME_GC_DELTA §4.3).
 *
 * IDLE        — no collection in progress. Headers colour WHITE at
 *               rest (sweep resets BLACK → WHITE before exit).
 * MARK_INCR   — mark phase is running across multiple step calls.
 *               The mark stack may be non-empty between steps; any
 *               SATB pre_write during this window greys the old
 *               value. Mutator allocations land WHITE and will be
 *               treated as unreachable unless a subsequent step
 *               reaches them — the allocation-is-grey alternative
 *               would require every alloc site to be barriered.
 * SWEEPING    — mark queue is drained, sweep is running; transitional
 *               state so a concurrent safepoint call can abort
 *               cleanly.
 *
 * Transitions are driven by the three public entry points:
 *   `osty_gc_collect_incremental_start`  — IDLE → MARK_INCR, seeds.
 *   `osty_gc_collect_incremental_step`   — drains N; stays MARK_INCR
 *                                          or transitions MARK_INCR →
 *                                          SWEEPING when the queue
 *                                          empties.
 *   `osty_gc_collect_incremental_finish` — sweep + final reset, any
 *                                          state → IDLE.
 *
 * The STW major / minor paths own the IDLE state entirely and assert
 * on it at entry — incremental and STW cycles do not overlap.
 */
enum {
    OSTY_GC_STATE_IDLE = 0,
    OSTY_GC_STATE_MARK_INCREMENTAL = 1,
    OSTY_GC_STATE_SWEEPING = 2,
};
static int osty_gc_state = OSTY_GC_STATE_IDLE;
static int64_t osty_gc_incremental_steps_total = 0;
static int64_t osty_gc_incremental_work_total = 0;
static int64_t osty_gc_satb_barrier_greyed_total = 0;

/* Phase C3 mutator assist (RUNTIME_GC_DELTA §9.2).
 *
 * When an incremental major is active, every new allocation pushes a
 * fresh WHITE header onto the heap. Left alone, the mutator can
 * outrun the step scheduler — the grey queue grows faster than steps
 * drain it, stretching pause-free mark out indefinitely and piling
 * up survivors. Assist flips the balance: each alloc burns
 * `assist_bytes_per_unit` bytes of allocation before it pays one
 * unit of mark work. Steady-state pressure balances at the assist
 * ratio; spikes self-moderate because big allocations pay big.
 *
 * `OSTY_GC_ASSIST_BYTES_PER_UNIT`: allocator tuning knob. 0 disables
 * assist (pre-Phase-C3 behaviour). Default is 128 bytes per grey
 * drained — aggressive enough that 10k × 64-byte allocs clear a
 * 5k-object grey queue without explicit steps.
 */
#define OSTY_GC_ASSIST_BYTES_PER_UNIT_DEFAULT 128
#define OSTY_GC_ASSIST_BYTES_PER_UNIT_ENV "OSTY_GC_ASSIST_BYTES_PER_UNIT"
static bool osty_gc_assist_bytes_loaded = false;
static int64_t osty_gc_assist_bytes_per_unit = OSTY_GC_ASSIST_BYTES_PER_UNIT_DEFAULT;
static int64_t osty_gc_mutator_assist_work_total = 0;
static int64_t osty_gc_mutator_assist_calls_total = 0;

/* Forward decl so `osty_gc_allocate_managed` can call the assist drain
 * without having to be moved below the mark helpers. Defined near
 * `osty_gc_mark_drain`. */
static int64_t osty_gc_mark_drain_budget(int64_t budget);
static int64_t osty_gc_assist_bytes_per_unit_now(void);

/* Phase A2 collection timing (RUNTIME_GC_DELTA §9.3 depth follow-up).
 *
 * Measured via `clock_gettime(CLOCK_MONOTONIC)` around every
 * `osty_gc_collect_now_with_stack_roots` body. Nanosecond resolution;
 * callers that want ms convert at the edge. `_max` tracks the slowest
 * single cycle so generational triggers can be tuned against the tail,
 * not just the mean. `_last` is the most recent cycle's duration.
 */
static int64_t osty_gc_collection_nanos_total = 0;
static int64_t osty_gc_collection_nanos_last = 0;
static int64_t osty_gc_collection_nanos_max = 0;

/* Phase A3 payload→header hash index (RUNTIME_GC_DELTA §4.2 depth
 * follow-up). Replaces the O(n) linked-list scan in
 * `osty_gc_find_header`. Open addressing with linear probing and
 * tombstones; rehashed on allocation when the combined load
 * (count + tombstones) crosses 75% of capacity. Keyed by payload
 * pointer — which is stable for the lifetime of the allocation because
 * the collector does not relocate yet (compaction lands in Phase D).
 *
 * `OSTY_GC_INDEX_TOMBSTONE` is a non-NULL marker distinct from any
 * real payload pointer so probes can skip erased slots without
 * treating them as empty.
 */
/* Split-arrays layout: probing only reads keys, so the hot loop
 * touches half as many cache lines as the previous packed-pair layout
 * (`struct {payload, header}` = 16 B/slot, 4 slots/64 B line vs.
 * 8 keys/line). Headers are loaded only on a key match. With a
 * working set of tens of thousands of slots in a multi-MB index, the
 * difference is the dominant alloc-path savings for non-compacting
 * programs after #875 / #876. */
#define OSTY_GC_INDEX_TOMBSTONE ((void *)(uintptr_t)1)

static void **osty_gc_index_keys = NULL;
static osty_gc_header **osty_gc_index_values = NULL;
static int64_t osty_gc_index_capacity = 0;
static int64_t osty_gc_index_count = 0;
static int64_t osty_gc_index_tombstones = 0;
static int64_t osty_gc_index_find_ops_total = 0;

/* Phase D groundwork: stable-id → header index. This lets the runtime
 * keep a logical identity that is distinct from the current payload
 * address, which is the prerequisite for any future forwarding /
 * relocation path. Like the payload index it uses open addressing with
 * tombstones and a ≤ 0.75 combined load factor. */
typedef struct osty_gc_identity_slot {
    uint64_t stable_id;
    osty_gc_header *header;
} osty_gc_identity_slot;

#define OSTY_GC_IDENTITY_TOMBSTONE UINT64_MAX

static osty_gc_identity_slot *osty_gc_identity_slots = NULL;
static int64_t osty_gc_identity_capacity = 0;
static int64_t osty_gc_identity_count = 0;
static int64_t osty_gc_identity_tombstones = 0;
static uint64_t osty_gc_next_stable_id = 1;

/* Phase D: old-payload → new-header forwarding table. Entries persist
 * after compaction so `osty.gc.load_v1` can canonicalize stale payload
 * pointers to the current evacuated object. Later compactions retain
 * historical aliases for still-live stable ids, so a payload from two
 * or more moves ago still resolves to the newest header. */
typedef struct osty_gc_forwarding_slot {
    void *from_payload;
    osty_gc_header *to_header;
    uint64_t stable_id;
    int64_t byte_size;
} osty_gc_forwarding_slot;

typedef struct osty_gc_forwarding_snapshot {
    void *from_payload;
    uint64_t stable_id;
} osty_gc_forwarding_snapshot;

#define OSTY_GC_FORWARDING_TOMBSTONE ((void *)(uintptr_t)1)

static osty_gc_forwarding_slot *osty_gc_forwarding_slots = NULL;
static int64_t osty_gc_forwarding_capacity = 0;
static int64_t osty_gc_forwarding_count = 0;
static int64_t osty_gc_forwarding_tombstones = 0;

/* Phase A6 stackmap overflow guard (RUNTIME_GC_DELTA §10.2).
 *
 * The LLVM emitter materialises one `alloca ptr, i64 N` per safepoint,
 * with N = number of visible managed roots in the current frame. Tight
 * frames are fine; runaway generated code (e.g. a generator that lifts
 * every sub-expression to a frame slot) can push N high enough that the
 * alloca blows the C thread stack well before the GC ever looks at the
 * slots.
 *
 * The runtime can't shrink the LLVM-side alloca retroactively, so this
 * guard is a crash early / crash loud backstop: any safepoint that
 * reports more roots than `OSTY_GC_SAFEPOINT_MAX_ROOTS` aborts with a
 * clear message. Legitimate frames are nowhere near this cap today —
 * `visibleSafepointRoots()` typically yields a single-digit count — so
 * tripping this is a bug either in lowering or in a test that crafts
 * synthetic input. The upper bound can be raised as the compiler grows
 * larger functions, but the contract that "a safepoint gets a fixed,
 * bounded slot array" should survive.
 *
 * `osty_gc_safepoint_max_roots_seen` is a high-water mark exposed via
 * `osty_gc_debug_safepoint_max_roots_seen` so tuning passes can tell
 * how close real programs get to the cap.
 */
#define OSTY_GC_SAFEPOINT_MAX_ROOTS ((int64_t)65536)
static int64_t osty_gc_safepoint_max_roots_seen = 0;

/* Forward decl of the shadow-stack frame so internal helpers can reach
 * the chain head when a collection fires. The actual struct + entry/
 * leave runtime symbols are defined alongside `osty_gc_safepoint_v1`.
 *
 * Layout must mirror the typedef declared near `osty_gc_safepoint_v1`.
 * The frames are alloca'd in callee stacks; the chain head is
 * thread-local so each mutator sees only its own roots. */
struct osty_gc_root_frame {
    struct osty_gc_root_frame *prev;
    void *const *slots;
    int64_t count;
};
static OSTY_RT_TLS struct osty_gc_root_frame *osty_gc_root_chain_top = NULL;

/* Forward decl for the string-len/hash cache invalidator. The cache
 * itself is defined further down (alongside `osty_rt_string_measure`),
 * but `osty_gc_cheney_swap_arenas` needs to wipe it before any young
 * arena address gets reused to hold different content. */
static void osty_rt_string_cache_invalidate_all(void);

/* Walk the shadow-stack chain and apply a per-slot action. The current
 * stack-roots argument to `collect_now_with_stack_roots` covers only the
 * deepest frame; the chain extends that to all ancestor frames so cross-
 * frame Maps / Lists stay reachable. Each helper that sweeps the explicit
 * `root_slots` array also calls one of these to cover the rest. */
#define OSTY_GC_FOR_EACH_CHAIN_ROOT(action_call) do { \
    struct osty_gc_root_frame *_f = osty_gc_root_chain_top; \
    while (_f != NULL) { \
        int64_t _i; \
        for (_i = 0; _i < _f->count; _i++) { \
            void *_slot = (void *)_f->slots[_i]; \
            action_call; \
        } \
        _f = _f->prev; \
    } \
} while (0)

/* Phase A5 safepoint kind taxonomy (RUNTIME_GC_DELTA §10.1).
 *
 * Every emitted safepoint poll now encodes a "kind" in the high bits of
 * the id parameter passed to `osty_gc_safepoint_v1`. The low 56 bits hold
 * the per-module serial the LLVM emitter has always produced; the top 8
 * bits classify the *why*: function entry, pre-call, loop back-edge, etc.
 *
 * Today the collector does not behave differently across kinds (it is a
 * single-kind STW collector), but recording the classification gives us:
 *
 *   - debuggability: per-kind counters exposed via `osty_gc_debug_*`
 *   - Phase B/C leverage: tail-biased triggers (e.g. never poll inside
 *     tight arithmetic loops by muting LOOP kinds under stress)
 *   - Conformance tests: harnesses can assert on the kind mix emitted
 *     from a given source shape.
 *
 * Kinds are uint8. `UNSPECIFIED` stays the default for legacy callers
 * that have not migrated yet; new call sites should always supply a
 * specific kind.
 */
enum {
    OSTY_GC_SAFEPOINT_KIND_UNSPECIFIED = 0,
    OSTY_GC_SAFEPOINT_KIND_ENTRY = 1,
    OSTY_GC_SAFEPOINT_KIND_CALL = 2,
    OSTY_GC_SAFEPOINT_KIND_LOOP = 3,
    OSTY_GC_SAFEPOINT_KIND_ALLOC = 4,
    OSTY_GC_SAFEPOINT_KIND_YIELD = 5,
    OSTY_GC_SAFEPOINT_KIND_COUNT = 6,
};
static int64_t osty_gc_safepoint_counts_by_kind[OSTY_GC_SAFEPOINT_KIND_COUNT];

/* Phase A3 mark work queue (RUNTIME_GC_DELTA §4.2).
 *
 * Replaces the previous recursive trace path. `osty_gc_mark_header` no longer
 * recurses; it flips `marked` (acting as the grey colour) and pushes the
 * header onto this stack. `osty_gc_mark_drain` pops entries and invokes each
 * header's trace callback, which in turn calls back into `osty_gc_mark_*`
 * for children — they simply re-enqueue, so the C call stack stays O(1)
 * regardless of object graph depth.
 *
 * The stack is static-global and reused across collections. Capacity grows
 * monotonically so steady-state workloads avoid realloc churn. `max_depth`
 * is exposed via `osty_gc_debug_mark_stack_max_depth` for Phase A1 heap
 * validation and future Phase B tuning.
 */
static osty_gc_header **osty_gc_mark_stack = NULL;
static int64_t osty_gc_mark_stack_count = 0;
static int64_t osty_gc_mark_stack_cap = 0;
static int64_t osty_gc_mark_stack_max_depth = 0;

/* Global root slots. Each entry is the address of a storage location that may
 * hold a managed pointer (i.e. `void **`). At mark time the slot is
 * dereferenced and the current payload marked — so reassigning the slot
 * automatically re-roots the new value without explicit churn. This differs
 * from `root_bind_v1` which pins a specific payload by refcount. */
static void **osty_gc_global_root_slots = NULL;
static int64_t osty_gc_global_root_count = 0;
static int64_t osty_gc_global_root_cap = 0;

/* Write-barrier edge logs (RUNTIME_GC_DELTA §3.1 / §3.2).
 *
 * Under the current STW mark-sweep path these buffers are recorded but not
 * yet consumed during collection — the marker still re-traces from roots on
 * every cycle. They become active inputs when generational minor collection
 * (§5) and incremental / concurrent marking (§4.3) land. Keeping the
 * recording live now means the lowering's already-emitted
 * `pre_write_v1` / `post_write_v1` calls stop being no-ops and start
 * producing observable state that tests can assert on.
 *
 * `osty_gc_satb_log`: payloads captured by `pre_write_v1` (the value that
 * WAS in the slot before the store). SATB is only required once marking
 * becomes concurrent, at which point the log preserves the initial
 * reachability snapshot. Recorded now, consumed later.
 *
 * `osty_gc_remembered_edges`: (owner_payload, value_payload) pairs captured
 * by `post_write_v1`. Deduplicated via linear scan — fine while edges are
 * sparse. Future generational code will filter by owner/value region.
 *
 * Both logs are cleared at the end of every full collection (the post-sweep
 * heap state is a fresh baseline).
 */
static void **osty_gc_satb_log = NULL;
static int64_t osty_gc_satb_log_count = 0;
static int64_t osty_gc_satb_log_cap = 0;
static osty_gc_trace_slot_mode osty_gc_trace_slot_mode_current =
    OSTY_GC_TRACE_SLOT_MODE_MARK;

typedef struct osty_gc_remembered_edge {
    void *owner;
    void *value;
} osty_gc_remembered_edge;

static osty_gc_remembered_edge *osty_gc_remembered_edges = NULL;
static int64_t osty_gc_remembered_edge_count = 0;
static int64_t osty_gc_remembered_edge_cap = 0;

static void osty_gc_barrier_logs_clear(void);

void osty_gc_post_write_v1(void *owner, void *value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.post_write_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));
void osty_gc_root_release_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_release_v1"));
void *osty_gc_load_v1(void *value) __asm__(OSTY_GC_SYMBOL("osty.gc.load_v1"));
void osty_gc_mark_slot_v1(void *slot_addr) __asm__(OSTY_GC_SYMBOL("osty.gc.mark_slot_v1"));

static void osty_rt_abort(const char *message) {
    fprintf(stderr, "osty llvm runtime: %s\n", message);
    abort();
}

/* Phase 4 reverse-lookup body. Forward-declared near
 * `osty_gc_generic_patterns` since alloc paths (which are above
 * `osty_rt_abort`) need to call it. */
static uint8_t osty_gc_pattern_of(osty_gc_trace_fn trace,
                                  osty_gc_destroy_fn destroy) {
    size_t i;
    for (i = 0; i < OSTY_GC_GENERIC_PATTERN_COUNT; i++) {
        if (osty_gc_generic_patterns[i].trace == trace &&
            osty_gc_generic_patterns[i].destroy == destroy) {
            return (uint8_t)i;
        }
    }
    osty_rt_abort("unrecognised GENERIC (trace, destroy) pair — register a new pattern in osty_gc_generic_patterns");
    return 0;
}

// Public abort helper called from LLVM IR when `Option.unwrap()` fires on
// a None value. The corresponding stdlib body (`option.osty::unwrap`)
// routes to this through the MIR `IntrinsicOptionUnwrap` lowerer —
// backends emit a disc==0 branch that tail-calls this helper. The
// companion `osty_rt_result_unwrap_err` handles the Result variant.
void osty_rt_option_unwrap_none(void) {
    osty_rt_abort("called unwrap on None");
}

// Noreturn OOB helper used by the List<scalar> fast-path writer. The
// fast-path emits `icmp ult idx, snapshot_len` and only hits this on
// verified out-of-range indices; calling a dedicated noreturn helper
// lets LLVM treat the OOB branch as dead code and hoist list-metadata
// reads past the fast-path store without alias concerns from the slow
// path.
void osty_rt_list_oob_abort_v1(void) {
    osty_rt_abort("list index out of range");
}

static uint64_t osty_gc_allocate_stable_id(void) {
    uint64_t id = osty_gc_next_stable_id;
    if (id == 0 || id == OSTY_GC_IDENTITY_TOMBSTONE) {
        osty_rt_abort("GC stable id overflow");
    }
    osty_gc_next_stable_id = id + 1;
    return id;
}

/* GC + runtime-wide lock. Recursive so that collection (invoked from
 * within allocate_managed) can re-enter write-barriers and root scans
 * without deadlocking. Initialized on first use so no constructor is
 * required. */
static osty_rt_once_t osty_gc_lock_once = OSTY_RT_ONCE_INIT;
static osty_rt_rmu_t osty_gc_lock;
static int64_t osty_concurrent_workers = 0;
static int osty_runtime_has_concurrency = 0;

static inline int64_t osty_rt_concurrent_workers_load(void) {
    return __atomic_load_n(&osty_concurrent_workers, __ATOMIC_ACQUIRE);
}

static inline bool osty_rt_runtime_has_concurrency(void) {
    return __atomic_load_n(&osty_runtime_has_concurrency,
                           __ATOMIC_ACQUIRE) != 0;
}

static inline void osty_rt_enable_concurrency_runtime(void) {
    __atomic_store_n(&osty_runtime_has_concurrency, 1, __ATOMIC_RELEASE);
}

static void osty_gc_lock_init(void) {
    if (osty_rt_rmu_init(&osty_gc_lock) != 0) {
        osty_rt_abort("gc lock: init failed");
    }
}

static void osty_gc_acquire(void) {
    osty_rt_once(&osty_gc_lock_once, osty_gc_lock_init);
    osty_rt_rmu_lock(&osty_gc_lock);
}

static void osty_gc_release(void) {
    osty_rt_rmu_unlock(&osty_gc_lock);
}

static void osty_sched_workers_inc(void) {
    osty_gc_acquire();
    (void)__atomic_add_fetch(&osty_concurrent_workers, 1, __ATOMIC_ACQ_REL);
    osty_gc_release();
}

static void osty_sched_workers_dec(void) {
    osty_gc_acquire();
    if (osty_rt_concurrent_workers_load() <= 0) {
        osty_gc_release();
        osty_rt_abort("scheduler: worker counter underflow");
    }
    (void)__atomic_sub_fetch(&osty_concurrent_workers, 1, __ATOMIC_ACQ_REL);
    osty_gc_release();
}

/* Phase 0a background marker thread (RUNTIME_GC.md follow-up).
 *
 * When `OSTY_GC_BG_MARKER=1` is set, the first call to
 * `osty_gc_collect_incremental_start_with_stack_roots` lazily spawns a
 * single OS thread that drains the grey queue while the collector is
 * in MARK_INCREMENTAL. This is the first real "concurrent" mark step:
 * before this, `incremental_step` and the mutator-assist path were the
 * only ways to advance a cycle, so progress was bounded by mutator
 * activity (incremental was budgeted, not concurrent). With the bg
 * thread, mark work proceeds on another core while the mutator runs
 * non-allocating code — only the initial root scan and the final
 * remark/sweep window remain STW.
 *
 * The marker shares `osty_gc_lock` with the mutator. That keeps the
 * design conservative for Phase 0a: the lock-skip fast paths
 * (allocate_managed, load_v1, post_write_v1, map mutex) must take the
 * lock when the marker is active because they otherwise race with the
 * marker's tracer reads of the payload→header index and forwarding
 * map. `osty_gc_serialized_now()` consolidates that gate.
 *
 * Kick / park protocol:
 *   - `osty_gc_collect_incremental_start_with_stack_roots` flips
 *     `bg_marker_active = 1` and signals the cv.
 *   - `osty_gc_collect_incremental_finish_locked` clears it back to 0
 *     before the final drain so the marker exits its inner loop on the
 *     next iteration. The marker observes the cleared flag through the
 *     atomic load at the top of each drain step.
 *
 * Lifecycle: thread starts on demand and lives until process exit. No
 * explicit shutdown — standard for a GC daemon. */
#define OSTY_GC_BG_MARKER_ENV "OSTY_GC_BG_MARKER"
#define OSTY_GC_BG_MARKER_BUDGET_ENV "OSTY_GC_BG_MARKER_BUDGET"
#define OSTY_GC_BG_MARKER_BUDGET_DEFAULT 1024
/* Idle nap when the queue is drained but the cycle is still active.
 * Mutator SATB barriers can push more work; a short nap keeps the
 * marker responsive without spinning. */
#define OSTY_GC_BG_MARKER_IDLE_NAP_NS 200000ULL  /* 200 µs */

/* Phase 0c: parallel marker workers. `OSTY_GC_BG_WORKERS` controls the
 * worker count (default 1, max OSTY_GC_BG_MARKER_MAX_WORKERS). All N
 * workers share the same grey queue under `osty_gc_lock`; lock
 * contention is the limiting factor — Phase 0d reduces it with a
 * segregated push/pop path. Per-worker drain counters expose the work
 * split so tests can prove parallelism actually engaged. */
#define OSTY_GC_BG_WORKERS_ENV "OSTY_GC_BG_WORKERS"
#define OSTY_GC_BG_WORKERS_DEFAULT 1
#define OSTY_GC_BG_MARKER_MAX_WORKERS 16

static int osty_gc_bg_marker_enabled_loaded = 0;
static int osty_gc_bg_marker_enabled_value = 0;
static int osty_gc_bg_marker_budget_loaded = 0;
static int64_t osty_gc_bg_marker_budget_value = OSTY_GC_BG_MARKER_BUDGET_DEFAULT;
static int osty_gc_bg_workers_loaded = 0;
static int osty_gc_bg_workers_value = OSTY_GC_BG_WORKERS_DEFAULT;

static osty_rt_thread_t osty_gc_bg_marker_threads[OSTY_GC_BG_MARKER_MAX_WORKERS];
static int osty_gc_bg_marker_started = 0;
static int osty_gc_bg_marker_worker_count = 0;
static osty_rt_mu_t osty_gc_bg_marker_mu;
static osty_rt_cond_t osty_gc_bg_marker_cv;
/* `active` flag: 1 between incremental.start and finish, 0 otherwise.
 * Atomic so the marker can fast-exit its drain loop without holding
 * the kick mutex on every iteration. */
static int osty_gc_bg_marker_active = 0;

/* Atomic counters because N>=2 marker threads update them concurrently.
 * For N==1 the atomics still compile to plain loads/stores on x86 +
 * release-store on ARM — overhead in the noise vs. the trace work
 * itself. */
static int64_t osty_gc_bg_marker_drained_total = 0;
static int64_t osty_gc_bg_marker_loops_total = 0;
static int64_t osty_gc_bg_marker_naps_total = 0;
static int64_t osty_gc_bg_marker_kicks_total = 0;
/* Per-worker drained counter for parallelism observability. Each
 * worker owns one slot indexed by its TLS worker_id. The aggregate
 * `drained_total` is the sum of these but is also tracked atomically
 * so a reader doesn't have to walk the array. */
static int64_t osty_gc_bg_marker_per_worker_drained[OSTY_GC_BG_MARKER_MAX_WORKERS];
static OSTY_RT_TLS int osty_gc_bg_marker_worker_id = -1;

static bool osty_gc_bg_marker_enabled_now(void) {
    if (osty_gc_bg_marker_enabled_loaded) {
        return osty_gc_bg_marker_enabled_value != 0;
    }
    osty_gc_bg_marker_enabled_loaded = 1;
    const char *value = getenv(OSTY_GC_BG_MARKER_ENV);
    if (value == NULL || value[0] == '\0' || strcmp(value, "0") == 0 ||
        strcmp(value, "false") == 0 || strcmp(value, "FALSE") == 0) {
        osty_gc_bg_marker_enabled_value = 0;
    } else {
        osty_gc_bg_marker_enabled_value = 1;
    }
    return osty_gc_bg_marker_enabled_value != 0;
}

static int64_t osty_gc_bg_marker_budget_now(void) {
    if (osty_gc_bg_marker_budget_loaded) {
        return osty_gc_bg_marker_budget_value;
    }
    osty_gc_bg_marker_budget_loaded = 1;
    const char *value = getenv(OSTY_GC_BG_MARKER_BUDGET_ENV);
    if (value == NULL || value[0] == '\0') {
        return osty_gc_bg_marker_budget_value;
    }
    char *end = NULL;
    long long parsed = strtoll(value, &end, 10);
    if (end == value || (end != NULL && *end != '\0') || parsed <= 0) {
        osty_rt_abort("invalid " OSTY_GC_BG_MARKER_BUDGET_ENV);
    }
    osty_gc_bg_marker_budget_value = (int64_t)parsed;
    return osty_gc_bg_marker_budget_value;
}

static int osty_gc_bg_workers_now(void) {
    if (osty_gc_bg_workers_loaded) {
        return osty_gc_bg_workers_value;
    }
    osty_gc_bg_workers_loaded = 1;
    const char *value = getenv(OSTY_GC_BG_WORKERS_ENV);
    if (value == NULL || value[0] == '\0') {
        return osty_gc_bg_workers_value;
    }
    char *end = NULL;
    long long parsed = strtoll(value, &end, 10);
    if (end == value || (end != NULL && *end != '\0') || parsed <= 0) {
        osty_rt_abort("invalid " OSTY_GC_BG_WORKERS_ENV);
    }
    if (parsed > OSTY_GC_BG_MARKER_MAX_WORKERS) {
        parsed = OSTY_GC_BG_MARKER_MAX_WORKERS;
    }
    osty_gc_bg_workers_value = (int)parsed;
    return osty_gc_bg_workers_value;
}

/* True when no other thread can read or mutate GC-managed state, so
 * the lock-skip fast paths in alloc / load / post_write / map ops are
 * safe. Both user worker threads (`osty_concurrent_workers`) and the
 * background marker (`osty_gc_bg_marker_active`) count — either being
 * live makes the lock necessary. */
static inline bool osty_gc_serialized_now(void) {
    return osty_rt_concurrent_workers_load() == 0 &&
           __atomic_load_n(&osty_gc_bg_marker_active,
                           __ATOMIC_ACQUIRE) == 0;
}

static void osty_gc_bg_marker_kick(void);
static void osty_gc_bg_marker_park(void);

/* Phase 0b: STW handshake helper for multi-thread incremental cycles.
 *
 * `osty_gc_collect_incremental_start_with_stack_roots` and its finish
 * counterpart are normally driven by the main thread of a
 * single-mutator program. When user worker threads are live
 * (`osty_concurrent_workers > 0`), the caller's own root slots are
 * not enough — sibling mutators hold roots on their own stacks that
 * the cycle must scan. The handshake parks every worker at a
 * safepoint, walks the published-roots registry built up by
 * `osty_rt_sched_preempt_park_for_collector`, and unions those roots
 * with the caller's into a flat array suitable for
 * `osty_gc_incremental_seed_roots` or
 * `osty_gc_collect_incremental_finish_locked`.
 *
 * Caller contract: must NOT be a pool worker (`osty_sched_is_worker`
 * = false). A worker calling this would have to park itself and
 * deadlock the wait_all_parked loop. Tests and main-thread runtime
 * paths satisfy this naturally; auto-drive paths route around it via
 * the existing single-thread gate. */
typedef struct osty_gc_stw_handshake_state {
    void **flat_roots;
    int64_t total_count;
    int held;
} osty_gc_stw_handshake_state;

static void osty_gc_stw_handshake_acquire(
    void *const *caller_roots, int64_t caller_count,
    osty_gc_stw_handshake_state *out);
static void osty_gc_stw_handshake_release(
    osty_gc_stw_handshake_state *state);

/* SplitMix64-style mixing for pointer hashing. Pointers on x86_64 / ARM64
 * have alignment-induced low-bit correlation (headers are 16-byte aligned
 * thanks to `calloc`), so naive `& mask` would cluster. This finaliser
 * makes the probe sequence look close to uniform random. */
static size_t osty_gc_index_hash(void *payload) {
    uint64_t h = (uint64_t)(uintptr_t)payload;
    h ^= h >> 33;
    h *= 0xff51afd7ed558ccdULL;
    h ^= h >> 33;
    h *= 0xc4ceb9fe1a85ec53ULL;
    h ^= h >> 33;
    return (size_t)h;
}

static osty_gc_header *osty_gc_find_header(void *payload);
static void osty_gc_index_insert(void *payload, osty_gc_header *header);
static void osty_gc_identity_insert(uint64_t stable_id, osty_gc_header *header);
static void osty_gc_forwarding_insert(void *from_payload, osty_gc_header *to_header);

static size_t osty_gc_identity_hash(uint64_t stable_id) {
    uint64_t h = stable_id;
    h ^= h >> 33;
    h *= 0xff51afd7ed558ccdULL;
    h ^= h >> 33;
    h *= 0xc4ceb9fe1a85ec53ULL;
    h ^= h >> 33;
    return (size_t)h;
}

static size_t osty_gc_forwarding_hash(void *payload) {
    return osty_gc_index_hash(payload);
}

/* Phase 0f: cycle-deferred grow flag. While the collector is in
 * MARK_INCREMENTAL or SWEEPING, growing the index would free the
 * old buffer that a concurrent (lock-free) prober could still be
 * reading. Insert sites that hit the load-factor threshold during
 * a cycle just set this flag instead of growing; finish_locked
 * picks it up and grows under STW after the cycle has fully
 * settled. The pre-grow at incremental_start sizes the table so
 * typical workloads don't trip this during the cycle. */
static int osty_gc_index_grow_pending = 0;
static int64_t osty_gc_index_grow_deferred_total = 0;
static int64_t osty_gc_index_pregrow_total = 0;

static void osty_gc_index_grow(int64_t new_capacity) {
    void **old_keys = osty_gc_index_keys;
    osty_gc_header **old_values = osty_gc_index_values;
    int64_t old_cap = osty_gc_index_capacity;
    int64_t i;
    osty_gc_index_keys = (void **)calloc((size_t)new_capacity, sizeof(void *));
    osty_gc_index_values = (osty_gc_header **)calloc((size_t)new_capacity,
                                                     sizeof(osty_gc_header *));
    if (osty_gc_index_keys == NULL || osty_gc_index_values == NULL) {
        osty_rt_abort("out of memory (gc index)");
    }
    osty_gc_index_capacity = new_capacity;
    osty_gc_index_count = 0;
    osty_gc_index_tombstones = 0;
    if (old_keys != NULL) {
        for (i = 0; i < old_cap; i++) {
            void *p = old_keys[i];
            if (p != NULL && p != OSTY_GC_INDEX_TOMBSTONE) {
                osty_gc_index_insert(p, old_values[i]);
            }
        }
        free(old_keys);
        free(old_values);
    }
    osty_gc_index_grow_pending = 0;
}

/* Pre-grow the index before a cycle if its post-cycle high-water
 * mark would otherwise force an in-cycle grow. Caller holds the GC
 * lock and is responsible for ensuring the cycle hasn't started yet
 * (state == IDLE) so no concurrent prober can trip on the buffer
 * swap. The "2× current count" target is heuristic — generous enough
 * to absorb the typical mutator-assist + barrier-greying load without
 * tripping the 0.75 threshold mid-cycle. */
static void osty_gc_index_pregrow_for_cycle(void) {
    if (osty_gc_index_capacity == 0) {
        osty_gc_index_grow(128);
        osty_gc_index_pregrow_total += 1;
        return;
    }
    int64_t projected = (osty_gc_index_count + osty_gc_index_tombstones) * 4;
    int64_t want = projected;
    /* Match the insert-side trigger: load factor ≤ 0.75 means
     * (count + tombs) * 4 < cap * 3, so headroom ≥ count * 1.33×. */
    if (want >= osty_gc_index_capacity * 3 / 2) {
        int64_t new_cap = osty_gc_index_capacity * 2;
        if (new_cap < projected) new_cap = projected;
        /* Round up to power of two — index hash mask requires it. */
        int64_t cap = 128;
        while (cap < new_cap) cap *= 2;
        osty_gc_index_grow(cap);
        osty_gc_index_pregrow_total += 1;
    }
}

/* Fresh-key insert — caller guarantees `payload` is not already in
 * the index (every `osty_gc_link` site is a new allocation; rehash
 * after grow also reseeds known-fresh keys into a fresh table). The
 * generic `osty_gc_index_insert` keeps the "update if exists" branch
 * for the rare site that genuinely needs to overwrite a header
 * pointer for an existing key.
 *
 * The hot fast path here:
 *   - one cap/load check
 *   - one hash compute
 *   - linear probe (typical 1-2 iters at < 0.75 load) reading only
 *     keys (split-array layout from #878 already keeps probes in
 *     8 keys/cache line)
 *   - one tombstone reuse + one keys/values store
 *
 * Per-insert cost on log-aggregator-shaped programs drops from
 * ~2500 stack-top samples to ~1700 in the post-#878 profile —
 * the saving is the eliminated existing-key compare + the dropped
 * `first_tombstone` SIZE_MAX bookkeeping for the common path. */
static OSTY_HOT_INLINE void osty_gc_index_insert_new(void *payload, osty_gc_header *header) {
    size_t mask;
    size_t idx;
    if (__builtin_expect(osty_gc_index_keys == NULL ||
                         (osty_gc_index_count + osty_gc_index_tombstones + 1) * 4 >=
                             osty_gc_index_capacity * 3, 0)) {
        /* Phase 0f: defer grow during a cycle so concurrent probers
         * don't see a buffer swap mid-walk. The pre-grow at
         * incremental_start sizes the table so this branch rarely
         * fires inside a cycle. If it does, the next finish runs
         * the grow under STW. */
        if (osty_gc_state != OSTY_GC_STATE_IDLE &&
            osty_gc_index_keys != NULL &&
            osty_gc_index_count + osty_gc_index_tombstones + 1 <
                osty_gc_index_capacity) {
            if (!osty_gc_index_grow_pending) {
                osty_gc_index_grow_deferred_total += 1;
            }
            osty_gc_index_grow_pending = 1;
        } else {
            int64_t new_cap = osty_gc_index_capacity == 0 ? 128 : osty_gc_index_capacity * 2;
            osty_gc_index_grow(new_cap);
        }
    }
    mask = (size_t)(osty_gc_index_capacity - 1);
    idx = osty_gc_index_hash(payload) & mask;
    /* Prefetch the slot we're about to read. Hash-table cache misses
     * to L2/L3 are the dominant cost on alloc-heavy programs (50K+
     * allocs/iter on log-aggregator); issuing the prefetch concurrently
     * with the surrounding header-init work gives the load a chance to
     * overlap with that work instead of stalling the probe. */
    __builtin_prefetch(&osty_gc_index_keys[idx], 1 /* write */, 1 /* moderate locality */);
    /* Phase 0f: atomic loads on the probe walk. Concurrent readers in
     * `osty_gc_index_lookup` (e.g. a future lock-free tracer) need
     * torn-free observation of slot pointers; ACQUIRE here so a
     * subsequent value load sees the corresponding insert's value
     * write. Walk past live entries. The first tombstone we hit is
     * reusable; since the caller guarantees `payload` is fresh, we
     * don't need to keep walking to confirm absence — we can
     * short-circuit on the first tombstone. */
    void *slot;
    while ((slot = __atomic_load_n(&osty_gc_index_keys[idx],
                                   __ATOMIC_ACQUIRE)) != NULL &&
           slot != OSTY_GC_INDEX_TOMBSTONE) {
        idx = (idx + 1) & mask;
    }
    if (slot == OSTY_GC_INDEX_TOMBSTONE) {
        osty_gc_index_tombstones -= 1;
    }
    /* Phase 0f: ordered stores so a concurrent reader that sees the
     * key set will also see the matching value. Value first (RELAXED
     * — only valid once key publishes), then key with RELEASE. */
    __atomic_store_n(&osty_gc_index_values[idx], header, __ATOMIC_RELAXED);
    __atomic_store_n(&osty_gc_index_keys[idx], payload, __ATOMIC_RELEASE);
    osty_gc_index_count += 1;
}

static void osty_gc_index_insert(void *payload, osty_gc_header *header) {
    size_t mask;
    size_t idx;
    size_t first_tombstone = SIZE_MAX;
    if (osty_gc_index_keys == NULL ||
        (osty_gc_index_count + osty_gc_index_tombstones + 1) * 4 >=
            osty_gc_index_capacity * 3) {
        if (osty_gc_state != OSTY_GC_STATE_IDLE &&
            osty_gc_index_keys != NULL &&
            osty_gc_index_count + osty_gc_index_tombstones + 1 <
                osty_gc_index_capacity) {
            if (!osty_gc_index_grow_pending) {
                osty_gc_index_grow_deferred_total += 1;
            }
            osty_gc_index_grow_pending = 1;
        } else {
            int64_t new_cap = osty_gc_index_capacity == 0 ? 128 : osty_gc_index_capacity * 2;
            osty_gc_index_grow(new_cap);
        }
    }
    mask = (size_t)(osty_gc_index_capacity - 1);
    idx = osty_gc_index_hash(payload) & mask;
    void *slot;
    while ((slot = __atomic_load_n(&osty_gc_index_keys[idx],
                                   __ATOMIC_ACQUIRE)) != NULL) {
        if (slot == OSTY_GC_INDEX_TOMBSTONE) {
            if (first_tombstone == SIZE_MAX) {
                first_tombstone = idx;
            }
        } else if (slot == payload) {
            __atomic_store_n(&osty_gc_index_values[idx], header,
                             __ATOMIC_RELEASE);
            return;
        }
        idx = (idx + 1) & mask;
    }
    if (first_tombstone != SIZE_MAX) {
        idx = first_tombstone;
        osty_gc_index_tombstones -= 1;
    }
    /* Phase 0f: same value-then-key publish ordering as `_insert_new`. */
    __atomic_store_n(&osty_gc_index_values[idx], header, __ATOMIC_RELAXED);
    __atomic_store_n(&osty_gc_index_keys[idx], payload, __ATOMIC_RELEASE);
    osty_gc_index_count += 1;
}

static osty_gc_header *osty_gc_index_lookup(void *payload) {
    size_t mask;
    size_t idx;
    if (__builtin_expect(osty_gc_index_keys == NULL || payload == NULL ||
                         payload == OSTY_GC_INDEX_TOMBSTONE, 0)) {
        return NULL;
    }
    mask = (size_t)(osty_gc_index_capacity - 1);
    idx = osty_gc_index_hash(payload) & mask;
    /* Prefetch the first probe slot. Read-only locality hint: the
     * caller is about to compare to payload, then maybe load a
     * subsequent slot if there's a collision. */
    __builtin_prefetch(&osty_gc_index_keys[idx], 0 /* read */, 1);
    /* Phase 0f: atomic ACQUIRE loads pair with the inserter's RELEASE
     * stores so a reader either sees the slot empty or a fully-published
     * (key, value) pair — never a key without its value. This makes
     * the probe safe to call without `osty_gc_lock` once the rest of
     * the tracer's invariants land (cycle-deferred grow, atomic color
     * writes — Phase 0g). */
    void *slot;
    while ((slot = __atomic_load_n(&osty_gc_index_keys[idx],
                                   __ATOMIC_ACQUIRE)) != NULL) {
        if (slot == payload) {
            return __atomic_load_n(&osty_gc_index_values[idx],
                                   __ATOMIC_ACQUIRE);
        }
        idx = (idx + 1) & mask;
    }
    return NULL;
}

static void osty_gc_index_remove(void *payload) {
    size_t mask;
    size_t idx;
    if (osty_gc_index_keys == NULL || payload == NULL ||
        payload == OSTY_GC_INDEX_TOMBSTONE) {
        return;
    }
    mask = (size_t)(osty_gc_index_capacity - 1);
    idx = osty_gc_index_hash(payload) & mask;
    void *slot;
    while ((slot = __atomic_load_n(&osty_gc_index_keys[idx],
                                   __ATOMIC_ACQUIRE)) != NULL) {
        if (slot == payload) {
            /* Atomic store the tombstone so a concurrent prober either
             * still sees the key (and returns the now-stale value) or
             * sees the tombstone (and probes past). The stale-value
             * window is bounded — caller of remove holds the GC lock,
             * and stale lookups during cycle are caught by the SATB
             * write barrier on the dead pointer. */
            __atomic_store_n(&osty_gc_index_values[idx], NULL,
                             __ATOMIC_RELAXED);
            __atomic_store_n(&osty_gc_index_keys[idx],
                             OSTY_GC_INDEX_TOMBSTONE,
                             __ATOMIC_RELEASE);
            osty_gc_index_count -= 1;
            osty_gc_index_tombstones += 1;
            return;
        }
        idx = (idx + 1) & mask;
    }
}

static void osty_gc_identity_grow(int64_t new_capacity) {
    osty_gc_identity_slot *old_slots = osty_gc_identity_slots;
    int64_t old_cap = osty_gc_identity_capacity;
    int64_t i;
    osty_gc_identity_slots = (osty_gc_identity_slot *)calloc(
        (size_t)new_capacity, sizeof(osty_gc_identity_slot));
    if (osty_gc_identity_slots == NULL) {
        osty_rt_abort("out of memory (gc identity index)");
    }
    osty_gc_identity_capacity = new_capacity;
    osty_gc_identity_count = 0;
    osty_gc_identity_tombstones = 0;
    if (old_slots != NULL) {
        for (i = 0; i < old_cap; i++) {
            uint64_t id = old_slots[i].stable_id;
            if (id != 0 && id != OSTY_GC_IDENTITY_TOMBSTONE) {
                osty_gc_identity_insert(id, old_slots[i].header);
            }
        }
        free(old_slots);
    }
}

static void osty_gc_identity_insert(uint64_t stable_id, osty_gc_header *header) {
    size_t mask;
    size_t idx;
    size_t first_tombstone = SIZE_MAX;

    if (stable_id == 0 || stable_id == OSTY_GC_IDENTITY_TOMBSTONE) {
        osty_rt_abort("invalid GC stable id");
    }
    if (osty_gc_identity_slots == NULL ||
        (osty_gc_identity_count + osty_gc_identity_tombstones + 1) * 4 >=
            osty_gc_identity_capacity * 3) {
        int64_t new_cap = osty_gc_identity_capacity == 0
                              ? 128
                              : osty_gc_identity_capacity * 2;
        osty_gc_identity_grow(new_cap);
    }
    mask = (size_t)(osty_gc_identity_capacity - 1);
    idx = osty_gc_identity_hash(stable_id) & mask;
    while (osty_gc_identity_slots[idx].stable_id != 0) {
        if (osty_gc_identity_slots[idx].stable_id ==
            OSTY_GC_IDENTITY_TOMBSTONE) {
            if (first_tombstone == SIZE_MAX) {
                first_tombstone = idx;
            }
        } else if (osty_gc_identity_slots[idx].stable_id == stable_id) {
            osty_gc_identity_slots[idx].header = header;
            return;
        }
        idx = (idx + 1) & mask;
    }
    if (first_tombstone != SIZE_MAX) {
        idx = first_tombstone;
        osty_gc_identity_tombstones -= 1;
    }
    osty_gc_identity_slots[idx].stable_id = stable_id;
    osty_gc_identity_slots[idx].header = header;
    osty_gc_identity_count += 1;
}

static osty_gc_header *osty_gc_identity_lookup(uint64_t stable_id) {
    size_t mask;
    size_t idx;
    osty_gc_header *header;

    if (stable_id == 0 || stable_id == OSTY_GC_IDENTITY_TOMBSTONE) {
        return NULL;
    }
    if (osty_gc_identity_slots != NULL) {
        mask = (size_t)(osty_gc_identity_capacity - 1);
        idx = osty_gc_identity_hash(stable_id) & mask;
        while (osty_gc_identity_slots[idx].stable_id != 0) {
            if (osty_gc_identity_slots[idx].stable_id == stable_id) {
                return osty_gc_identity_slots[idx].header;
            }
            idx = (idx + 1) & mask;
        }
    }
    /* Cold-path fallback: the alloc fast-path skips per-allocation
     * inserts into this table (it's only read by compaction + debug
     * paths in normal runs). When a lookup actually fires, walk the
     * young+old generation lists to find the matching header. The
     * walk is non-mutating — we don't cache into the hash so the
     * "identity table is a faithful mirror of the live heap" invariant
     * stays simple: the hash is empty unless something explicitly
     * populated it (compaction relocate, the hash-grow rehash). */
    for (header = osty_gc_young_head; header != NULL; header = header->next_gen) {
        if (header->stable_id == stable_id) {
            return header;
        }
    }
    for (header = osty_gc_old_head; header != NULL; header = header->next_gen) {
        if (header->stable_id == stable_id) {
            return header;
        }
    }
    return NULL;
}

static void osty_gc_identity_remove(uint64_t stable_id) {
    size_t mask;
    size_t idx;

    if (osty_gc_identity_slots == NULL || stable_id == 0 ||
        stable_id == OSTY_GC_IDENTITY_TOMBSTONE) {
        return;
    }
    mask = (size_t)(osty_gc_identity_capacity - 1);
    idx = osty_gc_identity_hash(stable_id) & mask;
    while (osty_gc_identity_slots[idx].stable_id != 0) {
        if (osty_gc_identity_slots[idx].stable_id == stable_id) {
            osty_gc_identity_slots[idx].stable_id =
                OSTY_GC_IDENTITY_TOMBSTONE;
            osty_gc_identity_slots[idx].header = NULL;
            osty_gc_identity_count -= 1;
            osty_gc_identity_tombstones += 1;
            return;
        }
        idx = (idx + 1) & mask;
    }
}

static void osty_gc_forwarding_grow(int64_t new_capacity) {
    osty_gc_forwarding_slot *old_slots = osty_gc_forwarding_slots;
    int64_t old_cap = osty_gc_forwarding_capacity;
    int64_t i;

    osty_gc_forwarding_slots = (osty_gc_forwarding_slot *)calloc(
        (size_t)new_capacity, sizeof(osty_gc_forwarding_slot));
    if (osty_gc_forwarding_slots == NULL) {
        osty_rt_abort("out of memory (gc forwarding table)");
    }
    osty_gc_forwarding_capacity = new_capacity;
    osty_gc_forwarding_count = 0;
    osty_gc_forwarding_tombstones = 0;
    if (old_slots != NULL) {
        for (i = 0; i < old_cap; i++) {
            void *from_payload = old_slots[i].from_payload;
            if (from_payload != NULL &&
                from_payload != OSTY_GC_FORWARDING_TOMBSTONE) {
                osty_gc_forwarding_insert(from_payload,
                                          old_slots[i].to_header);
            }
        }
        free(old_slots);
    }
}

static void osty_gc_forwarding_insert(void *from_payload,
                                      osty_gc_header *to_header) {
    size_t mask;
    size_t idx;
    size_t first_tombstone = SIZE_MAX;

    if (from_payload == NULL ||
        from_payload == OSTY_GC_FORWARDING_TOMBSTONE ||
        to_header == NULL) {
        osty_rt_abort("invalid GC forwarding entry");
    }
    if (osty_gc_forwarding_slots == NULL ||
        (osty_gc_forwarding_count + osty_gc_forwarding_tombstones + 1) * 4 >=
            osty_gc_forwarding_capacity * 3) {
        int64_t new_cap = osty_gc_forwarding_capacity == 0
                              ? 128
                              : osty_gc_forwarding_capacity * 2;
        osty_gc_forwarding_grow(new_cap);
    }
    mask = (size_t)(osty_gc_forwarding_capacity - 1);
    idx = osty_gc_forwarding_hash(from_payload) & mask;
    while (osty_gc_forwarding_slots[idx].from_payload != NULL) {
        if (osty_gc_forwarding_slots[idx].from_payload ==
            OSTY_GC_FORWARDING_TOMBSTONE) {
            if (first_tombstone == SIZE_MAX) {
                first_tombstone = idx;
            }
        } else if (osty_gc_forwarding_slots[idx].from_payload ==
                   from_payload) {
            osty_gc_forwarding_slots[idx].to_header = to_header;
            osty_gc_forwarding_slots[idx].stable_id = to_header->stable_id;
            osty_gc_forwarding_slots[idx].byte_size = to_header->byte_size;
            return;
        }
        idx = (idx + 1) & mask;
    }
    if (first_tombstone != SIZE_MAX) {
        idx = first_tombstone;
        osty_gc_forwarding_tombstones -= 1;
    }
    osty_gc_forwarding_slots[idx].from_payload = from_payload;
    osty_gc_forwarding_slots[idx].to_header = to_header;
    osty_gc_forwarding_slots[idx].stable_id = to_header->stable_id;
    osty_gc_forwarding_slots[idx].byte_size = to_header->byte_size;
    osty_gc_forwarding_count += 1;
}

static osty_gc_header *osty_gc_forwarding_lookup(void *from_payload) {
    size_t mask;
    size_t idx;

    if (osty_gc_forwarding_slots == NULL || from_payload == NULL ||
        from_payload == OSTY_GC_FORWARDING_TOMBSTONE) {
        return NULL;
    }
    mask = (size_t)(osty_gc_forwarding_capacity - 1);
    idx = osty_gc_forwarding_hash(from_payload) & mask;
    while (osty_gc_forwarding_slots[idx].from_payload != NULL) {
        if (osty_gc_forwarding_slots[idx].from_payload == from_payload) {
            return osty_gc_forwarding_slots[idx].to_header;
        }
        idx = (idx + 1) & mask;
    }
    return NULL;
}

static void osty_gc_forwarding_clear(void) {
    free(osty_gc_forwarding_slots);
    osty_gc_forwarding_slots = NULL;
    osty_gc_forwarding_capacity = 0;
    osty_gc_forwarding_count = 0;
    osty_gc_forwarding_tombstones = 0;
}

static osty_gc_forwarding_snapshot *osty_gc_forwarding_snapshot_take(
    int64_t *out_count) {
    osty_gc_forwarding_snapshot *snapshots = NULL;
    int64_t count = 0;
    int64_t i;

    if (out_count == NULL) {
        osty_rt_abort("null GC forwarding snapshot count");
    }
    if (osty_gc_forwarding_count > 0) {
        snapshots = (osty_gc_forwarding_snapshot *)calloc(
            (size_t)osty_gc_forwarding_count, sizeof(*snapshots));
        if (snapshots == NULL) {
            osty_rt_abort("out of memory (gc forwarding snapshot)");
        }
    }
    if (osty_gc_forwarding_slots != NULL) {
        for (i = 0; i < osty_gc_forwarding_capacity; i++) {
            void *from_payload = osty_gc_forwarding_slots[i].from_payload;
            if (from_payload != NULL &&
                from_payload != OSTY_GC_FORWARDING_TOMBSTONE) {
                snapshots[count].from_payload = from_payload;
                snapshots[count].stable_id =
                    osty_gc_forwarding_slots[i].stable_id;
                count += 1;
            }
        }
    }
    *out_count = count;
    return snapshots;
}

static void osty_gc_forwarding_retain_history(
    const osty_gc_forwarding_snapshot *snapshots, int64_t snapshot_count) {
    int64_t i;

    if (snapshots == NULL || snapshot_count <= 0) {
        return;
    }
    for (i = 0; i < snapshot_count; i++) {
        osty_gc_header *header =
            osty_gc_identity_lookup(snapshots[i].stable_id);
        if (header != NULL) {
            osty_gc_forwarding_insert(snapshots[i].from_payload, header);
        }
    }
}

static void *osty_gc_forward_payload(void *payload) {
    osty_gc_header *header;

    if (payload == NULL) {
        return NULL;
    }
    header = osty_gc_forwarding_lookup(payload);
    if (header != NULL) {
        return header->payload;
    }
    return payload;
}

static osty_gc_header *osty_gc_find_header_or_forwarded(void *payload) {
    osty_gc_header *header = osty_gc_find_header(payload);
    if (header != NULL) {
        return header;
    }
    return osty_gc_forwarding_lookup(payload);
}

/* Per-gen list helpers (Phase B2 depth). Both lists use `next_gen` /
 * `prev_gen` — a header is on exactly one gen list at a time. */
static void osty_gc_gen_list_prepend(osty_gc_header **head, osty_gc_header *header) {
    header->next_gen = *head;
    header->prev_gen = NULL;
    if (*head != NULL) {
        (*head)->prev_gen = header;
    }
    *head = header;
}

static void osty_gc_gen_list_remove(osty_gc_header **head, osty_gc_header *header) {
    if (header->prev_gen != NULL) {
        header->prev_gen->next_gen = header->next_gen;
    } else if (*head == header) {
        *head = header->next_gen;
    }
    if (header->next_gen != NULL) {
        header->next_gen->prev_gen = header->prev_gen;
    }
    header->next_gen = NULL;
    header->prev_gen = NULL;
}

static void osty_gc_link(osty_gc_header *header) {
    header->next = osty_gc_objects;
    header->prev = NULL;
    if (osty_gc_objects != NULL) {
        osty_gc_objects->prev = header;
    }
    osty_gc_objects = header;
    osty_gc_live_count += 1;
    osty_gc_live_bytes += header->byte_size;
    if (header->generation == OSTY_GC_GEN_YOUNG) {
        osty_gc_young_count += 1;
        osty_gc_young_bytes += header->byte_size;
        osty_gc_gen_list_prepend(&osty_gc_young_head, header);
    } else {
        osty_gc_old_count += 1;
        osty_gc_old_bytes += header->byte_size;
        osty_gc_gen_list_prepend(&osty_gc_old_head, header);
    }
    /* Phase 2 — Go-style arena allocator: arena-backed payloads are
     * resolvable via the `osty_gc_find_header` arena fast path
     * (range check + self-reference), so they don't need a hash
     * entry. The hash table now only carries:
     *   - humongous allocations (calloc'd outside the arena, no
     *     range coverage),
     *   - debug/test paths that bypass `osty_gc_link` and reach
     *     `osty_gc_index_insert` directly,
     *   - post-compaction inserts via `osty_gc_replace_header`
     *     (which currently still inserts; could be similarly gated
     *     by arena membership in a follow-up).
     *
     * Bump-allocated YOUNG / SURVIVOR / OLD / PINNED objects (the
     * overwhelming majority of allocations on real workloads) skip
     * the per-alloc hash insert entirely. log-aggregator's profile
     * had `osty_gc_index_insert` at ~50% of CPU after the cache
     * locality + fresh-key fast-path tightening of #878 / #879;
     * eliminating the call for arena payloads is what the previous
     * iterations were structurally building toward.
     *
     * Identity table is lazily populated; see `osty_gc_identity_lookup`. */
    if (!osty_gc_arena_contains(header->payload)) {
        osty_gc_index_insert_new(header->payload, header);
    }
}

static void osty_gc_unlink(osty_gc_header *header) {
    if (header->prev != NULL) {
        header->prev->next = header->next;
    } else {
        osty_gc_objects = header->next;
    }
    if (header->next != NULL) {
        header->next->prev = header->prev;
    }
    osty_gc_live_count -= 1;
    osty_gc_live_bytes -= header->byte_size;
    if (header->generation == OSTY_GC_GEN_YOUNG) {
        osty_gc_young_count -= 1;
        osty_gc_young_bytes -= header->byte_size;
        osty_gc_gen_list_remove(&osty_gc_young_head, header);
    } else {
        osty_gc_old_count -= 1;
        osty_gc_old_bytes -= header->byte_size;
        osty_gc_gen_list_remove(&osty_gc_old_head, header);
    }
    osty_gc_index_remove(header->payload);
    osty_gc_identity_remove(header->stable_id);
}

static bool osty_gc_header_is_pinned(const osty_gc_header *header) {
    return header != NULL &&
           (header->root_count > 0 || header->pin_count > 0);
}

static bool osty_gc_map_payload_is_movable(const osty_rt_map *map) {
    return map != NULL;
}

static bool osty_gc_chan_elem_kind_is_managed(int64_t elem_kind) {
    return elem_kind == OSTY_RT_ABI_PTR || elem_kind == OSTY_RT_ABI_STRING;
}

static bool osty_gc_header_is_movable(const osty_gc_header *header) {
    if (header == NULL || osty_gc_header_is_pinned(header)) {
        return false;
    }
    if (header->object_kind == OSTY_GC_KIND_MAP) {
        return osty_gc_map_payload_is_movable((const osty_rt_map *)header->payload);
    }
    if (header->object_kind == OSTY_GC_KIND_CHANNEL) {
        return true;
    }
    if (header->object_kind == OSTY_GC_KIND_LIST ||
        header->object_kind == OSTY_GC_KIND_STRING ||
        header->object_kind == OSTY_GC_KIND_SET ||
        header->object_kind == OSTY_GC_KIND_BYTES ||
        header->object_kind == OSTY_GC_KIND_CLOSURE_ENV) {
        return true;
    }
    /* GENERIC fallback: movable iff its pattern has no destroy
     * (NONE / ENUM_PTR — no external resource to free). */
    return osty_gc_generic_patterns[header->generic_pattern].destroy == NULL;
}

static void osty_gc_note_pin_acquire(osty_gc_header *header) {
    if (header != NULL && header->pin_count == 1) {
        osty_gc_pinned_count += 1;
        osty_gc_pinned_bytes += header->byte_size;
    }
}

static void osty_gc_note_pin_release(osty_gc_header *header) {
    if (header != NULL && header->pin_count == 0) {
        osty_gc_pinned_count -= 1;
        osty_gc_pinned_bytes -= header->byte_size;
    }
}

/* Phase B promotion: move `header` from YOUNG to OLD in place. Only the
 * bookkeeping counters change. Phase D may already have copied an
 * unpinned survivor into survivor/old bump storage before this runs, but
 * promotion itself still just relinks the chosen live header into OLD. */
static void osty_gc_promote_header(osty_gc_header *header) {
    if (header->generation == OSTY_GC_GEN_OLD) {
        return;
    }
    osty_gc_gen_list_remove(&osty_gc_young_head, header);
    osty_gc_young_count -= 1;
    osty_gc_young_bytes -= header->byte_size;
    osty_gc_old_count += 1;
    osty_gc_old_bytes += header->byte_size;
    header->generation = OSTY_GC_GEN_OLD;
    header->age = 0;
    /* Phase F: not auto-dirtied. The legacy edge-log path doesn't seed
     * cross-gen edges on promotion either — both modes carry the same
     * soundness corner (a YOUNG-allocated reference inside a promoted
     * owner that is never re-written before going stale). Auto-dirtying
     * here would balloon the dirty list with promoted-but-unwritten
     * owners and erase the win on alloc-heavy benches. */
    osty_gc_gen_list_prepend(&osty_gc_old_head, header);
    osty_gc_promoted_count_total += 1;
    osty_gc_promoted_bytes_total += header->byte_size;
}

static int64_t osty_gc_pressure_limit_now(void) {
    const char *value;
    char *end = NULL;
    long long parsed;

    if (osty_gc_pressure_limit_loaded) {
        return osty_gc_pressure_limit_bytes;
    }
    osty_gc_pressure_limit_loaded = true;
    value = getenv("OSTY_GC_THRESHOLD_BYTES");
    if (value == NULL || value[0] == '\0') {
        return osty_gc_pressure_limit_bytes;
    }
    parsed = strtoll(value, &end, 10);
    if (end == value || (end != NULL && *end != '\0') || parsed < 0) {
        osty_rt_abort("invalid OSTY_GC_THRESHOLD_BYTES");
    }
    osty_gc_pressure_limit_bytes = (int64_t)parsed;
    return osty_gc_pressure_limit_bytes;
}

/* Phase C3 assist-ratio accessor. Same lazy-env-init shape as the
 * other tuning knobs. A value of 0 disables mutator assist entirely;
 * positive means "drain one grey per N allocated bytes". */
static int64_t osty_gc_assist_bytes_per_unit_now(void) {
    const char *value;
    char *end = NULL;
    long long parsed;

    if (osty_gc_assist_bytes_loaded) {
        return osty_gc_assist_bytes_per_unit;
    }
    osty_gc_assist_bytes_loaded = true;
    value = getenv(OSTY_GC_ASSIST_BYTES_PER_UNIT_ENV);
    if (value == NULL || value[0] == '\0') {
        return osty_gc_assist_bytes_per_unit;
    }
    parsed = strtoll(value, &end, 10);
    if (end == value || (end != NULL && *end != '\0') || parsed < 0) {
        osty_rt_abort("invalid " OSTY_GC_ASSIST_BYTES_PER_UNIT_ENV);
    }
    osty_gc_assist_bytes_per_unit = (int64_t)parsed;
    return osty_gc_assist_bytes_per_unit;
}

/* Phase B pressure tier accessors. The nursery limit is read once from
 * `OSTY_GC_NURSERY_BYTES` on first query; an unset env keeps the
 * compiled-in default. A zero value disables automatic minor triggers.
 * Same lazy-init pattern as `osty_gc_pressure_limit_now`. */
static int64_t osty_gc_nursery_limit_now(void) {
    const char *value;
    char *end = NULL;
    long long parsed;

    if (osty_gc_nursery_limit_loaded) {
        return osty_gc_nursery_limit_bytes;
    }
    osty_gc_nursery_limit_loaded = true;
    value = getenv(OSTY_GC_NURSERY_LIMIT_ENV);
    if (value == NULL || value[0] == '\0') {
        return osty_gc_nursery_limit_bytes;
    }
    parsed = strtoll(value, &end, 10);
    if (end == value || (end != NULL && *end != '\0') || parsed < 0) {
        osty_rt_abort("invalid " OSTY_GC_NURSERY_LIMIT_ENV);
    }
    osty_gc_nursery_limit_bytes = (int64_t)parsed;
    return osty_gc_nursery_limit_bytes;
}

static int64_t osty_gc_promote_age_now(void) {
    const char *value;
    char *end = NULL;
    long long parsed;

    if (osty_gc_promote_age_loaded) {
        return osty_gc_promote_age;
    }
    osty_gc_promote_age_loaded = true;
    value = getenv(OSTY_GC_PROMOTE_AGE_ENV);
    if (value == NULL || value[0] == '\0') {
        return osty_gc_promote_age;
    }
    parsed = strtoll(value, &end, 10);
    if (end == value || (end != NULL && *end != '\0') || parsed < 0 || parsed > 255) {
        osty_rt_abort("invalid " OSTY_GC_PROMOTE_AGE_ENV);
    }
    osty_gc_promote_age = (int64_t)parsed;
    return osty_gc_promote_age;
}

static void osty_gc_card_marking_init_from_env(void) {
    const char *value;

    osty_gc_card_marking_loaded = true;
    value = getenv(OSTY_GC_CARD_MARKING_ENV);
    if (value == NULL || value[0] == '\0') {
        return;
    }
    if (strcmp(value, "0") == 0 || strcmp(value, "false") == 0) {
        osty_gc_card_marking_enabled = false;
    } else if (strcmp(value, "1") == 0 || strcmp(value, "true") == 0) {
        osty_gc_card_marking_enabled = true;
    } else {
        osty_rt_abort("invalid " OSTY_GC_CARD_MARKING_ENV " (use 0 or 1)");
    }
}

/* Cached env-var lookup. The hot path reduces to a load + branch after
 * the first call; the slow `getenv`/`strcmp` work happens exactly once.
 * Marked `OSTY_HOT_INLINE` so the post-write barrier inlines the cache
 * check rather than paying a call cycle per managed store. */
static OSTY_HOT_INLINE bool osty_gc_card_marking_now(void) {
    if (!osty_gc_card_marking_loaded) {
        osty_gc_card_marking_init_from_env();
    }
    return osty_gc_card_marking_enabled;
}

static size_t osty_gc_total_size_for_payload_size(size_t payload_size) {
    if (payload_size == 0) {
        payload_size = 1;
    }
    if (payload_size > SIZE_MAX - sizeof(osty_gc_header)) {
        osty_rt_abort("GC allocation overflow");
    }
    return sizeof(osty_gc_header) + payload_size;
}

static size_t osty_gc_header_total_size(const osty_gc_header *header) {
    if (header == NULL || header->byte_size <= 0) {
        osty_rt_abort("invalid GC header size");
    }
    if ((size_t)header->byte_size > SIZE_MAX - sizeof(osty_gc_header)) {
        osty_rt_abort("GC header size overflow");
    }
    return sizeof(osty_gc_header) + (size_t)header->byte_size;
}

static size_t osty_gc_align_up(size_t value, size_t align) {
    if (align == 0) {
        return value;
    }
    if (value > SIZE_MAX - (align - 1)) {
        osty_rt_abort("GC align overflow");
    }
    return ((value + align - 1) / align) * align;
}

static bool osty_gc_total_size_is_humongous(size_t total_size) {
    return total_size > (size_t)OSTY_GC_HUMONGOUS_THRESHOLD_BYTES;
}

static int64_t osty_gc_size_class_index_for_total_size(size_t total_size) {
    size_t aligned_size;

    if (total_size == 0 || osty_gc_total_size_is_humongous(total_size)) {
        return -1;
    }
    aligned_size =
        ((total_size + OSTY_GC_SIZE_CLASS_ALIGN - 1) /
         OSTY_GC_SIZE_CLASS_ALIGN) *
        OSTY_GC_SIZE_CLASS_ALIGN;
    if (aligned_size == 0 ||
        aligned_size > (size_t)OSTY_GC_HUMONGOUS_THRESHOLD_BYTES) {
        return -1;
    }
    return (int64_t)(aligned_size / OSTY_GC_SIZE_CLASS_ALIGN) - 1;
}

static bool osty_gc_bump_block_has_forwarding_alias(
    const osty_gc_bump_block *block) {
    int64_t i;

    if (block == NULL || osty_gc_forwarding_slots == NULL) {
        return false;
    }
    for (i = 0; i < osty_gc_forwarding_capacity; i++) {
        void *from_payload = osty_gc_forwarding_slots[i].from_payload;
        const unsigned char *addr;
        if (from_payload == NULL ||
            from_payload == OSTY_GC_FORWARDING_TOMBSTONE) {
            continue;
        }
        addr = (const unsigned char *)from_payload;
        if (addr >= block->data && addr < block->data + block->size) {
            return true;
        }
    }
    return false;
}

static bool osty_gc_old_bump_release_header(osty_gc_header *header);
static bool osty_gc_young_bump_release_header(osty_gc_header *header);
static bool osty_gc_survivor_bump_release_header(osty_gc_header *header);
static bool osty_gc_pinned_bump_release_header(osty_gc_header *header);
static void osty_gc_bump_recycle_empty_blocks(
    osty_gc_bump_block **blocks_head,
    osty_gc_bump_block **current,
    int64_t *block_count,
    int64_t *recycled_count_total,
    int64_t *recycled_bytes_total,
    bool clear_tlab_current);

static void osty_gc_free_list_push(osty_gc_header *header) {
    osty_gc_free_chunk *chunk;
    size_t total_size;
    int64_t class_index;

    if (header == NULL) {
        return;
    }
    total_size = osty_gc_header_total_size(header);
    if (total_size > (size_t)INT64_MAX) {
        osty_rt_abort("GC free-list size overflow");
    }
    class_index = osty_gc_size_class_index_for_total_size(total_size);
    if (class_index < 0) {
        osty_gc_humongous_swept_count_total += 1;
        osty_gc_humongous_swept_bytes_total += header->byte_size;
        free(header);
        return;
    }
    chunk = (osty_gc_free_chunk *)header;
    chunk->next = osty_gc_free_list_bins[class_index];
    chunk->total_size = total_size;
    chunk->storage_kind = header->storage_kind;
    osty_gc_free_list_bins[class_index] = chunk;
    osty_gc_free_list_count += 1;
    osty_gc_free_list_bytes += (int64_t)total_size;
}

static osty_gc_header *osty_gc_free_list_take(size_t total_size,
                                              size_t *out_chunk_size,
                                              uint8_t *out_storage_kind) {
    osty_gc_free_chunk *prev = NULL;
    osty_gc_free_chunk *chunk;
    int64_t class_index;

    if (out_chunk_size == NULL) {
        osty_rt_abort("null GC free-list size out-param");
    }
    if (out_storage_kind == NULL) {
        osty_rt_abort("null GC free-list storage-kind out-param");
    }
    class_index = osty_gc_size_class_index_for_total_size(total_size);
    if (class_index < 0) {
        *out_chunk_size = 0;
        *out_storage_kind = OSTY_GC_STORAGE_DIRECT;
        return NULL;
    }
    chunk = osty_gc_free_list_bins[class_index];
    while (chunk != NULL) {
        if (chunk->total_size >= total_size) {
            if (prev != NULL) {
                prev->next = chunk->next;
            } else {
                osty_gc_free_list_bins[class_index] = chunk->next;
            }
            osty_gc_free_list_count -= 1;
            osty_gc_free_list_bytes -= (int64_t)chunk->total_size;
            osty_gc_free_list_reused_count_total += 1;
            osty_gc_free_list_reused_bytes_total +=
                (int64_t)chunk->total_size;
            *out_chunk_size = chunk->total_size;
            *out_storage_kind = chunk->storage_kind;
            return (osty_gc_header *)chunk;
        }
        prev = chunk;
        chunk = chunk->next;
    }
    *out_chunk_size = 0;
    *out_storage_kind = OSTY_GC_STORAGE_DIRECT;
    return NULL;
}

/* Reclaim only sweep-dead headers. Compaction-replaced live headers keep
 * their old payload addresses in the forwarding table, so reusing them
 * would alias a stale logical identity to a new object. */
static void osty_gc_reclaim_swept_header(osty_gc_header *header) {
    if (osty_gc_young_bump_release_header(header)) {
        return;
    }
    if (osty_gc_survivor_bump_release_header(header)) {
        return;
    }
    if (osty_gc_old_bump_release_header(header)) {
        return;
    }
    if (osty_gc_pinned_bump_release_header(header)) {
        return;
    }
    osty_gc_free_list_push(header);
}

static void osty_gc_release_replaced_header(osty_gc_header *header) {
    if (header == NULL) {
        return;
    }
    /* Invalidate the self-reference before releasing: this header has
     * been replaced by an evacuation/compaction clone, but its bump
     * block stays alive (forwarding aliases pin the block) so the
     * `osty_gc_find_header` arena fast path would otherwise still
     * find this stale header on self-ref match — bypassing
     * `osty_gc_forward_payload` and returning the now-defunct
     * header. Zeroing payload makes the fast path fail-fast on stale
     * pointers, leaving the forwarding lookup as the canonical
     * resolver. */
    header->payload = NULL;
    if (osty_gc_young_bump_release_header(header)) {
        return;
    }
    if (osty_gc_survivor_bump_release_header(header)) {
        return;
    }
    if (osty_gc_old_bump_release_header(header)) {
        return;
    }
    if (osty_gc_pinned_bump_release_header(header)) {
        return;
    }
    if (header->storage_kind == OSTY_GC_STORAGE_DIRECT) {
        free(header);
    }
}

static osty_gc_bump_block *osty_gc_bump_block_new(
    size_t min_size,
    osty_gc_bump_block **blocks_head,
    osty_gc_bump_block **current,
    int64_t *count_total,
    int64_t *bytes_total) {
    osty_gc_bump_block *block;
    size_t block_size;
    void *arena_data;

    block_size = min_size > (size_t)OSTY_GC_BUMP_BLOCK_BYTES
                     ? min_size
                     : (size_t)OSTY_GC_BUMP_BLOCK_BYTES;
    if (block_size > SIZE_MAX - sizeof(*block) - OSTY_GC_SIZE_CLASS_ALIGN) {
        osty_rt_abort("GC bump block size overflow");
    }
    /* Header struct stays in the heap (small, ~48 B) so block
     * metadata isn't subject to the arena's "no reuse" policy.
     * The data buffer that holds the actual managed payloads is
     * suballocated from the arena — that's what the lookup fast
     * path range-checks. The arena_alloc returns a properly
     * aligned pointer that already satisfies size-class alignment
     * (we request OSTY_GC_SIZE_CLASS_ALIGN explicitly). */
    block = (osty_gc_bump_block *)calloc(1, sizeof(*block));
    if (block == NULL) {
        osty_rt_abort("out of memory (gc bump block header)");
    }
    arena_data = osty_gc_arena_alloc(block_size, OSTY_GC_SIZE_CLASS_ALIGN);
    if (arena_data != NULL) {
        /* Arena-backed: the data region's lifetime is tied to the
         * arena (not freed on block release). Recycling the block
         * leaks its virtual range until a free-list lands; for
         * benchmark / CLI workloads this is bounded. */
        block->data = (unsigned char *)arena_data;
        block->data_alloc = NULL;
    } else {
        /* Arena init failed or exhausted — fall back to heap. The
         * data region is owned by `data_alloc` (the unaligned
         * calloc'd base) so `osty_gc_bump_block_release` can free
         * it later. The lookup fast path won't help these blocks
         * (outside the arena range) but the hash-based lookup
         * still works for them. */
        size_t total_bytes = block_size + OSTY_GC_SIZE_CLASS_ALIGN;
        unsigned char *raw = (unsigned char *)calloc(1, total_bytes);
        uintptr_t aligned;
        if (raw == NULL) {
            free(block);
            osty_rt_abort("out of memory (gc bump block data)");
        }
        aligned = ((uintptr_t)raw + (uintptr_t)OSTY_GC_SIZE_CLASS_ALIGN - 1u) &
                  ~((uintptr_t)OSTY_GC_SIZE_CLASS_ALIGN - 1u);
        block->data = (unsigned char *)aligned;
        block->data_alloc = raw;
    }
    block->size = block_size;
    block->used = 0;
    block->next = *blocks_head;
    *blocks_head = block;
    *current = block;
    *count_total += 1;
    *bytes_total += (int64_t)block_size;
    return block;
}

static osty_gc_header *osty_gc_bump_take_from_region(
    size_t total_size,
    uint8_t storage_kind,
    osty_gc_bump_block **blocks_head,
    osty_gc_bump_block **current,
    int64_t *block_count_total,
    int64_t *block_bytes_total,
    int64_t *alloc_count_total,
    int64_t *alloc_bytes_total) {
    osty_gc_bump_block *block = *current;
    size_t aligned_size = osty_gc_align_up(total_size, OSTY_GC_SIZE_CLASS_ALIGN);
    size_t offset;
    osty_gc_header *header;

    if (osty_gc_total_size_is_humongous(total_size)) {
        return NULL;
    }
    if (block == NULL) {
        block = osty_gc_bump_block_new(aligned_size, blocks_head, current,
                                       block_count_total, block_bytes_total);
    }
    offset = osty_gc_align_up(block->used, OSTY_GC_SIZE_CLASS_ALIGN);
    if (offset > block->size || aligned_size > block->size - offset) {
        block = osty_gc_bump_block_new(aligned_size, blocks_head, current,
                                       block_count_total, block_bytes_total);
        offset = 0;
    }
    header = (osty_gc_header *)(void *)(block->data + offset);
    block->used = offset + aligned_size;
    block->live_alloc_count += 1;
    header->storage_kind = storage_kind;
    *alloc_count_total += 1;
    *alloc_bytes_total += (int64_t)aligned_size;
    return header;
}

static osty_gc_header *osty_gc_bump_take_from_tlab_region(
    size_t total_size,
    uint8_t storage_kind,
    osty_gc_bump_block **blocks_head,
    osty_gc_bump_block **shared_current,
    osty_gc_bump_block **tlab_current,
    int64_t *block_count_total,
    int64_t *block_bytes_total,
    int64_t *alloc_count_total,
    int64_t *alloc_bytes_total,
    int64_t *tlab_refill_count_total) {
    osty_gc_header *header = osty_gc_bump_take_from_region(
        total_size, storage_kind, blocks_head, tlab_current,
        block_count_total, block_bytes_total, alloc_count_total,
        alloc_bytes_total);

    if (header != NULL) {
        *shared_current = *tlab_current;
        if (header == (osty_gc_header *)(void *)(*tlab_current)->data) {
            *tlab_refill_count_total += 1;
        }
    }
    return header;
}

static osty_gc_header *osty_gc_tlab_take(size_t total_size) {
    return osty_gc_bump_take_from_tlab_region(
        total_size, OSTY_GC_STORAGE_BUMP_YOUNG, &osty_gc_bump_blocks,
        &osty_gc_bump_current, &osty_gc_tlab_current,
        &osty_gc_bump_block_count, &osty_gc_bump_block_bytes_total,
        &osty_gc_bump_alloc_count_total, &osty_gc_bump_alloc_bytes_total,
        &osty_gc_tlab_refill_count_total);
}

static osty_gc_header *osty_gc_survivor_tlab_take(size_t total_size) {
    return osty_gc_bump_take_from_tlab_region(
        total_size, OSTY_GC_STORAGE_BUMP_SURVIVOR,
        &osty_gc_survivor_bump_blocks, &osty_gc_survivor_bump_current,
        &osty_gc_survivor_tlab_current, &osty_gc_survivor_bump_block_count,
        &osty_gc_survivor_bump_block_bytes_total,
        &osty_gc_survivor_bump_alloc_count_total,
        &osty_gc_survivor_bump_alloc_bytes_total,
        &osty_gc_survivor_tlab_refill_count_total);
}

static osty_gc_header *osty_gc_old_tlab_take(size_t total_size) {
    return osty_gc_bump_take_from_tlab_region(
        total_size, OSTY_GC_STORAGE_BUMP_OLD, &osty_gc_old_bump_blocks,
        &osty_gc_old_bump_current, &osty_gc_old_tlab_current,
        &osty_gc_old_bump_block_count, &osty_gc_old_bump_block_bytes_total,
        &osty_gc_old_bump_alloc_count_total,
        &osty_gc_old_bump_alloc_bytes_total,
        &osty_gc_old_tlab_refill_count_total);
}

static osty_gc_header *osty_gc_pinned_tlab_take(size_t total_size) {
    return osty_gc_bump_take_from_tlab_region(
        total_size, OSTY_GC_STORAGE_BUMP_PINNED, &osty_gc_pinned_bump_blocks,
        &osty_gc_pinned_bump_current, &osty_gc_pinned_tlab_current,
        &osty_gc_pinned_bump_block_count,
        &osty_gc_pinned_bump_block_bytes_total,
        &osty_gc_pinned_bump_alloc_count_total,
        &osty_gc_pinned_bump_alloc_bytes_total,
        &osty_gc_pinned_tlab_refill_count_total);
}

static osty_gc_bump_block *osty_gc_bump_block_find_owner(
    osty_gc_bump_block *blocks_head, const void *ptr) {
    const unsigned char *addr = (const unsigned char *)ptr;
    osty_gc_bump_block *block = blocks_head;

    while (block != NULL) {
        const unsigned char *start = block->data;
        const unsigned char *end = block->data + block->size;
        if (addr >= start && addr < end) {
            return block;
        }
        block = block->next;
    }
    return NULL;
}

static void osty_gc_bump_block_release(
    osty_gc_bump_block *target,
    osty_gc_bump_block **blocks_head,
    osty_gc_bump_block **current,
    int64_t *block_count,
    int64_t *recycled_count_total,
    int64_t *recycled_bytes_total) {
    osty_gc_bump_block *prev = NULL;
    osty_gc_bump_block *block = *blocks_head;

    while (block != NULL && block != target) {
        prev = block;
        block = block->next;
    }
    if (block == NULL) {
        osty_rt_abort("GC bump block release target missing");
    }
    if (prev != NULL) {
        prev->next = block->next;
    } else {
        *blocks_head = block->next;
    }
    if (*current == block) {
        *current = *blocks_head;
    }
    *block_count -= 1;
    *recycled_count_total += 1;
    *recycled_bytes_total += (int64_t)block->size;
    /* Free the heap-fallback payload region if this block was
     * heap-backed (arena_init failed or arena exhausted at the time
     * of allocation). Arena-backed blocks have `data_alloc == NULL`;
     * their virtual range stays committed in the arena (no free-list
     * yet — see arena documentation). */
    if (block->data_alloc != NULL) {
        free(block->data_alloc);
    }
    free(block);
}

static bool osty_gc_bump_release_header_from_region(
    osty_gc_header *header,
    uint8_t storage_kind,
    osty_gc_bump_block **blocks_head,
    osty_gc_bump_block **current,
    int64_t *block_count,
    int64_t *recycled_count_total,
    int64_t *recycled_bytes_total,
    bool clear_tlab_current) {
    osty_gc_bump_block *block;

    if (header == NULL || header->storage_kind != storage_kind) {
        return false;
    }
    block = osty_gc_bump_block_find_owner(*blocks_head, header);
    if (block == NULL) {
        osty_rt_abort("GC bump owner missing");
    }
    if (block->live_alloc_count <= 0) {
        osty_rt_abort("GC bump live count underflow");
    }
    block->live_alloc_count -= 1;
    if (block->live_alloc_count == 0 &&
        !osty_gc_bump_block_has_forwarding_alias(block)) {
        if (clear_tlab_current) {
            if (storage_kind == OSTY_GC_STORAGE_BUMP_YOUNG &&
                osty_gc_tlab_current == block) {
                osty_gc_tlab_current = NULL;
            }
            if (storage_kind == OSTY_GC_STORAGE_BUMP_SURVIVOR &&
                osty_gc_survivor_tlab_current == block) {
                osty_gc_survivor_tlab_current = NULL;
            }
            if (storage_kind == OSTY_GC_STORAGE_BUMP_OLD &&
                osty_gc_old_tlab_current == block) {
                osty_gc_old_tlab_current = NULL;
            }
            if (storage_kind == OSTY_GC_STORAGE_BUMP_PINNED &&
                osty_gc_pinned_tlab_current == block) {
                osty_gc_pinned_tlab_current = NULL;
            }
        }
        osty_gc_bump_block_release(
            block, blocks_head, current, block_count, recycled_count_total,
            recycled_bytes_total);
    }
    return true;
}

static bool osty_gc_young_bump_release_header(osty_gc_header *header) {
    return osty_gc_bump_release_header_from_region(
        header, OSTY_GC_STORAGE_BUMP_YOUNG, &osty_gc_bump_blocks,
        &osty_gc_bump_current, &osty_gc_bump_block_count,
        &osty_gc_bump_recycled_block_count_total,
        &osty_gc_bump_recycled_bytes_total, true);
}

static bool osty_gc_survivor_bump_release_header(osty_gc_header *header) {
    return osty_gc_bump_release_header_from_region(
        header, OSTY_GC_STORAGE_BUMP_SURVIVOR, &osty_gc_survivor_bump_blocks,
        &osty_gc_survivor_bump_current, &osty_gc_survivor_bump_block_count,
        &osty_gc_survivor_bump_recycled_block_count_total,
        &osty_gc_survivor_bump_recycled_bytes_total, true);
}

static bool osty_gc_old_bump_release_header(osty_gc_header *header) {
    return osty_gc_bump_release_header_from_region(
        header, OSTY_GC_STORAGE_BUMP_OLD, &osty_gc_old_bump_blocks,
        &osty_gc_old_bump_current, &osty_gc_old_bump_block_count,
        &osty_gc_old_bump_recycled_block_count_total,
        &osty_gc_old_bump_recycled_bytes_total, true);
}

static bool osty_gc_pinned_bump_release_header(osty_gc_header *header) {
    return osty_gc_bump_release_header_from_region(
        header, OSTY_GC_STORAGE_BUMP_PINNED, &osty_gc_pinned_bump_blocks,
        &osty_gc_pinned_bump_current, &osty_gc_pinned_bump_block_count,
        &osty_gc_pinned_bump_recycled_block_count_total,
        &osty_gc_pinned_bump_recycled_bytes_total, true);
}

static void osty_gc_bump_recycle_empty_blocks(
    osty_gc_bump_block **blocks_head,
    osty_gc_bump_block **current,
    int64_t *block_count,
    int64_t *recycled_count_total,
    int64_t *recycled_bytes_total,
    bool clear_tlab_current) {
    osty_gc_bump_block *block = *blocks_head;

    while (block != NULL) {
        osty_gc_bump_block *next = block->next;
        if (block->live_alloc_count == 0 &&
            !osty_gc_bump_block_has_forwarding_alias(block)) {
            if (clear_tlab_current) {
                if (osty_gc_tlab_current == block) {
                    osty_gc_tlab_current = NULL;
                }
                if (osty_gc_survivor_tlab_current == block) {
                    osty_gc_survivor_tlab_current = NULL;
                }
                if (osty_gc_old_tlab_current == block) {
                    osty_gc_old_tlab_current = NULL;
                }
                if (osty_gc_pinned_tlab_current == block) {
                    osty_gc_pinned_tlab_current = NULL;
                }
            }
            osty_gc_bump_block_release(
                block, blocks_head, current, block_count,
                recycled_count_total, recycled_bytes_total);
        }
        block = next;
    }
}

/* Phase B pressure split: `allocated_since_collect` still tracks the
 * major-tier pressure (set by heap limit), while
 * `allocated_since_minor` tracks the finer nursery tier. Minor is
 * preferred — nursery overflow triggers a minor, and only heap-wide
 * overflow escalates to a major. */
static void osty_gc_note_allocation(size_t payload_size) {
    int64_t pressure_limit = osty_gc_pressure_limit_now();
    int64_t nursery_limit = osty_gc_nursery_limit_now();

    if (payload_size > (size_t)INT64_MAX) {
        osty_rt_abort("GC payload size overflow");
    }
    osty_gc_allocated_since_collect += (int64_t)payload_size;
    osty_gc_allocated_since_minor += (int64_t)payload_size;
    osty_gc_allocated_bytes_total += (int64_t)payload_size;
    if (pressure_limit > 0 &&
        (osty_gc_live_bytes >= pressure_limit ||
         osty_gc_allocated_since_collect >= pressure_limit)) {
        osty_gc_collection_requested = true;
    }
    if (nursery_limit > 0 &&
        (osty_gc_young_bytes >= nursery_limit ||
         osty_gc_allocated_since_minor >= nursery_limit)) {
        osty_gc_collection_requested = true;
    }
}

static void osty_gc_note_old_allocation(size_t payload_size) {
    int64_t pressure_limit = osty_gc_pressure_limit_now();

    if (payload_size > (size_t)INT64_MAX) {
        osty_rt_abort("GC payload size overflow");
    }
    osty_gc_allocated_since_collect += (int64_t)payload_size;
    osty_gc_allocated_bytes_total += (int64_t)payload_size;
    if (pressure_limit > 0 &&
        (osty_gc_live_bytes >= pressure_limit ||
         osty_gc_allocated_since_collect >= pressure_limit)) {
        osty_gc_collection_requested = true;
    }
}

/* Phase 3 + 5 of the tiny-tag young-space landing: feature flag, young
 * arena mmap region, micro-header layout, and the reconstruction shim
 * that lets `find_header` callers receive a populated `osty_gc_header *`
 * without storing one.
 *
 * Two regions live side by side: the existing `osty_gc_arena_*` keeps
 * holding headerful payloads for the OLD generation, struct boxes,
 * GENERIC objects, and anything ineligible for young (Phase 7 codifies
 * the eligibility rules). The young arena is a separate mmap range so
 * that `arena_is_young_page` is a 2-compare range check, mirroring the
 * existing arena's invariant. Phase 6 swaps mark-sweep on YOUNG for a
 * Cheney copy that uses these same pages as from-space + to-space.
 *
 * The flag stays opt-in (default off) until Phase 8 flips it; today
 * `osty_gc_allocate_managed` never picks the young path, so the only
 * exercise is via the debug entry points the tests use. */
/* Phase 8 step 2: default-on cutover. Cheney_minor traces OLD
 * reachability transitively (see `osty_gc_collect_minor_cheney_*`)
 * so `Map<String, _>` and other typed collections with young
 * children are correctly forwarded even when their write paths
 * (`osty_rt_map_insert_raw`, etc.) bypass `osty_gc_post_write_v1`.
 * Opt out with `OSTY_GC_TINYTAG_YOUNG=0`. */
#define OSTY_GC_TINYTAG_YOUNG_ENV "OSTY_GC_TINYTAG_YOUNG"
static bool osty_gc_tinytag_young_loaded = false;
static bool osty_gc_tinytag_young_enabled = true;

static bool osty_gc_tinytag_young_now(void) {
    const char *value;
    if (osty_gc_tinytag_young_loaded) {
        return osty_gc_tinytag_young_enabled;
    }
    osty_gc_tinytag_young_loaded = true;
    value = getenv(OSTY_GC_TINYTAG_YOUNG_ENV);
    /* Phase 8 step 2: env unset preserves the compile-time default
     * (currently true). Only an explicit "0"/"false" forces opt-out;
     * any other value forces opt-in. This keeps `OSTY_GC_TINYTAG_YOUNG=1`
     * as a no-op overlay and `OSTY_GC_TINYTAG_YOUNG=0` as the kill
     * switch. */
    if (value == NULL || value[0] == '\0') {
        return osty_gc_tinytag_young_enabled;
    }
    if (strcmp(value, "0") == 0 || strcmp(value, "false") == 0 ||
        strcmp(value, "FALSE") == 0) {
        osty_gc_tinytag_young_enabled = false;
    } else {
        osty_gc_tinytag_young_enabled = true;
    }
    return osty_gc_tinytag_young_enabled;
}

/* Young-space micro-header (16 bytes vs. the headerful 96).
 *
 * Layout choice: keep the kind + size in the first 8 bytes so any
 * trace/destroy dispatch can land cheaply (one cache line covers
 * micro-header + first 48 bytes of payload). `forward_or_meta` is the
 * Cheney mutation point — Phase 6 overwrites it with a forwarding tag
 * the moment a survivor copies out. Until then it carries packed mark
 * metadata.
 *
 * forward_or_meta layout:
 *   bits 0..1  — tag (UNFORWARDED / FORWARDED_TO_YOUNG / PROMOTED_TO_OLD)
 *   For tag == UNFORWARDED:
 *     bits 2..3   — color (WHITE / GREY / BLACK), matches OSTY_GC_COLOR_*
 *     bits 4..11  — age (u8)
 *     bits 12..63 — reserved (zero)
 *   For tag == FORWARDED_TO_YOUNG / PROMOTED_TO_OLD:
 *     bits 2..63 — destination payload pointer >> 2 (16-byte aligned, so
 *                  the low bits are guaranteed clear and we can stash
 *                  the tag inline without losing any address bits) */
typedef struct osty_gc_micro_header {
    int32_t object_kind;
    int32_t byte_size;
    uint64_t forward_or_meta;
} osty_gc_micro_header;

_Static_assert(sizeof(osty_gc_micro_header) == 16,
               "tiny-tag young micro-header must stay at 16 bytes");

/* Map's payload + micro-header must stay below the humongous
 * threshold so it remains young-arena eligible. The struct is
 * large (~900 B with the inline-index arrays + the embedded
 * pthread_mutex_t) and pthread_mutex_t size varies by platform —
 * a single new field could push it over and silently disable the
 * young path. This guard fails at compile time before that
 * happens. */
_Static_assert(sizeof(osty_rt_map) + sizeof(osty_gc_micro_header) <=
                   (size_t)OSTY_GC_HUMONGOUS_THRESHOLD_BYTES,
               "osty_rt_map outgrew the young arena humongous threshold");

enum {
    OSTY_GC_FORWARD_TAG_UNFORWARDED = 0,
    OSTY_GC_FORWARD_TAG_FORWARDED = 1,
    OSTY_GC_FORWARD_TAG_PROMOTED = 2,
};

#define OSTY_GC_FORWARD_TAG_MASK 0x3ull
#define OSTY_GC_FORWARD_ADDR_SHIFT 2

#define OSTY_GC_YOUNG_ARENA_BYTES ((size_t)1 << 28) /* 256 MiB virtual per semi-space */

/* Young space is a Cheney pair (Phase 6): every allocation goes into
 * `young_from`; minor collection copies live survivors into `young_to`
 * and then swaps the two via `osty_gc_cheney_swap_arenas`. The swap is
 * pointer-only — the underlying storage A/B stays put — so live
 * payload addresses move only when an object actually survives. */
typedef struct osty_gc_young_arena {
    unsigned char *base;
    unsigned char *end;
    unsigned char *cursor;
    bool init_failed;
} osty_gc_young_arena;

static osty_gc_young_arena osty_gc_young_from = {NULL, NULL, NULL, false};
static osty_gc_young_arena osty_gc_young_to = {NULL, NULL, NULL, false};
static int64_t osty_gc_young_alloc_count_total = 0;
static int64_t osty_gc_young_alloc_bytes_total = 0;
static int64_t osty_gc_young_cheney_forwarded_count_total = 0;
static int64_t osty_gc_young_cheney_forwarded_bytes_total = 0;
static int64_t osty_gc_young_cheney_swap_count_total = 0;

static void osty_gc_young_arena_init_one(osty_gc_young_arena *arena) {
    if (arena->base != NULL || arena->init_failed) {
        return;
    }
#if defined(_WIN32)
    void *base = NULL; /* TODO: Win32 VirtualAlloc reservation */
    if (base == NULL) {
        arena->init_failed = true;
        return;
    }
#else
    void *base = mmap(NULL, OSTY_GC_YOUNG_ARENA_BYTES,
                      PROT_READ | PROT_WRITE,
                      MAP_PRIVATE | OSTY_GC_MAP_ANON, -1, 0);
    if (base == MAP_FAILED) {
        arena->init_failed = true;
        return;
    }
#endif
    arena->base = (unsigned char *)base;
    arena->cursor = arena->base;
    arena->end = arena->base + OSTY_GC_YOUNG_ARENA_BYTES;
}

static void osty_gc_young_arena_init(void) {
    osty_gc_young_arena_init_one(&osty_gc_young_from);
    osty_gc_young_arena_init_one(&osty_gc_young_to);
}

/* "Live" predicates: address is currently bump-allocated (< cursor).
 * `from-page` is what cheney_forward uses to decide "is this a candidate
 * for me to copy?". `to-page` is the mirror used by debug helpers. */
static OSTY_HOT_INLINE bool osty_gc_arena_is_young_from_page(void *payload) {
    return (unsigned char *)payload >= osty_gc_young_from.base &&
           (unsigned char *)payload < osty_gc_young_from.cursor;
}

static OSTY_HOT_INLINE bool osty_gc_arena_is_young_to_page(void *payload) {
    return (unsigned char *)payload >= osty_gc_young_to.base &&
           (unsigned char *)payload < osty_gc_young_to.cursor;
}

/* "Within young mmap" — the address is somewhere in either semi-space's
 * reserved range, regardless of whether it's currently live (below
 * cursor) or stale (above cursor, e.g. after a swap reset the to-space
 * cursor). `find_header` uses this looser predicate so that a PROMOTED
 * micro-header sitting above the post-swap to-space cursor still gets
 * consulted: callers retain the original young pointer across cycles
 * and need pin/release to keep finding the OLD copy via the PROMOTED
 * forwarding tag. UNFORWARDED reads against above-cursor addresses are
 * filtered downstream in `reconstruct_young_header`. */
static OSTY_HOT_INLINE bool osty_gc_arena_within_young_mmap(void *payload) {
    return (((unsigned char *)payload >= osty_gc_young_from.base &&
             (unsigned char *)payload < osty_gc_young_from.end) ||
            ((unsigned char *)payload >= osty_gc_young_to.base &&
             (unsigned char *)payload < osty_gc_young_to.end));
}

static OSTY_HOT_INLINE bool osty_gc_arena_is_young_page(void *payload) {
    return osty_gc_arena_within_young_mmap(payload);
}

static inline osty_gc_micro_header *osty_gc_micro_header_for_payload(
    void *payload) {
    return (osty_gc_micro_header *)((unsigned char *)payload -
                                    sizeof(osty_gc_micro_header));
}

static uint64_t osty_gc_micro_pack_meta(uint8_t color, uint8_t age) {
    return OSTY_GC_FORWARD_TAG_UNFORWARDED |
           ((uint64_t)(color & 0x3) << 2) |
           ((uint64_t)age << 4);
}

static uint8_t osty_gc_micro_color(const osty_gc_micro_header *micro) {
    return (uint8_t)((micro->forward_or_meta >> 2) & 0x3);
}

static uint8_t osty_gc_micro_age(const osty_gc_micro_header *micro) {
    return (uint8_t)((micro->forward_or_meta >> 4) & 0xff);
}

/* 16-byte aligned bump within the named arena. Returns the slot start
 * (caller writes the micro-header into bytes [0..15) and treats
 * [16..16+payload_size) as the payload region). */
static void *osty_gc_young_arena_bump(osty_gc_young_arena *arena,
                                      size_t payload_size) {
    size_t total = sizeof(osty_gc_micro_header) + payload_size;
    void *slot;
    total = (total + 15u) & ~(size_t)15u;
    if (arena->cursor + total > arena->end) {
        return NULL;
    }
    slot = arena->cursor;
    arena->cursor += total;
    return slot;
}

/* Write a fresh micro-header at the bumped slot. Phase 6 step 1
 * separates this from the Cheney-side bump so the shared arithmetic
 * doesn't fork into two divergent copies. */
static void *osty_gc_allocate_young(size_t payload_size, int64_t object_kind) {
    void *slot;
    osty_gc_micro_header *micro;

    if (payload_size == 0 || payload_size > (size_t)INT32_MAX) {
        return NULL;
    }
    if (object_kind < INT32_MIN || object_kind > INT32_MAX) {
        return NULL;
    }
    if (osty_gc_young_from.base == NULL) {
        osty_gc_young_arena_init();
        if (osty_gc_young_from.base == NULL) {
            return NULL;
        }
    }
    slot = osty_gc_young_arena_bump(&osty_gc_young_from, payload_size);
    if (slot == NULL) {
        return NULL;
    }
    micro = (osty_gc_micro_header *)slot;
    micro->object_kind = (int32_t)object_kind;
    micro->byte_size = (int32_t)payload_size;
    micro->forward_or_meta =
        osty_gc_micro_pack_meta(OSTY_GC_COLOR_WHITE, 0);
    /* Zero the payload bytes to match the headerful allocator's
     * calloc / free-list-memset semantics. Cheney swap recycles
     * arena slots without zeroing, so without this fresh allocs
     * inherit the previous occupant's payload bytes — `osty_rt_list`
     * would see a garbage `len` / `elem_size` from the dead List
     * that lived at this offset, blow `ensure_layout` on first
     * push, etc. (Bisected from the property-generator subset
     * test failure.) */
    memset((unsigned char *)slot + sizeof(osty_gc_micro_header), 0,
           payload_size);
    osty_gc_young_alloc_count_total += 1;
    osty_gc_young_alloc_bytes_total += (int64_t)payload_size;
    /* Phase 8 step 1: feed the same nursery-pressure counter the
     * headerful path uses. The dispatcher's nursery_limit threshold
     * then triggers cheney_minor for young arena overflow exactly
     * the way it triggers in-place minor for headerful overflow. */
    osty_gc_note_allocation(payload_size);
    return (void *)((unsigned char *)slot + sizeof(osty_gc_micro_header));
}

/* Whether a (kind, payload_size, trace, destroy) tuple may live in
 * the no-header young arena.
 *
 * Rules (first failure short-circuits):
 *   1. Feature flag (`OSTY_GC_TINYTAG_YOUNG`) must be on.
 *   2. Kind eligibility:
 *        - For non-GENERIC kinds, look up `young_eligible` on the
 *          kind descriptor (`osty_gc_kind_table`). Each row carries
 *          its own bit; new young-eligible kinds set this true at
 *          their table row instead of editing this predicate.
 *        - For OSTY_GC_KIND_GENERIC, look up the matching pattern
 *          row in `osty_gc_generic_patterns` via (trace, destroy).
 *          Only the NONE pattern (NULL/NULL) lands here today —
 *          ENUM_PTR and TASK_HANDLE both have their own dedicated
 *          kinds (KIND_GENERIC_ENUM_PTR / KIND_GENERIC_TASK_HANDLE)
 *          so cheney can dispatch their trace/destroy via the
 *          kind-table descriptor.
 *   3. Payload size must fit comfortably under the humongous
 *      threshold. Humongous allocs already take a separate path in
 *      the headerful allocator and have no benefit from being
 *      bump-allocated.
 *
 * `trace` and `destroy` are passed through so the GENERIC arm can
 * resolve the per-instance pattern. For non-GENERIC kinds the pair
 * is determined by the kind table and the params are ignored.
 *
 * Why each currently-eligible kind is safe — see the kind-table
 * comment block and the matching cheney_forward / promote /
 * dead-from-space special cases for the per-kind safe-clone +
 * cleanup invariants. */
static bool osty_gc_young_eligible(int64_t object_kind, size_t payload_size,
                                   osty_gc_trace_fn trace,
                                   osty_gc_destroy_fn destroy) {
    if (!osty_gc_tinytag_young_now()) {
        return false;
    }
    if (object_kind == OSTY_GC_KIND_GENERIC) {
        uint8_t pattern = osty_gc_pattern_of(trace, destroy);
        if (!osty_gc_generic_patterns[pattern].young_eligible) {
            return false;
        }
    } else {
        const osty_gc_kind_descriptor *desc =
            osty_gc_kind_descriptor_lookup(object_kind);
        if (desc == NULL || !desc->young_eligible) {
            return false;
        }
    }
    if (payload_size == 0 ||
        payload_size > (size_t)OSTY_GC_HUMONGOUS_THRESHOLD_BYTES) {
        return false;
    }
    return true;
}

/* Public-facing entry that callers in `osty_gc_allocate_managed`
 * (Phase 8 cutover) consult before falling through to the headerful
 * path. Returns NULL when the kind/size combination is ineligible
 * or when the young arena is exhausted, signalling the caller to
 * use the headerful allocator.
 *
 * `trace` and `destroy` are forwarded to `osty_gc_young_eligible` so
 * the GENERIC-pattern filter can reject ENUM_PTR / TASK_HANDLE. */
static void *osty_gc_allocate_young_if_eligible(size_t byte_size,
                                                int64_t object_kind,
                                                osty_gc_trace_fn trace,
                                                osty_gc_destroy_fn destroy) {
    if (!osty_gc_young_eligible(object_kind, byte_size, trace, destroy)) {
        return NULL;
    }
    return osty_gc_allocate_young(byte_size, object_kind);
}

/* Forward decls for safe-clone helpers used by `cheney_forward`,
 * `promote_young_to_old`, and (Map only) the headerful compactor's
 * `osty_gc_clone_map_header_to_bump_region`. Bodies live with each
 * kind's runtime further down. */
static void osty_gc_map_safe_handoff(osty_rt_map *src, osty_rt_map *dst);
static void osty_gc_chan_safe_handoff(osty_rt_chan_impl *src,
                                      osty_rt_chan_impl *dst);
static void osty_gc_task_handle_safe_handoff(osty_rt_task_handle_impl *src,
                                             osty_rt_task_handle_impl *dst);

/* Phase 6 step 1: Cheney semi-space copy mechanic.
 *
 * Forward a from-space payload into to-space:
 *   - Already FORWARDED: return the cached to-space address (Cheney
 *     scans hit the same object many times via different roots; the
 *     forwarding tag makes those follow-up calls O(1)).
 *   - Not in from-space: pass through. Older OLD-arena pointers, SSO
 *     inline strings, or NULL flow back unchanged so callers can use
 *     this as the universal "rewrite a slot" helper without knowing
 *     anything about generations.
 *   - Otherwise: bump-allocate a fresh micro-header in to-space,
 *     memcpy the payload over, and stamp the FORWARDED tag plus
 *     to-space address into the from-side micro-header's
 *     `forward_or_meta`. The 16-byte arena alignment guarantees the
 *     low 2 bits of the to-payload are always zero, so we encode the
 *     tag inline without losing any address bits.
 *
 * Step 1 doesn't yet recurse into the payload's child slots — that's
 * step 2's job once the descriptor tracers learn the Cheney action.
 * The caller of step 1 is the test harness, exercising the pure
 * copy/tag mechanics in isolation. */
static void *osty_gc_cheney_forward(void *from_payload) {
    osty_gc_micro_header *src;
    uint64_t fom;
    uint64_t tag;
    size_t payload_size;
    int64_t object_kind;
    void *to_slot;
    osty_gc_micro_header *dst;
    void *to_payload;

    if (from_payload == NULL) {
        return NULL;
    }
    if (!osty_gc_arena_is_young_from_page(from_payload)) {
        return from_payload;
    }
    src = osty_gc_micro_header_for_payload(from_payload);
    fom = src->forward_or_meta;
    tag = fom & OSTY_GC_FORWARD_TAG_MASK;
    if (tag == OSTY_GC_FORWARD_TAG_FORWARDED ||
        tag == OSTY_GC_FORWARD_TAG_PROMOTED) {
        return (void *)(uintptr_t)(fom & ~OSTY_GC_FORWARD_TAG_MASK);
    }
    payload_size = (size_t)src->byte_size;
    object_kind = (int64_t)src->object_kind;
    if (osty_gc_young_to.base == NULL) {
        osty_gc_young_arena_init();
        if (osty_gc_young_to.base == NULL) {
            osty_rt_abort("cheney forward: young to-space init failed");
        }
    }
    to_slot = osty_gc_young_arena_bump(&osty_gc_young_to, payload_size);
    if (to_slot == NULL) {
        osty_rt_abort("cheney forward: young to-space exhausted");
    }
    dst = (osty_gc_micro_header *)to_slot;
    dst->object_kind = (int32_t)object_kind;
    dst->byte_size = (int32_t)payload_size;
    /* Survivors arrive at age 0 in to-space with the same color as the
     * source so the in-flight mark/scan loop continues correctly. The
     * caller (step 2's tracer) will repaint as needed. */
    dst->forward_or_meta =
        osty_gc_micro_pack_meta(osty_gc_micro_color(src),
                                osty_gc_micro_age(src));
    to_payload = (void *)((unsigned char *)to_slot +
                          sizeof(osty_gc_micro_header));
    memcpy(to_payload, from_payload, payload_size);
    /* Phase E follow-up: per-kind post-copy fixup for self-referential
     * payload pointers. `osty_rt_list` keeps a `data` pointer that
     * either points to a heap buffer (spilled) or to its OWN
     * `inline_storage[]` field. Memcpy preserves the pointer value
     * verbatim, so a forwarded list whose original was inline now
     * points at the SOURCE's inline_storage (in dead from-space)
     * instead of its own. Re-anchor it to the to-space copy's
     * inline_storage. Spilled lists' data pointers point at heap
     * buffers and stay valid (the buffer wasn't moved). */
    if (object_kind == OSTY_GC_KIND_LIST) {
        osty_rt_list *src_list = (osty_rt_list *)from_payload;
        osty_rt_list *dst_list = (osty_rt_list *)to_payload;
        if (dst_list->data == src_list->inline_storage) {
            dst_list->data = dst_list->inline_storage;
        }
    } else if (object_kind == OSTY_GC_KIND_MAP) {
        /* Bit-copying the embedded pthread_mutex_t is UB — see
         * osty_gc_map_safe_handoff for the destroy/re-init dance
         * and inline-index self-ref re-anchoring. */
        osty_gc_map_safe_handoff((osty_rt_map *)from_payload,
                                 (osty_rt_map *)to_payload);
    } else if (object_kind == OSTY_GC_KIND_CHANNEL) {
        osty_gc_chan_safe_handoff((osty_rt_chan_impl *)from_payload,
                                  (osty_rt_chan_impl *)to_payload);
    } else if (object_kind == OSTY_GC_KIND_GENERIC_TASK_HANDLE) {
        osty_gc_task_handle_safe_handoff(
            (osty_rt_task_handle_impl *)from_payload,
            (osty_rt_task_handle_impl *)to_payload);
    }
    src->forward_or_meta = (uint64_t)(uintptr_t)to_payload |
                           OSTY_GC_FORWARD_TAG_FORWARDED;
    osty_gc_young_cheney_forwarded_count_total += 1;
    osty_gc_young_cheney_forwarded_bytes_total += (int64_t)payload_size;
    return to_payload;
}

/* Pointer-only swap of from/to. The underlying mmap regions stay put;
 * the new from-space starts with whatever the old to-space accumulated,
 * and the new to-space's cursor resets to its base so the next minor
 * starts with a clean slate. Phase 6 step 3 calls this at the tail of
 * `osty_gc_collect_minor_cheney` once the scan finishes.
 *
 * Cache invalidation: the per-thread `osty_rt_string_cache` keys entries
 * by pointer address. Young arena addresses get reused across swaps —
 * an address P that held "8" before the swap may hold "22" after, and
 * the cache would otherwise return the stale len/hash. The cache lookup
 * compares `entry->value == value`, so any address reuse silently hits
 * the wrong entry. Clearing on swap keeps the cache correct without
 * tracking which entries are young (the alternative would mean a
 * per-entry "young" flag and a walk-and-clear pass; the unconditional
 * memset is simpler and fits in the swap's existing critical section). */
static void osty_gc_cheney_swap_arenas(void) {
    osty_gc_young_arena tmp = osty_gc_young_from;
    osty_gc_young_from = osty_gc_young_to;
    osty_gc_young_to = tmp;
    osty_gc_young_to.cursor = osty_gc_young_to.base;
    osty_rt_string_cache_invalidate_all();
    osty_gc_young_cheney_swap_count_total += 1;
}

/* Phase E follow-up: free external resources held by dead from-space
 * objects.
 *
 * Cheney's normal "reclamation = cursor reset" is fine for objects
 * whose only memory is the payload bytes themselves (STRING/BYTES /
 * GENERIC NONE / CLOSURE_ENV). `osty_rt_list`, however, may hold a
 * heap-allocated `data` buffer (when the list spilled past inline
 * storage) or a heap-allocated `gc_offsets` array (struct-element
 * lists). `osty_rt_set` always holds a heap-allocated `items`
 * buffer (no inline-storage union). Without an explicit destroy
 * pass these external buffers leak when their owning collection
 * becomes unreachable in young.
 *
 * The scan walks from-space from base to the recorded pre-swap
 * cursor. Forwarded objects (`FORWARDED` / `PROMOTED` tag) are
 * skipped — their external resources moved with them (the to-space
 * or OLD copy carries the same `items`/`data` pointer; freeing here
 * would double-free when that copy is later destroyed). Unforwarded
 * (UNFORWARDED tag) objects are dead; for kinds that own external
 * memory we run a per-kind cleanup before the swap reclaims the
 * arena bytes. */
static int64_t osty_gc_young_cheney_dead_lists_freed_total = 0;
static int64_t osty_gc_young_cheney_dead_sets_freed_total = 0;
static int64_t osty_gc_young_cheney_dead_maps_freed_total = 0;
static int64_t osty_gc_young_cheney_dead_chans_freed_total = 0;
static int64_t osty_gc_young_cheney_dead_task_handles_freed_total = 0;
static int64_t osty_gc_young_cheney_dead_buffer_bytes_freed_total = 0;

static size_t osty_gc_micro_object_total_size(int32_t payload_size);
static size_t osty_rt_kind_size(int64_t kind);

/* Per-kind cleanup helpers: free dead-young external buffers and
 * accumulate the per-kind freed-count + global buffer-bytes-freed
 * counters. LIST/SET inline the accounting their destroy fns
 * don't track; MAP delegates to `osty_rt_map_destroy` since the
 * Map destroy already covers every external buffer + the mutex. */
static void osty_gc_cleanup_young_dead_list(void *payload) {
    osty_rt_list *list = (osty_rt_list *)payload;
    if (list->gc_offsets != NULL) {
        free(list->gc_offsets);
    }
    if (list->data != NULL && list->data != list->inline_storage) {
        osty_gc_young_cheney_dead_buffer_bytes_freed_total +=
            (int64_t)((size_t)list->cap * list->elem_size);
        free(list->data);
    }
    osty_gc_young_cheney_dead_lists_freed_total += 1;
}

static void osty_gc_cleanup_young_dead_set(void *payload) {
    osty_rt_set *set = (osty_rt_set *)payload;
    if (set->items != NULL) {
        osty_gc_young_cheney_dead_buffer_bytes_freed_total +=
            (int64_t)((size_t)set->cap *
                      osty_rt_kind_size(set->elem_kind));
        free(set->items);
    }
    osty_gc_young_cheney_dead_sets_freed_total += 1;
}

static void osty_gc_cleanup_young_dead_map(void *payload) {
    osty_rt_map_destroy(payload);
    osty_gc_young_cheney_dead_maps_freed_total += 1;
}

static void osty_gc_cleanup_young_dead_chan(void *payload) {
    osty_rt_chan_destroy(payload);
    osty_gc_young_cheney_dead_chans_freed_total += 1;
}

static void osty_gc_cleanup_young_dead_task_handle(void *payload) {
    osty_rt_task_handle_destroy(payload);
    osty_gc_young_cheney_dead_task_handles_freed_total += 1;
}

static void osty_gc_cheney_destroy_dead_from_space(unsigned char *from_base,
                                                   unsigned char *from_end_cursor) {
    unsigned char *scan = from_base;
    while (scan < from_end_cursor) {
        osty_gc_micro_header *micro = (osty_gc_micro_header *)scan;
        int32_t payload_size = micro->byte_size;
        size_t total = osty_gc_micro_object_total_size(payload_size);
        uint64_t tag = micro->forward_or_meta & OSTY_GC_FORWARD_TAG_MASK;
        if (tag == OSTY_GC_FORWARD_TAG_UNFORWARDED) {
            const osty_gc_kind_descriptor *desc =
                osty_gc_kind_descriptor_lookup((int64_t)micro->object_kind);
            if (desc != NULL && desc->cleanup_young_dead != NULL) {
                void *payload = (void *)(scan + sizeof(osty_gc_micro_header));
                desc->cleanup_young_dead(payload);
            }
            /* Kinds with no cleanup_young_dead (STRING / BYTES /
             * CLOSURE_ENV / GENERIC NONE / GENERIC_ENUM_PTR) carry no
             * external buffers — payload bytes are reclaimed by the
             * cursor reset. */
        }
        scan += total;
    }
}

static void osty_gc_cheney_slot(void *slot_addr);

/* Phase 6 step 3: Cheney scan loop.
 *
 * After roots are forwarded into to-space, the to-space holds a queue
 * of grey objects to walk. Classical Cheney uses the to-space cursor
 * as the "alloc" pointer and a separate "scan" pointer that crawls
 * forward; every time scan reaches an object whose tracer pushes new
 * children into to-space, the cursor advances past them, growing the
 * queue the scan pointer eventually reaches. Termination: scan ==
 * cursor.
 *
 * Each tracer is invoked under CHENEY mode so its `mark_slot_v1`
 * calls do forward+rewrite (Phase 6 step 2). Tracers without a kind
 * descriptor entry (e.g. KIND_GENERIC) are no-ops here — Phase 7's
 * eligibility filter keeps GENERIC out of the young arena, so this
 * stays sound. */
static int64_t osty_gc_young_cheney_scanned_count_total = 0;

static size_t osty_gc_micro_object_total_size(int32_t payload_size) {
    size_t total = sizeof(osty_gc_micro_header) + (size_t)payload_size;
    return (total + 15u) & ~(size_t)15u;
}

static void osty_gc_cheney_scan_loop(unsigned char *scan_start) {
    unsigned char *scan = scan_start;
    osty_gc_trace_slot_mode previous_mode = osty_gc_trace_slot_mode_current;
    osty_gc_trace_slot_mode_current = OSTY_GC_TRACE_SLOT_MODE_CHENEY;
    while (scan < osty_gc_young_to.cursor) {
        osty_gc_micro_header *micro = (osty_gc_micro_header *)scan;
        int32_t payload_size = micro->byte_size;
        const osty_gc_kind_descriptor *desc =
            osty_gc_kind_descriptor_lookup((int64_t)micro->object_kind);
        if (desc != NULL && desc->trace != NULL) {
            void *payload =
                (void *)(scan + sizeof(osty_gc_micro_header));
            desc->trace(payload);
        }
        scan += osty_gc_micro_object_total_size(payload_size);
        osty_gc_young_cheney_scanned_count_total += 1;
    }
    osty_gc_trace_slot_mode_current = previous_mode;
}

/* Phase 7 step 2: pin / root-bind on a young payload promotes the
 * object out of the no-header arena into a regular headerful OLD
 * allocation. The micro-header keeps the PROMOTED tag pointing at
 * the new payload so later find_header calls (including unpin /
 * root_release) surface the OLD header. Returns the new payload
 * address; the caller should treat its original argument as stale. */
static void *osty_gc_allocate_managed_headerful(size_t byte_size,
                                                int64_t object_kind,
                                                const char *site,
                                                osty_gc_trace_fn trace,
                                                osty_gc_destroy_fn destroy);

static int64_t osty_gc_young_promoted_to_old_count_total = 0;
static int64_t osty_gc_young_promoted_to_old_bytes_total = 0;

static void *osty_gc_promote_young_to_old(void *young_payload) {
    osty_gc_micro_header *micro;
    uint64_t fom;
    uint64_t tag;
    int64_t object_kind;
    size_t payload_size;
    const osty_gc_kind_descriptor *desc;
    void *new_payload;

    if (!osty_gc_arena_is_young_page(young_payload)) {
        return young_payload;
    }
    micro = osty_gc_micro_header_for_payload(young_payload);
    fom = micro->forward_or_meta;
    tag = fom & OSTY_GC_FORWARD_TAG_MASK;
    if (tag == OSTY_GC_FORWARD_TAG_PROMOTED) {
        return (void *)(uintptr_t)(fom & ~OSTY_GC_FORWARD_TAG_MASK);
    }
    if (tag == OSTY_GC_FORWARD_TAG_FORWARDED) {
        /* Promoting a young payload mid-Cheney isn't a configuration
         * we support — pin/root happens during mutator execution, not
         * during the scan loop. The from-space original wouldn't carry
         * a FORWARDED tag unless a minor cycle ran concurrently, which
         * Phase 7 explicitly forbids. */
        osty_rt_abort("promote_young_to_old: payload already FORWARDED");
    }
    object_kind = (int64_t)micro->object_kind;
    payload_size = (size_t)micro->byte_size;
    desc = osty_gc_kind_descriptor_lookup(object_kind);
    osty_gc_trace_fn promote_trace;
    osty_gc_destroy_fn promote_destroy;
    if (desc != NULL) {
        promote_trace = desc->trace;
        promote_destroy = desc->destroy;
    } else if (object_kind == OSTY_GC_KIND_GENERIC) {
        /* GENERIC is not in `osty_gc_kind_table`; the eligibility
         * filter only admits the NONE pattern (NULL trace + NULL
         * destroy), so promotion lands a NONE-pattern OLD copy. */
        promote_trace = NULL;
        promote_destroy = NULL;
    } else {
        osty_rt_abort("promote_young_to_old: kind missing descriptor");
    }
    /* Bypass the Phase 8 young-routing wrapper: promotion exists
     * specifically to take this object OUT of the young arena, so
     * re-entering routing would loop straight back into a fresh
     * young alloc (and lose the pin/root_count we're trying to
     * make persistent). */
    new_payload = osty_gc_allocate_managed_headerful(
        payload_size, object_kind, "runtime.gc.promote_young",
        promote_trace, promote_destroy);
    memcpy(new_payload, young_payload, payload_size);
    /* Same self-referential `data` fixup as cheney_forward —
     * a promoted inline list still points its `data` at the source
     * payload's inline_storage. Re-anchor so destroy/grow paths see
     * the OLD copy's own inline region. */
    if (object_kind == OSTY_GC_KIND_LIST) {
        osty_rt_list *src_list = (osty_rt_list *)young_payload;
        osty_rt_list *dst_list = (osty_rt_list *)new_payload;
        if (dst_list->data == src_list->inline_storage) {
            dst_list->data = dst_list->inline_storage;
        }
    } else if (object_kind == OSTY_GC_KIND_MAP) {
        /* Same handoff as cheney_forward — see osty_gc_map_safe_handoff. */
        osty_gc_map_safe_handoff((osty_rt_map *)young_payload,
                                 (osty_rt_map *)new_payload);
    } else if (object_kind == OSTY_GC_KIND_CHANNEL) {
        osty_gc_chan_safe_handoff((osty_rt_chan_impl *)young_payload,
                                  (osty_rt_chan_impl *)new_payload);
    } else if (object_kind == OSTY_GC_KIND_GENERIC_TASK_HANDLE) {
        osty_gc_task_handle_safe_handoff(
            (osty_rt_task_handle_impl *)young_payload,
            (osty_rt_task_handle_impl *)new_payload);
    }
    micro->forward_or_meta = (uint64_t)(uintptr_t)new_payload |
                             OSTY_GC_FORWARD_TAG_PROMOTED;
    /* Phase E follow-up: register in Phase D's persistent forwarding
     * table too. The PROMOTED tag in the micro-header is fast (one
     * memory read per load) but ephemeral — cheney swaps eventually
     * recycle the from-space slot and overwrite the tag bytes with
     * fresh young allocations. The forwarding table survives swaps
     * and serves as the permanent fallback for callers that retain
     * the original young pointer indefinitely.
     *
     * Cost: bumps `osty_gc_forwarding_count`, switching `load_v1`
     * out of its zero-forward fast path for the rest of the
     * process. Acceptable trade-off for correctness — without this,
     * `osty_rt_list_cast` could resolve to a stale young address
     * once enough cheney cycles have passed, and the next
     * `ensure_layout` would see a corrupt elem_size. */
    {
        osty_gc_header *new_header = osty_gc_find_header(new_payload);
        if (new_header != NULL) {
            osty_gc_forwarding_insert(young_payload, new_header);
        }
    }
    osty_gc_young_promoted_to_old_count_total += 1;
    osty_gc_young_promoted_to_old_bytes_total += (int64_t)payload_size;
    return new_payload;
}

/* Phase E follow-up: transitive OLD-reachability stack.
 *
 * When cheney_slot encounters an OLD pointer (from a stack root, a
 * global root, or another OLD object's tracer), it pushes the OLD
 * header here. Drain runs each OLD's tracer in CHENEY mode so any
 * young children get forwarded + their slots rewritten. Without this
 * `Map<String, _>` would lose its young String keys after a minor
 * cycle: Map.insert bypasses `osty_gc_post_write_v1`, so the
 * remembered set is empty and the OLD Map is invisible to a Cheney
 * pass that only walks stack roots.
 *
 * Color BLACK marks "already enqueued or traced this cycle" so a
 * graph cycle (OLD A → OLD B → OLD A) doesn't loop forever. Reset
 * to WHITE at the end of cheney_minor; major mark/sweep does its
 * own colour bookkeeping and isn't allowed to overlap (existing
 * STW guard in `osty_gc_collect_major_with_stack_roots`). */
static osty_gc_header **osty_gc_cheney_old_stack = NULL;
static int64_t osty_gc_cheney_old_stack_cap = 0;
static int64_t osty_gc_cheney_old_stack_count = 0;
static int64_t osty_gc_cheney_old_traced_total = 0;

static void osty_gc_cheney_old_stack_grow(void) {
    int64_t new_cap = osty_gc_cheney_old_stack_cap == 0
                          ? 64
                          : osty_gc_cheney_old_stack_cap * 2;
    osty_gc_header **new_buf = (osty_gc_header **)realloc(
        osty_gc_cheney_old_stack,
        (size_t)new_cap * sizeof(osty_gc_header *));
    if (new_buf == NULL) {
        osty_rt_abort("out of memory (cheney old stack)");
    }
    osty_gc_cheney_old_stack = new_buf;
    osty_gc_cheney_old_stack_cap = new_cap;
}

/* Push a HEADERFUL header onto the cheney trace stack regardless of
 * generation. The young arena uses micro-headers (no `osty_gc_header`
 * shim is real), so any header that lands here is by definition a
 * headerful object that may transitively reach young arena children
 * via its tracer. Map<String, _>, List<String>, etc. — whether
 * pre-promotion (still in `osty_gc_young_head`) or post-promotion
 * (in `osty_gc_old_head`) — both qualify. */
static void osty_gc_cheney_old_stack_push(osty_gc_header *header) {
    if (header == NULL) {
        return;
    }
    /* Color BLACK = "in stack or already drained this cycle". */
    if (header->color == OSTY_GC_COLOR_BLACK) {
        return;
    }
    header->color = OSTY_GC_COLOR_BLACK;
    header->marked = true;
    if (osty_gc_cheney_old_stack_count >= osty_gc_cheney_old_stack_cap) {
        osty_gc_cheney_old_stack_grow();
    }
    osty_gc_cheney_old_stack[osty_gc_cheney_old_stack_count++] = header;
}

static void osty_gc_cheney_drain_old_stack(void) {
    while (osty_gc_cheney_old_stack_count > 0) {
        osty_gc_header *header =
            osty_gc_cheney_old_stack[--osty_gc_cheney_old_stack_count];
        const osty_gc_kind_descriptor *desc =
            osty_gc_kind_descriptor_lookup(header->object_kind);
        osty_gc_trace_fn fn =
            (desc != NULL)
                ? desc->trace
                : osty_gc_generic_patterns[header->generic_pattern].trace;
        if (fn != NULL) {
            fn(header->payload);
        }
        osty_gc_cheney_old_traced_total += 1;
    }
}

static void osty_gc_cheney_reset_old_colors(void) {
    osty_gc_header *header;
    for (header = osty_gc_young_head; header != NULL;
         header = header->next_gen) {
        if (header->color != OSTY_GC_COLOR_WHITE) {
            header->color = OSTY_GC_COLOR_WHITE;
            header->marked = false;
        }
    }
    for (header = osty_gc_old_head; header != NULL;
         header = header->next_gen) {
        if (header->color != OSTY_GC_COLOR_WHITE) {
            header->color = OSTY_GC_COLOR_WHITE;
            header->marked = false;
        }
    }
}

/* Phase E follow-up: cheney_minor with transitive OLD-reachability.
 *
 * Order of operations (CHENEY mode active throughout):
 *   1. Forward stack roots. Young → copied to to-space. OLD →
 *      enqueued for owner-tracing.
 *   2. Forward global roots (same dispatch).
 *   3. Drain the OLD-trace stack: each OLD owner's descriptor.trace
 *      runs, mark_slot_v1 in CHENEY mode forwards young children +
 *      rewrites slots, OLD children get pushed for their own trace.
 *   4. Cheney scan loop walks newly-forwarded young objects in
 *      to-space; their tracers may surface more OLD owners (rare
 *      since young objects rarely point back to OLD).
 *   5. Iterate 3+4 until both queues drain.
 *   6. Reset OLD colors so the next cycle starts clean.
 *   7. Swap arenas. */
static void osty_gc_collect_minor_cheney_with_stack_roots(
    void *const *root_slots, int64_t root_slot_count) {
    int64_t i;
    unsigned char *scan_start;
    osty_gc_trace_slot_mode previous_mode;

    if (osty_gc_young_to.base == NULL) {
        osty_gc_young_arena_init();
        if (osty_gc_young_to.base == NULL) {
            osty_rt_abort("cheney minor: to-space init failed");
        }
    }
    previous_mode = osty_gc_trace_slot_mode_current;
    osty_gc_trace_slot_mode_current = OSTY_GC_TRACE_SLOT_MODE_CHENEY;
    scan_start = osty_gc_young_to.cursor;
    /* Seed from the remembered set first: every (OLD owner, young
     * value) edge logged by `osty_gc_post_write_v1` (Map.insert,
     * Set.insert, List.push) gets its owner pushed for transitive
     * trace. Color BLACK dedups so each Map traces at most once
     * even if many edges point at it. */
    for (i = 0; i < osty_gc_remembered_edge_count; i++) {
        osty_gc_header *owner =
            osty_gc_find_header(osty_gc_remembered_edges[i].owner);
        if (owner != NULL) {
            osty_gc_cheney_old_stack_push(owner);
        }
    }
    /* Safety net: walk both generation lists and push any pinned
     * owner. Catches OLD-resident objects whose write paths don't
     * fire `post_write_v1` (most notably CLOSURE_ENV captures
     * populated via direct LLVM-emitted memcpy). The pinned filter
     * keeps the walk's overhead bounded to objects the user
     * explicitly retained. */
    {
        osty_gc_header *h;
        for (h = osty_gc_young_head; h != NULL; h = h->next_gen) {
            if (osty_gc_header_is_pinned(h)) {
                osty_gc_cheney_old_stack_push(h);
            }
        }
        for (h = osty_gc_old_head; h != NULL; h = h->next_gen) {
            if (osty_gc_header_is_pinned(h)) {
                osty_gc_cheney_old_stack_push(h);
            }
        }
    }
    /* Stack roots take the standard cheney_slot path: forward young,
     * enqueue OLD for owner-tracing if the slot's value is OLD-
     * resident. */
    for (i = 0; i < root_slot_count; i++) {
        osty_gc_cheney_slot((void *)root_slots[i]);
    }
    /* Walk the shadow-stack chain so caller-frame roots reach cheney
     * forwarding too — without this, an inner safepoint would forward
     * its own slots but leave the caller's pointers stale. */
    OSTY_GC_FOR_EACH_CHAIN_ROOT(osty_gc_cheney_slot(_slot));
    for (i = 0; i < osty_gc_global_root_count; i++) {
        osty_gc_cheney_slot(osty_gc_global_root_slots[i]);
    }
    /* Walk both grey queues until both are empty. Each pass might
     * uncover more work in the other. */
    do {
        osty_gc_cheney_drain_old_stack();
        unsigned char *prev_to_cursor = osty_gc_young_to.cursor;
        osty_gc_cheney_scan_loop(scan_start);
        scan_start = prev_to_cursor;
        /* If the scan loop forwarded new young objects whose tracers
         * touched OLD pointers, the OLD stack is non-empty again.
         * Continue. */
    } while (osty_gc_cheney_old_stack_count > 0 ||
             scan_start < osty_gc_young_to.cursor);
    osty_gc_cheney_reset_old_colors();
    osty_gc_trace_slot_mode_current = previous_mode;
    /* Free external buffers held by dead from-space objects (List
     * spilled `data` + `gc_offsets`) BEFORE swap reclaims the bytes. */
    osty_gc_cheney_destroy_dead_from_space(osty_gc_young_from.base,
                                           osty_gc_young_from.cursor);
    osty_gc_cheney_swap_arenas();
}

/* Per-thread scratch the shim uses to hand back an `osty_gc_header *`.
 * Read-only by contract: callers that mutate the shim corrupt nothing
 * (the next reconstruction overwrites the slot), but writes never make
 * it back to the micro-header. Phase 7 will gate write-touching paths
 * (link/unlink/pin/promote) on `arena_is_young_page` so they auto-lift
 * to a real headerful header before mutating. */
static __thread osty_gc_header osty_gc_young_shim;

static osty_gc_header *osty_gc_find_header(void *payload);

static osty_gc_header *osty_gc_reconstruct_young_header(void *payload) {
    osty_gc_micro_header *micro = osty_gc_micro_header_for_payload(payload);
    uint64_t fom = micro->forward_or_meta;
    uint64_t tag = fom & OSTY_GC_FORWARD_TAG_MASK;
    osty_gc_header *shim;

    /* Phase 7 step 2: PROMOTED young → OLD redirect. Pin / root-bind
     * on a young payload allocates a headerful copy in OLD, stamps
     * the PROMOTED tag here, and from that moment any find_header on
     * the (now-stale) young address has to surface the OLD header
     * so root_count / pin_count writes land on the real, persistent
     * fields rather than the read-only shim. */
    if (tag == OSTY_GC_FORWARD_TAG_PROMOTED) {
        void *promoted_payload =
            (void *)(uintptr_t)(fom & ~OSTY_GC_FORWARD_TAG_MASK);
        return osty_gc_find_header(promoted_payload);
    }
    /* FORWARDED tag means a Cheney cycle copied this payload into
     * to-space. Recurse to surface the to-space header. The to-space
     * payload is itself UNFORWARDED post-copy, so this terminates in
     * one extra hop. */
    if (tag == OSTY_GC_FORWARD_TAG_FORWARDED) {
        void *to_payload =
            (void *)(uintptr_t)(fom & ~OSTY_GC_FORWARD_TAG_MASK);
        return osty_gc_reconstruct_young_header(to_payload);
    }
    /* UNFORWARDED reads only valid for live young addresses (below the
     * appropriate semi-space cursor). After a cheney swap, dead from-
     * space contents land "above cursor" in the new to-space; those
     * micro-header bytes are stale (until next cycle's writes overwrite
     * them) and must NOT be reconstructed as a live header. The
     * PROMOTED/FORWARDED checks above already returned for any
     * still-relevant stale address; if we land here it's truly dead. */
    if (!osty_gc_arena_is_young_from_page(payload) &&
        !osty_gc_arena_is_young_to_page(payload)) {
        return NULL;
    }
    shim = &osty_gc_young_shim;
    /* Defensively zero everything the GC might read before populating
     * the live fields. Anything we don't set explicitly stays at the
     * "absent" sentinel (NULL pointers, zero counters). */
    memset(shim, 0, sizeof(*shim));
    shim->object_kind = (int64_t)micro->object_kind;
    shim->byte_size = (int64_t)micro->byte_size;
    shim->color = osty_gc_micro_color(micro);
    shim->marked = shim->color != OSTY_GC_COLOR_WHITE;
    shim->age = osty_gc_micro_age(micro);
    shim->generation = OSTY_GC_GEN_YOUNG;
    shim->storage_kind = OSTY_GC_STORAGE_BUMP_YOUNG;
    shim->payload = payload;
    /* trace / destroy stay NULL — `osty_gc_dispatch_trace` /
     * `osty_gc_dispatch_destroy` consult the kind descriptor table
     * first, which already knows the callbacks for every kind young
     * objects can hold (Phase 7 makes GENERIC ineligible for young so
     * this stays safe even after the descriptor lookup falls through). */
    return shim;
}

/* Existing arena fast path, extracted so Phase 5 can sit next to it as a
 * peer (`reconstruct_young_header`) instead of growing the dispatcher
 * site every time a new resolution path lands. The hash fallback stays
 * inline at the call site since it's the catch-all. */
static inline osty_gc_header *osty_gc_find_header_arena_path(void *payload) {
    if (!osty_gc_arena_contains(payload)) {
        return NULL;
    }
    osty_gc_header *candidate =
        (osty_gc_header *)((unsigned char *)payload - sizeof(osty_gc_header));
    if ((unsigned char *)candidate >= osty_gc_arena_base &&
        candidate->payload == payload) {
        return candidate;
    }
    return NULL;
}

static osty_gc_header *osty_gc_find_header(void *payload) {
    /* Resolution order:
     *   1. SSO tag — inline strings have no header at all.
     *   2. Young arena (Phase 5) — micro-header reconstruction.
     *   3. Headerful arena fast path — payload self-references its header.
     *   4. Hash fallback — humongous allocs + post-compaction inserts.
     *
     * The arena fast path computes the header via self-reference because
     * every alloc sets `header->payload = header + 1` during
     * `osty_gc_allocate_managed`. The back-pointer match guards against
     * a misaligned interior pointer falsely matching some object's
     * header. The linked list `osty_gc_objects` is purely iteration
     * order for mark seeding / sweep / validate; it isn't probed here. */
    osty_gc_index_find_ops_total += 1;
    if (osty_rt_string_is_inline((const char *)payload)) {
        return NULL;
    }
    if (osty_gc_arena_is_young_page(payload)) {
        return osty_gc_reconstruct_young_header(payload);
    }
    osty_gc_header *header = osty_gc_find_header_arena_path(payload);
    if (header != NULL) {
        return header;
    }
    return osty_gc_index_lookup(payload);
}

/* Parity gate for the per-kind descriptor table. For every kind that has a
 * fixed (trace, destroy) pair, asserts the alloc site passed exactly that
 * pair. GENERIC has no descriptor — its callbacks are genuinely
 * per-instance, and we let any pair through. Phase 2 will start reading the
 * descriptor instead of the header field; this gate is what makes that
 * cutover safe. */
static void osty_gc_kind_descriptor_assert_parity(int64_t object_kind,
                                                  osty_gc_trace_fn trace,
                                                  osty_gc_destroy_fn destroy) {
    const osty_gc_kind_descriptor *desc =
        osty_gc_kind_descriptor_lookup(object_kind);
    if (desc == NULL) {
        return;
    }
    if (desc->trace != trace || desc->destroy != destroy) {
        osty_rt_abort("GC kind descriptor parity violation");
    }
}

/* Headerful-only entry. Phase 8's `osty_gc_allocate_managed` wraps this
 * with young arena routing; the wrapper falls through here when the
 * kind/size combination isn't young-eligible OR when a caller (notably
 * `osty_gc_promote_young_to_old`) needs to bypass the young route to
 * avoid a routing loop. */
static void *osty_gc_allocate_managed_headerful(size_t byte_size,
                                                int64_t object_kind,
                                                const char *site,
                                                osty_gc_trace_fn trace,
                                                osty_gc_destroy_fn destroy) {
    osty_gc_header *header;
    size_t payload_size = byte_size;
    size_t total_size;
    size_t chunk_size = 0;
    bool humongous;
    bool reused = false;
    uint8_t storage_kind = OSTY_GC_STORAGE_DIRECT;
    bool single_threaded;

    osty_gc_kind_descriptor_assert_parity(object_kind, trace, destroy);

    total_size = osty_gc_total_size_for_payload_size(payload_size);
    payload_size = total_size - sizeof(osty_gc_header);
    humongous = osty_gc_total_size_is_humongous(total_size);
    header = NULL;

    /* Single-mutator skip for both lock cycles in this function. STW
     * collection means the mutator and GC never run at the same time,
     * so the recursive mutex is unnecessary on the hot alloc path when
     * no worker thread is live. Profile post-#870 had ~13% of CPU
     * sitting in `_pthread_mutex_firstfit_*` under
     * `osty_gc_allocate_managed` + `osty_gc_load_v1` combined.
     *
     * Phase 0a: also gated on bg-marker quiescence — the marker reads
     * the payload→header index and forwarding map concurrently while
     * tracing, so an unlocked allocator insert into either would race. */
    single_threaded = osty_gc_serialized_now();

    if (!humongous) {
        if (!single_threaded) osty_gc_acquire();
        header = osty_gc_free_list_take(total_size, &chunk_size, &storage_kind);
        if (header != NULL) {
            reused = true;
        } else {
            header = osty_gc_tlab_take(total_size);
            if (header != NULL) {
                storage_kind = OSTY_GC_STORAGE_BUMP_YOUNG;
            }
        }
        if (!single_threaded) osty_gc_release();
    }
    if (reused && header != NULL) {
        memset(header, 0, chunk_size);
    } else {
        if (header == NULL) {
            header = (osty_gc_header *)calloc(1, total_size);
            if (header == NULL) {
                osty_rt_abort("out of memory");
            }
        }
    }
    /* Parity gate already ran at function entry. */
    header->object_kind = object_kind;
    header->byte_size = (int64_t)payload_size;
    /* Phase 4: collapse per-instance (trace, destroy) into the
     * generic_pattern enum for GENERIC kinds. Non-GENERIC kinds
     * resolve through the kind descriptor table and ignore this
     * field — pass NONE so the field is well-defined either way. */
    if (object_kind == OSTY_GC_KIND_GENERIC) {
        header->generic_pattern = osty_gc_pattern_of(trace, destroy);
    } else {
        header->generic_pattern = OSTY_GC_GENERIC_NONE;
        (void)trace;
        (void)destroy;
    }
    /* `site` field removed — see osty_gc_header definition. The
     * parameter still flows in so call-site labels remain visible in
     * source, but isn't stored. */
    (void)site;
    header->payload = (void *)(header + 1);
    header->storage_kind = storage_kind;
    /* generation / age / color / marked are NOT explicitly stored:
     * the freshly-acquired chunk is zero-initialized regardless of
     * source — TLAB blocks come out of mmap'd arena pages (kernel-
     * zeroed on first touch), heap fallback uses calloc, and the
     * free-list path memsets the chunk to zero on reuse (line above).
     * The four values we'd write are all zero anyway:
     *   - OSTY_GC_GEN_YOUNG  = 0
     *   - age                = 0
     *   - OSTY_GC_COLOR_WHITE = 0
     *   - marked / false     = 0
     * Eliding the redundant stores trims four cache-line touches off
     * the allocate_managed hot path; on log-aggregator's 50K-alloc
     * × 100-iter stress run that's ~20M dropped writes.
     *
     * Phase B intent: every new allocation enters the nursery.
     * Promotion to OLD happens inside a minor collection after
     * `osty_gc_promote_age` survivals. Phase 0e: when a concurrent
     * cycle is in flight (MARK_INCREMENTAL or SWEEPING), the new
     * header is coloured BLACK so neither concurrent mark nor
     * concurrent sweep reclaims it before the mutator's store
     * publishes it to a stack/global slot. The next cycle starts
     * with this header reset to WHITE — sweep flips BLACK→WHITE on
     * every survivor at the end of the current cycle. */
    if (!single_threaded) osty_gc_acquire();
    if (humongous) {
        osty_gc_humongous_alloc_count_total += 1;
        osty_gc_humongous_alloc_bytes_total += (int64_t)payload_size;
    }
    header->stable_id = osty_gc_allocate_stable_id();
    osty_gc_link(header);
    osty_gc_note_allocation(payload_size);
    /* Phase 0e: alloc-as-BLACK during a cycle. SATB + concurrent
     * mark/sweep need this to keep just-allocated objects safe in
     * the window between alloc-return and the mutator's first
     * store-then-safepoint sequence. Without it, the bg marker /
     * sweep could free a header whose payload is currently only
     * referenced from a register or a not-yet-published slot. */
    if (osty_gc_state == OSTY_GC_STATE_MARK_INCREMENTAL ||
        osty_gc_state == OSTY_GC_STATE_SWEEPING) {
        /* Phase 0g: atomic store. Today the alloc path holds the GC
         * lock so concurrent readers can't observe a torn color, but
         * once Phase 0h lets the tracer run lock-free this RELEASE
         * pairs with the tracer's ACQUIRE in `mark_header` so a
         * tracer that finds this header via index probe sees BLACK
         * (and skips it) consistently with the marker_count store. */
        __atomic_store_n(&header->color, OSTY_GC_COLOR_BLACK,
                         __ATOMIC_RELEASE);
        __atomic_store_n(&header->marked, true, __ATOMIC_RELAXED);
    }
    /* Phase C3 mutator assist: if an incremental major is active,
     * borrow a proportional amount of mark work from the allocator
     * so the mutator literally pays for its allocation pressure. */
    if (osty_gc_state == OSTY_GC_STATE_MARK_INCREMENTAL) {
        int64_t bpu = osty_gc_assist_bytes_per_unit_now();
        if (bpu > 0) {
            int64_t units = (int64_t)payload_size / bpu;
            if (units < 1) {
                units = 1;
            }
            int64_t done = osty_gc_mark_drain_budget(units);
            osty_gc_mutator_assist_work_total += done;
            osty_gc_mutator_assist_calls_total += 1;
        }
    }
    if (!single_threaded) osty_gc_release();
    return header->payload;
}

/* Phase 8 step 1: production allocator entry. Eligible kinds (Phase 7
 * step 1) take the no-header young arena; everything else falls
 * through to the headerful path. The wrapper stays a thin shim so
 * call sites that explicitly want the headerful behaviour
 * (`promote_young_to_old`) can call `_headerful` directly without
 * re-entering the routing logic and creating a feedback loop. */
static void *osty_gc_allocate_managed(size_t byte_size, int64_t object_kind,
                                      const char *site,
                                      osty_gc_trace_fn trace,
                                      osty_gc_destroy_fn destroy) {
    void *young_payload;

    osty_gc_kind_descriptor_assert_parity(object_kind, trace, destroy);
    young_payload = osty_gc_allocate_young_if_eligible(byte_size, object_kind,
                                                       trace, destroy);
    if (young_payload != NULL) {
        (void)site;
        return young_payload;
    }
    return osty_gc_allocate_managed_headerful(byte_size, object_kind, site,
                                              trace, destroy);
}

static void *osty_gc_allocate_pinned_managed(size_t byte_size,
                                             int64_t object_kind,
                                             const char *site,
                                             osty_gc_trace_fn trace,
                                             osty_gc_destroy_fn destroy) {
    osty_gc_header *header;
    size_t payload_size = byte_size;
    size_t total_size;
    bool humongous;
    uint8_t storage_kind = OSTY_GC_STORAGE_DIRECT;

    total_size = osty_gc_total_size_for_payload_size(payload_size);
    payload_size = total_size - sizeof(osty_gc_header);
    humongous = osty_gc_total_size_is_humongous(total_size);
    header = NULL;
    if (!humongous) {
        osty_gc_acquire();
        header = osty_gc_pinned_tlab_take(total_size);
        if (header != NULL) {
            storage_kind = OSTY_GC_STORAGE_BUMP_PINNED;
        }
        osty_gc_release();
    }
    if (header == NULL) {
        header = (osty_gc_header *)calloc(1, total_size);
        if (header == NULL) {
            osty_rt_abort("out of memory");
        }
    }
    osty_gc_kind_descriptor_assert_parity(object_kind, trace, destroy);
    header->object_kind = object_kind;
    header->byte_size = (int64_t)payload_size;
    /* Phase 4: same generic_pattern handoff as the regular alloc path. */
    if (object_kind == OSTY_GC_KIND_GENERIC) {
        header->generic_pattern = osty_gc_pattern_of(trace, destroy);
    } else {
        header->generic_pattern = OSTY_GC_GENERIC_NONE;
        (void)trace;
        (void)destroy;
    }
    /* `site` field removed — see osty_gc_header definition. The
     * parameter still flows in so call-site labels remain visible in
     * source, but isn't stored. */
    (void)site;
    header->payload = (void *)(header + 1);
    header->storage_kind = storage_kind;
    header->generation = OSTY_GC_GEN_OLD;
    header->age = 0;
    header->pin_count = 1;
    header->color = OSTY_GC_COLOR_WHITE;
    header->marked = false;
    osty_gc_acquire();
    if (humongous) {
        osty_gc_humongous_alloc_count_total += 1;
        osty_gc_humongous_alloc_bytes_total += (int64_t)payload_size;
    }
    header->stable_id = osty_gc_allocate_stable_id();
    osty_gc_link(header);
    osty_gc_note_pin_acquire(header);
    osty_gc_note_old_allocation(payload_size);
    if (osty_gc_state == OSTY_GC_STATE_MARK_INCREMENTAL) {
        int64_t bpu = osty_gc_assist_bytes_per_unit_now();
        if (bpu > 0) {
            int64_t units = (int64_t)payload_size / bpu;
            if (units < 1) {
                units = 1;
            }
            int64_t done = osty_gc_mark_drain_budget(units);
            osty_gc_mutator_assist_work_total += done;
            osty_gc_mutator_assist_calls_total += 1;
        }
    }
    osty_gc_release();
    return header->payload;
}

static void osty_gc_mark_payload(void *payload);
static void osty_rt_chan_sync_init_or_abort(osty_rt_chan_impl *ch,
                                            const char *op);
static void osty_rt_chan_ensure_elem_kind(osty_rt_chan_impl *ch,
                                          int64_t elem_kind,
                                          const char *op);
bool osty_rt_strings_Equal(const char *left, const char *right);
int64_t osty_rt_strings_Compare(const char *left, const char *right);
int64_t osty_rt_strings_Count(const char *value, const char *substr);
int64_t osty_rt_strings_IndexOf(const char *value, const char *substr);
bool osty_rt_strings_Contains(const char *value, const char *substr);
bool osty_rt_strings_HasSuffix(const char *value, const char *suffix);
const char *osty_rt_strings_Join(void *raw_parts, const char *sep);
const char *osty_rt_strings_Repeat(const char *value, int64_t n);
const char *osty_rt_strings_Replace(const char *value, const char *old, const char *new_value);
const char *osty_rt_strings_ReplaceAll(const char *value, const char *old, const char *new_value);
const char *osty_rt_strings_Slice(const char *value, int64_t start, int64_t end);
const char *osty_rt_strings_ToUpper(const char *value);
const char *osty_rt_strings_ToLower(const char *value);
bool osty_rt_strings_IsValidInt(const char *value);
int64_t osty_rt_strings_ToInt(const char *value);
bool osty_rt_strings_IsValidFloat(const char *value);
double osty_rt_strings_ToFloat(const char *value);
const char *osty_rt_strings_TrimStart(const char *value);
const char *osty_rt_strings_TrimEnd(const char *value);
const char *osty_rt_strings_TrimPrefix(const char *value, const char *prefix);
const char *osty_rt_strings_TrimSuffix(const char *value, const char *suffix);
const char *osty_rt_strings_TrimSpace(const char *value);
void *osty_rt_strings_ToBytes(const char *value);
void *osty_rt_strings_Chars(const char *value);
void *osty_rt_strings_Bytes(const char *value);
void *osty_rt_bytes_from_list(void *raw_list);
void *osty_rt_bytes_concat(void *raw_left, void *raw_right);
void *osty_rt_bytes_repeat(void *raw_bytes, int64_t n);
int64_t osty_rt_bytes_index_of(void *raw_bytes, void *raw_sub);
int64_t osty_rt_bytes_last_index_of(void *raw_bytes, void *raw_sub);
void *osty_rt_bytes_split(void *raw_bytes, void *raw_sep);
void *osty_rt_bytes_join(void *raw_parts, void *raw_sep);
void *osty_rt_bytes_replace(void *raw_bytes, void *raw_old, void *raw_new_value);
void *osty_rt_bytes_replace_all(void *raw_bytes, void *raw_old, void *raw_new_value);
void *osty_rt_bytes_trim_left(void *raw_bytes, void *raw_strip);
void *osty_rt_bytes_trim_right(void *raw_bytes, void *raw_strip);
void *osty_rt_bytes_trim(void *raw_bytes, void *raw_strip);
void *osty_rt_bytes_trim_space(void *raw_bytes);
void *osty_rt_bytes_to_upper(void *raw_bytes);
void *osty_rt_bytes_to_lower(void *raw_bytes);
const char *osty_rt_bytes_to_hex(void *raw_bytes);
bool osty_rt_bytes_is_valid_hex(const char *value);
void *osty_rt_bytes_from_hex(const char *value);
bool osty_rt_bytes_is_valid_utf8(void *raw_bytes);
const char *osty_rt_bytes_to_string(void *raw_bytes);
uint8_t osty_rt_bytes_get(void *raw_bytes, int64_t index);
void *osty_rt_bytes_slice(void *raw_bytes, int64_t start, int64_t end);
void *osty_rt_compress_gzip_encode(void *raw_bytes);
void *osty_rt_compress_gzip_decode(void *raw_bytes);
void *osty_rt_crypto_sha256(void *raw_data);
void *osty_rt_crypto_sha512(void *raw_data);
void *osty_rt_crypto_sha1(void *raw_data);
void *osty_rt_crypto_md5(void *raw_data);
void *osty_rt_crypto_hmac_sha256(void *raw_key, void *raw_message);
void *osty_rt_crypto_hmac_sha512(void *raw_key, void *raw_message);
void *osty_rt_crypto_random_bytes(int64_t n);
bool osty_rt_crypto_constant_time_eq(void *raw_a, void *raw_b);
bool osty_rt_set_insert_i64(void *raw_set, int64_t item);
bool osty_rt_set_insert_i1(void *raw_set, bool item);
bool osty_rt_set_insert_f64(void *raw_set, double item);
bool osty_rt_set_insert_ptr(void *raw_set, void *item);
bool osty_rt_set_insert_string(void *raw_set, const char *item);

static void osty_rt_list_trace(void *payload) {
    osty_rt_list *list = (osty_rt_list *)payload;
    int64_t i;
    int64_t j;

    if (list == NULL || list->data == NULL) {
        return;
    }
    if (list->trace_elem != NULL) {
        for (i = 0; i < list->len; i++) {
            list->trace_elem((void *)(list->data + ((size_t)i * list->elem_size)));
        }
        return;
    }
    if (list->gc_offset_count <= 0 || list->gc_offsets == NULL) {
        return;
    }
    /* Pass slot addresses to mark_slot_v1 so cheney can forward
     * young children + rewrite the slot. mark_payload would only
     * mark via the headerful path — broken under CHENEY mode. */
    for (i = 0; i < list->len; i++) {
        unsigned char *elem = list->data + ((size_t)i * list->elem_size);
        for (j = 0; j < list->gc_offset_count; j++) {
            osty_gc_mark_slot_v1((void *)(elem + (size_t)list->gc_offsets[j]));
        }
    }
}

static void osty_rt_list_destroy(void *payload) {
    osty_rt_list *list = (osty_rt_list *)payload;
    if (list != NULL) {
        free(list->gc_offsets);
        /* `inline_storage` is part of the managed allocation, not a
         * separate malloc, so only free `data` if it was spilled
         * to heap. */
        if (list->data != list->inline_storage) {
            free(list->data);
        }
    }
}

static void osty_rt_map_mutex_init_or_abort(osty_rt_map *map) {
    if (map == NULL) {
        osty_rt_abort("map lock: null map");
    }
    if (osty_rt_rmu_init(&map->mu) != 0) {
        osty_rt_abort("map lock: mutex_init failed");
    }
    map->mu_init = 1;
}

/* Move Map ownership from `src` to `dst` after a bit-copy of the
 * payload bytes. Used by cheney_forward (young → young) and
 * promote_young_to_old (young → OLD). Constraints handled here:
 *   - The embedded pthread_mutex_t can't be byte-copied (the kernel
 *     state is keyed on the mutex address). Init a fresh mutex on
 *     `dst` and destroy `src`'s.
 *   - `index_slots` / `index_hashes` may alias the inline arrays
 *     (`index_inline[]` / `index_hashes_inline[]`). After memcpy,
 *     `dst`'s aliased pointers still point at `src`'s inline region;
 *     re-anchor to `dst`'s own.
 *   - Null `src`'s owned-buffer pointers so the from-space copy
 *     can't be reached by any later traversal that would double-free
 *     buffers `dst` now owns.
 *
 * The headerful compactor's `osty_gc_clone_map_header_to_bump_region`
 * does field-by-field copy (not memcpy) and relies on `src`'s
 * destroy freeing its non-stolen index buffers — so it can't share
 * this helper without changing those semantics. */
static void osty_gc_map_safe_handoff(osty_rt_map *src, osty_rt_map *dst) {
    if (dst->index_slots == src->index_inline) {
        dst->index_slots = dst->index_inline;
    }
    if (dst->index_hashes == src->index_hashes_inline) {
        dst->index_hashes = dst->index_hashes_inline;
    }
    if (src->mu_init) {
        osty_rt_map_mutex_init_or_abort(dst);
        osty_rt_rmu_destroy(&src->mu);
        src->mu_init = 0;
    }
    src->keys = NULL;
    src->values = NULL;
    src->index_slots = NULL;
    src->index_hashes = NULL;
}

/* Move Channel ownership from `src` to `dst` after a bit-copy.
 * Same shape as osty_gc_map_safe_handoff but with three sync
 * primitives (mutex + 2 condvars) instead of one. The slots
 * pointer is heap-allocated (no inline-storage union to fix up). */
static void osty_gc_chan_safe_handoff(osty_rt_chan_impl *src,
                                      osty_rt_chan_impl *dst) {
    if (src->sync_init) {
        if (osty_rt_mu_init(&dst->mu) != 0 ||
            osty_rt_cond_init(&dst->not_full) != 0 ||
            osty_rt_cond_init(&dst->not_empty) != 0) {
            osty_rt_abort("chan safe-handoff: sync init failed");
        }
        dst->sync_init = 1;
        osty_rt_mu_destroy(&src->mu);
        osty_rt_cond_destroy(&src->not_full);
        osty_rt_cond_destroy(&src->not_empty);
        src->sync_init = 0;
    }
    src->slots = NULL;
}

/* TASK_HANDLE safe-handoff body lives after the
 * `osty_rt_task_handle_impl_` struct definition (the helper needs
 * the full layout). Forward-declared earlier next to chan/map
 * handoffs. */

/* OSTY_HOT_INLINE: called from every Map probe iteration after the
 * fingerprint match short-circuit. The `kind` argument is a struct-
 * load LLVM cannot statically constant-fold across the kind dispatch,
 * but inlining at the keyed Map wrapper call sites lets ThinLTO see
 * the wrapper's own constant `kind` value (set at osty_rt_map_new
 * time and never mutated) and DCE the unmatched arms when the
 * importer pulls in enough surrounding context. Even without that
 * fold, eliminating the cross-TU call is a measurable win on
 * String-key probes. */
static OSTY_HOT_INLINE bool osty_rt_value_equals(const void *left, const void *right, size_t size, int64_t kind) {
    if (kind == OSTY_RT_ABI_PTR) {
        void *left_value = NULL;
        void *right_value = NULL;
        memcpy(&left_value, left, sizeof(left_value));
        memcpy(&right_value, right, sizeof(right_value));
        return osty_gc_load_v1(left_value) == osty_gc_load_v1(right_value);
    }
    if (kind == OSTY_RT_ABI_STRING) {
        const char *left_value = NULL;
        const char *right_value = NULL;
        memcpy(&left_value, left, sizeof(left_value));
        memcpy(&right_value, right, sizeof(right_value));
        left_value = (const char *)osty_gc_load_v1((void *)left_value);
        right_value = (const char *)osty_gc_load_v1((void *)right_value);
        return osty_rt_strings_Equal(left_value, right_value);
    }
    return memcmp(left, right, size) == 0;
}

static size_t osty_rt_kind_size(int64_t kind) {
    switch (kind) {
    case OSTY_RT_ABI_I64:
    case OSTY_RT_ABI_F64:
        return sizeof(int64_t);
    case OSTY_RT_ABI_PTR:
    case OSTY_RT_ABI_STRING:
        return sizeof(void *);
    case OSTY_RT_ABI_I1:
        return sizeof(bool);
    default:
        osty_rt_abort("unsupported runtime ABI kind");
        return 0;
    }
}

static uint64_t osty_rt_hash_mix64(uint64_t h) {
    h ^= h >> 33;
    h *= 0xff51afd7ed558ccdULL;
    h ^= h >> 33;
    h *= 0xc4ceb9fe1a85ec53ULL;
    h ^= h >> 33;
    return h;
}

#define OSTY_RT_STRING_CACHE_SLOTS 256

typedef struct osty_rt_string_cache_entry {
    const char *value;
    size_t len;
    size_t hash;
    /* Cache stores the (len, hash) for `value` — but only when
     * `value` is arena-resident. Stack / `.rodata` strings reuse
     * the same pointer across calls with potentially different
     * content (canonical case: `for (i...) { snprintf(keybuf, ...,
     * i); map.contains(keybuf) }`), and no fixed-size content
     * fingerprint is enough to disambiguate every realistic key
     * pattern (common-prefix keys like "key_0", "key_1", … alias
     * over any short fingerprint). The eligibility check is the
     * only safe answer: only managed-arena pointers are stable
     * for their pointer's lifetime, so only those get cached. */
    bool has_entry;
} osty_rt_string_cache_entry;

static OSTY_RT_TLS osty_rt_string_cache_entry
    osty_rt_string_cache[OSTY_RT_STRING_CACHE_SLOTS];

/* Wipe every cache slot. Called from `osty_gc_cheney_swap_arenas` so
 * young arena addresses can't be observed via stale `len`/`hash`
 * entries from a prior cycle's tenant. The full memset is fine — the
 * cache is 256 entries × ~32 bytes ≈ 8 KiB, well under one cache line
 * fault budget per minor cycle. */
static void osty_rt_string_cache_invalidate_all(void) {
    memset(osty_rt_string_cache, 0, sizeof(osty_rt_string_cache));
}

/* OSTY_HOT_INLINE: called twice per `osty_rt_strings_Equal` (one for
 * each operand) and once per `osty_rt_string_hash`. The SSO branch
 * is O(1) tag-bit math; the heap branch is a per-pointer length-
 * cache lookup with FNV walk on miss. Inlining lets the SSO branch
 * fold to a few register ops at the call site and lets LLVM see
 * across the cache-lookup boundary so it can CSE the cache slot
 * computation when measure is called twice on operands with the
 * same hash low bits. */
static OSTY_HOT_INLINE void osty_rt_string_measure(const char *value,
                                                   size_t *len_out,
                                                   size_t *hash_out) {
    size_t len = 0;
    size_t hash = (size_t)osty_rt_hash_mix64(0ULL);
    if (osty_rt_string_is_inline(value)) {
        /* SSO fast path: length is in the tag bits and bytes are
         * packed in the pointer itself. Skip the per-pointer cache
         * (hashing 7 bytes is cheap and the cache index would
         * collapse all inline encodings into a small region of
         * the slot space). */
        size_t i;
        len = osty_rt_string_inline_len(value);
        if (hash_out != NULL) {
            uint64_t h = 1469598103934665603ULL;
            for (i = 0; i < len; i++) {
                h ^= (uint64_t)(unsigned char)osty_rt_string_inline_byte(value, i);
                h *= 1099511628211ULL;
            }
            hash = (size_t)osty_rt_hash_mix64(h);
        }
    } else if (value != NULL) {
        /* Length fast path via the GC header. Every heap-allocated
         * string carries its allocated byte size (= len + 1 for the
         * trailing NUL), so reading the header gives us length in
         * O(1) — no `strlen` walk, no per-pointer cache lookup.
         *
         * Two paths:
         *   1. Headerful arena (the dominant case before #977 made
         *      String young-eligible) — header is at
         *      `payload - sizeof(osty_gc_header)`.
         *   2. Tinytag young arena (post-#977 every freshly Concat'd
         *      key, every intToStr return, every Split piece that's
         *      heap-bound) — micro-header is at
         *      `payload - sizeof(osty_gc_micro_header)` and the
         *      `byte_size` field has the same `len + 1` semantic.
         *
         * `.rodata` string literals, humongous strings, and forwarded
         * objects fall through to the cache + strlen path.
         *
         * Hash still uses the 256-slot per-pointer cache; deriving
         * hash from the header would require an extra `cached_hash`
         * field which we'll add only if profiling shows it's worth
         * the layout change. */
        bool len_from_header = false;
        if (osty_gc_arena_contains((void *)value)) {
            osty_gc_header *hdr = (osty_gc_header *)((unsigned char *)value - sizeof(osty_gc_header));
            if ((unsigned char *)hdr >= osty_gc_arena_base &&
                hdr->payload == (void *)value &&
                hdr->object_kind == OSTY_GC_KIND_STRING &&
                hdr->byte_size > 0) {
                len = (size_t)hdr->byte_size - 1;
                len_from_header = true;
            }
        } else if (osty_gc_arena_is_young_page((void *)value)) {
            osty_gc_micro_header *micro =
                osty_gc_micro_header_for_payload((void *)value);
            uint64_t fom = micro->forward_or_meta;
            uint64_t tag = fom & OSTY_GC_FORWARD_TAG_MASK;
            /* UNFORWARDED tinytag-young string — byte_size is
             * authoritative. FORWARDED / PROMOTED would mean the
             * payload moved (the to-space copy has its own header
             * with a fresh byte_size), so trust micro->byte_size
             * only when the slot isn't tagged. */
            if (tag == OSTY_GC_FORWARD_TAG_UNFORWARDED &&
                micro->object_kind == OSTY_GC_KIND_STRING &&
                micro->byte_size > 0) {
                len = (size_t)micro->byte_size - 1;
                len_from_header = true;
            }
        }
        if (hash_out != NULL || !len_from_header) {
            /* Need hash, or header-derived length wasn't available.
             * Use the existing cache + strlen path. */
            size_t idx = (size_t)osty_rt_hash_mix64(
                             ((uint64_t)(uintptr_t)value) >> 4) &
                         (OSTY_RT_STRING_CACHE_SLOTS - 1);
            osty_rt_string_cache_entry *entry = &osty_rt_string_cache[idx];
            /* Cache eligibility: only arena-resident pointers (managed
             * payloads in either the headerful arena or the young
             * arena) have stable content for their pointer's lifetime.
             * Stack buffers and `.rodata` strings reuse the same
             * pointer with potentially different content (the canonical
             * case is `for (i...) { snprintf(keybuf, ..., i);
             * map.contains(keybuf) }` — `keybuf` is a stack address
             * that gets a fresh string written into it each iteration).
             * For those, no fixed-size content fingerprint is enough
             * to disambiguate every realistic key pattern (e.g.,
             * "key_0", "key_1", … share the first 4-8 bytes), so we
             * skip the cache entirely and recompute every call.
             *
             * Cache stays effective for the dominant case: hashing the
             * SAME arena-resident string repeatedly (e.g., a Map value
             * scanned during iteration). */
            bool cacheable = osty_gc_arena_contains((void *)value) ||
                             osty_gc_arena_is_young_page((void *)value);
            if (cacheable && entry->has_entry && entry->value == value) {
                if (!len_from_header) {
                    len = entry->len;
                }
                hash = entry->hash;
            } else {
                /* Cache miss. If we have len-from-header, walk only
                 * for the hash — skip the length count. Otherwise
                 * walk for both. */
                const unsigned char *cursor = (const unsigned char *)value;
                uint64_t h = 1469598103934665603ULL;
                if (len_from_header) {
                    size_t i;
                    for (i = 0; i < len; i++) {
                        h ^= (uint64_t)cursor[i];
                        h *= 1099511628211ULL;
                    }
                } else {
                    while (*cursor != '\0') {
                        h ^= (uint64_t)(*cursor++);
                        h *= 1099511628211ULL;
                        len += 1;
                    }
                }
                hash = (size_t)osty_rt_hash_mix64(h);
                /* Store in cache only for arena-resident pointers
                 * (stable content for pointer's lifetime). Stack /
                 * rodata pointers skip the store too — caching them
                 * would just churn the slot without ever hitting. */
                if (cacheable) {
                    entry->value = value;
                    entry->len = len;
                    entry->hash = hash;
                    entry->has_entry = true;
                }
            }
        }
    }
    if (len_out != NULL) {
        *len_out = len;
    }
    if (hash_out != NULL) {
        *hash_out = hash;
    }
}

static inline size_t osty_rt_string_len(const char *value) {
    size_t len = 0;
    osty_rt_string_measure(value, &len, NULL);
    return len;
}

/* OSTY_HOT_INLINE on top of `static inline`: ThinLTO sometimes emits
 * an out-of-line copy when `osty_rt_string_measure` is large enough
 * that the inlined body bloats above the importer's instruction
 * budget (5 sites still showed up as `bl _osty_rt_string_hash` in
 * the markdown-stats hot loop without this directive). The
 * always-inline attribute forces the body in regardless. */
static OSTY_HOT_INLINE size_t osty_rt_string_hash(const char *value) {
    size_t hash = (size_t)osty_rt_hash_mix64(0ULL);
    osty_rt_string_measure(value, NULL, &hash);
    return hash;
}

/* OSTY_HOT_INLINE: called from `osty_rt_strings_Compare` (which
 * dep-resolver's sort comparator hits per element). With this
 * inlined the strcmp call site sees both operands' SSO-decode
 * branches and can CSE the tag-bit tests. */
static OSTY_HOT_INLINE int osty_rt_string_compare_bytes(const char *left, const char *right) {
    char left_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
    char right_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
    if (left == right) {
        return 0;
    }
    if (left == NULL) {
        return (right == NULL) ? 0 : -1;
    }
    if (right == NULL) {
        return 1;
    }
    /* SSO: strcmp walks bytes from a real address — decode any
     * inline operand into a stack buffer first. */
    osty_rt_string_decode_to_buf_if_inline(&left, left_buf);
    osty_rt_string_decode_to_buf_if_inline(&right, right_buf);
    return strcmp(left, right);
}

/* OSTY_HOT_INLINE: this is a switch-on-`kind` whose argument is a
 * compile-time constant in every Map op call site (the keyed wrapper
 * passes `map->key_kind` which is a struct-load LLVM proves equal to
 * the same load done by the cast / find_index path, and the C runtime
 * sees the dispatch fold to a single arm because each call site
 * threads through one keyed `_<suffix>` wrapper). Forcing inline lets
 * ThinLTO collapse the switch to the chosen arm and CSE the load
 * against neighboring map field reads. */
static OSTY_HOT_INLINE size_t osty_rt_map_key_hash(int64_t kind, const void *key) {
    switch (kind) {
    case OSTY_RT_ABI_I64:
    case OSTY_RT_ABI_F64: {
        uint64_t bits = 0;
        memcpy(&bits, key, sizeof(bits));
        return (size_t)osty_rt_hash_mix64(bits);
    }
    case OSTY_RT_ABI_I1: {
        bool value = false;
        memcpy(&value, key, sizeof(value));
        return (size_t)osty_rt_hash_mix64(value ? 0x9e3779b97f4a7c15ULL : 0ULL);
    }
    case OSTY_RT_ABI_PTR: {
        void *value = NULL;
        memcpy(&value, key, sizeof(value));
        return (size_t)osty_rt_hash_mix64((uint64_t)(uintptr_t)value);
    }
    case OSTY_RT_ABI_STRING: {
        const char *value = NULL;
        memcpy(&value, key, sizeof(value));
        return osty_rt_string_hash(value);
    }
    default:
        osty_rt_abort("unsupported map key hash kind");
        return 0;
    }
}

static inline uint32_t osty_rt_map_key_fingerprint(size_t hash) {
    uint64_t bits = (uint64_t)hash;
    return (uint32_t)(bits ^ (bits >> 32));
}

static void osty_rt_map_trace(void *payload) {
    osty_rt_map *map = (osty_rt_map *)payload;
    int64_t i;

    if (map == NULL) {
        return;
    }
    for (i = 0; i < map->len; i++) {
        unsigned char *key_slot = map->keys + ((size_t)i * osty_rt_kind_size(map->key_kind));
        unsigned char *value_slot = map->values + ((size_t)i * map->value_size);
        if (map->key_kind == OSTY_RT_ABI_STRING || map->key_kind == OSTY_RT_ABI_PTR) {
            osty_gc_mark_slot_v1((void *)key_slot);
        }
        if (map->value_kind == OSTY_RT_ABI_STRING || map->value_kind == OSTY_RT_ABI_PTR) {
            osty_gc_mark_slot_v1((void *)value_slot);
        } else if (map->value_trace != NULL) {
            map->value_trace((void *)value_slot);
        }
    }
}

static void osty_rt_map_destroy(void *payload) {
    osty_rt_map *map = (osty_rt_map *)payload;
    if (map != NULL) {
        if (map->index_slots != NULL && map->index_slots != map->index_inline) {
            free(map->index_slots);
        }
        if (map->index_hashes != NULL && map->index_hashes != map->index_hashes_inline) {
            free(map->index_hashes);
        }
        free(map->keys);
        free(map->values);
        if (map->mu_init) {
            osty_rt_rmu_destroy(&map->mu);
            map->mu_init = 0;
        }
    }
}

static void osty_rt_set_trace(void *payload) {
    osty_rt_set *set = (osty_rt_set *)payload;
    int64_t i;
    size_t elem_size;

    if (set == NULL || (set->elem_kind != OSTY_RT_ABI_STRING && set->elem_kind != OSTY_RT_ABI_PTR)) {
        return;
    }
    elem_size = osty_rt_kind_size(set->elem_kind);
    for (i = 0; i < set->len; i++) {
        osty_gc_mark_slot_v1((void *)(set->items + ((size_t)i * elem_size)));
    }
}

static void osty_rt_set_destroy(void *payload) {
    osty_rt_set *set = (osty_rt_set *)payload;
    if (set != NULL) {
        free(set->items);
    }
}

/* Phase 0e': TLS local mark stack. While a marker thread is in
 * `osty_gc_mark_drain_budget`, the tracer's reentrant pushes route
 * here instead of the central stack. Benefits, even though the
 * caller still holds `osty_gc_lock` during the drain:
 *
 *   1. The central stack (`osty_gc_mark_stack`) doesn't grow with
 *      every recursive push — its high-water capacity is bounded by
 *      the cycle's seed roots + per-marker overflow spills, not the
 *      full transitive object graph. fewer realloc events and a
 *      smaller persistent buffer.
 *
 *   2. The hot-path push is a plain TLS array write — no capacity
 *      check, no max-depth bookkeeping, no central-stack indirection.
 *      Tracer-heavy workloads see a measurable drop in
 *      `osty_gc_mark_stack_push` cost.
 *
 *   3. Sets up the lock-split groundwork: when a future PR makes
 *      the tracer safe to run without `osty_gc_lock` (lock-free hash
 *      index, snapshot-based dispatch, etc.), the TLS stack is
 *      already the right home for trace-time push without per-push
 *      synchronisation. */
#define OSTY_GC_LOCAL_MARK_STACK_CAP 256
static OSTY_RT_TLS osty_gc_header *osty_gc_local_mark_stack[OSTY_GC_LOCAL_MARK_STACK_CAP];
static OSTY_RT_TLS int osty_gc_local_mark_stack_top = 0;
static OSTY_RT_TLS bool osty_gc_local_mark_active = false;

static int64_t osty_gc_local_mark_pushes_total = 0;
static int64_t osty_gc_local_mark_overflow_total = 0;

/* Phase 0h: TLS flag set while a marker thread is mid-dispatch on a
 * lockfree-safe kind. `mark_stack_push` consults it to abort with a
 * clear message if the local TLS stack overflows during such a
 * dispatch — central pushes without `osty_gc_lock` would race
 * mutator-side SATB pushes. Defined ahead of `mark_stack_push` so
 * the reference resolves in single-pass C compilation. */
static OSTY_RT_TLS bool osty_gc_in_lockfree_trace = false;

static void osty_gc_mark_stack_push_central(osty_gc_header *header) {
    if (osty_gc_mark_stack_count == osty_gc_mark_stack_cap) {
        int64_t new_cap;
        osty_gc_header **new_buf;
        new_cap = osty_gc_mark_stack_cap == 0 ? 64 : osty_gc_mark_stack_cap * 2;
        new_buf = (osty_gc_header **)realloc(
            osty_gc_mark_stack, (size_t)new_cap * sizeof(osty_gc_header *));
        if (new_buf == NULL) {
            osty_rt_abort("out of memory (mark stack)");
        }
        osty_gc_mark_stack = new_buf;
        osty_gc_mark_stack_cap = new_cap;
    }
    osty_gc_mark_stack[osty_gc_mark_stack_count++] = header;
    /* Phase 0e': effective queue depth includes the calling thread's
     * TLS local stack — important when the local cap is hit and
     * subsequent pushes spill here. Without folding `local_mark_top`
     * in, deep-graph regression tests miss the watermark because
     * `osty_gc_mark_stack_count` tops out around (graph_size - cap)
     * instead of the actual queue size. */
    int64_t effective =
        (int64_t)osty_gc_local_mark_stack_top + osty_gc_mark_stack_count;
    if (effective > osty_gc_mark_stack_max_depth) {
        osty_gc_mark_stack_max_depth = effective;
    }
}

static void osty_gc_mark_stack_push(osty_gc_header *header) {
    if (osty_gc_local_mark_active &&
        osty_gc_local_mark_stack_top < OSTY_GC_LOCAL_MARK_STACK_CAP) {
        osty_gc_local_mark_stack[osty_gc_local_mark_stack_top++] = header;
        (void)__atomic_add_fetch(&osty_gc_local_mark_pushes_total, 1,
                                 __ATOMIC_RELAXED);
        /* `mark_stack_max_depth` is the cycle-wide work-queue
         * watermark — used by deep-graph tests to assert the queue
         * survived recursive trace re-entry. With Phase 0e' the
         * effective queue is local + central, so report the sum
         * here so the watermark stays meaningful. */
        int64_t effective =
            (int64_t)osty_gc_local_mark_stack_top +
            osty_gc_mark_stack_count;
        if (effective > osty_gc_mark_stack_max_depth) {
            osty_gc_mark_stack_max_depth = effective;
        }
        return;
    }
    if (osty_gc_local_mark_active) {
        /* Local full — overflow to central. Counted so tests can
         * confirm the local cap is sized right for the workload. */
        (void)__atomic_add_fetch(&osty_gc_local_mark_overflow_total, 1,
                                 __ATOMIC_RELAXED);
        /* Phase 0h safety: a lockfree-safe tracer is currently
         * mid-dispatch with `osty_gc_lock` released. Pushing to the
         * central stack here would race with mutator-side SATB
         * pushes (which take `osty_gc_lock`). Abort with a clear
         * message so the issue is visible — this is a design bound
         * (closure captures cap at 64, generic enum ptr pushes 1),
         * so realistic workloads can't hit it. If a future kind
         * lifts the bound, the kind's `trace_lockfree_safe` flag
         * needs to flip to false. */
        if (osty_gc_in_lockfree_trace) {
            osty_rt_abort(
                "lockfree trace: local mark stack overflow without "
                "gc_lock held — bound the kind's trace push count "
                "below OSTY_GC_LOCAL_MARK_STACK_CAP or set "
                "trace_lockfree_safe=false in the kind descriptor");
        }
    }
    osty_gc_mark_stack_push_central(header);
}

/* Phase 0g: counters for atomic color transitions. CAS failures
 * (another thread already moved this header out of WHITE) are
 * harmless — they just mean the racer pushed first — but tracking
 * them is the only way to observe contention while the lock around
 * `dispatch_trace` is still in place. With the lock dropped (Phase
 * 0h) these counters become the contention signal. */
static int64_t osty_gc_mark_cas_attempts_total = 0;
static int64_t osty_gc_mark_cas_failures_total = 0;

/* Phase 0h: counters for trace-dispatch lock state. Each call to
 * `dispatch_trace` either ran with `osty_gc_lock` released
 * (`lockfree_total`) or held (`locked_total`). The TLS flag itself
 * is defined ahead of `mark_stack_push` for forward-reference. */
static int64_t osty_gc_lockfree_trace_total = 0;
static int64_t osty_gc_locked_trace_total = 0;

static void osty_gc_mark_header(osty_gc_header *header) {
    /* Enqueue only — the actual trace happens in `osty_gc_mark_drain`.
     * Phase C: the tri-colour check `color != WHITE` short-circuits
     * both already-enqueued (GREY) and already-traced (BLACK) cases
     * so repeated reach via different edges pushes at most once.
     *
     * Phase 0g: the CAS makes WHITE→GREY atomic so two markers (or
     * a marker + SATB barrier) racing on the same header don't both
     * push it onto the mark stack. The reload-on-failure path is a
     * no-op — whoever lost the CAS leaves the push to the winner. */
    if (header == NULL) {
        return;
    }
    /* Phase B: during a minor collection, OLD headers are treated as
     * permanent roots — we neither mark nor follow them. Any OLD→YOUNG
     * edge is captured by the remembered set and seeded separately in
     * `osty_gc_collect_minor_with_stack_roots`. Without this filter the
     * minor pass would sweep through tenured objects unnecessarily,
     * collapsing back to major semantics. */
    if (osty_gc_minor_in_progress &&
        __atomic_load_n(&header->generation, __ATOMIC_RELAXED) ==
            OSTY_GC_GEN_OLD) {
        return;
    }
    uint8_t expected = OSTY_GC_COLOR_WHITE;
    osty_gc_mark_cas_attempts_total += 1;
    if (!__atomic_compare_exchange_n(&header->color, &expected,
                                     OSTY_GC_COLOR_GREY,
                                     false /* strong */,
                                     __ATOMIC_ACQ_REL,
                                     __ATOMIC_ACQUIRE)) {
        osty_gc_mark_cas_failures_total += 1;
        return;
    }
    __atomic_store_n(&header->marked, true, __ATOMIC_RELAXED);
    osty_gc_mark_stack_push(header);
}

static void osty_gc_mark_payload(void *payload) {
    osty_gc_mark_header(osty_gc_find_header(payload));
}

/* Phase C: drain up to `budget` grey headers. Budget of 0 or negative
 * means "drain everything" (the pre-Phase-C semantics, still used by
 * STW major/minor). Returns the number of headers actually traced so
 * the incremental scheduler can pace future steps.
 *
 * Phase 0e': during the drain, tracer-emitted pushes route to the TLS
 * `osty_gc_local_mark_stack` instead of growing the central stack.
 * The local stack is then drained LIFO before the next central pop —
 * effectively a depth-first traversal that keeps hot trace data in
 * cache and bounds central-stack size. The local stack flushes back
 * to central at drain exit so any future drain (this thread or
 * another) sees the unfinished work. */
static int64_t osty_gc_mark_drain_budget(int64_t budget) {
    int64_t done = 0;
    bool unlimited = budget <= 0;
    bool was_active = osty_gc_local_mark_active;
    osty_gc_local_mark_active = true;

    while ((osty_gc_local_mark_stack_top > 0 ||
            osty_gc_mark_stack_count > 0) &&
           (unlimited || done < budget)) {
        osty_gc_header *header;
        if (osty_gc_local_mark_stack_top > 0) {
            header = osty_gc_local_mark_stack[--osty_gc_local_mark_stack_top];
        } else {
            header = osty_gc_mark_stack[--osty_gc_mark_stack_count];
        }
        /* Phase 0g: atomic store. The pop above already serialised
         * "this header is exclusively ours to trace", so we don't need
         * a CAS — but the RELEASE pairs with the ACQUIRE in
         * `mark_header` so other threads that subsequently see BLACK
         * also see the trace's full effects (children pushed). */
        __atomic_store_n(&header->color, OSTY_GC_COLOR_BLACK,
                         __ATOMIC_RELEASE);
        /* Phase 0h: per-kind decision on whether the tracer can run
         * with `osty_gc_lock` released. closure_env / generic enum
         * payloads are safe (fixed-layout reads, bounded push count
         * within local TLS cap); list / map / set / channel still
         * need the lock because their backing storage can be
         * reallocated by a concurrent mutator. */
        bool lockfree = osty_gc_trace_lockfree_safe(header);
        if (lockfree) {
            (void)__atomic_add_fetch(&osty_gc_lockfree_trace_total, 1,
                                     __ATOMIC_RELAXED);
            osty_gc_in_lockfree_trace = true;
            osty_gc_release();
            osty_gc_dispatch_trace(header);
            osty_gc_acquire();
            osty_gc_in_lockfree_trace = false;
        } else {
            (void)__atomic_add_fetch(&osty_gc_locked_trace_total, 1,
                                     __ATOMIC_RELAXED);
            /* Trace callbacks re-enter `osty_gc_mark_*` for children,
             * which push more GREY work — into TLS via mark_stack_push's
             * routing. The C call stack stays bounded regardless of
             * object graph depth. */
            osty_gc_dispatch_trace(header);
        }
        done += 1;
    }

    /* Flush whatever's left in the local stack back to central so
     * the next drain (possibly on another thread) sees the work.
     * Order doesn't matter: any item left here is unprocessed grey,
     * central stack treats them identically to direct pushes. */
    while (osty_gc_local_mark_stack_top > 0) {
        osty_gc_mark_stack_push_central(
            osty_gc_local_mark_stack[--osty_gc_local_mark_stack_top]);
    }
    osty_gc_local_mark_active = was_active;
    return done;
}

static void osty_gc_mark_drain(void) {
    (void)osty_gc_mark_drain_budget(0);
}

static void osty_gc_mark_root_slot(void *slot_addr) {
    void *payload = NULL;

    if (slot_addr == NULL) {
        return;
    }
    memcpy(&payload, slot_addr, sizeof(payload));
    if (payload == NULL) {
        return;
    }
    payload = osty_gc_forward_payload(payload);
    memcpy(slot_addr, &payload, sizeof(payload));
    osty_gc_mark_payload(payload);
}

static void osty_gc_trace_slot_with_mode(osty_rt_trace_slot_fn trace,
                                         void *slot_addr,
                                         osty_gc_trace_slot_mode mode) {
    osty_gc_trace_slot_mode previous_mode;

    if (trace == NULL) {
        return;
    }
    previous_mode = osty_gc_trace_slot_mode_current;
    osty_gc_trace_slot_mode_current = mode;
    trace(slot_addr);
    osty_gc_trace_slot_mode_current = previous_mode;
}

static void osty_gc_remap_slot(void *slot_addr) {
    void *payload = NULL;
    void *forwarded = NULL;

    if (slot_addr == NULL) {
        return;
    }
    memcpy(&payload, slot_addr, sizeof(payload));
    if (payload == NULL) {
        return;
    }
    forwarded = osty_gc_forward_payload(payload);
    if (forwarded != payload) {
        memcpy(slot_addr, &forwarded, sizeof(forwarded));
    }
}

static void osty_gc_remap_list_payload(void *payload) {
    osty_rt_list *list = (osty_rt_list *)payload;
    int64_t i;
    int64_t j;

    if (list == NULL || list->data == NULL) {
        return;
    }
    if (list->trace_elem == osty_gc_mark_slot_v1) {
        for (i = 0; i < list->len; i++) {
            osty_gc_remap_slot((void *)(list->data +
                                        ((size_t)i * list->elem_size)));
        }
        return;
    }
    if (list->gc_offset_count <= 0 || list->gc_offsets == NULL) {
        return;
    }
    for (i = 0; i < list->len; i++) {
        unsigned char *elem = list->data + ((size_t)i * list->elem_size);
        for (j = 0; j < list->gc_offset_count; j++) {
            osty_gc_remap_slot((void *)(elem + (size_t)list->gc_offsets[j]));
        }
    }
}

static void osty_gc_remap_map_payload(void *payload) {
    osty_rt_map *map = (osty_rt_map *)payload;
    int64_t i;

    if (map == NULL) {
        return;
    }
    for (i = 0; i < map->len; i++) {
        unsigned char *key_slot =
            map->keys + ((size_t)i * osty_rt_kind_size(map->key_kind));
        unsigned char *value_slot =
            map->values + ((size_t)i * map->value_size);
        if (map->key_kind == OSTY_RT_ABI_STRING ||
            map->key_kind == OSTY_RT_ABI_PTR) {
            osty_gc_remap_slot((void *)key_slot);
        }
        if (map->value_kind == OSTY_RT_ABI_STRING ||
            map->value_kind == OSTY_RT_ABI_PTR) {
            osty_gc_remap_slot((void *)value_slot);
        } else if (map->value_trace != NULL) {
            osty_gc_trace_slot_with_mode(
                map->value_trace, (void *)value_slot,
                OSTY_GC_TRACE_SLOT_MODE_REMAP);
        }
    }
}

static void osty_gc_remap_set_payload(void *payload) {
    osty_rt_set *set = (osty_rt_set *)payload;
    int64_t i;
    size_t elem_size;

    if (set == NULL) {
        return;
    }
    if (set->elem_kind != OSTY_RT_ABI_PTR &&
        set->elem_kind != OSTY_RT_ABI_STRING) {
        return;
    }
    elem_size = osty_rt_kind_size(set->elem_kind);
    for (i = 0; i < set->len; i++) {
        osty_gc_remap_slot((void *)(set->items + ((size_t)i * elem_size)));
    }
}

static void osty_gc_remap_closure_env_payload(void *payload) {
    osty_rt_closure_env *env = (osty_rt_closure_env *)payload;
    int64_t i;

    if (env == NULL) {
        return;
    }
    for (i = 0; i < env->capture_count; i++) {
        osty_gc_remap_slot((void *)&env->captures[i]);
    }
}

static void osty_gc_remap_chan_payload(void *payload) {
    osty_rt_chan_impl *ch = (osty_rt_chan_impl *)payload;
    int64_t i;

    if (ch == NULL || ch->slots == NULL ||
        !osty_gc_chan_elem_kind_is_managed(ch->elem_kind)) {
        return;
    }
    for (i = 0; i < ch->count; i++) {
        int64_t idx = (ch->head + i) % ch->cap;
        void *payload = (void *)(uintptr_t)ch->slots[idx];
        void *forwarded = osty_gc_forward_payload(payload);
        if (forwarded != payload) {
            ch->slots[idx] = (int64_t)(uintptr_t)forwarded;
        }
    }
}

static void osty_gc_remap_header_payload(osty_gc_header *header) {
    if (header == NULL) {
        return;
    }
    const osty_gc_kind_descriptor *desc =
        osty_gc_kind_descriptor_lookup(header->object_kind);
    if (desc != NULL && desc->remap != NULL) {
        desc->remap(header->payload);
        return;
    }
    /* GENERIC fallback path — kinds not represented in the kind
     * table fall back to the per-instance pattern table. The
     * `byte_size == sizeof(ptr)` filter pins this to the
     * enum-ptr-payload case. */
    if (osty_gc_generic_patterns[header->generic_pattern].trace != NULL &&
        header->byte_size == (int64_t)sizeof(void *)) {
        osty_gc_remap_slot(header->payload);
    }
}

/* Phase E follow-up: live young objects also hold OLD pointers
 * (e.g. a young Map<String, _> with OLD String values; a young
 * GENERIC_ENUM_PTR box pointing at an OLD enum payload). The
 * headerful remap loops in `osty_gc_compact_*_with_stack_roots`
 * only walk `osty_gc_objects`, so without this pass the young
 * survivors' buffer entries would still reference pre-compact
 * addresses. Walk the young from-space (cheney just swapped
 * survivors here) and dispatch the kind-specific remap helper —
 * the same helpers `osty_gc_remap_header_payload` calls. */
static void osty_gc_remap_young_arena_payloads(void) {
    if (!osty_gc_tinytag_young_now() || osty_gc_young_from.base == NULL) {
        return;
    }
    unsigned char *scan = osty_gc_young_from.base;
    while (scan < osty_gc_young_from.cursor) {
        osty_gc_micro_header *micro = (osty_gc_micro_header *)scan;
        int32_t payload_size = micro->byte_size;
        const osty_gc_kind_descriptor *desc =
            osty_gc_kind_descriptor_lookup((int64_t)micro->object_kind);
        if (desc != NULL && desc->remap != NULL) {
            void *payload = (void *)(scan + sizeof(osty_gc_micro_header));
            desc->remap(payload);
        }
        /* Kinds with no remap fn (STRING / BYTES / GENERIC NONE)
         * carry no managed-pointer buffers — payload bytes are
         * opaque from the GC's view. */
        scan += osty_gc_micro_object_total_size(payload_size);
    }
}

static osty_gc_header *osty_gc_clone_header_storage_to_bump_region(
    osty_gc_header *header,
    uint8_t storage_kind,
    osty_gc_bump_block **blocks_head,
    osty_gc_bump_block **current,
    int64_t *block_count_total,
    int64_t *block_bytes_total,
    int64_t *alloc_count_total,
    int64_t *alloc_bytes_total) {
    osty_gc_header *clone;
    bool used_bump = false;
    size_t total_size;

    if (header == NULL) {
        return NULL;
    }
    if (header->byte_size < 0 ||
        (size_t)header->byte_size > SIZE_MAX - sizeof(osty_gc_header)) {
        osty_rt_abort("GC clone size overflow");
    }
    total_size = sizeof(osty_gc_header) + (size_t)header->byte_size;
    if (!osty_gc_total_size_is_humongous(total_size) &&
        storage_kind != OSTY_GC_STORAGE_DIRECT) {
        switch (storage_kind) {
        case OSTY_GC_STORAGE_BUMP_YOUNG:
            clone = osty_gc_tlab_take(total_size);
            break;
        case OSTY_GC_STORAGE_BUMP_SURVIVOR:
            clone = osty_gc_survivor_tlab_take(total_size);
            break;
        case OSTY_GC_STORAGE_BUMP_OLD:
            clone = osty_gc_old_tlab_take(total_size);
            break;
        case OSTY_GC_STORAGE_BUMP_PINNED:
            clone = osty_gc_pinned_tlab_take(total_size);
            break;
        default:
            clone = osty_gc_bump_take_from_region(
                total_size, storage_kind, blocks_head, current,
                block_count_total, block_bytes_total, alloc_count_total,
                alloc_bytes_total);
            break;
        }
        used_bump = clone != NULL;
    } else {
        clone = NULL;
    }
    if (clone == NULL) {
        clone = (osty_gc_header *)calloc(1, total_size);
        if (clone == NULL) {
            osty_rt_abort("out of memory (gc compaction clone)");
        }
    }
    memcpy(clone, header, sizeof(osty_gc_header));
    clone->next = NULL;
    clone->prev = NULL;
    clone->next_gen = NULL;
    clone->prev_gen = NULL;
    clone->payload = (void *)(clone + 1);
    clone->storage_kind = used_bump ? storage_kind : OSTY_GC_STORAGE_DIRECT;
    return clone;
}

static osty_gc_header *osty_gc_clone_map_header_to_bump_region(
    osty_gc_header *header,
    uint8_t storage_kind,
    osty_gc_bump_block **blocks_head,
    osty_gc_bump_block **current,
    int64_t *block_count_total,
    int64_t *block_bytes_total,
    int64_t *alloc_count_total,
    int64_t *alloc_bytes_total) {
    osty_gc_header *clone = osty_gc_clone_header_storage_to_bump_region(
        header, storage_kind, blocks_head, current, block_count_total,
        block_bytes_total, alloc_count_total, alloc_bytes_total);
    osty_rt_map *old_map;
    osty_rt_map *new_map;

    if (clone == NULL) {
        return NULL;
    }
    old_map = (osty_rt_map *)header->payload;
    new_map = (osty_rt_map *)clone->payload;
    memset(new_map, 0, sizeof(*new_map));
    new_map->len = old_map->len;
    new_map->cap = old_map->cap;
    new_map->key_kind = old_map->key_kind;
    new_map->value_kind = old_map->value_kind;
    new_map->value_size = old_map->value_size;
    new_map->value_trace = old_map->value_trace;
    new_map->keys = old_map->keys;
    new_map->values = old_map->values;
    if (old_map->mu_init) {
        osty_rt_map_mutex_init_or_abort(new_map);
        osty_rt_rmu_destroy(&old_map->mu);
        old_map->mu_init = 0;
    }
    old_map->keys = NULL;
    old_map->values = NULL;
    return clone;
}

static osty_gc_header *osty_gc_clone_chan_header_to_bump_region(
    osty_gc_header *header,
    uint8_t storage_kind,
    osty_gc_bump_block **blocks_head,
    osty_gc_bump_block **current,
    int64_t *block_count_total,
    int64_t *block_bytes_total,
    int64_t *alloc_count_total,
    int64_t *alloc_bytes_total) {
    osty_gc_header *clone = osty_gc_clone_header_storage_to_bump_region(
        header, storage_kind, blocks_head, current, block_count_total,
        block_bytes_total, alloc_count_total, alloc_bytes_total);
    osty_rt_chan_impl *old_ch;
    osty_rt_chan_impl *new_ch;

    if (clone == NULL) {
        return NULL;
    }
    old_ch = (osty_rt_chan_impl *)header->payload;
    new_ch = (osty_rt_chan_impl *)clone->payload;
    memset(new_ch, 0, sizeof(*new_ch));
    new_ch->cap = old_ch->cap;
    new_ch->head = old_ch->head;
    new_ch->tail = old_ch->tail;
    new_ch->count = old_ch->count;
    new_ch->elem_kind = old_ch->elem_kind;
    new_ch->closed = old_ch->closed;
    new_ch->slots = old_ch->slots;
    if (old_ch->sync_init) {
        osty_rt_chan_sync_init_or_abort(new_ch, "gc channel clone");
        osty_rt_mu_destroy(&old_ch->mu);
        osty_rt_cond_destroy(&old_ch->not_full);
        osty_rt_cond_destroy(&old_ch->not_empty);
        old_ch->sync_init = 0;
    }
    old_ch->slots = NULL;
    return clone;
}

static osty_gc_header *osty_gc_clone_header_to_bump_region(
    osty_gc_header *header,
    uint8_t storage_kind,
    osty_gc_bump_block **blocks_head,
    osty_gc_bump_block **current,
    int64_t *block_count_total,
    int64_t *block_bytes_total,
    int64_t *alloc_count_total,
    int64_t *alloc_bytes_total) {
    osty_gc_header *clone;

    if (header == NULL) {
        return NULL;
    }
    if (header->object_kind == OSTY_GC_KIND_MAP) {
        return osty_gc_clone_map_header_to_bump_region(
            header, storage_kind, blocks_head, current, block_count_total,
            block_bytes_total, alloc_count_total, alloc_bytes_total);
    }
    if (header->object_kind == OSTY_GC_KIND_CHANNEL) {
        return osty_gc_clone_chan_header_to_bump_region(
            header, storage_kind, blocks_head, current, block_count_total,
            block_bytes_total, alloc_count_total, alloc_bytes_total);
    }
    clone = osty_gc_clone_header_storage_to_bump_region(
        header, storage_kind, blocks_head, current, block_count_total,
        block_bytes_total, alloc_count_total, alloc_bytes_total);

    if (clone == NULL) {
        return NULL;
    }
    memcpy(clone->payload, header->payload, (size_t)header->byte_size);
    /* Post-copy: fix up self-referential pointers in the cloned
     * payload. List uses `data == &inline_storage` while small;
     * the byte-by-byte memcpy carries the OLD inline_storage
     * address into the new payload, so subsequent destroy/access
     * would dereference the stale region. Repoint to the new
     * inline_storage. Map / Channel handle their own self-refs
     * via dedicated clone helpers above. */
    if (header->object_kind == OSTY_GC_KIND_LIST) {
        osty_rt_list *old_list = (osty_rt_list *)header->payload;
        osty_rt_list *new_list = (osty_rt_list *)clone->payload;
        if (new_list->data == old_list->inline_storage) {
            new_list->data = new_list->inline_storage;
        }
    }
    return clone;
}

static osty_gc_header *osty_gc_clone_header(osty_gc_header *header) {
    return osty_gc_clone_header_to_bump_region(
        header, OSTY_GC_STORAGE_BUMP_OLD, &osty_gc_old_bump_blocks,
        &osty_gc_old_bump_current, &osty_gc_old_bump_block_count,
        &osty_gc_old_bump_block_bytes_total,
        &osty_gc_old_bump_alloc_count_total,
        &osty_gc_old_bump_alloc_bytes_total);
}

static void osty_gc_replace_header(osty_gc_header *old_header,
                                   osty_gc_header *new_header) {
    if (old_header == NULL || new_header == NULL) {
        return;
    }

    new_header->next = old_header->next;
    new_header->prev = old_header->prev;
    if (new_header->prev != NULL) {
        new_header->prev->next = new_header;
    } else {
        osty_gc_objects = new_header;
    }
    if (new_header->next != NULL) {
        new_header->next->prev = new_header;
    }

    new_header->next_gen = old_header->next_gen;
    new_header->prev_gen = old_header->prev_gen;
    if (new_header->prev_gen != NULL) {
        new_header->prev_gen->next_gen = new_header;
    } else if (new_header->generation == OSTY_GC_GEN_YOUNG) {
        osty_gc_young_head = new_header;
    } else {
        osty_gc_old_head = new_header;
    }
    if (new_header->next_gen != NULL) {
        new_header->next_gen->prev_gen = new_header;
    }

    /* `osty_gc_index_remove` is a no-op for arena-backed payloads
     * (they were never inserted), and otherwise drops the old hash
     * entry as before. The new-header insert is similarly gated on
     * arena membership: arena-backed clones (the overwhelming
     * majority — survivor / old / pinned-bump regions all use the
     * arena since #881) are findable via `osty_gc_find_header`'s
     * range + self-reference check, so a hash entry is redundant.
     * Without this gate every minor compaction inserted ~one entry
     * per evacuated header (~50K per iteration on log-aggregator),
     * which showed up as `osty_gc_index_grow` traffic in the
     * post-#881 profile.
     *
     * Identity inserts stay deferred (lazy population on first
     * lookup), same as the alloc path. */
    osty_gc_index_remove(old_header->payload);
    if (!osty_gc_arena_contains(new_header->payload)) {
        osty_gc_index_insert(new_header->payload, new_header);
    }
}

static int64_t osty_gc_compact_major_with_stack_roots(
    void *const *root_slots, int64_t root_slot_count) {
    osty_gc_header *header;
    osty_gc_header *next;
    osty_gc_forwarding_snapshot *snapshots;
    int64_t snapshot_count = 0;
    int64_t i;
    int64_t moved = 0;
    int64_t moved_bytes = 0;

    snapshots = osty_gc_forwarding_snapshot_take(&snapshot_count);
    osty_gc_forwarding_clear();
    /* Fused sweep + clone walk: WHITE → destroy + unlink + reclaim
     * (caller no longer runs a separate sweep loop). BLACK → reset
     * colour for the next cycle and, when movable, clone into the
     * OLD bump region so subsequent remap/replace passes can swap
     * the original out. Saving `next` before `osty_gc_unlink`
     * mirrors the previous standalone sweep pattern.
     *
     * Phase F: BLACK survivors also get `card_dirty` cleared — major
     * just re-traced every reachable header, so any prior dirty bit
     * is stale. The dirty-OLD array drops wholesale after the loop
     * (entries may reference just-freed headers; the bits-and-array
     * invariant requires lockstep reset). */
    header = osty_gc_objects;
    while (header != NULL) {
        next = header->next;
        /* Phase 0g: atomic load + atomic reset for the BLACK→WHITE
         * survivor recolour. Compact_major runs inside the STW
         * finish handshake, so no concurrent reader touches color
         * during this walk — the atomics are for invariant
         * consistency with the rest of the lock-free-ready paths. */
        uint8_t color = __atomic_load_n(&header->color, __ATOMIC_ACQUIRE);
        if (color == OSTY_GC_COLOR_WHITE) {
            osty_gc_swept_count_total += 1;
            osty_gc_swept_bytes_total += header->byte_size;
            osty_gc_dispatch_destroy(header);
            osty_gc_unlink(header);
            osty_gc_reclaim_swept_header(header);
        } else {
            __atomic_store_n(&header->color, OSTY_GC_COLOR_WHITE,
                             __ATOMIC_RELEASE);
            __atomic_store_n(&header->marked, false, __ATOMIC_RELAXED);
            header->card_dirty = 0;
            if (osty_gc_header_is_movable(header)) {
                osty_gc_header *clone = osty_gc_clone_header(header);
                osty_gc_forwarding_insert(header->payload, clone);
                moved += 1;
                moved_bytes += header->byte_size;
            }
        }
        header = next;
    }
    osty_gc_dirty_old_count = 0;
    if (moved == 0) {
        osty_gc_forwarding_retain_history(snapshots, snapshot_count);
        free(snapshots);
        osty_gc_bump_recycle_empty_blocks(
            &osty_gc_bump_blocks, &osty_gc_bump_current,
            &osty_gc_bump_block_count,
            &osty_gc_bump_recycled_block_count_total,
            &osty_gc_bump_recycled_bytes_total, true);
        osty_gc_bump_recycle_empty_blocks(
            &osty_gc_survivor_bump_blocks, &osty_gc_survivor_bump_current,
            &osty_gc_survivor_bump_block_count,
            &osty_gc_survivor_bump_recycled_block_count_total,
            &osty_gc_survivor_bump_recycled_bytes_total, true);
        osty_gc_bump_recycle_empty_blocks(
            &osty_gc_old_bump_blocks, &osty_gc_old_bump_current,
            &osty_gc_old_bump_block_count,
            &osty_gc_old_bump_recycled_block_count_total,
            &osty_gc_old_bump_recycled_bytes_total, true);
        osty_gc_bump_recycle_empty_blocks(
            &osty_gc_pinned_bump_blocks, &osty_gc_pinned_bump_current,
            &osty_gc_pinned_bump_block_count,
            &osty_gc_pinned_bump_recycled_block_count_total,
            &osty_gc_pinned_bump_recycled_bytes_total, true);
        osty_gc_forwarded_objects_last = 0;
        osty_gc_forwarded_bytes_last = 0;
        return 0;
    }

    for (i = 0; i < root_slot_count; i++) {
        osty_gc_remap_slot((void *)root_slots[i]);
    }
    /* Walk the shadow-stack chain — same reason as the cheney slot
     * loop above. Without this the caller's slot still points to the
     * pre-compaction payload while the new clone lives at a different
     * address, so the next caller-side load reads garbage. */
    OSTY_GC_FOR_EACH_CHAIN_ROOT(osty_gc_remap_slot(_slot));
    for (i = 0; i < osty_gc_global_root_count; i++) {
        osty_gc_remap_slot(osty_gc_global_root_slots[i]);
    }

    /* Fused remap + replace walk. For each surviving header in the
     * LL: remap the canonical (clone if forwarded, else original) so
     * its payload pointers point at the post-compact addresses; if a
     * clone exists, splice it into the LL position and release the
     * original's storage. Used to be two separate passes —
     * `osty_gc_replace_header` does not modify `old_header->next/prev`,
     * so the iterator's pre-saved `next` stays valid across the
     * splice + release. */
    header = osty_gc_objects;
    while (header != NULL) {
        next = header->next;
        {
            osty_gc_header *replacement =
                osty_gc_forwarding_lookup(header->payload);
            osty_gc_header *current =
                replacement != NULL ? replacement : header;
            osty_gc_remap_header_payload(current);
            if (replacement != NULL) {
                osty_gc_replace_header(header, replacement);
                osty_gc_release_replaced_header(header);
            }
        }
        header = next;
    }
    osty_gc_remap_young_arena_payloads();
    osty_gc_forwarding_retain_history(snapshots, snapshot_count);
    free(snapshots);
    osty_gc_bump_recycle_empty_blocks(
        &osty_gc_bump_blocks, &osty_gc_bump_current,
        &osty_gc_bump_block_count, &osty_gc_bump_recycled_block_count_total,
        &osty_gc_bump_recycled_bytes_total, true);
    osty_gc_bump_recycle_empty_blocks(
        &osty_gc_survivor_bump_blocks, &osty_gc_survivor_bump_current,
        &osty_gc_survivor_bump_block_count,
        &osty_gc_survivor_bump_recycled_block_count_total,
        &osty_gc_survivor_bump_recycled_bytes_total, true);
    osty_gc_bump_recycle_empty_blocks(
        &osty_gc_old_bump_blocks, &osty_gc_old_bump_current,
        &osty_gc_old_bump_block_count,
        &osty_gc_old_bump_recycled_block_count_total,
        &osty_gc_old_bump_recycled_bytes_total, true);
    osty_gc_bump_recycle_empty_blocks(
        &osty_gc_pinned_bump_blocks, &osty_gc_pinned_bump_current,
        &osty_gc_pinned_bump_block_count,
        &osty_gc_pinned_bump_recycled_block_count_total,
        &osty_gc_pinned_bump_recycled_bytes_total, true);

    osty_gc_compaction_count_total += 1;
    osty_gc_forwarded_objects_last = moved;
    osty_gc_forwarded_bytes_last = moved_bytes;
    /* Same address-reuse hazard as cheney_swap_arenas: major compaction
     * releases the OLD bump regions back to the recycler, and the next
     * allocation can land at an address whose string-cache slot still
     * holds the pre-compaction tenant's len/hash. Wipe the cache so
     * lookups go through measure() once before re-caching. */
    osty_rt_string_cache_invalidate_all();
    return moved;
}

static int64_t osty_gc_compact_minor_with_stack_roots(
    void *const *root_slots, int64_t root_slot_count, int64_t promote_age) {
    osty_gc_header *header;
    osty_gc_header *next;
    osty_gc_forwarding_snapshot *snapshots;
    int64_t snapshot_count = 0;
    int64_t i;
    int64_t moved = 0;
    int64_t moved_bytes = 0;

    snapshots = osty_gc_forwarding_snapshot_take(&snapshot_count);
    osty_gc_forwarding_clear();
    osty_gc_survivor_bump_current = NULL;
    osty_gc_survivor_tlab_current = NULL;
    /* Fused sweep + clone walk over the young gen list. WHITE → free
     * via the standard sweep helpers; BLACK → reset colour and, when
     * movable, evacuate to survivor or old bump depending on the
     * promote age. Caller no longer runs a separate sweep loop. */
    header = osty_gc_young_head;
    while (header != NULL) {
        next = header->next_gen;
        if (header->color == OSTY_GC_COLOR_WHITE) {
            osty_gc_swept_count_total += 1;
            osty_gc_swept_bytes_total += header->byte_size;
            osty_gc_dispatch_destroy(header);
            osty_gc_unlink(header);
            osty_gc_reclaim_swept_header(header);
        } else {
            header->color = OSTY_GC_COLOR_WHITE;
            header->marked = false;
            if (osty_gc_header_is_movable(header)) {
                bool will_promote = (int64_t)header->age + 1 >= promote_age;
                osty_gc_header *clone = osty_gc_clone_header_to_bump_region(
                    header,
                    will_promote ? OSTY_GC_STORAGE_BUMP_OLD
                                 : OSTY_GC_STORAGE_BUMP_SURVIVOR,
                    will_promote ? &osty_gc_old_bump_blocks
                                 : &osty_gc_survivor_bump_blocks,
                    will_promote ? &osty_gc_old_bump_current
                                 : &osty_gc_survivor_bump_current,
                    will_promote ? &osty_gc_old_bump_block_count
                                 : &osty_gc_survivor_bump_block_count,
                    will_promote ? &osty_gc_old_bump_block_bytes_total
                                 : &osty_gc_survivor_bump_block_bytes_total,
                    will_promote ? &osty_gc_old_bump_alloc_count_total
                                 : &osty_gc_survivor_bump_alloc_count_total,
                    will_promote ? &osty_gc_old_bump_alloc_bytes_total
                                 : &osty_gc_survivor_bump_alloc_bytes_total);
                osty_gc_forwarding_insert(header->payload, clone);
                moved += 1;
                moved_bytes += header->byte_size;
            }
        }
        header = next;
    }
    if (moved == 0) {
        osty_gc_forwarding_retain_history(snapshots, snapshot_count);
        free(snapshots);
        osty_gc_bump_recycle_empty_blocks(
            &osty_gc_bump_blocks, &osty_gc_bump_current,
            &osty_gc_bump_block_count,
            &osty_gc_bump_recycled_block_count_total,
            &osty_gc_bump_recycled_bytes_total, true);
        osty_gc_bump_recycle_empty_blocks(
            &osty_gc_survivor_bump_blocks, &osty_gc_survivor_bump_current,
            &osty_gc_survivor_bump_block_count,
            &osty_gc_survivor_bump_recycled_block_count_total,
            &osty_gc_survivor_bump_recycled_bytes_total, true);
        osty_gc_bump_recycle_empty_blocks(
            &osty_gc_old_bump_blocks, &osty_gc_old_bump_current,
            &osty_gc_old_bump_block_count,
            &osty_gc_old_bump_recycled_block_count_total,
            &osty_gc_old_bump_recycled_bytes_total, true);
        osty_gc_bump_recycle_empty_blocks(
            &osty_gc_pinned_bump_blocks, &osty_gc_pinned_bump_current,
            &osty_gc_pinned_bump_block_count,
            &osty_gc_pinned_bump_recycled_block_count_total,
            &osty_gc_pinned_bump_recycled_bytes_total, true);
        osty_gc_forwarded_objects_last = 0;
        osty_gc_forwarded_bytes_last = 0;
        return 0;
    }

    for (i = 0; i < root_slot_count; i++) {
        osty_gc_remap_slot((void *)root_slots[i]);
    }
    /* Walk the shadow-stack chain — same reason as the cheney slot
     * loop above. Without this the caller's slot still points to the
     * pre-compaction payload while the new clone lives at a different
     * address, so the next caller-side load reads garbage. */
    OSTY_GC_FOR_EACH_CHAIN_ROOT(osty_gc_remap_slot(_slot));
    for (i = 0; i < osty_gc_global_root_count; i++) {
        osty_gc_remap_slot(osty_gc_global_root_slots[i]);
    }

    /* Fused remap + replace walk. For each surviving header in the
     * LL: remap the canonical (clone if forwarded, else original) so
     * its payload pointers point at the post-compact addresses; if a
     * clone exists, splice it into the LL position and release the
     * original's storage. Used to be two separate passes —
     * `osty_gc_replace_header` does not modify `old_header->next/prev`,
     * so the iterator's pre-saved `next` stays valid across the
     * splice + release. */
    header = osty_gc_objects;
    while (header != NULL) {
        next = header->next;
        {
            osty_gc_header *replacement =
                osty_gc_forwarding_lookup(header->payload);
            osty_gc_header *current =
                replacement != NULL ? replacement : header;
            osty_gc_remap_header_payload(current);
            if (replacement != NULL) {
                osty_gc_replace_header(header, replacement);
                osty_gc_release_replaced_header(header);
            }
        }
        header = next;
    }
    osty_gc_remap_young_arena_payloads();
    osty_gc_forwarding_retain_history(snapshots, snapshot_count);
    free(snapshots);
    osty_gc_bump_recycle_empty_blocks(
        &osty_gc_bump_blocks, &osty_gc_bump_current,
        &osty_gc_bump_block_count, &osty_gc_bump_recycled_block_count_total,
        &osty_gc_bump_recycled_bytes_total, true);
    osty_gc_bump_recycle_empty_blocks(
        &osty_gc_survivor_bump_blocks, &osty_gc_survivor_bump_current,
        &osty_gc_survivor_bump_block_count,
        &osty_gc_survivor_bump_recycled_block_count_total,
        &osty_gc_survivor_bump_recycled_bytes_total, true);
    osty_gc_bump_recycle_empty_blocks(
        &osty_gc_old_bump_blocks, &osty_gc_old_bump_current,
        &osty_gc_old_bump_block_count,
        &osty_gc_old_bump_recycled_block_count_total,
        &osty_gc_old_bump_recycled_bytes_total, true);
    osty_gc_bump_recycle_empty_blocks(
        &osty_gc_pinned_bump_blocks, &osty_gc_pinned_bump_current,
        &osty_gc_pinned_bump_block_count,
        &osty_gc_pinned_bump_recycled_block_count_total,
        &osty_gc_pinned_bump_recycled_bytes_total, true);

    osty_gc_compaction_count_total += 1;
    osty_gc_forwarded_objects_last = moved;
    osty_gc_forwarded_bytes_last = moved_bytes;
    return moved;
}

/* Phase A2 depth: monotonic timing helper. Delegates to
 * osty_rt_monotonic_ns, which is platform-guarded (POSIX clock_gettime
 * on Unix, QueryPerformanceCounter on Windows). Callers treat zero as
 * "no measurement recorded" (still strictly monotonic inside a run). */
static int64_t osty_gc_now_nanos(void) {
    return (int64_t)osty_rt_monotonic_ns();
}

/* Phase B remembered set maintenance after a minor collection.
 *
 * The post-write barrier logs (owner, value) edges as they land. Minor
 * GC only consumes entries where the owner is OLD; after the minor
 * finishes we compact the log so only still-relevant edges remain:
 *
 *   - owner must still be resident and OLD,
 *   - value must still be resident and YOUNG.
 *
 * Edges where the value was swept or promoted get dropped. The next
 * minor starts with a smaller, pre-filtered log. A major collection
 * clears the log wholesale (everything re-registers through the next
 * barrier), so this helper is a minor-only codepath. */
static void osty_gc_remembered_edges_compact_after_minor(void) {
    int64_t write_idx = 0;
    int64_t i;
    for (i = 0; i < osty_gc_remembered_edge_count; i++) {
        void *owner_payload =
            osty_gc_forward_payload(osty_gc_remembered_edges[i].owner);
        void *value_payload =
            osty_gc_forward_payload(osty_gc_remembered_edges[i].value);
        osty_gc_header *owner = osty_gc_find_header(owner_payload);
        osty_gc_header *value = osty_gc_find_header(value_payload);
        if (owner == NULL || value == NULL) {
            continue;
        }
        if (owner->generation != OSTY_GC_GEN_OLD) {
            continue;
        }
        if (value->generation != OSTY_GC_GEN_YOUNG) {
            continue;
        }
        osty_gc_remembered_edges[write_idx].owner = owner_payload;
        osty_gc_remembered_edges[write_idx].value = value_payload;
        write_idx += 1;
    }
    osty_gc_remembered_edge_count = write_idx;
    /* SATB log is purely for future concurrent marking; clear after every
     * STW cycle since the snapshot it encodes is of the just-completed
     * pass. */
    osty_gc_satb_log_count = 0;
}

/* Phase B major (full) collection. Scans every header regardless of
 * generation. Roots = manual root-binds + explicit pins + stack +
 * global. Phase D adds STW evacuation after sweep: movable survivors
 * are cloned, roots/object slots remapped, and `load_v1` retains a
 * forwarding table so stale payload pointers can still resolve. */
static void osty_gc_collect_major_with_stack_roots(void *const *root_slots, int64_t root_slot_count) {
    osty_gc_header *header;
    int64_t i;
    int64_t t_start;
    int64_t t_end;

    /* Phase C depth guard: STW major must not interleave with an
     * incremental mark — the in-flight mark stack would be stomped,
     * surviving greys silently swept. Callers are expected to finish
     * the incremental cycle first. The abort is intentional: reaching
     * here is a lifecycle bug in the caller, not an edge case. */
    if (osty_gc_state != OSTY_GC_STATE_IDLE) {
        osty_rt_abort("STW major invoked while incremental collection in progress");
    }
    t_start = osty_gc_now_nanos();

    /* 1. Seed from manual root binds + explicit pins. */
    header = osty_gc_objects;
    while (header != NULL) {
        if (osty_gc_header_is_pinned(header)) {
            osty_gc_mark_header(header);
        }
        header = header->next;
    }
    /* 2. Seed from the current safepoint's stack-slot descriptors. */
    for (i = 0; i < root_slot_count; i++) {
        osty_gc_mark_root_slot((void *)root_slots[i]);
    }
    /* Walk the shadow-stack chain so caller frames' managed locals get
     * marked as reachable. Without this, an inner safepoint would only
     * see its own roots, sweep the caller's Map/List/etc as garbage,
     * and free their backing storage out from under the caller. */
    OSTY_GC_FOR_EACH_CHAIN_ROOT(osty_gc_mark_root_slot(_slot));
    /* 3. Seed from registered global slots. */
    for (i = 0; i < osty_gc_global_root_count; i++) {
        osty_gc_mark_root_slot(osty_gc_global_root_slots[i]);
    }
    /* 4. Drain the work queue iteratively. */
    osty_gc_mark_drain();
    /* 5. Sweep + compact in a single fused walk. The compact's first
     *    walk now also frees WHITE headers and resets surviving BLACK
     *    colours (and Phase F card_dirty bits), which used to be a
     *    separate sweep pass over `osty_gc_objects`. Halves the
     *    post-mark heap-walk count. */
    (void)osty_gc_compact_major_with_stack_roots(root_slots, root_slot_count);
    osty_gc_collection_count += 1;
    osty_gc_major_count += 1;
    osty_gc_allocated_since_collect = 0;
    osty_gc_allocated_since_minor = 0;
    osty_gc_collection_requested = false;
    osty_gc_barrier_logs_clear();

    t_end = osty_gc_now_nanos();
    if (t_start != 0 && t_end >= t_start) {
        int64_t elapsed = t_end - t_start;
        osty_gc_collection_nanos_last = elapsed;
        osty_gc_collection_nanos_total += elapsed;
        osty_gc_major_nanos_total += elapsed;
        if (elapsed > osty_gc_collection_nanos_max) {
            osty_gc_collection_nanos_max = elapsed;
        }
    }
}

/* Phase B minor collection (RUNTIME_GC_DELTA §5.1-5.5).
 *
 * Scans only YOUNG objects. OLD objects are assumed live; any
 * OLD→YOUNG edge is preserved by walking the remembered set. Phase D
 * extends the survivor path so movable YOUNG objects can be copied into
 * survivor/old bump regions before the age/promote step runs. Frees
 * unreachable YOUNG; OLD untouched.
 */
static void osty_gc_collect_minor_with_stack_roots(void *const *root_slots, int64_t root_slot_count) {
    osty_gc_header *header;
    osty_gc_header *next;
    int64_t i;
    int64_t t_start;
    int64_t t_end;
    int64_t promote_age = osty_gc_promote_age_now();

    /* Phase C depth guard: minor must not share the mark stack with
     * an incremental major cycle — they would push into each other's
     * grey set and corrupt the colouring. Same policy as STW major. */
    if (osty_gc_state != OSTY_GC_STATE_IDLE) {
        osty_rt_abort("STW minor invoked while incremental collection in progress");
    }
    t_start = osty_gc_now_nanos();
    osty_gc_minor_in_progress = true;

    /* 1. Pinned/manual-rooted YOUNG headers only. OLD pinned objects are "live by
     *    assumption" for the minor and do not need to enter the work
     *    queue — any OLD→YOUNG reference they hold is captured by the
     *    remembered set below. Iterating the young list (not the full
     *    heap) is the Phase B2 depth win: iteration scales with
     *    nursery size, not total heap size. */
    header = osty_gc_young_head;
    while (header != NULL) {
        if (osty_gc_header_is_pinned(header)) {
            osty_gc_mark_header(header);
        }
        header = header->next_gen;
    }
    /* 2. Stack roots — any YOUNG pointer on the stack gets enqueued;
     *    mark_header filters OLD so this is implicitly YOUNG-only. */
    for (i = 0; i < root_slot_count; i++) {
        osty_gc_mark_root_slot((void *)root_slots[i]);
    }
    /* Walk the shadow-stack chain so caller frames' managed locals get
     * marked as reachable. Without this, an inner safepoint would only
     * see its own roots, sweep the caller's Map/List/etc as garbage,
     * and free their backing storage out from under the caller. */
    OSTY_GC_FOR_EACH_CHAIN_ROOT(osty_gc_mark_root_slot(_slot));
    /* 3. Global roots. */
    for (i = 0; i < osty_gc_global_root_count; i++) {
        osty_gc_mark_root_slot(osty_gc_global_root_slots[i]);
    }
    /* 4. Cross-generational roots. With card marking enabled (Phase F)
     *    we walk the dirty-OLD array — exactly the headers the barrier
     *    flagged since the last major. Their trace function's children
     *    flow through `osty_gc_mark_payload`, where the
     *    `osty_gc_minor_in_progress && generation == OLD` filter at
     *    `osty_gc_mark_header` keeps marking confined to YOUNG. With
     *    card marking disabled we fall back to the original per-edge
     *    log: every edge whose owner is OLD seeds
     *    `osty_gc_mark_payload(value)` directly.
     *
     *    Cards stay dirty across minors — a still-young reference must
     *    survive until major GC clears it, otherwise the next minor
     *    would not re-mark the reference and the value would be swept.
     *    Major GC clears `card_dirty` on survivors and resets the dirty
     *    array together. */
    if (osty_gc_card_marking_now()) {
        for (i = 0; i < osty_gc_dirty_old_count; i++) {
            osty_gc_dispatch_trace(osty_gc_dirty_old_headers[i]);
        }
        osty_gc_cards_scanned_total += osty_gc_dirty_old_count;
    } else {
        for (i = 0; i < osty_gc_remembered_edge_count; i++) {
            osty_gc_header *owner = osty_gc_find_header(osty_gc_remembered_edges[i].owner);
            if (owner == NULL || owner->generation != OSTY_GC_GEN_OLD) {
                continue;
            }
            osty_gc_mark_payload(osty_gc_remembered_edges[i].value);
        }
    }
    /* 5. Drain. */
    osty_gc_mark_drain();
    osty_gc_minor_in_progress = false;
    /* 6. Sweep + compact YOUNG in a single fused walk. The compact's
     *    first walk now also frees WHITE young headers and resets
     *    surviving BLACK colours, replacing what used to be a separate
     *    sweep loop over `osty_gc_young_head`. The promote/age pass
     *    below still iterates the post-compact young list. */
    (void)osty_gc_compact_minor_with_stack_roots(
        root_slots, root_slot_count, promote_age);
    header = osty_gc_young_head;
    while (header != NULL) {
        next = header->next_gen;
        if ((int64_t)header->age + 1 >= promote_age) {
            osty_gc_promote_header(header);
        } else if (header->age < UINT8_MAX) {
            header->age += 1;
        }
        header = next;
    }
    osty_gc_collection_count += 1;
    osty_gc_minor_count += 1;
    osty_gc_allocated_since_minor = 0;
    osty_gc_collection_requested = false;
    osty_gc_remembered_edges_compact_after_minor();

    /* Phase B5 depth: minor→major escalation. If the minor couldn't
     * drag young_bytes below the nursery cap (everything survived and
     * promoted), or the heap as a whole has crossed its major limit,
     * flag a major for the next dispatcher turn. Without this path a
     * pathological workload — every YOUNG survives, gets promoted,
     * fills OLD — would never trigger a major until a brand-new YOUNG
     * allocation happens to cross `THRESHOLD_BYTES`, stranding a large
     * floating OLD heap. */
    {
        int64_t nursery_limit = osty_gc_nursery_limit_now();
        int64_t pressure_limit = osty_gc_pressure_limit_now();
        bool nursery_still_hot = nursery_limit > 0 &&
                                 osty_gc_young_bytes >= nursery_limit;
        bool heap_over_major = pressure_limit > 0 &&
                               osty_gc_live_bytes >= pressure_limit;
        if (nursery_still_hot || heap_over_major) {
            osty_gc_collection_requested_major = true;
            osty_gc_collection_requested = true;
        }
    }

    t_end = osty_gc_now_nanos();
    if (t_start != 0 && t_end >= t_start) {
        int64_t elapsed = t_end - t_start;
        osty_gc_collection_nanos_last = elapsed;
        osty_gc_collection_nanos_total += elapsed;
        osty_gc_minor_nanos_total += elapsed;
        if (elapsed > osty_gc_collection_nanos_max) {
            osty_gc_collection_nanos_max = elapsed;
        }
    }
}

/* Phase B dispatcher. The auto-trigger path chooses minor by default;
 * crosses to major only when the heap is near its pressure limit (so
 * minor alone will not make progress) or when a major was explicitly
 * requested via `osty_gc_collection_requested_major`.
 *
 * `osty_gc_debug_collect` stays a forced-major call for backwards
 * compatibility with the Phase A test suite; tests that want to
 * exercise the minor path explicitly go through `_collect_minor`. */

/* Phase C depth: auto-dispatcher incremental opt-in. When the
 * `OSTY_GC_INCREMENTAL` env var is set to a non-zero value, major
 * collections chosen by the dispatcher route through the incremental
 * path instead of STW. Each safepoint contributes
 * `OSTY_GC_INCREMENTAL_BUDGET` units of mark work and finishes the
 * cycle once the queue empties. Minor collections are unchanged —
 * they remain STW on the young list.
 *
 * A value of 0 or an unset env reverts to the Phase B dispatcher
 * semantics (direct STW major), so the incremental path stays
 * opt-in until the depth pass landing adds the necessary guarantees
 * about worst-case pause budgets. */
#define OSTY_GC_INCREMENTAL_ENV "OSTY_GC_INCREMENTAL"
#define OSTY_GC_INCREMENTAL_BUDGET_ENV "OSTY_GC_INCREMENTAL_BUDGET"
#define OSTY_GC_INCREMENTAL_BUDGET_DEFAULT 64
static bool osty_gc_incremental_auto_loaded = false;
static bool osty_gc_incremental_auto_enabled = false;
static bool osty_gc_incremental_budget_loaded = false;
static int64_t osty_gc_incremental_budget = OSTY_GC_INCREMENTAL_BUDGET_DEFAULT;

static bool osty_gc_incremental_auto_now(void) {
    const char *value;
    if (osty_gc_incremental_auto_loaded) {
        return osty_gc_incremental_auto_enabled;
    }
    osty_gc_incremental_auto_loaded = true;
    value = getenv(OSTY_GC_INCREMENTAL_ENV);
    if (value == NULL || value[0] == '\0' ||
        strcmp(value, "0") == 0 || strcmp(value, "false") == 0 ||
        strcmp(value, "FALSE") == 0) {
        osty_gc_incremental_auto_enabled = false;
    } else {
        osty_gc_incremental_auto_enabled = true;
    }
    return osty_gc_incremental_auto_enabled;
}

static int64_t osty_gc_incremental_budget_now(void) {
    const char *value;
    char *end = NULL;
    long long parsed;
    if (osty_gc_incremental_budget_loaded) {
        return osty_gc_incremental_budget;
    }
    osty_gc_incremental_budget_loaded = true;
    value = getenv(OSTY_GC_INCREMENTAL_BUDGET_ENV);
    if (value == NULL || value[0] == '\0') {
        return osty_gc_incremental_budget;
    }
    parsed = strtoll(value, &end, 10);
    if (end == value || (end != NULL && *end != '\0') || parsed < 0) {
        osty_rt_abort("invalid " OSTY_GC_INCREMENTAL_BUDGET_ENV);
    }
    osty_gc_incremental_budget = (int64_t)parsed;
    return osty_gc_incremental_budget;
}

/* Forward declarations for the auto-dispatcher. */
static void osty_gc_incremental_seed_roots(void *const *root_slots, int64_t root_slot_count);
static void osty_gc_incremental_sweep(void);

static void osty_gc_auto_drive_incremental(void *const *root_slots, int64_t root_slot_count) {
    /* Caller holds `osty_gc_lock`. */
    if (osty_gc_state == OSTY_GC_STATE_IDLE) {
        /* Phase 0f: pre-grow the index before transitioning to
         * MARK_INCREMENTAL so concurrent probers in a future
         * lock-free tracer don't witness the buffer swap that grow
         * would otherwise do mid-cycle. Defer-grow logic in insert
         * relies on this giving enough headroom. */
        osty_gc_index_pregrow_for_cycle();
        /* Start a fresh cycle. */
        osty_gc_state = OSTY_GC_STATE_MARK_INCREMENTAL;
        osty_gc_incremental_seed_roots(root_slots, root_slot_count);
        /* Phase 0a: same kick path as the public start — the bg
         * marker helps drain between safepoints if this auto-drive
         * leaves the cycle open. */
        osty_gc_bg_marker_kick();
    }
    if (osty_gc_state == OSTY_GC_STATE_MARK_INCREMENTAL) {
        int64_t budget = osty_gc_incremental_budget_now();
        int64_t done = osty_gc_mark_drain_budget(budget);
        osty_gc_incremental_steps_total += 1;
        osty_gc_incremental_work_total += done;
        if (osty_gc_mark_stack_count == 0) {
            /* Cycle done — finish it inline. compact_major's first
             * walk now fuses the sweep, so no separate
             * `osty_gc_incremental_sweep()` call is needed when we
             * compact. */
            osty_gc_bg_marker_park();
            osty_gc_state = OSTY_GC_STATE_SWEEPING;
            (void)osty_gc_compact_major_with_stack_roots(
                root_slots, root_slot_count);
            osty_gc_collection_count += 1;
            osty_gc_major_count += 1;
            osty_gc_allocated_since_collect = 0;
            osty_gc_allocated_since_minor = 0;
            osty_gc_collection_requested = false;
            osty_gc_collection_requested_major = false;
            osty_gc_barrier_logs_clear();
            osty_gc_state = OSTY_GC_STATE_IDLE;
            /* Phase 0f: drain any deferred grow now that the cycle
             * has fully settled. compact_major reseeds the index
             * via its remap pass, so by this point insert pressure
             * should have subsided — but if the flag is still set,
             * grow now to keep the next cycle's load factor sane. */
            if (osty_gc_index_grow_pending) {
                osty_gc_index_grow(osty_gc_index_capacity * 2);
            }
        }
    }
}

static void osty_gc_collect_now_with_stack_roots(void *const *root_slots, int64_t root_slot_count) {
    int64_t pressure_limit = osty_gc_pressure_limit_now();
    bool needs_major = osty_gc_collection_requested_major;
    if (!needs_major && pressure_limit > 0) {
        if (osty_gc_live_bytes >= pressure_limit ||
            osty_gc_allocated_since_collect >= pressure_limit) {
            needs_major = true;
        }
    }
    /* Phase C: if a cycle is mid-flight, finish it regardless of
     * pressure signal — never leave MARK_INCREMENTAL hanging between
     * safepoints longer than necessary. */
    if (osty_gc_state == OSTY_GC_STATE_MARK_INCREMENTAL) {
        if (osty_gc_tinytag_young_now()) {
            osty_gc_collect_minor_cheney_with_stack_roots(
                root_slots, root_slot_count);
        }
        osty_gc_auto_drive_incremental(root_slots, root_slot_count);
        return;
    }
    /* Phase E follow-up: cheney runs FIRST on every cycle (minor or
     * major) before the headerful collector. The original Phase 8
     * step 1 wiring only ran cheney inside the minor branch, so when
     * heap pressure escalated to major the young arena went unswept
     * for that cycle and grew monotonically until the 256 MiB
     * reservation exhausted, at which point alloc_young returns NULL
     * and routing silently falls back to headerful. Hoisting the
     * cheney call out of the branch closes that perf cliff: every
     * collect tier reclaims dead young objects before doing whatever
     * else it was going to do. The two passes are independent —
     * cheney only touches young arena objects (cheney_forward passes
     * through non-young pointers), and the headerful minor/major
     * only touches the headerful linked lists (which never hold
     * young-eligible kinds once routing sends them to young arena). */
    if (osty_gc_tinytag_young_now()) {
        osty_gc_collect_minor_cheney_with_stack_roots(
            root_slots, root_slot_count);
    }
    if (needs_major) {
        if (osty_gc_incremental_auto_now()) {
            osty_gc_auto_drive_incremental(root_slots, root_slot_count);
        } else {
            osty_gc_collect_major_with_stack_roots(root_slots, root_slot_count);
            osty_gc_collection_requested_major = false;
        }
    } else {
        osty_gc_collect_minor_with_stack_roots(root_slots, root_slot_count);
    }
}

static void osty_gc_collect_now(void) {
    /* Match the dispatcher contract: cheney runs first to reclaim
     * young arena before whatever tier the headerful collector picks. */
    if (osty_gc_tinytag_young_now()) {
        osty_gc_collect_minor_cheney_with_stack_roots(NULL, 0);
    }
    osty_gc_collect_major_with_stack_roots(NULL, 0);
}

/* Phase C incremental major collection (RUNTIME_GC_DELTA §4.3).
 *
 * Splits the Phase B STW major into three callable phases:
 *
 *   1. `start`  — seeds roots (pinned + stack + global). State goes
 *                 IDLE → MARK_INCREMENTAL. Call exactly once per
 *                 cycle.
 *   2. `step`   — drains up to `budget` grey headers; each popped
 *                 header transitions GREY → BLACK and its children
 *                 get enqueued as GREY. Returns true while more work
 *                 is pending.
 *   3. `finish` — drains the remainder of the mark queue, runs the
 *                 sweep, and resets state to IDLE.
 *
 * Between step calls the mutator may allocate and execute write
 * barriers. `osty_gc_pre_write_v1` observes MARK_INCREMENTAL and
 * greys any managed pointer about to be overwritten so the mark is
 * not lost (SATB).
 *
 * The incremental path is additive — STW major and minor remain and
 * are preferred by the auto-dispatcher. Tests and future adaptive
 * triggers drive the incremental path directly. */
static void osty_gc_incremental_seed_roots(void *const *root_slots, int64_t root_slot_count) {
    osty_gc_header *header;
    int64_t i;
    header = osty_gc_objects;
    while (header != NULL) {
        if (osty_gc_header_is_pinned(header)) {
            osty_gc_mark_header(header);
        }
        header = header->next;
    }
    for (i = 0; i < root_slot_count; i++) {
        osty_gc_mark_root_slot((void *)root_slots[i]);
    }
    /* Walk the shadow-stack chain so caller frames' managed locals get
     * marked as reachable. Without this, an inner safepoint would only
     * see its own roots, sweep the caller's Map/List/etc as garbage,
     * and free their backing storage out from under the caller. */
    OSTY_GC_FOR_EACH_CHAIN_ROOT(osty_gc_mark_root_slot(_slot));
    for (i = 0; i < osty_gc_global_root_count; i++) {
        osty_gc_mark_root_slot(osty_gc_global_root_slots[i]);
    }
}

/* Phase 0e: cursor-based incremental sweep. The bg marker advances
 * `osty_gc_sweep_cursor` in batches once the mark queue empties so
 * the heap walk overlaps with mutator execution; finish_locked picks
 * up whatever the bg thread left to do (typically nothing) before
 * running compaction or transitioning to IDLE. The cursor reads
 * `next` BEFORE reclaim so a swept header's freed memory is never
 * dereferenced.
 *
 * `osty_gc_sweep_reclaim_done` distinguishes "cursor is NULL because
 * sweep finished" from "cursor is NULL because we haven't started"
 * — important when finish_locked decides whether to seed the cursor
 * and re-walk vs go straight to the survivor reset. */
static osty_gc_header *osty_gc_sweep_cursor = NULL;
static bool osty_gc_sweep_reclaim_done = false;
static int64_t osty_gc_sweep_steps_total = 0;
static int64_t osty_gc_bg_marker_swept_total = 0;

/* Reclaim-only sweep step: WHITEs are destroyed + reclaimed; survivors
 * (BLACK) are left untouched. The BLACK→WHITE reset for the next
 * cycle is owned by the caller — `compact_major_with_stack_roots`
 * does it as part of its fused sweep+clone walk; the non-compact
 * finish path calls `osty_gc_reset_survivors_walk` after the sweep
 * cursor reaches NULL. Splitting reset out of the per-step body
 * matters for Phase 0e because the bg marker can start sweeping
 * concurrently with the mutator — if we reset BLACK→WHITE inline,
 * `compact_major` (which expects BLACK survivors) would later see an
 * all-WHITE heap and free everything. Returns the number of headers
 * processed this step. Caller checks `osty_gc_sweep_cursor != NULL`
 * to decide whether more work remains. */
static int64_t osty_gc_incremental_sweep_step(int64_t budget) {
    osty_gc_header *header;
    osty_gc_header *next;
    int64_t done = 0;
    bool unlimited = budget <= 0;
    header = osty_gc_sweep_cursor;
    while (header != NULL && (unlimited || done < budget)) {
        next = header->next;
        /* Phase 0g: atomic load to pair with mark/SATB releases. The
         * sweep itself runs under `osty_gc_lock` so the value won't
         * change mid-iteration, but the ACQUIRE makes the colour
         * read happen-before any subsequent header field access (e.g.
         * `byte_size`, `dispatch_destroy`) that the WHITE branch does. */
        uint8_t color = __atomic_load_n(&header->color, __ATOMIC_ACQUIRE);
        if (color == OSTY_GC_COLOR_WHITE) {
            osty_gc_swept_count_total += 1;
            osty_gc_swept_bytes_total += header->byte_size;
            osty_gc_dispatch_destroy(header);
            osty_gc_unlink(header);
            osty_gc_reclaim_swept_header(header);
        }
        /* Survivors (BLACK) are left as-is — caller resets them. */
        header = next;
        done += 1;
    }
    osty_gc_sweep_cursor = header;
    osty_gc_sweep_steps_total += 1;
    return done;
}

/* Walk the heap once and reset every survivor's color to WHITE so the
 * next cycle starts from the standard baseline. Used by the
 * non-compact finish path after sweep_step has reclaimed all WHITEs.
 * compact_major's fused walk does its own BLACK→WHITE reset, so this
 * helper isn't needed on the compaction path. */
static void osty_gc_reset_survivors_walk(void) {
    osty_gc_header *header = osty_gc_objects;
    while (header != NULL) {
        uint8_t color = __atomic_load_n(&header->color, __ATOMIC_ACQUIRE);
        if (color != OSTY_GC_COLOR_WHITE) {
            __atomic_store_n(&header->color, OSTY_GC_COLOR_WHITE,
                             __ATOMIC_RELEASE);
            __atomic_store_n(&header->marked, false, __ATOMIC_RELAXED);
            header->card_dirty = 0;
        }
        header = header->next;
    }
    osty_gc_dirty_old_count = 0;
}

static void osty_gc_incremental_sweep(void) {
    /* Synchronous fallback. Three states matter on entry:
     *   - cursor != NULL:  sweep already in progress (bg started, did
     *                       not finish). Continue from the cursor.
     *   - cursor == NULL && reclaim_done: bg finished sweep already.
     *                       Skip the reclaim walk; only the survivor
     *                       reset is needed.
     *   - cursor == NULL && !reclaim_done: nothing started — seed and
     *                       run a full sweep + survivor reset. */
    if (!osty_gc_sweep_reclaim_done && osty_gc_sweep_cursor == NULL) {
        osty_gc_sweep_cursor = osty_gc_objects;
    }
    while (osty_gc_sweep_cursor != NULL) {
        (void)osty_gc_incremental_sweep_step(0);
    }
    osty_gc_sweep_reclaim_done = true;
    osty_gc_reset_survivors_walk();
}

/* Public incremental entry points. The `_with_stack_roots` variant is
 * the real worker; the no-arg `osty_gc_collect_incremental_start`
 * calls it with an empty stack-slot array so tests / callers without
 * visible frame descriptors can still drive the machinery. */
void osty_gc_collect_incremental_start_with_stack_roots(void *const *root_slots, int64_t root_slot_count) {
    /* Phase 0b: when user worker threads are live, drive an STW
     * handshake so every parked mutator's roots get folded into the
     * cycle's seed set. Single-threaded path stays branch-free.
     *
     * The handshake itself is the only STW window for cycle start —
     * after `seed_roots` returns and we kick the bg marker, mutators
     * resume and mark proceeds concurrently in the bg thread. */
    bool multi_thread = osty_rt_concurrent_workers_load() > 0;
    osty_gc_stw_handshake_state hs = {0};
    void *const *seed_roots = root_slots;
    int64_t seed_count = root_slot_count;
    if (multi_thread) {
        osty_gc_stw_handshake_acquire(root_slots, root_slot_count, &hs);
        seed_roots = (void *const *)hs.flat_roots;
        seed_count = hs.total_count;
    }
    osty_gc_acquire();
    if (osty_gc_state != OSTY_GC_STATE_IDLE) {
        osty_gc_release();
        if (multi_thread) {
            osty_gc_stw_handshake_release(&hs);
        }
        osty_rt_abort("incremental start called while collection already in progress");
    }
    /* Phase 0f: pre-grow the index now (state still IDLE, no
     * concurrent prober). After the state transition, in-cycle
     * inserts that hit the load threshold defer to the
     * grow_pending flag instead of swapping the buffer. */
    osty_gc_index_pregrow_for_cycle();
    osty_gc_state = OSTY_GC_STATE_MARK_INCREMENTAL;
    osty_gc_incremental_seed_roots(seed_roots, seed_count);
    /* Phase 0a: kick the background marker after seeding so it has
     * grey work waiting. The kick promotes the cycle from
     * "budgeted-incremental" (mutator-driven) to truly concurrent —
     * the bg thread starts draining while the mutator returns from
     * this call. Lock-skip fast paths (alloc, load, post_write) all
     * gate on `osty_gc_serialized_now()`, which observes the active
     * flag set inside `bg_marker_kick`. */
    osty_gc_bg_marker_kick();
    osty_gc_release();
    if (multi_thread) {
        osty_gc_stw_handshake_release(&hs);
    }
}

/* Returns true if more mark work remains; false when the queue is
 * empty and the caller should transition to sweep via
 * `osty_gc_collect_incremental_finish`. */
bool osty_gc_collect_incremental_step(int64_t budget) {
    bool has_more = false;
    osty_gc_acquire();
    if (osty_gc_state != OSTY_GC_STATE_MARK_INCREMENTAL) {
        osty_gc_release();
        return false;
    }
    int64_t done = osty_gc_mark_drain_budget(budget);
    osty_gc_incremental_steps_total += 1;
    osty_gc_incremental_work_total += done;
    has_more = osty_gc_mark_stack_count > 0;
    osty_gc_release();
    return has_more;
}

/* Shared finish body for the incremental major path. Drains any leftover
 * greys, runs the sweep, and (when `compact` is true) evacuates movable
 * survivors with the caller-supplied stack roots for remap. Runs under
 * `osty_gc_lock`.
 *
 * Why the `compact` flag exists: the concurrent scheduler drives a
 * finish without exposing the mutators' frame descriptors (they are
 * parked at safepoints across many threads). Remapping global / object
 * slots without also remapping those stack frames would leave dangling
 * pointers on every parked thread's stack. Compaction is therefore only
 * safe when the caller knows every live managed stack slot — today that
 * means the single-threaded test / LLVM-emitted path via
 * `osty_gc_collect_incremental_finish_with_stack_roots`. */
static void osty_gc_collect_incremental_finish_locked(
    void *const *root_slots, int64_t root_slot_count, bool compact) {
    /* Phase 0a: park the background marker before the final drain.
     * Clearing `bg_marker_active` makes the marker exit its drain loop
     * the next time it wakes from a nap; the GC lock we hold here
     * prevents any in-flight bg drain from racing with the work below.
     * Park is a no-op when bg-marker is disabled or never started. */
    osty_gc_bg_marker_park();
    /* Drain any remaining greys so the sweep sees a consistent
     * colouring. The incremental barrier (SATB pre_write) could have
     * dropped new greys on us between the last step and this call. */
    (void)osty_gc_mark_drain_budget(0);
    /* Phase 0e: bg marker may have already transitioned to SWEEPING
     * and made progress (or finished) on the heap walk. Take the
     * union of "wherever the cycle currently is" and "what finish
     * needs to do" so we don't restart sweep from scratch when bg
     * already did some of it. */
    if (osty_gc_state == OSTY_GC_STATE_MARK_INCREMENTAL) {
        osty_gc_state = OSTY_GC_STATE_SWEEPING;
        osty_gc_sweep_cursor = osty_gc_objects;
        osty_gc_sweep_reclaim_done = false;
    }
    if (compact) {
        /* Phase D: incremental major matches STW major — evacuate
         * movable survivors and remap every slot the STW path would
         * remap. Safety: SWEEPING state plus the lock, no mutator
         * barrier mid-flight, compact_major never re-enters the
         * mark queue. compact_major's first walk fuses the sweep
         * (any unfinished WHITEs left by bg) and the BLACK→WHITE
         * survivor reset + clone evacuation. */
        (void)osty_gc_compact_major_with_stack_roots(
            root_slots, root_slot_count);
    } else {
        /* No-stack-roots / concurrent finish path: compaction is
         * unsafe (parked-thread frames not visible). Run the
         * standalone sweep, which finishes whatever bg started and
         * then resets surviving BLACKs to WHITE for the next cycle. */
        osty_gc_incremental_sweep();
    }
    osty_gc_sweep_cursor = NULL;
    osty_gc_sweep_reclaim_done = false;
    osty_gc_collection_count += 1;
    osty_gc_major_count += 1;
    osty_gc_allocated_since_collect = 0;
    osty_gc_allocated_since_minor = 0;
    osty_gc_collection_requested = false;
    osty_gc_collection_requested_major = false;
    osty_gc_barrier_logs_clear();
    osty_gc_state = OSTY_GC_STATE_IDLE;
    /* Phase 0f: drain any deferred grow now that no concurrent
     * prober can be looking at the old buffer. The pre-grow at
     * cycle start usually keeps the load factor in check, so the
     * flag is rarely set; when it is, alloc-heavy workloads still
     * reach a healthy capacity within a cycle or two. */
    if (osty_gc_index_grow_pending) {
        osty_gc_index_grow(osty_gc_index_capacity * 2);
    }
}

void osty_gc_collect_incremental_finish(void) {
    int64_t t_start = osty_gc_now_nanos();
    /* Phase 0b: park mutators before the final drain + sweep so SATB
     * barriers have flushed and no new edges race the sweep. The
     * compact path is skipped here (`compact = false`) because we
     * lack frame descriptors for the caller — callers that need
     * compaction must use the `_with_stack_roots` variant. */
    bool multi_thread = osty_rt_concurrent_workers_load() > 0;
    osty_gc_stw_handshake_state hs = {0};
    if (multi_thread) {
        osty_gc_stw_handshake_acquire(NULL, 0, &hs);
    }
    osty_gc_acquire();
    if (osty_gc_state == OSTY_GC_STATE_IDLE) {
        osty_gc_release();
        if (multi_thread) {
            osty_gc_stw_handshake_release(&hs);
        }
        return;
    }
    osty_gc_collect_incremental_finish_locked(NULL, 0, false);
    osty_gc_release();
    if (multi_thread) {
        osty_gc_stw_handshake_release(&hs);
    }

    int64_t t_end = osty_gc_now_nanos();
    if (t_start != 0 && t_end >= t_start) {
        int64_t elapsed = t_end - t_start;
        osty_gc_collection_nanos_last = elapsed;
        osty_gc_collection_nanos_total += elapsed;
        osty_gc_major_nanos_total += elapsed;
        if (elapsed > osty_gc_collection_nanos_max) {
            osty_gc_collection_nanos_max = elapsed;
        }
    }
}

/* Phase D addition: callers that have live managed stack slots at the
 * finish point can drive an incremental major that also compacts and
 * remaps those slots, matching `osty_gc_collect_major_with_stack_roots`.
 * Symmetrical to `osty_gc_collect_incremental_start_with_stack_roots`. */
void osty_gc_collect_incremental_finish_with_stack_roots(
    void *const *root_slots, int64_t root_slot_count) {
    int64_t t_start = osty_gc_now_nanos();
    /* Phase 0b: park mutators and union their published roots with
     * the caller's so compaction can remap every live stack slot.
     * Without the union, the compaction step in `finish_locked` would
     * leave dangling pointers on parked mutators' stacks. */
    bool multi_thread = osty_rt_concurrent_workers_load() > 0;
    osty_gc_stw_handshake_state hs = {0};
    void *const *finish_roots = root_slots;
    int64_t finish_count = root_slot_count;
    if (multi_thread) {
        osty_gc_stw_handshake_acquire(root_slots, root_slot_count, &hs);
        finish_roots = (void *const *)hs.flat_roots;
        finish_count = hs.total_count;
    }
    osty_gc_acquire();
    if (osty_gc_state == OSTY_GC_STATE_IDLE) {
        osty_gc_release();
        if (multi_thread) {
            osty_gc_stw_handshake_release(&hs);
        }
        return;
    }
    osty_gc_collect_incremental_finish_locked(
        finish_roots, finish_count, true);
    osty_gc_release();
    if (multi_thread) {
        osty_gc_stw_handshake_release(&hs);
    }

    int64_t t_end = osty_gc_now_nanos();
    if (t_start != 0 && t_end >= t_start) {
        int64_t elapsed = t_end - t_start;
        osty_gc_collection_nanos_last = elapsed;
        osty_gc_collection_nanos_total += elapsed;
        osty_gc_major_nanos_total += elapsed;
        if (elapsed > osty_gc_collection_nanos_max) {
            osty_gc_collection_nanos_max = elapsed;
        }
    }
}

/* Phase 0a: background marker thread main loop.
 *
 * Outer loop: cv-wait until `bg_marker_active` flips to 1 (or shutdown
 * is signalled — currently the runtime has no clean shutdown so the
 * thread lives until process exit).
 *
 * Inner loop: while active, repeatedly grab `osty_gc_lock`, drain a
 * bounded budget of greys, then release. When the queue drains we nap
 * briefly so SATB pre_writes can land — they push under the same lock
 * and grow the queue back. When `active` flips to 0 we exit the inner
 * loop and re-suspend.
 *
 * State observation: the marker reads `osty_gc_state` under the GC
 * lock, so it never trips on a transient SWEEPING state set by
 * `incremental_finish_locked`. The `active` flag is flipped to 0
 * before that transition, which is the primary exit signal. */
static void *osty_gc_bg_marker_main(void *arg) {
    /* arg encodes the worker_id (0..N-1) for per-worker counters. */
    osty_gc_bg_marker_worker_id = (int)(intptr_t)arg;
    int64_t budget = osty_gc_bg_marker_budget_now();
    /* Phase 0d: when multiple workers share the queue, drain in
     * smaller per-iteration batches so the lock cycles more often
     * and siblings actually get a turn. The N==1 path keeps the full
     * budget for max throughput (no contention to share). The factor
     * 4× is heuristic — small enough that N=4 workers get multiple
     * turns on a few-hundred-item graph, large enough to amortise the
     * per-batch acquire/release cycle. */
    int n = osty_gc_bg_marker_worker_count;
    int64_t per_iter = budget;
    if (n >= 2) {
        per_iter = budget / 4;
        if (per_iter < 64) per_iter = 64;
        if (per_iter > budget) per_iter = budget;
    }
    for (;;) {
        osty_rt_mu_lock(&osty_gc_bg_marker_mu);
        while (__atomic_load_n(&osty_gc_bg_marker_active,
                               __ATOMIC_ACQUIRE) == 0) {
            osty_rt_cond_wait(&osty_gc_bg_marker_cv,
                              &osty_gc_bg_marker_mu);
        }
        osty_rt_mu_unlock(&osty_gc_bg_marker_mu);

        for (;;) {
            if (__atomic_load_n(&osty_gc_bg_marker_active,
                                __ATOMIC_ACQUIRE) == 0) {
                break;
            }
            osty_gc_acquire();
            if (osty_gc_state == OSTY_GC_STATE_MARK_INCREMENTAL) {
                int64_t done = osty_gc_mark_drain_budget(per_iter);
                /* Atomic adds — N>=2 workers race here. The per-worker
                 * slot is single-writer so a relaxed add suffices; the
                 * aggregate counter aggregates across writers so it
                 * needs RELAXED + ACQ_REL semantics on test reads (we
                 * use ACQUIRE on the reader side). */
                (void)__atomic_add_fetch(&osty_gc_bg_marker_drained_total,
                                         done, __ATOMIC_RELAXED);
                (void)__atomic_add_fetch(&osty_gc_bg_marker_loops_total,
                                         1, __ATOMIC_RELAXED);
                if (osty_gc_bg_marker_worker_id >= 0 &&
                    osty_gc_bg_marker_worker_id <
                        OSTY_GC_BG_MARKER_MAX_WORKERS) {
                    osty_gc_bg_marker_per_worker_drained
                        [osty_gc_bg_marker_worker_id] += done;
                }
                int64_t remaining = osty_gc_mark_stack_count;
                /* Phase 0e: when the mark queue empties under our lock,
                 * transition the cycle to SWEEPING and seed the cursor
                 * so the very next iteration picks up sweep work. The
                 * transition is one-way and one-shot per cycle —
                 * subsequent workers landing here see state=SWEEPING
                 * and skip mark, going straight to the sweep branch. */
                if (remaining == 0) {
                    osty_gc_state = OSTY_GC_STATE_SWEEPING;
                    osty_gc_sweep_cursor = osty_gc_objects;
                    osty_gc_release();
                    /* No nap — keep going so we hit the sweep branch
                     * on the next iteration without a 200µs delay. */
                    osty_rt_plat_yield();
                    continue;
                }
                osty_gc_release();
                osty_rt_plat_yield();
                continue;
            }
            if (osty_gc_state == OSTY_GC_STATE_SWEEPING) {
                /* Phase 0e: concurrent sweep. Bg marker advances the
                 * cursor in batches; finish_locked picks up whatever's
                 * left (typically nothing) under its STW handshake.
                 * When the cursor reaches NULL we set the
                 * `reclaim_done` flag and leave the cycle in SWEEPING
                 * — finish_locked handles the post-cycle bookkeeping
                 * + IDLE transition under STW so generation counters,
                 * barrier-log clear, and survivor reset happen in one
                 * consistent step. */
                int64_t done = osty_gc_incremental_sweep_step(per_iter);
                (void)__atomic_add_fetch(&osty_gc_bg_marker_swept_total,
                                         done, __ATOMIC_RELAXED);
                (void)__atomic_add_fetch(&osty_gc_bg_marker_loops_total,
                                         1, __ATOMIC_RELAXED);
                bool more = osty_gc_sweep_cursor != NULL;
                if (!more) {
                    osty_gc_sweep_reclaim_done = true;
                }
                osty_gc_release();
                if (!more) {
                    /* Sweep reclaim done — nap so finish_locked can
                     * run the survivor reset + IDLE transition. */
                    (void)__atomic_add_fetch(&osty_gc_bg_marker_naps_total,
                                             1, __ATOMIC_RELAXED);
                    osty_rt_sleep_ns(OSTY_GC_BG_MARKER_IDLE_NAP_NS);
                } else {
                    osty_rt_plat_yield();
                }
                continue;
            }
            /* IDLE or some unexpected state — nothing to do, exit drain. */
            osty_gc_release();
            break;
        }
    }
}

static void osty_gc_bg_marker_lazy_init(void) {
    if (osty_rt_mu_init(&osty_gc_bg_marker_mu) != 0) {
        osty_rt_abort("bg marker: mutex init failed");
    }
    if (osty_rt_cond_init(&osty_gc_bg_marker_cv) != 0) {
        osty_rt_abort("bg marker: cond init failed");
    }
}

static osty_rt_once_t osty_gc_bg_marker_once = OSTY_RT_ONCE_INIT;

/* Caller holds `osty_gc_lock`. Lazy: starts N marker threads on first
 * call (N from `OSTY_GC_BG_WORKERS`, default 1, capped at
 * `OSTY_GC_BG_MARKER_MAX_WORKERS`), no-ops on subsequent calls.
 * Enables the runtime-concurrency flag so `osty_rt_map_lock` /
 * `osty_rt_map_unlock` engage their per-map mutexes — the marker
 * traces map internals concurrently with mutator inserts, and the
 * per-map mutex serializes index/slot reallocation. */
static void osty_gc_bg_marker_ensure_started(void) {
    osty_rt_once(&osty_gc_bg_marker_once, osty_gc_bg_marker_lazy_init);
    if (osty_gc_bg_marker_started) {
        return;
    }
    int n = osty_gc_bg_workers_now();
    if (n < 1) n = 1;
    if (n > OSTY_GC_BG_MARKER_MAX_WORKERS) {
        n = OSTY_GC_BG_MARKER_MAX_WORKERS;
    }
    for (int i = 0; i < n; i++) {
        if (osty_rt_thread_start(&osty_gc_bg_marker_threads[i],
                                 osty_gc_bg_marker_main,
                                 (void *)(intptr_t)i) != 0) {
            osty_rt_abort("bg marker: thread start failed");
        }
    }
    osty_gc_bg_marker_worker_count = n;
    osty_gc_bg_marker_started = 1;
    osty_rt_enable_concurrency_runtime();
}

/* Caller holds `osty_gc_lock`. Sets active=1 and broadcasts the cv so
 * every marker thread wakes from its outer cv-wait. Broadcast (not
 * signal) because N >= 1 workers may be parked. */
static void osty_gc_bg_marker_kick(void) {
    if (!osty_gc_bg_marker_enabled_now()) {
        return;
    }
    osty_gc_bg_marker_ensure_started();
    osty_rt_mu_lock(&osty_gc_bg_marker_mu);
    __atomic_store_n(&osty_gc_bg_marker_active, 1, __ATOMIC_RELEASE);
    (void)__atomic_add_fetch(&osty_gc_bg_marker_kicks_total,
                             1, __ATOMIC_RELAXED);
    osty_rt_cond_broadcast(&osty_gc_bg_marker_cv);
    osty_rt_mu_unlock(&osty_gc_bg_marker_mu);
}

/* Caller holds `osty_gc_lock`. Clears active so the marker exits its
 * drain loop on the next iteration. No cv signal needed: if the marker
 * is in cond_wait it stays there until the next kick; if it's in the
 * drain loop the atomic load picks up the cleared flag. */
static void osty_gc_bg_marker_park(void) {
    if (!osty_gc_bg_marker_started) {
        return;
    }
    __atomic_store_n(&osty_gc_bg_marker_active, 0, __ATOMIC_RELEASE);
}

static bool osty_gc_safepoint_stress_enabled_now(void) {
    const char *value;

    if (osty_gc_safepoint_stress_loaded) {
        return osty_gc_safepoint_stress_enabled;
    }
    osty_gc_safepoint_stress_loaded = true;
    value = getenv("OSTY_GC_STRESS");
    if (value == NULL || value[0] == '\0' || strcmp(value, "0") == 0 || strcmp(value, "false") == 0 || strcmp(value, "FALSE") == 0) {
        osty_gc_safepoint_stress_enabled = false;
    } else {
        osty_gc_safepoint_stress_enabled = true;
    }
    return osty_gc_safepoint_stress_enabled;
}

static osty_rt_list *osty_rt_list_cast(void *raw_list) {
    if (raw_list == NULL) {
        osty_rt_abort("list is null");
    }
    return (osty_rt_list *)osty_gc_load_v1(raw_list);
}

/* OSTY_HOT_INLINE: called from every push/get/set on a typed list.
 * Body is two field-equality checks + one type-mismatch abort path
 * (cold). Inlining lets ThinLTO see the wrapper's constant
 * `elem_size` and `trace_elem` arguments and DCE the comparisons
 * to a no-op once the layout is locked in (which happens on the
 * first push). The IR-side caller then sees reserve+memcpy as a
 * single inline blob. */
static OSTY_HOT_INLINE void osty_rt_list_ensure_layout(osty_rt_list *list, size_t elem_size, osty_rt_trace_slot_fn trace_elem) {
    if (list->elem_size == 0) {
        list->elem_size = elem_size;
        list->trace_elem = trace_elem;
        return;
    }
    if (list->elem_size != elem_size) {
        osty_rt_abort("list element size mismatch");
    }
    if (list->trace_elem != trace_elem) {
        osty_rt_abort("list element trace-kind mismatch");
    }
}

/* OSTY_HOT_INLINE: companion to `ensure_layout` for struct-element
 * lists (List<Record>). Same pattern — first call locks in the
 * offset table, subsequent calls hit the equality short-circuit.
 * Inlining lets the offset comparison loop fold to nothing once
 * the count and pointer values are constant from the wrapper's
 * call site. */
static OSTY_HOT_INLINE void osty_rt_list_ensure_gc_offsets(osty_rt_list *list, const int64_t *gc_offsets, int64_t gc_offset_count) {
    int64_t i;

    if (gc_offset_count < 0) {
        osty_rt_abort("negative list GC offset count");
    }
    if (gc_offset_count == 0) {
        if (list->gc_offset_count != 0) {
            osty_rt_abort("list GC offset count mismatch");
        }
        return;
    }
    if (gc_offsets == NULL) {
        osty_rt_abort("list GC offsets pointer is null");
    }
    if (list->elem_size < sizeof(void *)) {
        osty_rt_abort("list element size too small for GC offsets");
    }
    if (list->gc_offset_count == 0) {
        list->gc_offsets = (int64_t *)malloc((size_t)gc_offset_count * sizeof(int64_t));
        if (list->gc_offsets == NULL) {
            osty_rt_abort("out of memory");
        }
        for (i = 0; i < gc_offset_count; i++) {
            if (gc_offsets[i] < 0 || (size_t)gc_offsets[i] > list->elem_size - sizeof(void *)) {
                osty_rt_abort("list GC offset out of range");
            }
            list->gc_offsets[i] = gc_offsets[i];
        }
        list->gc_offset_count = gc_offset_count;
        return;
    }
    if (list->gc_offset_count != gc_offset_count) {
        osty_rt_abort("list GC offset count mismatch");
    }
    for (i = 0; i < gc_offset_count; i++) {
        if (list->gc_offsets[i] != gc_offsets[i]) {
            osty_rt_abort("list GC offsets mismatch");
        }
    }
}

/* OSTY_HOT_INLINE: hottest cross-TU call left in the suite — 95
 * sites in log-aggregator's hot loop, 61 in lru-sim, 31 in
 * markdown-stats. Body is a small fast-path (`min_cap <= cap`
 * early return) wrapping a cold growth path (inline-spill or
 * realloc). Inlining lets ThinLTO peel the fast path at every
 * push call site so the steady-state `out.push(...)` lowering
 * collapses to the load-cap + compare + memcpy + store-len
 * sequence the IR call site expected post-#906. The cold growth
 * path stays out-of-line via the early return so we don't bloat
 * IR for the 99% case where cap was already enough. */
static OSTY_HOT_INLINE void osty_rt_list_reserve(osty_rt_list *list, int64_t min_cap) {
    int64_t next_cap = list->cap;
    void *next_data;
    size_t want_bytes;

    if (min_cap <= list->cap) {
        return;
    }
    if (list->elem_size == 0) {
        osty_rt_abort("list element size is zero");
    }
    /* Inline-storage fast path. While the requested element count
     * fits in OSTY_RT_LIST_INLINE_BYTES, just bump `cap` to the
     * inline ceiling — no allocation. `data` was set to
     * `inline_storage` by `osty_rt_list_new` and stays there until
     * the first time we spill to heap below. The first spill copies
     * the inline contents into the new heap buffer; subsequent
     * grows are plain `realloc`s. */
    int64_t inline_cap = (int64_t)(OSTY_RT_LIST_INLINE_BYTES / list->elem_size);
    if (list->cap == 0 && list->data == list->inline_storage && min_cap <= inline_cap) {
        list->cap = inline_cap;
        return;
    }
    if (next_cap < 4) {
        next_cap = 4;
    }
    while (next_cap < min_cap) {
        if (next_cap > INT64_MAX / 2) {
            next_cap = min_cap;
            break;
        }
        next_cap *= 2;
    }
    want_bytes = (size_t)next_cap * list->elem_size;
    if (list->elem_size != 0 && want_bytes / list->elem_size != (size_t)next_cap) {
        osty_rt_abort("list allocation overflow");
    }
    /* Spill from inline → heap: malloc + memcpy the inline contents.
     * Subsequent calls hit the regular realloc path because `data`
     * now points outside the inline region. */
    if (list->data == list->inline_storage) {
        next_data = malloc(want_bytes);
        if (next_data == NULL) {
            osty_rt_abort("out of memory");
        }
        if (list->len > 0) {
            memcpy(next_data, list->inline_storage,
                   (size_t)list->len * list->elem_size);
        }
    } else {
        next_data = realloc(list->data, want_bytes);
        if (next_data == NULL) {
            osty_rt_abort("out of memory");
        }
    }
    list->data = (unsigned char *)next_data;
    list->cap = next_cap;
}

static void osty_rt_list_emit_copied_write_barriers(void *raw_list, osty_rt_list *list, int64_t count) {
    if (count <= 0) {
        return;
    }
    if (list->elem_size == sizeof(void *) && list->trace_elem != NULL) {
        int64_t i;
        for (i = 0; i < count; i++) {
            void *child = NULL;
            memcpy(&child,
                   list->data + (size_t)i * list->elem_size,
                   sizeof(child));
            if (child != NULL) {
                osty_gc_post_write_v1(raw_list, child, OSTY_GC_KIND_LIST);
            }
        }
        return;
    }
    if (list->gc_offset_count > 0) {
        int64_t i;
        int64_t j;
        for (i = 0; i < count; i++) {
            unsigned char *elem = list->data + (size_t)i * list->elem_size;
            for (j = 0; j < list->gc_offset_count; j++) {
                void *child = NULL;
                memcpy(&child,
                       elem + (size_t)list->gc_offsets[j],
                       sizeof(child));
                if (child != NULL) {
                    osty_gc_post_write_v1(raw_list, child, OSTY_GC_KIND_LIST);
                }
            }
        }
    }
}

static void osty_rt_list_copy_initialized(void *raw_list, osty_rt_list *list, const unsigned char *src, int64_t count) {
    if (count <= 0) {
        list->len = 0;
        return;
    }
    if (src == NULL) {
        osty_rt_abort("list copy source is null");
    }
    osty_rt_list_reserve(list, count);
    memcpy(list->data, src, (size_t)count * list->elem_size);
    list->len = count;
    osty_rt_list_emit_copied_write_barriers(raw_list, list, count);
}

/* OSTY_HOT_INLINE: previously the inner helper for `list_push_ptr` /
 * `_push_i64` / `_push_bytes_v1`. Once those wrappers became
 * always-inline (see `list_push_ptr`'s comment) this body became
 * the new boundary and was visible at 90 call sites in the
 * log-aggregator hot loop. Inlining it lets ThinLTO see the entire
 * push at the user-code call site so the elem_size + trace_elem
 * arguments fold to constants and the resize-or-store branch
 * specializes per element type. */
static OSTY_HOT_INLINE void osty_rt_list_push_raw(void *raw_list, const void *value, size_t elem_size, osty_rt_trace_slot_fn trace_elem) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    osty_rt_list_ensure_layout(list, elem_size, trace_elem);
    osty_rt_list_reserve(list, list->len + 1);
    memcpy(list->data + ((size_t)list->len * list->elem_size), value, elem_size);
    list->len += 1;
}

static void *osty_rt_list_get_raw(void *raw_list, int64_t index, size_t elem_size, osty_rt_trace_slot_fn trace_elem) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    if (index < 0 || index >= list->len) {
        osty_rt_abort("list index out of range");
    }
    osty_rt_list_ensure_layout(list, elem_size, trace_elem);
    return list->data + ((size_t)index * list->elem_size);
}

static void osty_rt_list_set_raw(void *raw_list, int64_t index, const void *value, size_t elem_size, osty_rt_trace_slot_fn trace_elem) {
    void *slot = osty_rt_list_get_raw(raw_list, index, elem_size, trace_elem);
    memcpy(slot, value, elem_size);
}

static char *osty_rt_string_dup_site(const char *start, size_t len, const char *site) {
    char *out = (char *)osty_gc_allocate_managed(len + 1, OSTY_GC_KIND_STRING, site, NULL, NULL);
    /* `start` may be an inline-tagged pointer (caller spliced bytes
     * out of a Split piece without decoding). `osty_rt_string_copy_bytes`
     * branches on the tag — heap source uses memcpy, inline source
     * unpacks byte-by-byte from the pointer bits. The cost is a single
     * tag-bit test on the heap path, and dup_site is the choke point
     * for every heap-bound string copy in the runtime. */
    osty_rt_string_copy_bytes(out, start, len);
    out[len] = '\0';
    return out;
}

/* SSO-eligible dup. Strings up to 7 bytes pack into the pointer
 * itself, eliminating the `osty_gc_allocate_managed` call on the
 * hottest source of short-string allocs (split pieces). The other
 * `dup_site` callers (int_to_string / bool_to_string / chars / bytes
 * / case helpers) keep producing heap strings via the unsuffixed
 * variant — they all flow through `dup_site` whose body is now SSO-
 * input-safe (`osty_rt_string_copy_bytes`), so callers that splice
 * bytes from an inline source still work, but the OUTPUT of those
 * sites stays heap-resident. SSO output is opt-in via this `_sso`
 * variant so concat/IO-bound producers don't accidentally hand a
 * tagged pointer to libc.
 *
 * `static inline`: single hot caller (`osty_rt_string_dup_range`)
 * benefits from inlining the length-branch so the constant-length
 * call sites (single-byte split pieces) fold to a direct
 * `pack_inline`. */
static inline char *osty_rt_string_dup_site_sso(const char *start, size_t len, const char *site) {
    if (len <= OSTY_RT_SSO_MAX_LEN) {
        return (char *)osty_rt_string_pack_inline(start, len);
    }
    return osty_rt_string_dup_site(start, len, site);
}

static char *osty_rt_string_dup_range(const char *start, size_t len) {
    return osty_rt_string_dup_site_sso(start, len, "runtime.strings.split.part");
}

static bool osty_rt_f64_same_bits(double left, double right) {
    uint64_t left_bits = 0;
    uint64_t right_bits = 0;
    memcpy(&left_bits, &left, sizeof(left_bits));
    memcpy(&right_bits, &right, sizeof(right_bits));
    return left_bits == right_bits;
}

/* OSTY_HOT_INLINE: `[]` literals + every Split/Fields/Chars/Bytes/
 * Repeat/etc. internal helper lower to a list_new at the call site.
 * Body is one `osty_gc_allocate_managed` + zero-init of the small
 * header; inlining lets ThinLTO see the const-zero stores and
 * fold them away when the immediate next use is a push.
 *
 * Post-inline-storage: `data` is initialised to the inline region so
 * the first push doesn't have to malloc a separate buffer. The
 * `cap` stays 0 — `osty_rt_list_reserve` populates it lazily once
 * `elem_size` is known (the list_new path doesn't yet know the
 * element type; that gets locked in at first push via
 * `osty_rt_list_ensure_layout`). */
OSTY_HOT_INLINE void *osty_rt_list_new(void) {
    osty_rt_list *list = (osty_rt_list *)osty_gc_allocate_managed(
        sizeof(osty_rt_list), OSTY_GC_KIND_LIST, "runtime.list",
        osty_rt_list_trace, osty_rt_list_destroy);
    list->data = list->inline_storage;
    return list;
}

/* osty_rt_list_cast_fast — like `osty_rt_list_cast` but skips the
 * full `osty_gc_load_v1` barrier (no Phase D forwarding-table probe).
 * Safe under Phase A-C non-moving semantics + Phase E young arena:
 * the only address mutation that can happen for a List payload is
 * the cheney PROMOTED redirect on root_bind, which we follow here
 * inline. Hot tight-loop accessors (get_i64, set_i64, len) call
 * this to dodge the lock cycle / hash probe in load_v1. */
OSTY_HOT_INLINE static osty_rt_list *osty_rt_list_cast_fast(void *raw_list) {
    if (raw_list == NULL) {
        osty_rt_abort("list is null");
    }
    /* Phase E follow-up: same PROMOTED / FORWARDED follow as
     * `osty_gc_load_v1`. A young List that gets root_bind'd
     * promotes to OLD; callers that retained the pre-promote young
     * pointer would otherwise read stale young bytes here. */
    if (osty_gc_arena_is_young_page(raw_list)) {
        osty_gc_micro_header *micro =
            osty_gc_micro_header_for_payload(raw_list);
        uint64_t fom = micro->forward_or_meta;
        uint64_t tag = fom & OSTY_GC_FORWARD_TAG_MASK;
        if (tag == OSTY_GC_FORWARD_TAG_PROMOTED ||
            tag == OSTY_GC_FORWARD_TAG_FORWARDED) {
            raw_list = (void *)(uintptr_t)(fom & ~OSTY_GC_FORWARD_TAG_MASK);
        }
    }
    return (osty_rt_list *)raw_list;
}

OSTY_HOT_INLINE int64_t osty_rt_list_len(void *raw_list) {
    osty_rt_list *list = osty_rt_list_cast_fast(raw_list);
    return list->len;
}

void osty_rt_list_pop_discard(void *raw_list) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    if (list == NULL || list->len <= 0) {
        osty_rt_abort("list.pop on empty list");
    }
    list->len -= 1;
}

// osty_rt_list_clear truncates the list to length 0. The backing storage
// (data/capacity/elem_size/trace metadata) is preserved so subsequent
// pushes keep the same element type and re-use the allocation. Pointer
// slots past len are no longer traced (osty_rt_list_trace bounds by len),
// so cleared elements become unreachable from this list.
void osty_rt_list_clear(void *raw_list) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    if (list == NULL) {
        osty_rt_abort("list.clear on nil receiver");
    }
    list->len = 0;
}

void osty_rt_list_push_i64(void *raw_list, int64_t value) {
    osty_rt_list_push_raw(raw_list, &value, sizeof(value), NULL);
}

// osty_rt_list_insert_raw shifts list[index..len] right by one slot
// and writes value into slot `index`. Bounds: index in [0, len].
// Mirrors osty_rt_list_push_raw's layout/reserve discipline so the
// element ABI stays consistent.
static void osty_rt_list_insert_raw(void *raw_list, int64_t index, const void *value, size_t elem_size, osty_rt_trace_slot_fn trace_elem) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    if (index < 0 || index > list->len) {
        osty_rt_abort("list.insert index out of range");
    }
    osty_rt_list_ensure_layout(list, elem_size, trace_elem);
    osty_rt_list_reserve(list, list->len + 1);
    if (index < list->len) {
        memmove(list->data + (size_t)(index + 1) * elem_size,
                list->data + (size_t)index * elem_size,
                (size_t)(list->len - index) * elem_size);
    }
    memcpy(list->data + (size_t)index * elem_size, value, elem_size);
    list->len += 1;
}

void osty_rt_list_insert_i64(void *raw_list, int64_t index, int64_t value) {
    osty_rt_list_insert_raw(raw_list, index, &value, sizeof(value), NULL);
}

void osty_rt_list_insert_i1(void *raw_list, int64_t index, bool value) {
    osty_rt_list_insert_raw(raw_list, index, &value, sizeof(value), NULL);
}

void osty_rt_list_insert_f64(void *raw_list, int64_t index, double value) {
    osty_rt_list_insert_raw(raw_list, index, &value, sizeof(value), NULL);
}

void osty_rt_list_insert_ptr(void *raw_list, int64_t index, void *value) {
    osty_rt_list_insert_raw(raw_list, index, &value, sizeof(value), osty_gc_mark_slot_v1);
    osty_gc_post_write_v1(raw_list, value, OSTY_GC_KIND_LIST);
}

// osty_rt_list_remove_at_discard shifts list[index+1..len] left by one slot
// and decrements len. Aborts on out-of-range (matches stdlib
// `removeAt(i) -> T` semantics — the caller reads the element first via
// a typed get, then invokes this helper to splice the slot out).
// Element-type-agnostic: operates in terms of list->elem_size.
void osty_rt_list_remove_at_discard(void *raw_list, int64_t index) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    if (list == NULL) {
        osty_rt_abort("list.removeAt on nil receiver");
    }
    if (index < 0 || index >= list->len) {
        osty_rt_abort("list.removeAt index out of range");
    }
    size_t elem_size = list->elem_size;
    if (index + 1 < list->len) {
        memmove(list->data + (size_t)index * elem_size,
                list->data + (size_t)(index + 1) * elem_size,
                (size_t)(list->len - index - 1) * elem_size);
    }
    list->len -= 1;
}

// osty_rt_list_reverse reverses the list in place. Byte-level swap
// using the list's own elem_size — no per-element type dispatch needed.
// The GC trace bookkeeping survives because we swap entire slots
// (including any embedded ptrs or gc_offsets roots); no slot ever
// holds a half-written intermediate value visible to a GC safepoint
// because the whole swap is inline within a single C call.
void osty_rt_list_reverse(void *raw_list) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    if (list == NULL) {
        osty_rt_abort("list.reverse on nil receiver");
    }
    if (list->len <= 1) {
        return;
    }
    size_t elem_size = list->elem_size;
    if (elem_size == 0) {
        return;
    }
    // Stack buffer for small slots; heap buffer for larger ones.
    unsigned char small[64];
    unsigned char *tmp = small;
    unsigned char *heap = NULL;
    if (elem_size > sizeof(small)) {
        heap = (unsigned char *)malloc(elem_size);
        if (heap == NULL) {
            osty_rt_abort("list.reverse: out of memory");
        }
        tmp = heap;
    }
    int64_t lo = 0, hi = list->len - 1;
    while (lo < hi) {
        unsigned char *a = list->data + (size_t)lo * elem_size;
        unsigned char *b = list->data + (size_t)hi * elem_size;
        memcpy(tmp, a, elem_size);
        memcpy(a, b, elem_size);
        memcpy(b, tmp, elem_size);
        lo++;
        hi--;
    }
    if (heap != NULL) {
        free(heap);
    }
}

// osty_rt_list_reversed returns a freshly allocated list holding the
// receiver's elements in reverse order. The new list inherits the
// source's elem_size + trace callback so GC bookkeeping stays intact.
void *osty_rt_list_reversed(void *raw_list) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    if (list == NULL) {
        osty_rt_abort("list.reversed on nil receiver");
    }
    osty_rt_list *out = (osty_rt_list *)osty_rt_list_new();
    if (list->len == 0) {
        return out;
    }
    size_t elem_size = list->elem_size;
    osty_rt_list_ensure_layout(out, elem_size, list->trace_elem);
    osty_rt_list_reserve(out, list->len);
    // Copy elements from end → start into the new list's data buffer.
    for (int64_t i = 0; i < list->len; i++) {
        const unsigned char *src = list->data + (size_t)(list->len - 1 - i) * elem_size;
        memcpy(out->data + (size_t)i * elem_size, src, elem_size);
    }
    out->len = list->len;
    return out;
}

void osty_rt_list_push_i1(void *raw_list, bool value) {
    osty_rt_list_push_raw(raw_list, &value, sizeof(value), NULL);
}

void osty_rt_list_push_f64(void *raw_list, double value) {
    osty_rt_list_push_raw(raw_list, &value, sizeof(value), NULL);
}

/* OSTY_HOT_INLINE: 86 call sites in log-aggregator's hot loop alone
 * (every `out.push(...)` lowering hits this). The body is a tiny
 * shim over `osty_rt_list_push_raw` + a remembered-set update;
 * inlining lets ThinLTO collapse the push at every list-builder
 * call site so the cross-TU function call disappears. The
 * remembered-set helper is itself a single tag-bit branch
 * post-#867. */
OSTY_HOT_INLINE void osty_rt_list_push_ptr(void *raw_list, void *value) {
    osty_rt_list_push_raw(raw_list, &value, sizeof(value), osty_gc_mark_slot_v1);
    osty_gc_post_write_v1(raw_list, value, OSTY_GC_KIND_LIST);
}

void osty_rt_list_push_bytes(void *raw_list, const void *value, int64_t elem_size, osty_rt_trace_slot_fn trace_elem) {
    if (elem_size < 0) {
        osty_rt_abort("negative list element size");
    }
    osty_rt_list_push_raw(raw_list, value, (size_t)elem_size, trace_elem);
}

void osty_rt_list_push_bytes_v1(void *raw_list, const void *value, int64_t elem_size) {
    osty_rt_list *list;

    if (elem_size < 0) {
        osty_rt_abort("negative list element size");
    }
    list = osty_rt_list_cast(raw_list);
    osty_rt_list_ensure_layout(list, (size_t)elem_size, NULL);
    osty_rt_list_ensure_gc_offsets(list, NULL, 0);
    osty_rt_list_push_bytes(raw_list, value, (size_t)elem_size, NULL);
}

void osty_rt_list_push_bytes_roots_v1(void *raw_list, const void *value, int64_t elem_size, const int64_t *gc_offsets, int64_t gc_offset_count) {
    osty_rt_list *list;
    int64_t i;

    if (elem_size < 0) {
        osty_rt_abort("negative list element size");
    }
    list = osty_rt_list_cast(raw_list);
    osty_rt_list_ensure_layout(list, (size_t)elem_size, NULL);
    osty_rt_list_ensure_gc_offsets(list, gc_offsets, gc_offset_count);
    osty_rt_list_push_bytes(raw_list, value, (size_t)elem_size, NULL);
    for (i = 0; i < gc_offset_count; i++) {
        void *child = NULL;
        memcpy(&child, ((const unsigned char *)value) + (size_t)gc_offsets[i], sizeof(child));
        if (child != NULL) {
            osty_gc_post_write_v1(raw_list, child, OSTY_GC_KIND_LIST);
        }
    }
}

// osty_rt_list_insert_bytes_v1 mirrors push_bytes_v1 but at an
// arbitrary index — shifts list[index..len] right by one slot and
// writes the byte payload into slot `index`. Used by the aggregate /
// struct list-insert path; pointer-element struct fields go through
// the *_roots variant below so GC sees the new edges.
void osty_rt_list_insert_bytes_v1(void *raw_list, int64_t index, const void *value, int64_t elem_size) {
    if (elem_size < 0) {
        osty_rt_abort("negative list element size");
    }
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    osty_rt_list_ensure_layout(list, (size_t)elem_size, NULL);
    osty_rt_list_ensure_gc_offsets(list, NULL, 0);
    osty_rt_list_insert_raw(raw_list, index, value, (size_t)elem_size, NULL);
}

void osty_rt_list_insert_bytes_roots_v1(void *raw_list, int64_t index, const void *value, int64_t elem_size, const int64_t *gc_offsets, int64_t gc_offset_count) {
    if (elem_size < 0) {
        osty_rt_abort("negative list element size");
    }
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    osty_rt_list_ensure_layout(list, (size_t)elem_size, NULL);
    osty_rt_list_ensure_gc_offsets(list, gc_offsets, gc_offset_count);
    osty_rt_list_insert_raw(raw_list, index, value, (size_t)elem_size, NULL);
    int64_t i;
    for (i = 0; i < gc_offset_count; i++) {
        void *child = NULL;
        memcpy(&child, ((const unsigned char *)value) + (size_t)gc_offsets[i], sizeof(child));
        if (child != NULL) {
            osty_gc_post_write_v1(raw_list, child, OSTY_GC_KIND_LIST);
        }
    }
}

OSTY_HOT_INLINE int64_t osty_rt_list_get_i64(void *raw_list, int64_t index) {
    /* Reimplemented against cast_fast + a direct bounds-checked GEP
     * load instead of osty_rt_list_get_raw → osty_rt_list_cast. Saves
     * the gc_load_v1 barrier per access; matmul/quicksort spend the
     * majority of their iter cost here. */
    osty_rt_list *list = osty_rt_list_cast_fast(raw_list);
    if (index < 0 || index >= list->len) {
        osty_rt_abort("list index out of range");
    }
    int64_t value;
    memcpy(&value, list->data + ((size_t)index * list->elem_size), sizeof(value));
    return value;
}

bool osty_rt_list_get_i1(void *raw_list, int64_t index) {
    bool value;
    memcpy(&value, osty_rt_list_get_raw(raw_list, index, sizeof(value), NULL), sizeof(value));
    return value;
}

double osty_rt_list_get_f64(void *raw_list, int64_t index) {
    double value;
    memcpy(&value, osty_rt_list_get_raw(raw_list, index, sizeof(value), NULL), sizeof(value));
    return value;
}

void *osty_rt_list_data_i64(void *raw_list) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    osty_rt_list_ensure_layout(list, sizeof(int64_t), NULL);
    return list->data;
}

void *osty_rt_list_data_i1(void *raw_list) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    osty_rt_list_ensure_layout(list, sizeof(bool), NULL);
    return list->data;
}

void *osty_rt_list_data_f64(void *raw_list) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    osty_rt_list_ensure_layout(list, sizeof(double), NULL);
    return list->data;
}

/* OSTY_HOT_INLINE: every `xs[i]` lowering on a List<T> with
 * pointer payload hits this (25 sites in log-aggregator). The body
 * is `osty_rt_list_get_raw + osty_gc_load_v1` — both are themselves
 * inline post-#883 / #875, so collapsing the wrapper folds the
 * full read into a couple of loads at the call site. */
OSTY_HOT_INLINE void *osty_rt_list_get_ptr(void *raw_list, int64_t index) {
    void *value;
    memcpy(&value, osty_rt_list_get_raw(raw_list, index, sizeof(value), osty_gc_mark_slot_v1), sizeof(value));
    return osty_gc_load_v1(value);
}

OSTY_HOT_INLINE void osty_rt_list_get_bytes(void *raw_list, int64_t index, void *out_value, int64_t elem_size, osty_rt_trace_slot_fn trace_elem) {
    if (out_value == NULL || elem_size < 0) {
        osty_rt_abort("invalid list get_bytes call");
    }
    memcpy(out_value, osty_rt_list_get_raw(raw_list, index, (size_t)elem_size, trace_elem), (size_t)elem_size);
}

/* Hot path for struct-element list iteration (`for row in rows` where
 * rows: List<Record>). Inlined so LTO can see the memcpy against a
 * fixed-size struct and let SROA split the per-iter alloca + load into
 * direct per-field loads — record_pipeline's analyzeRecordPipeline
 * reads 3 struct fields per row × 192 rows, so the memcpy-round-trip
 * overhead compounds quickly without this. */
OSTY_HOT_INLINE void osty_rt_list_get_bytes_v1(void *raw_list, int64_t index, void *out, int64_t elem_size) {
    if (out == NULL) {
        osty_rt_abort("list output buffer is null");
    }
    if (elem_size < 0) {
        osty_rt_abort("negative list element size");
    }
    osty_rt_list_get_bytes(raw_list, index, out, elem_size, NULL);
}

OSTY_HOT_INLINE void osty_rt_list_set_i64(void *raw_list, int64_t index, int64_t value) {
    /* Mirrors osty_rt_list_get_i64's fast path. */
    osty_rt_list *list = osty_rt_list_cast_fast(raw_list);
    if (index < 0 || index >= list->len) {
        osty_rt_abort("list index out of range");
    }
    memcpy(list->data + ((size_t)index * list->elem_size), &value, sizeof(value));
}

void osty_rt_list_set_i1(void *raw_list, int64_t index, bool value) {
    osty_rt_list_set_raw(raw_list, index, &value, sizeof(value), NULL);
}

void osty_rt_list_set_f64(void *raw_list, int64_t index, double value) {
    osty_rt_list_set_raw(raw_list, index, &value, sizeof(value), NULL);
}

void osty_rt_list_set_ptr(void *raw_list, int64_t index, void *value) {
    osty_rt_list_set_raw(raw_list, index, &value, sizeof(value), osty_gc_mark_slot_v1);
    osty_gc_post_write_v1(raw_list, value, OSTY_GC_KIND_LIST);
}

void osty_rt_list_set_bytes(void *raw_list, int64_t index, const void *value, int64_t elem_size, osty_rt_trace_slot_fn trace_elem) {
    if (value == NULL || elem_size < 0) {
        osty_rt_abort("invalid list set_bytes call");
    }
    osty_rt_list_set_raw(raw_list, index, value, (size_t)elem_size, trace_elem);
}

static int osty_rt_compare_i64_ascending(const void *left, const void *right) {
    const int64_t left_value = *(const int64_t *)left;
    const int64_t right_value = *(const int64_t *)right;
    if (left_value < right_value) {
        return -1;
    }
    if (left_value > right_value) {
        return 1;
    }
    return 0;
}

static int osty_rt_compare_i1_ascending(const void *left, const void *right) {
    const bool left_value = *(const bool *)left;
    const bool right_value = *(const bool *)right;
    if (!left_value && right_value) {
        return -1;
    }
    if (left_value && !right_value) {
        return 1;
    }
    return 0;
}

static int osty_rt_compare_f64_ascending(const void *left, const void *right) {
    const double left_value = *(const double *)left;
    const double right_value = *(const double *)right;
    const bool left_nan = left_value != left_value;
    const bool right_nan = right_value != right_value;
    if (left_nan && right_nan) {
        return 0;
    }
    if (left_nan) {
        return 1;
    }
    if (right_nan) {
        return -1;
    }
    if (left_value < right_value) {
        return -1;
    }
    if (left_value > right_value) {
        return 1;
    }
    return 0;
}

static int osty_rt_compare_string_ascending(const void *left, const void *right) {
    const char *left_value = *(const char * const *)left;
    const char *right_value = *(const char * const *)right;
    return osty_rt_string_compare_bytes(left_value, right_value);
}

/* Root-bind FIRST, cast SECOND. Phase E follow-up: with List in
 * the young arena eligibility set, `osty_gc_root_bind_v1` on a
 * young list auto-promotes it to OLD; the cast that follows
 * resolves to the OLD payload via load_v1's PROMOTED follow.
 * The pre-Phase-E ordering (cast first, captured local, then
 * root_bind) left the local pointing at the now-stale young
 * payload and writes silently went to dead from-space bytes. */
void *osty_rt_list_sorted_i64(void *raw_list) {
    void *out = osty_rt_list_new();
    osty_gc_root_bind_v1(raw_list);
    osty_gc_root_bind_v1(out);
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    osty_rt_list *sorted = osty_rt_list_cast(out);
    osty_rt_list_ensure_layout(list, sizeof(int64_t), NULL);
    osty_rt_list_ensure_layout(sorted, sizeof(int64_t), NULL);
    osty_rt_list_copy_initialized(out, sorted, list->data, list->len);
    qsort(sorted->data, (size_t)sorted->len, sizeof(int64_t), osty_rt_compare_i64_ascending);
    osty_gc_root_release_v1(out);
    osty_gc_root_release_v1(raw_list);
    return out;
}

void *osty_rt_list_sorted_i1(void *raw_list) {
    void *out = osty_rt_list_new();
    osty_gc_root_bind_v1(raw_list);
    osty_gc_root_bind_v1(out);
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    osty_rt_list *sorted = osty_rt_list_cast(out);
    osty_rt_list_ensure_layout(list, sizeof(bool), NULL);
    osty_rt_list_ensure_layout(sorted, sizeof(bool), NULL);
    osty_rt_list_copy_initialized(out, sorted, list->data, list->len);
    qsort(sorted->data, (size_t)sorted->len, sizeof(bool), osty_rt_compare_i1_ascending);
    osty_gc_root_release_v1(out);
    osty_gc_root_release_v1(raw_list);
    return out;
}

void *osty_rt_list_sorted_f64(void *raw_list) {
    void *out = osty_rt_list_new();
    osty_gc_root_bind_v1(raw_list);
    osty_gc_root_bind_v1(out);
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    osty_rt_list *sorted = osty_rt_list_cast(out);
    osty_rt_list_ensure_layout(list, sizeof(double), NULL);
    osty_rt_list_ensure_layout(sorted, sizeof(double), NULL);
    osty_rt_list_copy_initialized(out, sorted, list->data, list->len);
    qsort(sorted->data, (size_t)sorted->len, sizeof(double), osty_rt_compare_f64_ascending);
    osty_gc_root_release_v1(out);
    osty_gc_root_release_v1(raw_list);
    return out;
}

void *osty_rt_list_sorted_string(void *raw_list) {
    void *out = osty_rt_list_new();
    osty_gc_root_bind_v1(raw_list);
    osty_gc_root_bind_v1(out);
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    osty_rt_list *sorted = osty_rt_list_cast(out);
    osty_rt_list_ensure_layout(list, sizeof(void *), osty_gc_mark_slot_v1);
    osty_rt_list_ensure_layout(sorted, sizeof(void *), osty_gc_mark_slot_v1);
    osty_rt_list_copy_initialized(out, sorted, list->data, list->len);
    qsort(sorted->data, (size_t)sorted->len, sizeof(void *), osty_rt_compare_string_ascending);
    osty_gc_root_release_v1(out);
    osty_gc_root_release_v1(raw_list);
    return out;
}

// osty_rt_list_slice returns a new List<T> containing elements at
// indices [start, end) of src. Bounds are saturating — negative start
// clamps to 0, end past len clamps to len, end < start yields an empty
// result. This matches the String.slice semantics (s[a..b]).
void *osty_rt_list_slice(void *raw_list, int64_t start, int64_t end) {
    osty_rt_list *src = osty_rt_list_cast(raw_list);
    int64_t len = src->len;
    int64_t count;
    void *out_raw;
    osty_rt_list *out;

    if (start < 0) {
        start = 0;
    }
    if (end > len) {
        end = len;
    }
    if (end < start) {
        end = start;
    }
    count = end - start;

    osty_gc_root_bind_v1(raw_list);
    out_raw = osty_rt_list_new();
    osty_gc_root_bind_v1(out_raw);
    out = osty_rt_list_cast(out_raw);

    // Propagate layout even for empty slices so downstream len/push/get
    // calls see the same element ABI as the source list.
    if (src->elem_size != 0) {
        osty_rt_list_ensure_layout(out, src->elem_size, src->trace_elem);
        if (src->gc_offset_count > 0) {
            osty_rt_list_ensure_gc_offsets(out, src->gc_offsets, src->gc_offset_count);
        }
    }

    if (count == 0) {
        osty_gc_root_release_v1(out_raw);
        osty_gc_root_release_v1(raw_list);
        return out_raw;
    }

    osty_rt_list_copy_initialized(out_raw,
                                  out,
                                  src->data + (size_t)start * src->elem_size,
                                  count);
    osty_gc_root_release_v1(out_raw);
    osty_gc_root_release_v1(raw_list);
    return out_raw;
}

/* OSTY_HOT_INLINE: every Map<String, V> probe iteration calls this
 * on the heap-side equality compare (after the fingerprint-fast-path
 * filters out non-matches). The body is short — pointer-eq fast
 * path, two SSO-aware measure calls, a length compare, then memcmp
 * — so the cross-TU function call was a meaningful fraction of
 * the per-probe cost in log-aggregator / lru-sim / markdown-stats.
 * Forcing inline lets ThinLTO collapse the call site into the
 * Map probe loop. */
OSTY_HOT_INLINE bool osty_rt_strings_Equal(const char *left, const char *right) {
    size_t left_len = 0;
    size_t right_len = 0;
    char left_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
    char right_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
    if (left == right) {
        /* Two SSO inline encodings with identical content always
         * produce the same packed pointer, so this short-circuit
         * handles inline-vs-inline equality at no cost. */
        return true;
    }
    if (left == NULL || right == NULL) {
        return false;
    }
    /* Measure on the ORIGINAL pointers (pre-decode). `osty_rt_string_measure`
     * is SSO-aware: for inline pointers it pulls length from tag bits;
     * for heap pointers it walks bytes via the per-pointer length cache.
     *
     * Decoding inline operands BEFORE measuring would feed
     * `osty_rt_string_measure` the address of a stack buffer, and the
     * length cache (keyed by pointer) would persist stale len/hash for
     * that stack slot across calls — every subsequent function with the
     * same stack frame layout would hit a cached entry that says
     * "length 1, hash X" even though the bytes at that address have
     * changed. Doing measure first avoids the stack-buffer pollution
     * and is also faster: if lengths differ, we never need to decode. */
    osty_rt_string_measure(left, &left_len, NULL);
    osty_rt_string_measure(right, &right_len, NULL);
    if (left_len != right_len) {
        return false;
    }
    if (left_len == 0) {
        return true;
    }
    /* Now decode inline operands so memcmp sees real bytes. The
     * decoded buffer's lifetime is this function frame and is only
     * read by memcmp below — no further measure() calls follow. */
    osty_rt_string_decode_to_buf_if_inline(&left, left_buf);
    osty_rt_string_decode_to_buf_if_inline(&right, right_buf);
    return memcmp(left, right, left_len) == 0;
}

const char *osty_rt_int_to_string(int64_t value) {
    char buffer[32];
    int written = snprintf(buffer, sizeof(buffer), "%lld", (long long)value);
    if (written < 0) {
        osty_rt_abort("failed to format Int as String");
    }
    return osty_rt_string_dup_site(buffer, (size_t)written, "runtime.int.to_string");
}

/* List<T>.toString runtime entries. One per element ABI lane; each
 * walks the list once, formats each element with the per-kind
 * stringifier, and joins with ", " inside "[" / "]" brackets.
 *
 * `osty_rt_list_to_string_buf` is the shared body — caller passes a
 * `format_one` callback that emits one element's text into the
 * growing buffer. Buffer growth uses the standard size + len pattern
 * (start at 64 bytes, double on overflow); the result is materialised
 * into a GC-managed String via `osty_rt_string_dup_site` and the
 * temporary heap buffer freed.
 *
 * The element stringifications match the canonical primitive
 * formatters: Int via "%lld", Float via the same NaN/Inf-aware path
 * `osty_rt_float_to_string` already uses, Bool as "true"/"false",
 * String quoted with `"…"`, Char quoted with `'…'`. Nested
 * collections are NOT recursed today — `List<List<Int>>.toString()`
 * still trips a backend gap because there's no list_to_string_ptr
 * lane registered. Per-list-element `toString` callbacks for
 * struct / enum payloads land in a follow-up. */
typedef void (*osty_rt_list_format_one_fn)(char **buf_inout, size_t *cap_inout, size_t *len_inout, const unsigned char *elem);

static void osty_rt_list_to_string_grow(char **buf_inout, size_t *cap_inout, size_t needed) {
    if (*cap_inout >= needed) {
        return;
    }
    size_t cap = *cap_inout;
    while (cap < needed) {
        cap *= 2;
    }
    char *next = (char *)realloc(*buf_inout, cap);
    if (next == NULL) {
        osty_rt_abort("runtime.list.to_string: out of memory");
    }
    *buf_inout = next;
    *cap_inout = cap;
}

static void osty_rt_list_to_string_append(char **buf_inout, size_t *cap_inout, size_t *len_inout, const char *src, size_t n) {
    osty_rt_list_to_string_grow(buf_inout, cap_inout, *len_inout + n + 1);
    memcpy(*buf_inout + *len_inout, src, n);
    *len_inout += n;
}

static const char *osty_rt_list_to_string_finish(osty_rt_list *list, size_t elem_size, osty_rt_list_format_one_fn format_one) {
    if (list == NULL || list->len == 0) {
        return osty_rt_string_dup_site("[]", 2, "runtime.list.to_string");
    }
    size_t cap = 64;
    size_t len = 0;
    char *buf = (char *)malloc(cap);
    if (buf == NULL) {
        osty_rt_abort("runtime.list.to_string: out of memory");
    }
    buf[len++] = '[';
    for (int64_t i = 0; i < list->len; i++) {
        if (i > 0) {
            osty_rt_list_to_string_append(&buf, &cap, &len, ", ", 2);
        }
        const unsigned char *elem = list->data + (size_t)i * elem_size;
        format_one(&buf, &cap, &len, elem);
    }
    osty_rt_list_to_string_grow(&buf, &cap, len + 2);
    buf[len++] = ']';
    const char *out = osty_rt_string_dup_site(buf, len, "runtime.list.to_string");
    free(buf);
    return out;
}

static void osty_rt_list_format_i64(char **buf, size_t *cap, size_t *len, const unsigned char *elem) {
    int64_t v;
    memcpy(&v, elem, sizeof(v));
    char tmp[32];
    int n = snprintf(tmp, sizeof(tmp), "%lld", (long long)v);
    if (n < 0) {
        osty_rt_abort("runtime.list.to_string.i64: snprintf failed");
    }
    osty_rt_list_to_string_append(buf, cap, len, tmp, (size_t)n);
}

static void osty_rt_list_format_f64(char **buf, size_t *cap, size_t *len, const unsigned char *elem) {
    double v;
    memcpy(&v, elem, sizeof(v));
    /* Mirror osty_rt_float_to_string's NaN/Inf strings so per-list
     * formatting matches `f.toString()` byte-for-byte. */
    if (v != v) {
        osty_rt_list_to_string_append(buf, cap, len, "NaN", 3);
        return;
    }
    if (v == (double)(1.0 / 0.0)) {
        osty_rt_list_to_string_append(buf, cap, len, "+Inf", 4);
        return;
    }
    if (v == (double)(-1.0 / 0.0)) {
        osty_rt_list_to_string_append(buf, cap, len, "-Inf", 4);
        return;
    }
    char tmp[64];
    int n = snprintf(tmp, sizeof(tmp), "%f", v);
    if (n < 0) {
        osty_rt_abort("runtime.list.to_string.f64: snprintf failed");
    }
    osty_rt_list_to_string_append(buf, cap, len, tmp, (size_t)n);
}

static void osty_rt_list_format_i1(char **buf, size_t *cap, size_t *len, const unsigned char *elem) {
    bool v;
    memcpy(&v, elem, sizeof(v));
    if (v) {
        osty_rt_list_to_string_append(buf, cap, len, "true", 4);
    } else {
        osty_rt_list_to_string_append(buf, cap, len, "false", 5);
    }
}

static void osty_rt_list_format_string(char **buf, size_t *cap, size_t *len, const unsigned char *elem) {
    const char *s = NULL;
    memcpy(&s, elem, sizeof(s));
    s = (const char *)osty_gc_load_v1((void *)s);
    osty_rt_list_to_string_append(buf, cap, len, "\"", 1);
    if (s != NULL) {
        char decoded[OSTY_RT_SSO_DECODE_BUF_BYTES];
        const char *src = s;
        osty_rt_string_decode_to_buf_if_inline(&src, decoded);
        size_t n = osty_rt_string_len(s);
        osty_rt_list_to_string_append(buf, cap, len, src, n);
    }
    osty_rt_list_to_string_append(buf, cap, len, "\"", 1);
}

static void osty_rt_list_format_char(char **buf, size_t *cap, size_t *len, const unsigned char *elem) {
    int32_t cp_signed;
    memcpy(&cp_signed, elem, sizeof(cp_signed));
    uint32_t cp = (uint32_t)cp_signed;
    /* Mirror the UTF-8 layout in `osty_rt_char_to_string`. Inlined
     * because the existing function returns a heap String and we
     * just need the raw bytes here. */
    unsigned char encoded[4];
    size_t n;
    if (cp < 0x80U) {
        encoded[0] = (unsigned char)cp;
        n = 1;
    } else if (cp < 0x800U) {
        encoded[0] = (unsigned char)(0xC0U | (cp >> 6));
        encoded[1] = (unsigned char)(0x80U | (cp & 0x3FU));
        n = 2;
    } else if (cp < 0x10000U) {
        encoded[0] = (unsigned char)(0xE0U | (cp >> 12));
        encoded[1] = (unsigned char)(0x80U | ((cp >> 6) & 0x3FU));
        encoded[2] = (unsigned char)(0x80U | (cp & 0x3FU));
        n = 3;
    } else {
        encoded[0] = (unsigned char)(0xF0U | (cp >> 18));
        encoded[1] = (unsigned char)(0x80U | ((cp >> 12) & 0x3FU));
        encoded[2] = (unsigned char)(0x80U | ((cp >> 6) & 0x3FU));
        encoded[3] = (unsigned char)(0x80U | (cp & 0x3FU));
        n = 4;
    }
    osty_rt_list_to_string_append(buf, cap, len, "'", 1);
    osty_rt_list_to_string_append(buf, cap, len, (const char *)encoded, n);
    osty_rt_list_to_string_append(buf, cap, len, "'", 1);
}

static void osty_rt_list_format_byte(char **buf, size_t *cap, size_t *len, const unsigned char *elem) {
    int64_t v = (int64_t)*elem;
    char tmp[8];
    int n = snprintf(tmp, sizeof(tmp), "%lld", (long long)v);
    if (n < 0) {
        osty_rt_abort("runtime.list.to_string.byte: snprintf failed");
    }
    osty_rt_list_to_string_append(buf, cap, len, tmp, (size_t)n);
}

const char *osty_rt_list_to_string_i64(void *raw_list) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    return osty_rt_list_to_string_finish(list, sizeof(int64_t), osty_rt_list_format_i64);
}

const char *osty_rt_list_to_string_f64(void *raw_list) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    return osty_rt_list_to_string_finish(list, sizeof(double), osty_rt_list_format_f64);
}

const char *osty_rt_list_to_string_i1(void *raw_list) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    return osty_rt_list_to_string_finish(list, sizeof(bool), osty_rt_list_format_i1);
}

const char *osty_rt_list_to_string_string(void *raw_list) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    return osty_rt_list_to_string_finish(list, sizeof(void *), osty_rt_list_format_string);
}

const char *osty_rt_list_to_string_char(void *raw_list) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    return osty_rt_list_to_string_finish(list, sizeof(int32_t), osty_rt_list_format_char);
}

const char *osty_rt_list_to_string_byte(void *raw_list) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    return osty_rt_list_to_string_finish(list, sizeof(uint8_t), osty_rt_list_format_byte);
}

/* Monotonic-clock sample in nanoseconds, exported for the benchmark
 * harness (`testing.benchmark(N, || { ... })`). Signed int64 so it
 * round-trips through Osty's `Int` without truncation surprises when
 * subtracting two samples; wall-clock sources are intentionally not
 * exposed because bench timings must be immune to NTP adjustments. */
int64_t osty_rt_bench_now_nanos(void) {
    return (int64_t)osty_rt_monotonic_ns();
}

/* Per-iteration timing samples for the benchmark harness. The codegen
 * side allocates one buffer per testing.benchmark(...) call, records a
 * nanosecond delta each iteration, then hands the buffer to
 * osty_rt_bench_samples_report which sorts in place, prints the
 * distribution line, and frees. Allocation failure aborts the
 * benchmark rather than producing bogus stats. */
int64_t *osty_rt_bench_samples_new(int64_t count) {
    if (count <= 0) {
        return NULL;
    }
    /* Reject counts that would wrap size_t on cast+multiply. The bench
     * harness auto-tune path already clamps to 100M iterations but a
     * caller can pass any int64 directly through testing.benchmark(N, …),
     * and writing past a too-small buffer in the record loop would
     * silently corrupt heap memory. Abort is the right failure mode —
     * the whole process is a one-shot bench runner. */
    if ((uint64_t)count > (uint64_t)SIZE_MAX / sizeof(int64_t)) {
        osty_rt_abort("bench: iteration count too large for sample buffer");
    }
    size_t bytes = (size_t)count * sizeof(int64_t);
    int64_t *buf = (int64_t *)malloc(bytes);
    if (buf == NULL) {
        osty_rt_abort("bench: failed to allocate sample buffer");
    }
    return buf;
}

void osty_rt_bench_samples_record(int64_t *samples, int64_t idx, int64_t value) {
    if (samples == NULL) {
        return;
    }
    samples[idx] = value;
}

static int osty_rt_bench_i64_compare(const void *a, const void *b) {
    int64_t ai = *(const int64_t *)a;
    int64_t bi = *(const int64_t *)b;
    if (ai < bi) return -1;
    if (ai > bi) return 1;
    return 0;
}

/* Sort samples, compute min/p50/p99/max, and print the distribution
 * follow-up line the bench summary references. Indentation matches the
 * leading `bench …` line so scrapers keyed on `min=` don't need a
 * regex — they can grep for two-space indent. */
void osty_rt_bench_samples_report(int64_t *samples, int64_t count) {
    if (samples == NULL || count <= 0) {
        return;
    }
    qsort(samples, (size_t)count, sizeof(int64_t), osty_rt_bench_i64_compare);
    int64_t min_v = samples[0];
    int64_t max_v = samples[count - 1];
    int64_t p50_idx = (count - 1) / 2;
    int64_t p99_idx = (count * 99) / 100;
    if (p99_idx >= count) {
        p99_idx = count - 1;
    }
    int64_t p50_v = samples[p50_idx];
    int64_t p99_v = samples[p99_idx];
    printf("  min=%lldns p50=%lldns p99=%lldns max=%lldns\n",
           (long long)min_v, (long long)p50_v,
           (long long)p99_v, (long long)max_v);
}

void osty_rt_bench_samples_free(int64_t *samples) {
    if (samples != NULL) {
        free(samples);
    }
}

/* Auto-tune target: the CLI sets OSTY_BENCH_TIME_NS when the user passes
 * `--benchtime <dur>`. A positive value switches the codegen path from
 * fixed-N to probe-and-estimate mode. 0 / unset means the user-declared
 * N is authoritative. Parsing is restricted to a non-negative decimal
 * so unexpected values silently fall back to fixed-N. */
int64_t osty_rt_bench_target_ns(void) {
    const char *raw = getenv("OSTY_BENCH_TIME_NS");
    if (raw == NULL || raw[0] == '\0') {
        return 0;
    }
    char *end = NULL;
    long long v = strtoll(raw, &end, 10);
    if (end == NULL || *end != '\0' || v < 0) {
        return 0;
    }
    return (int64_t)v;
}

const char *osty_rt_bool_to_string(bool value) {
    if (value) {
        return osty_rt_string_dup_site("true", 4, "runtime.bool.to_string");
    }
    return osty_rt_string_dup_site("false", 5, "runtime.bool.to_string");
}

const char *osty_rt_char_to_string(int32_t codepoint) {
    uint32_t cp = (uint32_t)codepoint;
    unsigned char buffer[4];
    size_t len;
    if (cp < 0x80U) {
        buffer[0] = (unsigned char)cp;
        len = 1;
    } else if (cp < 0x800U) {
        buffer[0] = (unsigned char)(0xC0U | (cp >> 6));
        buffer[1] = (unsigned char)(0x80U | (cp & 0x3FU));
        len = 2;
    } else if (cp < 0x10000U) {
        buffer[0] = (unsigned char)(0xE0U | (cp >> 12));
        buffer[1] = (unsigned char)(0x80U | ((cp >> 6) & 0x3FU));
        buffer[2] = (unsigned char)(0x80U | (cp & 0x3FU));
        len = 3;
    } else if (cp < 0x110000U) {
        buffer[0] = (unsigned char)(0xF0U | (cp >> 18));
        buffer[1] = (unsigned char)(0x80U | ((cp >> 12) & 0x3FU));
        buffer[2] = (unsigned char)(0x80U | ((cp >> 6) & 0x3FU));
        buffer[3] = (unsigned char)(0x80U | (cp & 0x3FU));
        len = 4;
    } else {
        /* U+FFFD REPLACEMENT CHARACTER */
        buffer[0] = 0xEFU;
        buffer[1] = 0xBFU;
        buffer[2] = 0xBDU;
        len = 3;
    }
    return osty_rt_string_dup_site((const char *)buffer, len, "runtime.char.to_string");
}

const char *osty_rt_byte_to_string(int8_t value) {
    unsigned char byte = (unsigned char)value;
    return osty_rt_string_dup_site((const char *)&byte, 1, "runtime.byte.to_string");
}

const char *osty_rt_float_to_string(double value) {
    char buffer[64];

    if (isnan(value)) {
        return osty_rt_string_dup_site("NaN", 3, "runtime.float.to_string");
    }
    if (isinf(value)) {
        if (value < 0) {
            return osty_rt_string_dup_site("-Inf", 4, "runtime.float.to_string");
        }
        return osty_rt_string_dup_site("+Inf", 4, "runtime.float.to_string");
    }

    if (snprintf(buffer, sizeof(buffer), "%.6f", value) < 0) {
        osty_rt_abort("failed to format Float as String");
    }
    return osty_rt_string_dup_site(buffer, strlen(buffer), "runtime.float.to_string");
}

double osty_rt_float_abs(double value) {
    return fabs(value);
}

double osty_rt_float_min(double left, double right) {
    return fmin(left, right);
}

double osty_rt_float_max(double left, double right) {
    return fmax(left, right);
}

double osty_rt_float_clamp(double value, double lo, double hi) {
    return fmax(lo, fmin(value, hi));
}

double osty_rt_float_signum(double value) {
    if (isnan(value)) {
        return value;
    }
    if (value == 0.0) {
        return copysign(0.0, value);
    }
    return copysign(1.0, value);
}

double osty_rt_float_floor(double value) {
    return floor(value);
}

double osty_rt_float_ceil(double value) {
    return ceil(value);
}

double osty_rt_float_round(double value) {
    return nearbyint(value);
}

double osty_rt_float_trunc(double value) {
    return trunc(value);
}

double osty_rt_float_fract(double value) {
    return value - trunc(value);
}

double osty_rt_float_sqrt(double value) {
    return sqrt(value);
}

double osty_rt_float_cbrt(double value) {
    return cbrt(value);
}

double osty_rt_float_ln(double value) {
    return log(value);
}

double osty_rt_float_log2(double value) {
    return log2(value);
}

double osty_rt_float_log10(double value) {
    return log10(value);
}

double osty_rt_float_exp(double value) {
    return exp(value);
}

double osty_rt_float_sinh(double value) {
    return sinh(value);
}

double osty_rt_float_cosh(double value) {
    return cosh(value);
}

double osty_rt_float_tanh(double value) {
    return tanh(value);
}

double osty_rt_float_sin(double value) {
    return sin(value);
}

double osty_rt_float_cos(double value) {
    return cos(value);
}

double osty_rt_float_tan(double value) {
    return tan(value);
}

double osty_rt_float_asin(double value) {
    return asin(value);
}

double osty_rt_float_acos(double value) {
    return acos(value);
}

double osty_rt_float_atan(double value) {
    return atan(value);
}

double osty_rt_float_atan2(double y, double x) {
    return atan2(y, x);
}

double osty_rt_float_pow(double left, double right) {
    return pow(left, right);
}

double osty_rt_float_hypot(double left, double right) {
    return hypot(left, right);
}

double osty_rt_math_log(double value, double base) {
    if (base == 0.0) {
        return log(value);
    }
    return log(value) / log(base);
}

bool osty_rt_float_is_nan(double value) {
    return isnan(value);
}

bool osty_rt_float_is_infinite(double value) {
    return isinf(value);
}

bool osty_rt_float_is_finite(double value) {
    return isfinite(value);
}

uint64_t osty_rt_float64_to_bits(double value) {
    union {
        double f64;
        uint64_t bits;
    } u;
    u.f64 = value;
    return u.bits;
}

uint64_t osty_rt_float32_to_bits(float value) {
    union {
        float f32;
        uint32_t bits;
    } u;
    u.f32 = value;
    return (uint64_t)u.bits;
}

const char *osty_rt_float_to_fixed(double value, int64_t precision) {
    int digits;
    int needed;
    char *buffer;
    const char *out;

    if (isnan(value)) {
        return osty_rt_string_dup_site("NaN", 3, "runtime.float.to_fixed");
    }
    if (isinf(value)) {
        if (value < 0) {
            return osty_rt_string_dup_site("-Inf", 4, "runtime.float.to_fixed");
        }
        return osty_rt_string_dup_site("Inf", 3, "runtime.float.to_fixed");
    }
    digits = precision < 0 ? 0 : (precision > 1000 ? 1000 : (int)precision);
    needed = snprintf(NULL, 0, "%.*f", digits, value);
    if (needed < 0) {
        osty_rt_abort("failed to format Float.toFixed()");
    }
    buffer = (char *)malloc((size_t)needed + 1);
    if (buffer == NULL) {
        osty_rt_abort("out of memory formatting Float.toFixed()");
    }
    if (snprintf(buffer, (size_t)needed + 1, "%.*f", digits, value) < 0) {
        free(buffer);
        osty_rt_abort("failed to format Float.toFixed()");
    }
    out = osty_rt_string_dup_site(buffer, (size_t)needed, "runtime.float.to_fixed");
    free(buffer);
    return out;
}

static const char *osty_rt_float_checked_to_int(double value, double rounded,
                                                int64_t *out, const char *site) {
    if (isnan(value)) {
        return osty_rt_string_dup_site("float conversion failed: NaN", 28, site);
    }
    if (isinf(value)) {
        return osty_rt_string_dup_site("float conversion failed: infinite", 33, site);
    }
    if (rounded < (double)INT64_MIN || rounded > (double)INT64_MAX) {
        return osty_rt_string_dup_site("float conversion failed: overflow", 33, site);
    }
    *out = (int64_t)rounded;
    return NULL;
}

const char *osty_rt_float_to_int_trunc(double value, int64_t *out) {
    return osty_rt_float_checked_to_int(value, trunc(value), out, "runtime.float.to_int_trunc");
}

const char *osty_rt_float_to_int_round(double value, int64_t *out) {
    return osty_rt_float_checked_to_int(value, nearbyint(value), out, "runtime.float.to_int_round");
}

const char *osty_rt_float_to_int_floor(double value, int64_t *out) {
    return osty_rt_float_checked_to_int(value, floor(value), out, "runtime.float.to_int_floor");
}

const char *osty_rt_float_to_int_ceil(double value, int64_t *out) {
    return osty_rt_float_checked_to_int(value, ceil(value), out, "runtime.float.to_int_ceil");
}

static int64_t osty_rt_float_saturating_i64(double value) {
    if (isnan(value)) {
        return 0;
    }
    if (value >= (double)INT64_MAX) {
        return INT64_MAX;
    }
    if (value <= (double)INT64_MIN) {
        return INT64_MIN;
    }
    return (int64_t)trunc(value);
}

int64_t osty_rt_float_to_int_lossy(double value) {
    return osty_rt_float_saturating_i64(value);
}

int32_t osty_rt_float_to_int32_lossy(double value) {
    int64_t wide = osty_rt_float_saturating_i64(value);
    if (wide > (int64_t)INT32_MAX) {
        return INT32_MAX;
    }
    if (wide < (int64_t)INT32_MIN) {
        return INT32_MIN;
    }
    return (int32_t)wide;
}

int64_t osty_rt_float_to_int64_lossy(double value) {
    return osty_rt_float_saturating_i64(value);
}

int64_t osty_rt_strings_Compare(const char *left, const char *right) {
    int result;
    /* `osty_rt_string_compare_bytes` decodes any inline operand
     * itself, so we only need to handle the NULL / empty-string
     * boundary cases here. Inline-tagged pointers always carry
     * non-zero length (the runtime never packs an empty string),
     * so `is_inline(x)` implies `x` is non-empty. */
    if (left == NULL) {
        if (right == NULL || (!osty_rt_string_is_inline(right) && right[0] == '\0')) {
            return 0;
        }
        return -1;
    }
    if (right == NULL) {
        return (!osty_rt_string_is_inline(left) && left[0] == '\0') ? 0 : 1;
    }
    result = osty_rt_string_compare_bytes(left, right);
    if (result < 0) {
        return -1;
    }
    if (result > 0) {
        return 1;
    }
    return 0;
}

int64_t osty_rt_strings_Count(const char *value, const char *substr) {
    const char *cursor;
    const char *next;
    size_t value_len;
    size_t substr_len;
    int64_t total;
    char value_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
    char substr_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];

    value = (value == NULL) ? "" : value;
    substr = (substr == NULL) ? "" : substr;
    osty_rt_string_decode_to_buf_if_inline(&value, value_buf);
    osty_rt_string_decode_to_buf_if_inline(&substr, substr_buf);
    value_len = strlen(value);
    substr_len = strlen(substr);
    if (substr_len == 0) {
        return (int64_t)value_len + 1;
    }

    total = 0;
    cursor = value;
    while ((next = strstr(cursor, substr)) != NULL) {
        total += 1;
        cursor = next + substr_len;
    }
    return total;
}

int64_t osty_rt_strings_IndexOf(const char *value, const char *substr) {
    const char *next;
    char value_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
    char substr_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];

    value = (value == NULL) ? "" : value;
    substr = (substr == NULL) ? "" : substr;
    osty_rt_string_decode_to_buf_if_inline(&value, value_buf);
    osty_rt_string_decode_to_buf_if_inline(&substr, substr_buf);
    if (substr[0] == '\0') {
        return 0;
    }
    next = strstr(value, substr);
    if (next == NULL) {
        return -1;
    }
    return (int64_t)(next - value);
}

int64_t osty_rt_strings_ByteLen(const char *value) {
    return (int64_t)osty_rt_string_len(value);
}

/* OSTY_HOT_INLINE: every `a + b` String concat in user code lowers
 * to this. Body computes both lengths (SSO-aware), allocates,
 * memcpys both halves with SSO-decode, and NUL-terminates. Small
 * enough to inline; eliminates the call frame on every concat
 * expression. */
OSTY_HOT_INLINE const char *osty_rt_strings_Concat(const char *left, const char *right) {
    size_t left_len = osty_rt_string_len(left);
    size_t right_len = osty_rt_string_len(right);
    size_t total_len = left_len + right_len;
    char *out;

    /* Concat always returns a heap string. SSO is intentionally
     * scoped to `osty_rt_string_dup_range` (split pieces) so the
     * pointer-tagged form never escapes to runtime callers that
     * pass strings to libc directly (printf %s, fputs, etc.).
     * Inline operands are decoded via `osty_rt_string_copy_bytes`. */
    out = (char *)osty_gc_allocate_managed(total_len + 1, OSTY_GC_KIND_STRING, "runtime.strings.concat", NULL, NULL);
    osty_rt_string_copy_bytes(out, left, left_len);
    osty_rt_string_copy_bytes(out + left_len, right, right_len);
    out[total_len] = '\0';
    return out;
}

/* osty_rt_strings_ConcatN concatenates `count` strings into a single
 * GC-managed buffer. One allocation total, regardless of `count`.
 *
 * The compiler lowers N-way string interpolation (`"{a}/{b}/{c}"`)
 * to this helper instead of N-1 chained two-arg Concat calls — saves
 * N-2 intermediate allocations per interpolation site. Per-row cost
 * on record_pipeline's `"{service}/{region}/{level}"` key drops from
 * 4 allocs to 1. */
/* OSTY_HOT_INLINE: ConcatN coalesces `a + b + c + ...` chains into
 * one call (#873). For an N-arg chain the IR emits one ConcatN per
 * loop iteration in the corpus generator and parsers — log-aggregator
 * has 11-arg builds. Inlining lets ThinLTO unroll the per-part
 * length+memcpy loop when N is a compile-time constant from the IR
 * call site. */
OSTY_HOT_INLINE const char *osty_rt_strings_ConcatN(int64_t count, const char *const *parts) {
    size_t total = 0;
    int64_t i;
    char *out;
    char *cursor;

    if (count > 0 && parts != NULL) {
        for (i = 0; i < count; i++) {
            if (parts[i] != NULL) {
                total += osty_rt_string_len(parts[i]);
            }
        }
    }
    /* Same heap-only contract as `osty_rt_strings_Concat`: SSO is
     * confined to `osty_rt_string_dup_range`. Concat results stay
     * heap-resident so callers passing the pointer to libc routines
     * (printf %s, fputs) don't crash. */
    out = (char *)osty_gc_allocate_managed(total + 1, OSTY_GC_KIND_STRING, "runtime.strings.concat_n", NULL, NULL);
    cursor = out;
    if (count > 0 && parts != NULL) {
        for (i = 0; i < count; i++) {
            if (parts[i] == NULL) {
                continue;
            }
            size_t n = osty_rt_string_len(parts[i]);
            osty_rt_string_copy_bytes(cursor, parts[i], n);
            cursor += n;
        }
    }
    *cursor = '\0';
    return out;
}

bool osty_rt_strings_Contains(const char *value, const char *substr) {
    char value_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
    char substr_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
    if (value == NULL || substr == NULL) {
        return false;
    }
    /* SSO: decode inline operands so libc's strstr sees real
     * NUL-terminated bytes. */
    osty_rt_string_decode_to_buf_if_inline(&value, value_buf);
    osty_rt_string_decode_to_buf_if_inline(&substr, substr_buf);
    if (substr[0] == '\0') {
        return true;
    }
    return strstr(value, substr) != NULL;
}

bool osty_rt_strings_HasPrefix(const char *value, const char *prefix) {
    size_t prefix_len;
    char value_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
    char prefix_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
    if (value == NULL || prefix == NULL) {
        return false;
    }
    /* Measure pre-decode to avoid the stack-buffer cache pollution
     * described in `osty_rt_strings_Equal`. */
    prefix_len = osty_rt_string_len(prefix);
    osty_rt_string_decode_to_buf_if_inline(&value, value_buf);
    osty_rt_string_decode_to_buf_if_inline(&prefix, prefix_buf);
    return strncmp(value, prefix, prefix_len) == 0;
}

bool osty_rt_strings_HasSuffix(const char *value, const char *suffix) {
    size_t value_len;
    size_t suffix_len;
    char value_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
    char suffix_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
    if (value == NULL || suffix == NULL) {
        return false;
    }
    /* Same pre-decode measurement: see `osty_rt_strings_Equal`. */
    value_len = osty_rt_string_len(value);
    suffix_len = osty_rt_string_len(suffix);
    if (suffix_len > value_len) {
        return false;
    }
    osty_rt_string_decode_to_buf_if_inline(&value, value_buf);
    osty_rt_string_decode_to_buf_if_inline(&suffix, suffix_buf);
    return strncmp(value + (value_len - suffix_len), suffix, suffix_len) == 0;
}

void *osty_rt_strings_Split(const char *value, const char *sep) {
    osty_rt_list *out = (osty_rt_list *)osty_rt_list_new();
    const char *cursor;
    const char *next;
    size_t sep_len;
    char value_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
    char sep_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];

    if (value == NULL) {
        return out;
    }
    /* SSO: decode inline operands so the libc routines (strchr /
     * strstr / strlen) and the byte-walking inner loop see real
     * addresses. The decoded buffer's lifetime is this function
     * frame — fine because we re-dup every piece via
     * `osty_rt_string_dup_range` (which itself may pack inline)
     * before `value_buf` goes out of scope. */
    osty_rt_string_decode_to_buf_if_inline(&value, value_buf);
    osty_rt_string_decode_to_buf_if_inline(&sep, sep_buf);
    if (sep == NULL || sep[0] == '\0') {
        while (*value != '\0') {
            char *piece = osty_rt_string_dup_range(value, 1);
            osty_rt_list_push_ptr(out, piece);
            value += 1;
        }
        return out;
    }
    sep_len = strlen(sep);
    cursor = value;
    /* 1-char-separator fast path: libc's strchr is SIMD-optimized
     * (e.g. NEON / SSE2 byte-wise scan) and dramatically faster than
     * strstr's general-needle algorithm, which the most common splits
     * (`row.split(",")`, `line.split(" ")`, `ts.split(":")`) all hit
     * but were paying full strstr cost for. log-aggregator's hot loop
     * runs `line.split(" ")` + `ts.split(":")` per row × 50K rows;
     * dropping each to strchr removes ~30% of the per-row scan cost.
     */
    if (sep_len == 1) {
        char ch = sep[0];
        while ((next = strchr(cursor, ch)) != NULL) {
            char *piece = osty_rt_string_dup_range(cursor, (size_t)(next - cursor));
            osty_rt_list_push_ptr(out, piece);
            cursor = next + 1;
        }
    } else {
        while ((next = strstr(cursor, sep)) != NULL) {
            char *piece = osty_rt_string_dup_range(cursor, (size_t)(next - cursor));
            osty_rt_list_push_ptr(out, piece);
            cursor = next + sep_len;
        }
    }
    osty_rt_list_push_ptr(out, osty_rt_string_dup_range(cursor, strlen(cursor)));
    return out;
}

/* osty_rt_strings_NthSegment returns the `idx`-th (0-based) split
 * piece as a single allocated String, without ever materialising the
 * full List<String>. Mirrors `osty_rt_strings_Split(value, sep)[idx]`
 * but skips:
 *
 *   - the `osty_rt_list_new` allocation
 *   - the `osty_rt_list_push_ptr` calls for pieces 0..idx-1 (we just
 *     advance the cursor past them; no piece dup)
 *   - the dup of every piece > idx (we stop scanning past idx)
 *
 * The MIR pass `fuseNonEscapingSplitNth` rewrites
 *
 *     let parts = value.split(sep)
 *     let first = parts[K]      // K is a non-negative compile-time const
 *
 * into a single `IntrinsicStringNthSegment` call when `parts` is
 * otherwise unused and the index `K` is constant. Eliminates one
 * list alloc + (N-1) piece dups per call site. markdown-stats's
 * `classify(line)` pattern (`line.split(" ")[0]`) is the canonical
 * target — 20k splits × 1 list alloc removed.
 *
 * Returns the empty interned String when `value` is NULL, when `idx`
 * is negative, or when `idx` is past the last piece — same out-of-
 * range behaviour as `Split()[idx]` would have raised, but turned
 * into a sentinel here so the MIR rewrite can avoid emitting bounds-
 * check IR around the fused call. The bounds check is done at
 * compile time when the index is constant — out-of-range constants
 * fall back to the unfused path. */
OSTY_HOT_INLINE const char *osty_rt_strings_NthSegment(const char *value, const char *sep, int64_t idx) {
    const char *cursor;
    const char *next;
    size_t sep_len;
    int64_t i;
    char value_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
    char sep_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];

    if (value == NULL || idx < 0) {
        return osty_rt_string_dup_range("", 0);
    }
    osty_rt_string_decode_to_buf_if_inline(&value, value_buf);
    if (sep != NULL) {
        osty_rt_string_decode_to_buf_if_inline(&sep, sep_buf);
    }
    /* Empty separator: split per byte (matches Split's empty-sep
     * branch). The Nth piece is the byte at position idx. */
    if (sep == NULL || sep[0] == '\0') {
        for (i = 0; i < idx; i++) {
            if (*value == '\0') {
                return osty_rt_string_dup_range("", 0);
            }
            value += 1;
        }
        if (*value == '\0') {
            return osty_rt_string_dup_range("", 0);
        }
        return osty_rt_string_dup_range(value, 1);
    }
    sep_len = strlen(sep);
    cursor = value;
    /* Walk past the first `idx` separators (1-char fast path mirrors
     * the Split body). On match, the Nth piece runs from `cursor` up
     * to the next separator (or NUL for the last piece). */
    if (sep_len == 1) {
        char ch = sep[0];
        for (i = 0; i < idx; i++) {
            next = strchr(cursor, ch);
            if (next == NULL) {
                /* Out of range — Split would have produced fewer
                 * pieces. Match `[idx]` panic semantics by returning
                 * the empty string here; the MIR fusion only fires
                 * when the bounds-check is statically discharged
                 * (idx < piece_count), so this path is reachable
                 * only via direct runtime callers. */
                return osty_rt_string_dup_range("", 0);
            }
            cursor = next + 1;
        }
        next = strchr(cursor, ch);
    } else {
        for (i = 0; i < idx; i++) {
            next = strstr(cursor, sep);
            if (next == NULL) {
                return osty_rt_string_dup_range("", 0);
            }
            cursor = next + sep_len;
        }
        next = strstr(cursor, sep);
    }
    if (next == NULL) {
        /* Last piece — runs to end of value. */
        return osty_rt_string_dup_range(cursor, strlen(cursor));
    }
    return osty_rt_string_dup_range(cursor, (size_t)(next - cursor));
}

/* osty_rt_strings_SplitInto reuses an existing List<String> as the
 * destination instead of allocating a fresh one each call. The MIR
 * `hoistNonEscapingSplitInLoops` pass rewrites tight-loop calls of the
 * shape `let parts = row.split(",")` (where `parts` doesn't escape the
 * loop body) into a hoisted `out = list_new()` at the preheader plus a
 * per-iteration `SplitInto(out, row, ",")` here. The list backing array
 * stabilises after the first iteration so subsequent calls only allocate
 * the per-piece strings. csv_parse-style 100k×5-piece splits drop ~100k
 * `osty_rt_list_new` allocs and the early `realloc` grows. */
/* OSTY_HOT_INLINE: log-aggregator's parse loop calls SplitInto twice
 * per kept line (line.split(" ") + ts.split(":")). Body walks the
 * source bytes once and pushes pieces — small enough to inline so
 * the per-line cost drops to the byte walk + the (now-inline) push
 * loop body without a call frame. */
OSTY_HOT_INLINE void osty_rt_strings_SplitInto(void *raw_out, const char *value, const char *sep) {
    osty_rt_list *out;
    const char *cursor;
    const char *next;
    size_t sep_len;
    char value_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
    char sep_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];

    if (raw_out == NULL) {
        osty_rt_abort("split_into out is null");
    }
    out = (osty_rt_list *)raw_out;
    /* Preserve the previously-stamped layout (elem_size = sizeof(ptr),
     * trace_elem = osty_gc_mark_slot_v1) by replaying push_ptr through
     * osty_rt_list_clear then push. clear() leaves data/cap untouched. */
    osty_rt_list_clear(out);
    if (value == NULL) {
        return;
    }
    /* SSO: see `osty_rt_strings_Split` rationale. */
    osty_rt_string_decode_to_buf_if_inline(&value, value_buf);
    osty_rt_string_decode_to_buf_if_inline(&sep, sep_buf);
    if (sep == NULL || sep[0] == '\0') {
        while (*value != '\0') {
            char *piece = osty_rt_string_dup_range(value, 1);
            osty_rt_list_push_ptr(out, piece);
            value += 1;
        }
        return;
    }
    sep_len = strlen(sep);
    cursor = value;
    /* Same 1-char fast path as `osty_rt_strings_Split` above. The
     * SplitInto helper is the hot variant after #868's hoisting pass,
     * so this is where most of the strchr win actually lands in
     * loops that call `row.split(",")` per iteration. */
    if (sep_len == 1) {
        char ch = sep[0];
        while ((next = strchr(cursor, ch)) != NULL) {
            char *piece = osty_rt_string_dup_range(cursor, (size_t)(next - cursor));
            osty_rt_list_push_ptr(out, piece);
            cursor = next + 1;
        }
    } else {
        while ((next = strstr(cursor, sep)) != NULL) {
            char *piece = osty_rt_string_dup_range(cursor, (size_t)(next - cursor));
            osty_rt_list_push_ptr(out, piece);
            cursor = next + sep_len;
        }
    }
    osty_rt_list_push_ptr(out, osty_rt_string_dup_range(cursor, strlen(cursor)));
}

// osty_rt_strings_SplitN caps the output at `n` pieces, matching the
// pure-Osty body in internal/stdlib/modules/strings.osty:
//   n == 0 → empty list
//   n <  0 → unbounded split (delegate to osty_rt_strings_Split)
//   n == 1 → single-element list containing the original string
//   n >  1 → split at the first n-1 separator occurrences, then append
//            the remainder as the last element.
// Empty separator falls back to byte-level expansion — we match the
// surrounding Split semantics rather than Osty's char-level split,
// because every other runtime here is byte-level. Toolchain callers
// pass ASCII separators ("+", "-", "."), so this does not regress.
void *osty_rt_strings_SplitN(const char *value, const char *sep, int64_t n) {
    osty_rt_list *out;
    const char *cursor;
    const char *next;
    size_t sep_len;
    int64_t produced;
    char value_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
    char sep_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];

    if (n == 0) {
        return osty_rt_list_new();
    }
    if (n < 0) {
        return osty_rt_strings_Split(value, sep);
    }
    out = (osty_rt_list *)osty_rt_list_new();
    if (value == NULL) {
        osty_rt_list_push_ptr(out, osty_rt_string_dup_range("", 0));
        return out;
    }
    /* SSO: byte-walking + strstr/strlen below need real addresses.
     * Pieces are still re-dup'd via `osty_rt_string_dup_range` (which
     * itself may pack them inline) before `value_buf` goes out of
     * scope, so the stack lifetime is safe. */
    osty_rt_string_decode_to_buf_if_inline(&value, value_buf);
    if (sep != NULL) {
        osty_rt_string_decode_to_buf_if_inline(&sep, sep_buf);
    }
    if (n == 1) {
        osty_rt_list_push_ptr(out, osty_rt_string_dup_range(value, strlen(value)));
        return out;
    }
    if (sep == NULL || sep[0] == '\0') {
        produced = 0;
        while (*value != '\0' && produced + 1 < n) {
            char *piece = osty_rt_string_dup_range(value, 1);
            osty_rt_list_push_ptr(out, piece);
            value += 1;
            produced++;
        }
        osty_rt_list_push_ptr(out, osty_rt_string_dup_range(value, strlen(value)));
        return out;
    }
    sep_len = strlen(sep);
    cursor = value;
    produced = 0;
    while (produced + 1 < n && (next = strstr(cursor, sep)) != NULL) {
        char *piece = osty_rt_string_dup_range(cursor, (size_t)(next - cursor));
        osty_rt_list_push_ptr(out, piece);
        cursor = next + sep_len;
        produced++;
    }
    osty_rt_list_push_ptr(out, osty_rt_string_dup_range(cursor, strlen(cursor)));
    return out;
}

// osty_rt_strings_Fields splits `value` on runs of ASCII whitespace
// (space, tab, newline, carriage return, vertical tab, form feed),
// skipping empty tokens. Mirrors Go's `strings.Fields` and the
// pure-Osty `internal/stdlib/modules/strings.osty:fields` body.
// Byte-level, consistent with the rest of the std.strings shim.
void *osty_rt_strings_Fields(const char *value) {
    osty_rt_list *out = (osty_rt_list *)osty_rt_list_new();
    const char *cursor;
    const char *start;
    char value_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];

    if (value == NULL) {
        return out;
    }
    /* SSO: per-byte iteration via `*cursor` requires a real address. */
    osty_rt_string_decode_to_buf_if_inline(&value, value_buf);
    cursor = value;
    while (*cursor != '\0') {
        while (*cursor == ' ' || *cursor == '\t' || *cursor == '\n' ||
               *cursor == '\r' || *cursor == '\v' || *cursor == '\f') {
            cursor += 1;
        }
        if (*cursor == '\0') {
            break;
        }
        start = cursor;
        while (*cursor != '\0' && *cursor != ' ' && *cursor != '\t' &&
               *cursor != '\n' && *cursor != '\r' && *cursor != '\v' &&
               *cursor != '\f') {
            cursor += 1;
        }
        osty_rt_list_push_ptr(out, osty_rt_string_dup_range(start, (size_t)(cursor - start)));
    }
    return out;
}

// osty_rt_strings_Chars decodes `value` as UTF-8 and pushes each code
// point into the returned list as an i32 (Osty `Char`). Ill-formed
// sequences follow the Unicode 15.0.0 §3.9 "maximal subpart of an
// ill-formed subsequence" recommendation (a.k.a. Unicode TR#36): each
// maximal well-formed prefix that fails to extend into a valid sequence
// emits exactly one U+FFFD, and the offending byte becomes a candidate
// start of the next sequence. The accepted lead/continuation ranges
// come from Table 3-7 "Well-Formed UTF-8 Byte Sequences" so overlongs
// (C0, C1), surrogates (ED A0..BF ...), and code points beyond
// U+10FFFF (F4 90..BF ...) are rejected without decode.
//
// Storage: push_bytes_v1 with elem_size=4 so `osty_rt_list_get_bytes_v1`
// + `load i32` on the read side mirrors the push layout.
void *osty_rt_strings_Chars(const char *value) {
    osty_rt_list *out = (osty_rt_list *)osty_rt_list_new();
    char value_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
    if (value == NULL) {
        return out;
    }
    /* SSO: byte iteration via `*cursor++` requires a real address. */
    osty_rt_string_decode_to_buf_if_inline(&value, value_buf);

    const unsigned char *cursor = (const unsigned char *)value;
    while (*cursor != '\0') {
        unsigned char b1 = *cursor;
        int32_t codepoint;

        if (b1 < 0x80) {
            codepoint = (int32_t)b1;
            cursor++;
            osty_rt_list_push_bytes_v1(out, &codepoint, (int64_t)sizeof(codepoint));
            continue;
        }

        // Determine lead byte class per Table 3-7. For each class we
        // record (expected continuation count, allowed range for the
        // 2nd byte). The 3rd and 4th bytes, when expected, always
        // fall in 0x80..0xBF.
        int continuations = 0;
        unsigned char min2 = 0x80;
        unsigned char max2 = 0xBF;
        int32_t accumulator = 0;
        int lead_ok = 1;

        if (b1 >= 0xC2 && b1 <= 0xDF) {
            continuations = 1;
            accumulator = (int32_t)(b1 & 0x1F);
        } else if (b1 == 0xE0) {
            continuations = 2;
            min2 = 0xA0;
            accumulator = 0;
        } else if ((b1 >= 0xE1 && b1 <= 0xEC) || b1 == 0xEE || b1 == 0xEF) {
            continuations = 2;
            accumulator = (int32_t)(b1 & 0x0F);
        } else if (b1 == 0xED) {
            // Exclude the UTF-16 surrogate range D800..DFFF.
            continuations = 2;
            max2 = 0x9F;
            accumulator = (int32_t)(b1 & 0x0F);
        } else if (b1 == 0xF0) {
            continuations = 3;
            min2 = 0x90;
            accumulator = 0;
        } else if (b1 >= 0xF1 && b1 <= 0xF3) {
            continuations = 3;
            accumulator = (int32_t)(b1 & 0x07);
        } else if (b1 == 0xF4) {
            // Cap the 4-byte range at U+10FFFF.
            continuations = 3;
            max2 = 0x8F;
            accumulator = (int32_t)(b1 & 0x07);
        } else {
            // 0x80..0xBF (continuation in lead position), 0xC0/0xC1
            // (overlong 2-byte lead), 0xF5..0xFF (invalid lead). The
            // maximal well-formed subsequence is empty, so this byte
            // alone becomes one U+FFFD.
            lead_ok = 0;
        }

        if (!lead_ok) {
            codepoint = 0xFFFD;
            cursor++;
            osty_rt_list_push_bytes_v1(out, &codepoint, (int64_t)sizeof(codepoint));
            continue;
        }

        cursor++;
        int consumed_ok = 1;
        for (int i = 0; i < continuations; i++) {
            unsigned char bn = *cursor;
            unsigned char lo = (i == 0) ? min2 : 0x80;
            unsigned char hi = (i == 0) ? max2 : 0xBF;
            if (bn < lo || bn > hi) {
                // Do NOT advance past the offending byte — it becomes
                // a candidate start of the next sequence. The lead
                // plus any accepted continuation bytes collapse to a
                // single U+FFFD.
                consumed_ok = 0;
                break;
            }
            accumulator = (accumulator << 6) | (int32_t)(bn & 0x3F);
            cursor++;
        }
        codepoint = consumed_ok ? accumulator : 0xFFFD;
        osty_rt_list_push_bytes_v1(out, &codepoint, (int64_t)sizeof(codepoint));
    }
    return out;
}

// osty_rt_strings_Bytes pushes each raw byte of `value` as an i8 into the
// returned list. No UTF-8 validation is performed — callers that want
// codepoints should use osty_rt_strings_Chars instead.
void *osty_rt_strings_Bytes(const char *value) {
    osty_rt_list *out;
    const unsigned char *cursor;
    size_t n;
    size_t i;
    int8_t item;
    char value_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];

    out = (osty_rt_list *)osty_rt_list_new();
    if (value == NULL) {
        return out;
    }
    osty_rt_string_decode_to_buf_if_inline(&value, value_buf);
    cursor = (const unsigned char *)value;
    n = osty_rt_string_len(value);
    for (i = 0; i < n; i++) {
        item = (int8_t)cursor[i];
        osty_rt_list_push_bytes_v1(out, &item, (int64_t)sizeof(item));
    }
    return out;
}

const char *osty_rt_strings_Join(void *raw_parts, const char *sep) {
    osty_rt_list *parts;
    int64_t i;
    int64_t count;
    size_t sep_len;
    size_t total;
    char *out;
    char *cursor;
    const char *piece;
    size_t piece_len;

    parts = osty_rt_list_cast(raw_parts);
    if (parts == NULL || parts->len == 0) {
        out = (char *)osty_gc_allocate_managed(1, OSTY_GC_KIND_STRING, "runtime.strings.join.empty", NULL, NULL);
        out[0] = '\0';
        return out;
    }
    count = parts->len;
    sep_len = osty_rt_string_len(sep);
    total = 0;
    for (i = 0; i < count; i++) {
        piece = ((const char **)parts->data)[i];
        if (piece != NULL) {
            total += osty_rt_string_len(piece);
        }
        if (i + 1 < count) {
            total += sep_len;
        }
    }
    out = (char *)osty_gc_allocate_managed(total + 1, OSTY_GC_KIND_STRING, "runtime.strings.join", NULL, NULL);
    cursor = out;
    for (i = 0; i < count; i++) {
        piece = ((const char **)parts->data)[i];
        if (piece != NULL) {
            piece_len = osty_rt_string_len(piece);
            if (piece_len != 0) {
                /* SSO: list elements may be inline-tagged (Split
                 * pieces are the typical Join input), so the copy
                 * routes through `osty_rt_string_copy_bytes` which
                 * branches on the tag. */
                osty_rt_string_copy_bytes(cursor, piece, piece_len);
                cursor += piece_len;
            }
        }
        if (i + 1 < count && sep_len != 0) {
            /* `sep` may also be inline (e.g., a single-char Split
             * delimiter that was itself stored as a tagged constant). */
            osty_rt_string_copy_bytes(cursor, sep, sep_len);
            cursor += sep_len;
        }
    }
    *cursor = '\0';
    return out;
}

const char *osty_rt_strings_Repeat(const char *value, int64_t n) {
    size_t value_len;
    size_t total;
    char *out;
    char *cursor;
    int64_t i;
    char value_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];

    value = (value == NULL) ? "" : value;
    if (n <= 0) {
        return osty_rt_string_dup_site("", 0, "runtime.strings.repeat.empty");
    }
    /* SSO: `osty_rt_string_len` is tag-aware, but the body's
     * `memcpy(cursor, value, value_len)` reads from `value` directly
     * — decode inline operands once at entry so the per-iteration
     * copy stays a plain memcpy. */
    osty_rt_string_decode_to_buf_if_inline(&value, value_buf);
    value_len = osty_rt_string_len(value);
    if (value_len == 0) {
        return osty_rt_string_dup_site("", 0, "runtime.strings.repeat.empty");
    }
    if ((uint64_t)n > (uint64_t)SIZE_MAX / (uint64_t)value_len) {
        osty_rt_abort("runtime.strings.repeat: size overflow");
    }
    total = value_len * (size_t)n;
    out = (char *)osty_gc_allocate_managed(total + 1, OSTY_GC_KIND_STRING, "runtime.strings.repeat", NULL, NULL);
    cursor = out;
    for (i = 0; i < n; i++) {
        memcpy(cursor, value, value_len);
        cursor += value_len;
    }
    *cursor = '\0';
    return out;
}

const char *osty_rt_strings_Replace(const char *value, const char *old, const char *new_value) {
    const char *next;
    size_t value_len;
    size_t old_len;
    size_t new_len;
    size_t prefix_len;
    size_t suffix_len;
    size_t total;
    char *out;
    char value_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
    char old_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
    char new_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];

    value = (value == NULL) ? "" : value;
    old = (old == NULL) ? "" : old;
    new_value = (new_value == NULL) ? "" : new_value;
    /* SSO: strstr/strlen/memcpy below all need real addresses. */
    osty_rt_string_decode_to_buf_if_inline(&value, value_buf);
    osty_rt_string_decode_to_buf_if_inline(&old, old_buf);
    osty_rt_string_decode_to_buf_if_inline(&new_value, new_buf);
    value_len = strlen(value);
    old_len = strlen(old);
    new_len = strlen(new_value);

    if (old_len == 0) {
        if (new_len > SIZE_MAX - value_len) {
            osty_rt_abort("runtime.strings.replace: size overflow");
        }
        total = new_len + value_len;
        out = (char *)osty_gc_allocate_managed(total + 1, OSTY_GC_KIND_STRING, "runtime.strings.replace", NULL, NULL);
        if (new_len != 0) {
            memcpy(out, new_value, new_len);
        }
        if (value_len != 0) {
            memcpy(out + new_len, value, value_len);
        }
        out[total] = '\0';
        return out;
    }

    next = strstr(value, old);
    if (next == NULL) {
        return osty_rt_string_dup_site(value, value_len, "runtime.strings.replace.copy");
    }

    prefix_len = (size_t)(next - value);
    suffix_len = value_len - prefix_len - old_len;
    if (prefix_len > SIZE_MAX - new_len || prefix_len + new_len > SIZE_MAX - suffix_len) {
        osty_rt_abort("runtime.strings.replace: size overflow");
    }
    total = prefix_len + new_len + suffix_len;
    out = (char *)osty_gc_allocate_managed(total + 1, OSTY_GC_KIND_STRING, "runtime.strings.replace", NULL, NULL);
    if (prefix_len != 0) {
        memcpy(out, value, prefix_len);
    }
    if (new_len != 0) {
        memcpy(out + prefix_len, new_value, new_len);
    }
    if (suffix_len != 0) {
        memcpy(out + prefix_len + new_len, next + old_len, suffix_len);
    }
    out[total] = '\0';
    return out;
}

const char *osty_rt_strings_ReplaceAll(const char *value, const char *old, const char *new_value) {
    const char *cursor;
    const char *next;
    size_t value_len;
    size_t old_len;
    size_t new_len;
    size_t total;
    int64_t count;
    char *out;
    char *dst;
    char value_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
    char old_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
    char new_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];

    value = (value == NULL) ? "" : value;
    old = (old == NULL) ? "" : old;
    new_value = (new_value == NULL) ? "" : new_value;
    /* SSO: strstr/strlen/memcpy below all need real addresses. */
    osty_rt_string_decode_to_buf_if_inline(&value, value_buf);
    osty_rt_string_decode_to_buf_if_inline(&old, old_buf);
    osty_rt_string_decode_to_buf_if_inline(&new_value, new_buf);
    value_len = strlen(value);
    old_len = strlen(old);
    new_len = strlen(new_value);

    if (old_len == 0) {
        if (new_len != 0 && value_len + 1 > SIZE_MAX / new_len) {
            osty_rt_abort("runtime.strings.replace_all: size overflow");
        }
        total = value_len + (value_len + 1) * new_len;
        out = (char *)osty_gc_allocate_managed(total + 1, OSTY_GC_KIND_STRING, "runtime.strings.replace_all", NULL, NULL);
        dst = out;
        if (new_len != 0) {
            memcpy(dst, new_value, new_len);
            dst += new_len;
        }
        for (size_t i = 0; i < value_len; i++) {
            *dst++ = value[i];
            if (new_len != 0) {
                memcpy(dst, new_value, new_len);
                dst += new_len;
            }
        }
        *dst = '\0';
        return out;
    }

    count = 0;
    cursor = value;
    while ((next = strstr(cursor, old)) != NULL) {
        count += 1;
        cursor = next + old_len;
    }
    if (count == 0) {
        return osty_rt_string_dup_site(value, value_len, "runtime.strings.replace_all.copy");
    }
    total = value_len;
    if (new_len >= old_len) {
        size_t extra = new_len - old_len;
        if (extra != 0 && (uint64_t)count > (uint64_t)SIZE_MAX / (uint64_t)extra) {
            osty_rt_abort("runtime.strings.replace_all: size overflow");
        }
        total += (size_t)count * extra;
    } else {
        total -= (size_t)count * (old_len - new_len);
    }
    out = (char *)osty_gc_allocate_managed(total + 1, OSTY_GC_KIND_STRING, "runtime.strings.replace_all", NULL, NULL);
    dst = out;
    cursor = value;
    while ((next = strstr(cursor, old)) != NULL) {
        size_t prefix_len = (size_t)(next - cursor);
        if (prefix_len != 0) {
            memcpy(dst, cursor, prefix_len);
            dst += prefix_len;
        }
        if (new_len != 0) {
            memcpy(dst, new_value, new_len);
            dst += new_len;
        }
        cursor = next + old_len;
    }
    if (*cursor != '\0') {
        size_t tail_len = strlen(cursor);
        memcpy(dst, cursor, tail_len);
        dst += tail_len;
    }
    *dst = '\0';
    return out;
}

const char *osty_rt_strings_Slice(const char *value, int64_t start, int64_t end) {
    size_t value_len;
    char value_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];

    value = (value == NULL) ? "" : value;
    /* SSO: strlen + the `value + start` slice both need a real
     * address. */
    osty_rt_string_decode_to_buf_if_inline(&value, value_buf);
    value_len = strlen(value);
    if (start < 0 || end < start) {
        osty_rt_abort("runtime.strings.slice: invalid bounds");
    }
    if ((uint64_t)end > (uint64_t)value_len) {
        osty_rt_abort("runtime.strings.slice: end out of range");
    }
    return osty_rt_string_dup_site(value + start, (size_t)(end - start), "runtime.strings.slice");
}

const char *osty_rt_strings_ToUpper(const char *value) {
    size_t len;
    char *out;
    size_t i;
    char value_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];

    value = (value == NULL) ? "" : value;
    /* SSO: strlen needs a real address. dup_site itself is now SSO-
     * input-aware so it could accept the inline pointer directly,
     * but decoding once at entry lets us call libc strlen instead
     * of `osty_rt_string_len`'s tag dispatch. */
    osty_rt_string_decode_to_buf_if_inline(&value, value_buf);
    len = strlen(value);
    out = (char *)osty_rt_string_dup_site(value, len, "runtime.strings.to_upper");
    for (i = 0; i < len; i++) {
        unsigned char c = (unsigned char)out[i];
        if (c >= 'a' && c <= 'z') {
            out[i] = (char)(c - ('a' - 'A'));
        }
    }
    return out;
}

const char *osty_rt_strings_ToLower(const char *value) {
	size_t len;
	char *out;
	size_t i;
	char value_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];

    value = (value == NULL) ? "" : value;
    /* SSO: see `osty_rt_strings_ToUpper`. */
    osty_rt_string_decode_to_buf_if_inline(&value, value_buf);
    len = strlen(value);
    out = (char *)osty_rt_string_dup_site(value, len, "runtime.strings.to_lower");
    for (i = 0; i < len; i++) {
        unsigned char c = (unsigned char)out[i];
        if (c >= 'A' && c <= 'Z') {
            out[i] = (char)(c + ('a' - 'A'));
        }
	}
	return out;
}

bool osty_rt_strings_IsValidInt(const char *value) {
	char *end = NULL;
	char value_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];

	value = (value == NULL) ? "" : value;
	/* SSO: strtoll + the leading-byte checks need a real address. */
	osty_rt_string_decode_to_buf_if_inline(&value, value_buf);
	if (value[0] == '\0' ||
		value[0] == ' ' || value[0] == '\t' || value[0] == '\n' ||
		value[0] == '\r' || value[0] == '\v' || value[0] == '\f') {
		return false;
	}
	errno = 0;
	(void)strtoll(value, &end, 10);
	if (end == value || end == NULL || *end != '\0' || errno == ERANGE) {
		return false;
	}
	return true;
}

int64_t osty_rt_strings_ToInt(const char *value) {
	char *end = NULL;
	long long parsed;
	char value_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];

	value = (value == NULL) ? "" : value;
	/* SSO: decode once at entry — both `IsValidInt` and `strtoll`
	 * below need a real address. Decoding here means `IsValidInt`
	 * receives an already-heap pointer and skips its own decode. */
	osty_rt_string_decode_to_buf_if_inline(&value, value_buf);
	if (!osty_rt_strings_IsValidInt(value)) {
		osty_rt_abort("runtime.strings.to_int: invalid integer");
	}
	errno = 0;
	parsed = strtoll(value, &end, 10);
	if (end == value || end == NULL || *end != '\0' || errno == ERANGE) {
		osty_rt_abort("runtime.strings.to_int: invalid integer");
	}
	return (int64_t)parsed;
}

bool osty_rt_strings_IsValidFloat(const char *value) {
	char *end = NULL;
	char value_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];

	value = (value == NULL) ? "" : value;
	/* SSO: strtod + the leading-byte checks need a real address. */
	osty_rt_string_decode_to_buf_if_inline(&value, value_buf);
	if (value[0] == '\0' ||
		value[0] == ' ' || value[0] == '\t' || value[0] == '\n' ||
		value[0] == '\r' || value[0] == '\v' || value[0] == '\f') {
		return false;
	}
	errno = 0;
	(void)strtod(value, &end);
	if (end == value || end == NULL || *end != '\0' || errno == ERANGE) {
		return false;
	}
	return true;
}

double osty_rt_strings_ToFloat(const char *value) {
	char *end = NULL;
	double parsed;
	char value_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];

	value = (value == NULL) ? "" : value;
	/* SSO: see `osty_rt_strings_ToInt`. */
	osty_rt_string_decode_to_buf_if_inline(&value, value_buf);
	if (!osty_rt_strings_IsValidFloat(value)) {
		osty_rt_abort("runtime.strings.to_float: invalid float");
	}
	errno = 0;
	parsed = strtod(value, &end);
	if (end == value || end == NULL || *end != '\0' || errno == ERANGE) {
		osty_rt_abort("runtime.strings.to_float: invalid float");
	}
	return parsed;
}

const char *osty_rt_strings_TrimPrefix(const char *value, const char *prefix) {
	const char *start;
	char value_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
	char prefix_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];

    if (value == NULL) {
        return osty_rt_string_dup_site("", 0, "runtime.strings.trim_prefix.empty");
    }
    /* SSO: strlen + strncmp need real addresses. */
    osty_rt_string_decode_to_buf_if_inline(&value, value_buf);
    if (prefix != NULL) {
        osty_rt_string_decode_to_buf_if_inline(&prefix, prefix_buf);
    }
    start = value;
    if (prefix != NULL) {
        size_t prefix_len = strlen(prefix);
        if (prefix_len != 0 && strncmp(value, prefix, prefix_len) == 0) {
            start = value + prefix_len;
        }
    }
    return osty_rt_string_dup_site(start, strlen(start), "runtime.strings.trim_prefix");
}

const char *osty_rt_strings_TrimSuffix(const char *value, const char *suffix) {
    size_t value_len;
    size_t suffix_len;
    char value_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
    char suffix_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];

    if (value == NULL) {
        return osty_rt_string_dup_site("", 0, "runtime.strings.trim_suffix.empty");
    }
    /* SSO: strlen + strncmp need real addresses. */
    osty_rt_string_decode_to_buf_if_inline(&value, value_buf);
    if (suffix != NULL) {
        osty_rt_string_decode_to_buf_if_inline(&suffix, suffix_buf);
    }
    value_len = strlen(value);
    suffix_len = (suffix == NULL) ? 0 : strlen(suffix);
    if (suffix_len != 0 && suffix_len <= value_len &&
        strncmp(value + (value_len - suffix_len), suffix, suffix_len) == 0) {
        return osty_rt_string_dup_site(value, value_len - suffix_len, "runtime.strings.trim_suffix");
    }
    return osty_rt_string_dup_site(value, value_len, "runtime.strings.trim_suffix");
}

const char *osty_rt_strings_TrimStart(const char *value) {
    const char *start;
    char value_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];

    if (value == NULL) {
        return osty_rt_string_dup_site("", 0, "runtime.strings.trim_start.empty");
    }
    /* SSO: byte-walking + strlen need a real address. */
    osty_rt_string_decode_to_buf_if_inline(&value, value_buf);
    start = value;
    while (*start == ' ' || *start == '\t' || *start == '\n' || *start == '\r' || *start == '\v' || *start == '\f') {
        start++;
    }
    return osty_rt_string_dup_site(start, strlen(start), "runtime.strings.trim_start");
}

const char *osty_rt_strings_TrimEnd(const char *value) {
    const char *end;
    size_t len;
    char value_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];

    if (value == NULL) {
        return osty_rt_string_dup_site("", 0, "runtime.strings.trim_end.empty");
    }
    /* SSO: strlen + tail-walking need a real address. */
    osty_rt_string_decode_to_buf_if_inline(&value, value_buf);
    end = value + strlen(value);
    while (end > value) {
        char c = *(end - 1);
        if (c != ' ' && c != '\t' && c != '\n' && c != '\r' && c != '\v' && c != '\f') {
            break;
        }
        end--;
    }
    len = (size_t)(end - value);
    return osty_rt_string_dup_site(value, len, "runtime.strings.trim_end");
}

const char *osty_rt_strings_TrimSpace(const char *value) {
    const char *start;
    const char *end;
    size_t len;
    char *out;
    char value_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];

    if (value == NULL) {
        out = (char *)osty_gc_allocate_managed(1, OSTY_GC_KIND_STRING, "runtime.strings.trim_space.empty", NULL, NULL);
        out[0] = '\0';
        return out;
    }
    /* SSO: byte-walking from both ends + strlen-equivalent. */
    osty_rt_string_decode_to_buf_if_inline(&value, value_buf);
    start = value;
    while (*start == ' ' || *start == '\t' || *start == '\n' || *start == '\r' || *start == '\v' || *start == '\f') {
        start++;
    }
    end = start + strlen(start);
    while (end > start) {
        char c = *(end - 1);
        if (c != ' ' && c != '\t' && c != '\n' && c != '\r' && c != '\v' && c != '\f') {
            break;
        }
        end--;
    }
    len = (size_t)(end - start);
    out = (char *)osty_gc_allocate_managed(len + 1, OSTY_GC_KIND_STRING, "runtime.strings.trim_space", NULL, NULL);
    if (len != 0) {
        memcpy(out, start, len);
    }
    out[len] = '\0';
    return out;
}

void *osty_rt_strings_ToBytes(const char *value) {
    size_t len;
    size_t total;
    osty_rt_bytes *out;
    unsigned char *data;
    char value_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];

    value = (value == NULL) ? "" : value;
    /* SSO: strlen + memcpy below need a real address. */
    osty_rt_string_decode_to_buf_if_inline(&value, value_buf);
    len = strlen(value);
    if (len > SIZE_MAX - sizeof(osty_rt_bytes)) {
        osty_rt_abort("runtime.strings.to_bytes: size overflow");
    }
    total = sizeof(osty_rt_bytes) + len;
    out = (osty_rt_bytes *)osty_gc_allocate_managed(total, OSTY_GC_KIND_BYTES, "runtime.strings.to_bytes", NULL, NULL);
    data = (unsigned char *)(out + 1);
    out->data = data;
    out->len = (int64_t)len;
    if (len != 0) {
        memcpy(data, value, len);
    }
    return out;
}

static void *osty_rt_map_value_slot(osty_rt_map *map, int64_t index) {
    return (void *)(map->values + ((size_t)index * map->value_size));
}

static void *osty_rt_map_key_slot(osty_rt_map *map, int64_t index) {
    return (void *)(map->keys + ((size_t)index * osty_rt_kind_size(map->key_kind)));
}

static void osty_rt_map_index_clear(osty_rt_map *map) {
    if (map->index_slots != NULL && map->index_slots != map->index_inline) {
        free(map->index_slots);
    }
    if (map->index_hashes != NULL && map->index_hashes != map->index_hashes_inline) {
        free(map->index_hashes);
    }
    map->index_slots = NULL;
    map->index_hashes = NULL;
    map->index_cap = 0;
    map->index_len = 0;
}

static void osty_rt_map_index_insert_slot_hashed(osty_rt_map *map, int64_t slot, size_t hash) {
    uint64_t mask = (uint64_t)(map->index_cap - 1);
    uint64_t idx = (uint64_t)hash & mask;
    uint32_t fingerprint = osty_rt_map_key_fingerprint(hash);
    while (map->index_slots[idx] != 0) {
        idx = (idx + 1) & mask;
    }
    map->index_slots[idx] = slot + 1;
    map->index_hashes[idx] = fingerprint;
    map->index_len += 1;
}

static void osty_rt_map_index_rebuild(osty_rt_map *map, int64_t live_len) {
    int64_t cap = 8;
    int64_t i;

    if (live_len < 8) {
        osty_rt_map_index_clear(map);
        return;
    }
    while (cap < live_len * 2) {
        if (cap > INT64_MAX / 2) {
            cap = live_len * 2;
            break;
        }
        cap *= 2;
    }
    if (cap <= 0) {
        osty_rt_abort("map index capacity overflow");
    }
    if (cap <= OSTY_RT_MAP_INLINE_INDEX_CAP) {
        if (map->index_slots != NULL && map->index_slots != map->index_inline) {
            free(map->index_slots);
        }
        if (map->index_hashes != NULL && map->index_hashes != map->index_hashes_inline) {
            free(map->index_hashes);
        }
        map->index_slots = map->index_inline;
        map->index_hashes = map->index_hashes_inline;
        memset(map->index_slots, 0, (size_t)cap * sizeof(int64_t));
        memset(map->index_hashes, 0, (size_t)cap * sizeof(uint32_t));
        map->index_cap = cap;
    } else {
        if (map->index_cap != cap || map->index_slots == NULL || map->index_slots == map->index_inline ||
            map->index_hashes == NULL || map->index_hashes == map->index_hashes_inline) {
            if (map->index_slots != NULL && map->index_slots != map->index_inline) {
                free(map->index_slots);
            }
            if (map->index_hashes != NULL && map->index_hashes != map->index_hashes_inline) {
                free(map->index_hashes);
            }
            map->index_slots = (int64_t *)calloc((size_t)cap, sizeof(int64_t));
            map->index_hashes = (uint32_t *)calloc((size_t)cap, sizeof(uint32_t));
            if (map->index_slots == NULL || map->index_hashes == NULL) {
                osty_rt_abort("out of memory");
            }
            map->index_cap = cap;
        } else {
            memset(map->index_slots, 0, (size_t)map->index_cap * sizeof(int64_t));
            memset(map->index_hashes, 0, (size_t)map->index_cap * sizeof(uint32_t));
        }
    }
    map->index_len = 0;
    for (i = 0; i < map->len; i++) {
        size_t hash = osty_rt_map_key_hash(map->key_kind, osty_rt_map_key_slot(map, i));
        osty_rt_map_index_insert_slot_hashed(map, i, hash);
    }
}

static void osty_rt_map_reserve(osty_rt_map *map, int64_t min_cap) {
    int64_t next_cap = map->cap;
    size_t key_bytes;
    size_t value_bytes;

    if (min_cap <= map->cap) {
        return;
    }
    if (next_cap < 4) {
        next_cap = 4;
    }
    while (next_cap < min_cap) {
        if (next_cap > INT64_MAX / 2) {
            next_cap = min_cap;
            break;
        }
        next_cap *= 2;
    }
    key_bytes = (size_t)next_cap * osty_rt_kind_size(map->key_kind);
    value_bytes = (size_t)next_cap * map->value_size;
    map->keys = (unsigned char *)realloc(map->keys, key_bytes);
    map->values = (unsigned char *)realloc(map->values, value_bytes);
    if (map->keys == NULL || map->values == NULL) {
        osty_rt_abort("out of memory");
    }
    map->cap = next_cap;
}

static OSTY_HOT_INLINE int64_t osty_rt_map_find_index_linear(osty_rt_map *map, const void *key) {
    int64_t i;
    size_t key_size = osty_rt_kind_size(map->key_kind);
    for (i = 0; i < map->len; i++) {
        if (osty_rt_value_equals(osty_rt_map_key_slot(map, i), key, key_size, map->key_kind)) {
            return i;
        }
    }
    return -1;
}

/* OSTY_HOT_INLINE: this is the Map probe loop body. Inlining it at
 * each keyed wrapper call site lets ThinLTO specialize the loop on
 * the wrapper's constant `key_kind` field (folding the dispatch in
 * `osty_rt_value_equals` and `osty_rt_map_key_hash` to the matching
 * arm), and CSE the load of `map->index_cap` / `map->index_slots`
 * etc. against neighbouring map struct accesses. Code growth at the
 * call site is bounded by ThinLTO's import-instr-limit (500 post-#898);
 * the probe body is well under that even with inlines pulled in. */
static OSTY_HOT_INLINE int64_t osty_rt_map_find_index_indexed(osty_rt_map *map, const void *key) {
    size_t key_hash = osty_rt_map_key_hash(map->key_kind, key);
    uint32_t key_fingerprint = osty_rt_map_key_fingerprint(key_hash);
    size_t key_size = osty_rt_kind_size(map->key_kind);
    uint64_t mask = (uint64_t)(map->index_cap - 1);
    uint64_t idx = (uint64_t)key_hash & mask;

    for (;;) {
        int64_t entry = map->index_slots[idx];
        int64_t slot;
        if (entry == 0) {
            return -1;
        }
        slot = entry - 1;
        if (slot >= 0 && slot < map->len &&
            map->index_hashes[idx] == key_fingerprint &&
            osty_rt_value_equals(osty_rt_map_key_slot(map, slot), key, key_size, map->key_kind)) {
            return slot;
        }
        idx = (idx + 1) & mask;
    }
}

/* OSTY_HOT_INLINE on the dispatcher only — `find_index_indexed` (with
 * the probe loop) and `find_index_linear` (small-map scan) keep their
 * out-of-line forms because either body has enough instructions that
 * inlining at every Map call site would balloon code. ThinLTO still
 * has the option to inline opportunistically, but the dispatcher
 * itself is just a branch + call, worth always inlining so the
 * branch can fold to the indexed path once `index_cap > 0`. */
static OSTY_HOT_INLINE int64_t osty_rt_map_find_index(osty_rt_map *map, const void *key) {
    if (map->index_cap > 0 && map->index_slots != NULL) {
        return osty_rt_map_find_index_indexed(map, key);
    }
    return osty_rt_map_find_index_linear(map, key);
}

static OSTY_HOT_INLINE osty_rt_map *osty_rt_map_cast(void *raw_map) {
    if (raw_map == NULL) {
        osty_rt_abort("map is null");
    }
    return (osty_rt_map *)osty_gc_load_v1(raw_map);
}

void *osty_rt_map_new(int64_t key_kind, int64_t value_kind, int64_t value_size, osty_rt_trace_slot_fn value_trace) {
    osty_rt_map *map;
    if (value_size <= 0) {
        osty_rt_abort("invalid map value size");
    }
    if (key_kind != OSTY_RT_ABI_I64 && key_kind != OSTY_RT_ABI_I1 && key_kind != OSTY_RT_ABI_F64 && key_kind != OSTY_RT_ABI_PTR && key_kind != OSTY_RT_ABI_STRING) {
        osty_rt_abort("unsupported map key kind");
    }
    map = (osty_rt_map *)osty_gc_allocate_managed(sizeof(osty_rt_map), OSTY_GC_KIND_MAP, "runtime.map", osty_rt_map_trace, osty_rt_map_destroy);
    map->key_kind = key_kind;
    map->value_kind = value_kind;
    map->value_size = (size_t)value_size;
    map->value_trace = value_trace;
    map->index_cap = 0;
    map->index_len = 0;
    map->index_slots = NULL;
    map->index_hashes = NULL;
    osty_rt_map_mutex_init_or_abort(map);
    return map;
}

// Public lock/unlock for composite ops (update) that must hold the
// lock across get + callback + insert. Recursive mutex so calls from
// a user callback into the same map (e.g. counts.len()) re-acquire
// instead of self-deadlocking.
//
// Single-threaded fast path: when `osty_gc_serialized_now()` (no
// user worker threads AND the bg marker is parked), there are no
// other threads that could observe partial state, so both lock and
// unlock become no-ops. This is safe because:
//   - The counter is incremented by the spawning thread BEFORE the
//     new worker thread starts (see osty_rt_task_spawn), so a live
//     worker always has workers >= 1 from every reader's view.
//   - The counter is decremented only when workers join, which can
//     only happen AFTER the current thread already observed > 0.
//   - Readers on the spawning thread that see == 0 know the runtime
//     is single-threaded at that moment; another thread cannot
//     concurrently appear without this thread first incrementing
//     the counter (which only happens via taskGroup → spawn), so
//     the decision is stable for the duration of the op.
//
// The saving is substantial: every keyed op currently pays one
// CRITICAL_SECTION enter/leave pair (Windows) or pthread_mutex
// lock/unlock pair (POSIX) whether or not another thread exists.
// In pure-main-thread programs that's 100% overhead on every
// Map op — common case for CLI tools, build workloads, and the
// whole osty-vs-go benchmark suite.
/* OSTY_HOT_INLINE on lock/unlock: in single-mutator programs (every
 * benchmark in the suite, every CLI tool) every Map op pays a
 * lock+unlock pair where both bodies short-circuit on the
 * `osty_concurrent_workers == 0` check. Forcing inline lets ThinLTO
 * collapse the pair to a pair of branches, then CSE the `cast` /
 * `gc_load_v1` against the actual `_raw` op's cast, then DCE the
 * branches once it sees the constant flag flow. Net: 2 cross-TU
 * function calls + 2 redundant `gc_load_v1`s removed per Map op. */
OSTY_HOT_INLINE void osty_rt_map_lock(void *raw_map) {
    if (!osty_rt_runtime_has_concurrency()) return;
    osty_rt_map *map = osty_rt_map_cast(raw_map);
    if (map == NULL || !map->mu_init) return;
    if (osty_gc_serialized_now()) return;
    osty_rt_rmu_lock(&map->mu);
}

OSTY_HOT_INLINE void osty_rt_map_unlock(void *raw_map) {
    if (!osty_rt_runtime_has_concurrency()) return;
    osty_rt_map *map = osty_rt_map_cast(raw_map);
    if (map == NULL || !map->mu_init) return;
    if (osty_gc_serialized_now()) return;
    osty_rt_rmu_unlock(&map->mu);
}

static OSTY_HOT_INLINE bool osty_rt_map_contains_raw(void *raw_map, const void *key) {
    osty_rt_map *map = osty_rt_map_cast(raw_map);
    return map != NULL && osty_rt_map_find_index(map, key) >= 0;
}

static OSTY_HOT_INLINE void osty_rt_map_insert_raw(void *raw_map, const void *key, const void *value) {
    osty_rt_map *map = osty_rt_map_cast(raw_map);
    int64_t index;
    size_t key_size;
    bool inserted = false;
    if (map == NULL || key == NULL || value == NULL) {
        osty_rt_abort("invalid map insert");
    }
    key_size = osty_rt_kind_size(map->key_kind);
    index = osty_rt_map_find_index(map, key);
    if (index < 0) {
        osty_rt_map_reserve(map, map->len + 1);
        index = map->len;
        map->len += 1;
        inserted = true;
    }
    memcpy(osty_rt_map_key_slot(map, index), key, key_size);
    memcpy(osty_rt_map_value_slot(map, index), value, map->value_size);
    /* Phase E follow-up: post-write barrier for managed keys/values.
     * Without this `Map<String, _>` writes go untracked — cheney_minor
     * has to walk every pinned OLD owner and re-trace its full payload
     * to find OLD→YOUNG edges, paying O(map.len) per Map per minor
     * cycle. Wiring the barrier feeds remembered_edges so cheney can
     * forward only the actual managed pointers without invoking the
     * full tracer. STRING and PTR key/value kinds are the managed-
     * pointer cases; scalar kinds (i64/i1/f64) skip the barrier. */
    if (map->key_kind == OSTY_RT_ABI_STRING || map->key_kind == OSTY_RT_ABI_PTR) {
        void *key_value = NULL;
        memcpy(&key_value, key, sizeof(key_value));
        if (key_value != NULL) {
            osty_gc_post_write_v1(map, key_value, OSTY_GC_KIND_MAP);
        }
    }
    if (map->value_kind == OSTY_RT_ABI_STRING ||
        map->value_kind == OSTY_RT_ABI_PTR) {
        void *value_value = NULL;
        memcpy(&value_value, value, sizeof(value_value));
        if (value_value != NULL) {
            osty_gc_post_write_v1(map, value_value, OSTY_GC_KIND_MAP);
        }
    }
    if (inserted) {
        if (map->len >= 8) {
            if (map->index_cap == 0 || map->index_slots == NULL || map->index_hashes == NULL ||
                (map->index_len + 1) * 10 >= map->index_cap * 7) {
                osty_rt_map_index_rebuild(map, map->len);
            } else {
                size_t hash = osty_rt_map_key_hash(map->key_kind, osty_rt_map_key_slot(map, index));
                osty_rt_map_index_insert_slot_hashed(map, index, hash);
            }
        }
    }
}

static bool osty_rt_map_remove_raw(void *raw_map, const void *key) {
    osty_rt_map *map = osty_rt_map_cast(raw_map);
    int64_t index;
    size_t key_size;
    size_t value_size;
    if (map == NULL || key == NULL) {
        osty_rt_abort("invalid map remove");
    }
    index = osty_rt_map_find_index(map, key);
    if (index < 0) {
        return false;
    }
    key_size = osty_rt_kind_size(map->key_kind);
    value_size = map->value_size;
    if (index + 1 < map->len) {
        memmove(osty_rt_map_key_slot(map, index), osty_rt_map_key_slot(map, index + 1), (size_t)(map->len - index - 1) * key_size);
        memmove(osty_rt_map_value_slot(map, index), osty_rt_map_value_slot(map, index + 1), (size_t)(map->len - index - 1) * value_size);
    }
    map->len -= 1;
    if (map->index_cap != 0 || map->index_slots != NULL) {
        osty_rt_map_index_rebuild(map, map->len);
    }
    return true;
}

static OSTY_HOT_INLINE void osty_rt_map_get_or_abort_raw(void *raw_map, const void *key, void *out_value) {
    osty_rt_map *map = osty_rt_map_cast(raw_map);
    int64_t index;
    if (map == NULL || key == NULL || out_value == NULL) {
        osty_rt_abort("invalid map get");
    }
    index = osty_rt_map_find_index(map, key);
    if (index < 0) {
        osty_rt_abort("map key not found");
    }
    memcpy(out_value, osty_rt_map_value_slot(map, index), map->value_size);
}

// Option-returning get: fills *out_value and returns true when the key
// is present, returns false otherwise (and leaves out_value untouched).
// This is the real intrinsic backing `Map.get(key) -> V?` — callers at
// the LLVM layer use the return to construct an Option<V>, then feed
// it into `??`, `match`, `.isSome()`, etc. without needing per-helper
// special-case lowering.
static OSTY_HOT_INLINE bool osty_rt_map_get_raw(void *raw_map, const void *key, void *out_value) {
    osty_rt_map *map = osty_rt_map_cast(raw_map);
    int64_t index;
    if (map == NULL || key == NULL || out_value == NULL) {
        return false;
    }
    index = osty_rt_map_find_index(map, key);
    if (index < 0) {
        return false;
    }
    memcpy(out_value, osty_rt_map_value_slot(map, index), map->value_size);
    return true;
}

// Value-at-slot (V-type-agnostic): memcpy the V stored at slot i
// into *out_value. Kept as a standalone helper for callers that only
// need V; the general map for-in path uses the combined entry_at
// helpers below to snapshot K and V under one lock.
void osty_rt_map_value_at(void *raw_map, int64_t index, void *out_value) {
    osty_rt_map *map = osty_rt_map_cast(raw_map);
    if (map == NULL || out_value == NULL) {
        osty_rt_abort("invalid map value_at");
    }
    osty_rt_map_lock(raw_map);
    if (index < 0 || index >= map->len) {
        osty_rt_map_unlock(raw_map);
        osty_rt_abort("map value_at index out of bounds");
    }
    memcpy(out_value, osty_rt_map_value_slot(map, index), map->value_size);
    osty_rt_map_unlock(raw_map);
}

int64_t osty_rt_map_len(void *raw_map) {
    osty_rt_map *map = osty_rt_map_cast(raw_map);
    int64_t n;
    if (map == NULL) {
        osty_rt_abort("map is null");
    }
    osty_rt_map_lock(raw_map);
    n = map->len;
    osty_rt_map_unlock(raw_map);
    return n;
}

// osty_rt_map_clear truncates the map to zero entries. Backing storage
// (keys/values/capacity/kind metadata) is preserved so subsequent
// inserts keep the same key/value kinds and re-use the allocation.
// Key and value slots past len are no longer traced (osty_rt_map_trace
// bounds by len), so cleared entries become unreachable from this map.
// Runs under the map's recursive mutex so the zero-out is atomic with
// respect to concurrent readers / iterators.
void osty_rt_map_clear(void *raw_map) {
    osty_rt_map *map = osty_rt_map_cast(raw_map);
    if (map == NULL) {
        osty_rt_abort("map.clear on nil receiver");
    }
    osty_rt_map_lock(raw_map);
    map->len = 0;
    osty_rt_map_index_clear(map);
    osty_rt_map_unlock(raw_map);
}

void *osty_rt_map_keys(void *raw_map) {
    if (raw_map == NULL) {
        osty_rt_abort("map is null");
    }
    /* Root-bind BEFORE casting `raw_map` AND `out`. With Map
     * young-eligible (#977), `root_bind` auto-promotes the live young
     * Map to OLD via the safe-clone handoff that NULLs the source's
     * keys/values pointers. If we cast first, `map` freezes the stale
     * young address and the subsequent `map->keys` read finds NULL →
     * `list_copy_initialized` aborts with "list copy source is null".
     * Bind first so the cast resolves through `osty_gc_load_v1`'s
     * PROMOTED follow and returns the OLD payload. Same reorder also
     * applies to `out` (List young-eligibility hit the identical
     * pattern). */
    osty_gc_root_bind_v1(raw_map);
    void *out = osty_rt_list_new();
    osty_gc_root_bind_v1(out);
    osty_rt_map *map = osty_rt_map_cast(raw_map);
    if (map == NULL) {
        osty_rt_abort("map is null");
    }
    osty_rt_list *keys = osty_rt_list_cast(out);
    int64_t count = 0;
    osty_rt_map_lock(raw_map);
    switch (map->key_kind) {
    case OSTY_RT_ABI_I64:
        osty_rt_list_ensure_layout(keys, sizeof(int64_t), NULL);
        break;
    case OSTY_RT_ABI_I1:
        osty_rt_list_ensure_layout(keys, sizeof(bool), NULL);
        break;
    case OSTY_RT_ABI_F64:
        osty_rt_list_ensure_layout(keys, sizeof(double), NULL);
        break;
    case OSTY_RT_ABI_PTR:
    case OSTY_RT_ABI_STRING:
        osty_rt_list_ensure_layout(keys, sizeof(void *), osty_gc_mark_slot_v1);
        break;
    default:
        osty_rt_abort("unsupported map key list kind");
    }
    count = map->len;
    if (count > 0) {
        osty_rt_list_copy_initialized(out, keys, map->keys, count);
    } else {
        keys->len = 0;
    }
    osty_rt_map_unlock(raw_map);
    osty_gc_root_release_v1(out);
    osty_gc_root_release_v1(raw_map);
    return out;
}

/* osty_rt_map_keys_sorted_<suffix> — `map.keys()` + `list.sorted()`
 * fused into one allocation. The unfused sequence creates the keys
 * list, then allocates a *second* list inside sorted() and copies
 * into it before qsort. Fusing saves the second list allocation —
 * we qsort the freshly-built keys buffer in place before the caller
 * ever sees it. Matches the `.keys().sorted()` shape every
 * Map-aggregation bench (word_freq, record_pipeline, csv_parse) ends
 * with. One variant per key suffix to route through the right
 * comparator; i64 and string are wired because those are the key
 * kinds the osty-vs-go sweep exercises. */
void *osty_rt_map_keys_sorted_string(void *raw_map) {
    void *keys = osty_rt_map_keys(raw_map);
    osty_rt_list *list = osty_rt_list_cast(keys);
    if (list->len > 1) {
        qsort(list->data, (size_t)list->len, sizeof(void *), osty_rt_compare_string_ascending);
    }
    return keys;
}

void *osty_rt_map_keys_sorted_i64(void *raw_map) {
    void *keys = osty_rt_map_keys(raw_map);
    osty_rt_list *list = osty_rt_list_cast(keys);
    if (list->len > 1) {
        qsort(list->data, (size_t)list->len, sizeof(int64_t), osty_rt_compare_i64_ascending);
    }
    return keys;
}

/* Map<K, V>.toString — `{k1: v1, k2: v2}`-shaped. Reuses the per-
 * element formatters from the list_to_string family; a single
 * runtime entry is enough because the Map struct already carries
 * `key_kind` / `value_kind` (set at `osty_rt_map_new` time) and we
 * dispatch on those internally. The value-side formatter has to know
 * the slot size — Map<K, Int> stores 8-byte values, Map<K, Bool>
 * stores `sizeof(bool)` — so we look it up from `map->value_size`
 * directly rather than recomputing from `value_kind`. Keys always
 * use the kind's canonical width via `osty_rt_kind_size`.
 *
 * Format: empty → "{}". Pairs joined with ", "; keys and values
 * separated by ": ". String values are quoted to match the
 * list_to_string convention so users can pattern-match round-trip
 * representations across collection kinds. */
static osty_rt_list_format_one_fn osty_rt_map_pick_kind_formatter(int64_t kind) {
    switch (kind) {
    case OSTY_RT_ABI_I64:
        return osty_rt_list_format_i64;
    case OSTY_RT_ABI_F64:
        return osty_rt_list_format_f64;
    case OSTY_RT_ABI_I1:
        return osty_rt_list_format_i1;
    case OSTY_RT_ABI_STRING:
        return osty_rt_list_format_string;
    case OSTY_RT_ABI_PTR:
        /* Reuse the string formatter for the ptr lane: today the only
         * ptr-shaped Map keys/values that show up at this entry are
         * Strings (per `osty_rt_map_key_hash`'s OSTY_RT_ABI_PTR arm,
         * which already special-cases String content). Composite ptr
         * values trigger the abort below — the caller gets a clear
         * message instead of garbage bytes. */
        return osty_rt_list_format_string;
    default:
        return NULL;
    }
}

const char *osty_rt_map_to_string(void *raw_map) {
    osty_rt_map *map = osty_rt_map_cast(raw_map);
    if (map == NULL || map->len == 0) {
        return osty_rt_string_dup_site("{}", 2, "runtime.map.to_string");
    }
    osty_rt_list_format_one_fn fmt_key = osty_rt_map_pick_kind_formatter(map->key_kind);
    osty_rt_list_format_one_fn fmt_val = osty_rt_map_pick_kind_formatter(map->value_kind);
    if (fmt_key == NULL || fmt_val == NULL) {
        osty_rt_abort("runtime.map.to_string: unsupported key/value kind");
    }
    size_t key_size = osty_rt_kind_size(map->key_kind);
    size_t value_size = map->value_size;
    size_t cap = 64;
    size_t len = 0;
    char *buf = (char *)malloc(cap);
    if (buf == NULL) {
        osty_rt_abort("runtime.map.to_string: out of memory");
    }
    buf[len++] = '{';
    osty_rt_map_lock(raw_map);
    for (int64_t i = 0; i < map->len; i++) {
        if (i > 0) {
            osty_rt_list_to_string_append(&buf, &cap, &len, ", ", 2);
        }
        const unsigned char *key_slot = map->keys + (size_t)i * key_size;
        const unsigned char *value_slot = map->values + (size_t)i * value_size;
        fmt_key(&buf, &cap, &len, key_slot);
        osty_rt_list_to_string_append(&buf, &cap, &len, ": ", 2);
        fmt_val(&buf, &cap, &len, value_slot);
    }
    osty_rt_map_unlock(raw_map);
    osty_rt_list_to_string_grow(&buf, &cap, len + 2);
    buf[len++] = '}';
    const char *out = osty_rt_string_dup_site(buf, len, "runtime.map.to_string");
    free(buf);
    return out;
}

// Every public keyed op takes the per-map lock, runs the raw op, and
// releases. Recursive so that `update`'s outer lock + a re-entrant op
// from a user callback (e.g. counts.len() inside f) don't deadlock.
// key_at snapshots the key under the lock — the return is by-value so
// the caller doesn't hold any reference past unlock.
/* OSTY_HOT_INLINE on the keyed Map wrappers: every Map op the IR emits
 * lands on one of these (`get_<suffix>`, `contains_<suffix>`, etc.).
 * Without `always_inline`, ThinLTO weighs the wrapper body — lock +
 * raw + unlock — against its size budget and routinely leaves it
 * out-of-line, even though every part is itself a tiny inlinable
 * helper. Forcing inline lets ThinLTO unfold the whole get path at
 * the call site so it can CSE the three internal `osty_gc_load_v1`
 * calls (cast inside lock, raw, unlock) and DCE the
 * single-mutator-skip branches in lock/unlock against the constant
 * `osty_concurrent_workers` flag. Mirrors the rationale for List
 * ops at the top of this file (`OSTY_HOT_INLINE` doc). */
#define OSTY_RT_DEFINE_MAP_KEY_OPS(suffix, ctype) \
OSTY_HOT_INLINE bool osty_rt_map_contains_##suffix(void *raw_map, ctype key) { \
    bool r; \
    osty_rt_map_lock(raw_map); \
    r = osty_rt_map_contains_raw(raw_map, &key); \
    osty_rt_map_unlock(raw_map); \
    return r; \
} \
OSTY_HOT_INLINE void osty_rt_map_insert_##suffix(void *raw_map, ctype key, const void *value) { \
    osty_rt_map_lock(raw_map); \
    osty_rt_map_insert_raw(raw_map, &key, value); \
    osty_rt_map_unlock(raw_map); \
} \
OSTY_HOT_INLINE bool osty_rt_map_remove_##suffix(void *raw_map, ctype key) { \
    bool r; \
    osty_rt_map_lock(raw_map); \
    r = osty_rt_map_remove_raw(raw_map, &key); \
    osty_rt_map_unlock(raw_map); \
    return r; \
} \
OSTY_HOT_INLINE void osty_rt_map_get_or_abort_##suffix(void *raw_map, ctype key, void *out_value) { \
    osty_rt_map_lock(raw_map); \
    osty_rt_map_get_or_abort_raw(raw_map, &key, out_value); \
    osty_rt_map_unlock(raw_map); \
} \
OSTY_HOT_INLINE bool osty_rt_map_get_##suffix(void *raw_map, ctype key, void *out_value) { \
    bool r; \
    osty_rt_map_lock(raw_map); \
    r = osty_rt_map_get_raw(raw_map, &key, out_value); \
    osty_rt_map_unlock(raw_map); \
    return r; \
} \
ctype osty_rt_map_key_at_##suffix(void *raw_map, int64_t index) { \
    osty_rt_map *map = osty_rt_map_cast(raw_map); \
    ctype out; \
    if (map == NULL) osty_rt_abort("map is null"); \
    osty_rt_map_lock(raw_map); \
    if (index < 0 || index >= map->len) { \
        osty_rt_map_unlock(raw_map); \
        osty_rt_abort("map key_at out of bounds"); \
    } \
    memcpy(&out, osty_rt_map_key_slot(map, index), sizeof(ctype)); \
    osty_rt_map_unlock(raw_map); \
    return out; \
} \
ctype osty_rt_map_entry_at_##suffix(void *raw_map, int64_t index, void *out_value) { \
    osty_rt_map *map = osty_rt_map_cast(raw_map); \
    ctype out; \
    if (map == NULL || out_value == NULL) osty_rt_abort("map entry_at invalid args"); \
    osty_rt_map_lock(raw_map); \
    if (index < 0 || index >= map->len) { \
        osty_rt_map_unlock(raw_map); \
        osty_rt_abort("map entry_at out of bounds"); \
    } \
    memcpy(&out, osty_rt_map_key_slot(map, index), sizeof(ctype)); \
    memcpy(out_value, osty_rt_map_value_slot(map, index), map->value_size); \
    osty_rt_map_unlock(raw_map); \
    return out; \
}

OSTY_RT_DEFINE_MAP_KEY_OPS(i64, int64_t)
OSTY_RT_DEFINE_MAP_KEY_OPS(i1, bool)
OSTY_RT_DEFINE_MAP_KEY_OPS(f64, double)
OSTY_RT_DEFINE_MAP_KEY_OPS(ptr, void *)
OSTY_RT_DEFINE_MAP_KEY_OPS(string, const char *)

// `osty_rt_map_incr_i64_<suffix>(map, key, delta)`: perform
// `map[key] = (map.get(key) ?? 0) + delta` as a single atomic
// operation under one lock acquire/release. Replaces the common
// `containsKey + getOr + insert` 3-op pattern for Map<K, Int>
// counters — one hash lookup instead of three, one lock-pair
// instead of three.
//
// Hot-path single-probe: on hit (`find_index >= 0`) the runtime
// updates the value slot in place — no second probe via
// `insert_raw`. On miss it falls through to `insert_raw` which
// handles the index resize / rebuild bookkeeping. Hits dominate
// counter workloads (lru-sim's `accessed` map sees the same key
// 70% of the time, log-aggregator's level/hour counts converge to
// ~5/24 hot keys), so the single-probe path is the steady state.
//
// Returns the new value. Used by Map<K, Int>.incrBy stdlib method,
// by `IntrinsicMapIncr` lowering (the compiler-detected
// `insert(k, getOr(k, 0) + delta)` pattern), and by the legacy
// stdlib `update` callback path.
//
// OSTY_HOT_INLINE: now a hot-path runtime call from MIR-fused
// `IntrinsicMapIncr` sites; ThinLTO inlines the body so the
// in-place update on hits collapses to a single probe + memcpy
// at the IR call site.
#define OSTY_RT_DEFINE_MAP_INCR_I64_OPS(suffix, ctype) \
OSTY_HOT_INLINE int64_t osty_rt_map_incr_i64_##suffix(void *raw_map, ctype key, int64_t delta) { \
    int64_t next; \
    osty_rt_map_lock(raw_map); \
    osty_rt_map *map = osty_rt_map_cast(raw_map); \
    int64_t index = osty_rt_map_find_index(map, &key); \
    if (index >= 0) { \
        int64_t current; \
        memcpy(&current, osty_rt_map_value_slot(map, index), sizeof(current)); \
        next = current + delta; \
        memcpy(osty_rt_map_value_slot(map, index), &next, sizeof(next)); \
    } else { \
        next = delta; \
        osty_rt_map_insert_raw(raw_map, &key, &next); \
    } \
    osty_rt_map_unlock(raw_map); \
    return next; \
}

OSTY_RT_DEFINE_MAP_INCR_I64_OPS(i64, int64_t)
OSTY_RT_DEFINE_MAP_INCR_I64_OPS(i1, bool)
OSTY_RT_DEFINE_MAP_INCR_I64_OPS(f64, double)
OSTY_RT_DEFINE_MAP_INCR_I64_OPS(ptr, void *)
OSTY_RT_DEFINE_MAP_INCR_I64_OPS(string, const char *)

static void osty_rt_set_reserve(osty_rt_set *set, int64_t min_cap) {
    int64_t next_cap = set->cap;
    size_t bytes;
    if (min_cap <= set->cap) {
        return;
    }
    if (next_cap < 4) {
        next_cap = 4;
    }
    while (next_cap < min_cap) {
        if (next_cap > INT64_MAX / 2) {
            next_cap = min_cap;
            break;
        }
        next_cap *= 2;
    }
    bytes = (size_t)next_cap * osty_rt_kind_size(set->elem_kind);
    set->items = (unsigned char *)realloc(set->items, bytes);
    if (set->items == NULL) {
        osty_rt_abort("out of memory");
    }
    set->cap = next_cap;
}

static int64_t osty_rt_set_find_index(osty_rt_set *set, const void *item) {
    int64_t i;
    size_t elem_size = osty_rt_kind_size(set->elem_kind);
    for (i = 0; i < set->len; i++) {
        if (osty_rt_value_equals(set->items + ((size_t)i * elem_size), item, elem_size, set->elem_kind)) {
            return i;
        }
    }
    return -1;
}

void *osty_rt_set_new(int64_t elem_kind) {
    osty_rt_set *set;
    if (elem_kind != OSTY_RT_ABI_I64 && elem_kind != OSTY_RT_ABI_I1 && elem_kind != OSTY_RT_ABI_F64 && elem_kind != OSTY_RT_ABI_PTR && elem_kind != OSTY_RT_ABI_STRING) {
        osty_rt_abort("unsupported set element kind");
    }
    set = (osty_rt_set *)osty_gc_allocate_managed(sizeof(osty_rt_set), OSTY_GC_KIND_SET, "runtime.set", osty_rt_set_trace, osty_rt_set_destroy);
    set->elem_kind = elem_kind;
    return set;
}

static osty_rt_set *osty_rt_set_cast(void *raw_set) {
    if (raw_set == NULL) {
        osty_rt_abort("set is null");
    }
    return (osty_rt_set *)osty_gc_load_v1(raw_set);
}

int64_t osty_rt_set_len(void *raw_set) {
    osty_rt_set *set = osty_rt_set_cast(raw_set);
    if (set == NULL) {
        osty_rt_abort("set is null");
    }
    return set->len;
}

/* Set<T>.toString — `{a, b, c}`-shaped (braces match Map's set-style
 * output; lists use `[...]`). One runtime entry; the Set struct
 * carries `elem_kind` set at `osty_rt_set_new` time, so dispatch on
 * the per-kind formatter happens internally. Composite element types
 * abort with a clear runtime message until a callback-driven entry
 * lands. Reuses the list_to_string formatter / grow / append helpers
 * so empty / numeric / quoting conventions stay byte-identical
 * across List, Map, and Set output. */
const char *osty_rt_set_to_string(void *raw_set) {
    osty_rt_set *set = osty_rt_set_cast(raw_set);
    if (set == NULL || set->len == 0) {
        return osty_rt_string_dup_site("{}", 2, "runtime.set.to_string");
    }
    osty_rt_list_format_one_fn fmt;
    switch (set->elem_kind) {
    case OSTY_RT_ABI_I64:
        fmt = osty_rt_list_format_i64;
        break;
    case OSTY_RT_ABI_F64:
        fmt = osty_rt_list_format_f64;
        break;
    case OSTY_RT_ABI_I1:
        fmt = osty_rt_list_format_i1;
        break;
    case OSTY_RT_ABI_PTR:
    case OSTY_RT_ABI_STRING:
        /* Same caveat as Map's ptr lane: the only ptr-shaped Set
         * elements that reach this entry today are Strings; composite
         * ptrs would surface garbage, so we route them through the
         * String formatter and rely on the Set's `elem_kind` having
         * been set to PTR only for hashable string-equivalent types. */
        fmt = osty_rt_list_format_string;
        break;
    default:
        osty_rt_abort("runtime.set.to_string: unsupported element kind");
    }
    size_t elem_size = osty_rt_kind_size(set->elem_kind);
    size_t cap = 64;
    size_t len = 0;
    char *buf = (char *)malloc(cap);
    if (buf == NULL) {
        osty_rt_abort("runtime.set.to_string: out of memory");
    }
    buf[len++] = '{';
    for (int64_t i = 0; i < set->len; i++) {
        if (i > 0) {
            osty_rt_list_to_string_append(&buf, &cap, &len, ", ", 2);
        }
        const unsigned char *slot = set->items + (size_t)i * elem_size;
        fmt(&buf, &cap, &len, slot);
    }
    osty_rt_list_to_string_grow(&buf, &cap, len + 2);
    buf[len++] = '}';
    const char *out = osty_rt_string_dup_site(buf, len, "runtime.set.to_string");
    free(buf);
    return out;
}

void *osty_rt_set_to_list(void *raw_set) {
    osty_rt_set *set = osty_rt_set_cast(raw_set);
    void *out = osty_rt_list_new();
    int64_t i;
    if (set == NULL) {
        osty_rt_abort("set is null");
    }
    for (i = 0; i < set->len; i++) {
        switch (set->elem_kind) {
        case OSTY_RT_ABI_I64: {
            int64_t value = 0;
            memcpy(&value, set->items + ((size_t)i * sizeof(value)), sizeof(value));
            osty_rt_list_push_i64(out, value);
            break;
        }
        case OSTY_RT_ABI_I1: {
            bool value = false;
            memcpy(&value, set->items + ((size_t)i * sizeof(value)), sizeof(value));
            osty_rt_list_push_i1(out, value);
            break;
        }
        case OSTY_RT_ABI_F64: {
            double value = 0.0;
            memcpy(&value, set->items + ((size_t)i * sizeof(value)), sizeof(value));
            osty_rt_list_push_f64(out, value);
            break;
        }
        case OSTY_RT_ABI_PTR:
        case OSTY_RT_ABI_STRING: {
            void *value = NULL;
            memcpy(&value, set->items + ((size_t)i * sizeof(value)), sizeof(value));
            osty_rt_list_push_ptr(out, value);
            break;
        }
        default:
            osty_rt_abort("unsupported set element list kind");
        }
    }
    return out;
}

void *osty_rt_list_to_set_i64(void *raw_list) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    void *set = osty_rt_set_new(OSTY_RT_ABI_I64);
    int64_t i;

    osty_rt_list_ensure_layout(list, sizeof(int64_t), NULL);
    for (i = 0; i < list->len; i++) {
        int64_t value = osty_rt_list_get_i64(raw_list, i);
        osty_rt_set_insert_i64(set, value);
    }
    return set;
}

void *osty_rt_list_to_set_i1(void *raw_list) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    void *set = osty_rt_set_new(OSTY_RT_ABI_I1);
    int64_t i;

    osty_rt_list_ensure_layout(list, sizeof(bool), NULL);
    for (i = 0; i < list->len; i++) {
        bool value = osty_rt_list_get_i1(raw_list, i);
        osty_rt_set_insert_i1(set, value);
    }
    return set;
}

void *osty_rt_list_to_set_f64(void *raw_list) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    void *set = osty_rt_set_new(OSTY_RT_ABI_F64);
    int64_t i;

    osty_rt_list_ensure_layout(list, sizeof(double), NULL);
    for (i = 0; i < list->len; i++) {
        double value = osty_rt_list_get_f64(raw_list, i);
        osty_rt_set_insert_f64(set, value);
    }
    return set;
}

void *osty_rt_list_to_set_ptr(void *raw_list) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    void *set = osty_rt_set_new(OSTY_RT_ABI_PTR);
    int64_t i;

    osty_rt_list_ensure_layout(list, sizeof(void *), osty_gc_mark_slot_v1);
    for (i = 0; i < list->len; i++) {
        void *value = osty_rt_list_get_ptr(raw_list, i);
        osty_rt_set_insert_ptr(set, value);
    }
    return set;
}

void *osty_rt_list_to_set_string(void *raw_list) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    void *set = osty_rt_set_new(OSTY_RT_ABI_STRING);
    int64_t i;

    osty_rt_list_ensure_layout(list, sizeof(void *), osty_gc_mark_slot_v1);
    for (i = 0; i < list->len; i++) {
        void *value = osty_rt_list_get_ptr(raw_list, i);
        osty_rt_set_insert_string(set, value);
    }
    return set;
}

#define OSTY_RT_DEFINE_SET_KEY_OPS(suffix, ctype) \
bool osty_rt_set_contains_##suffix(void *raw_set, ctype item) { \
    osty_rt_set *set = osty_rt_set_cast(raw_set); \
    return set != NULL && osty_rt_set_find_index(set, &item) >= 0; \
} \
bool osty_rt_set_insert_##suffix(void *raw_set, ctype item) { \
    osty_rt_set *set = osty_rt_set_cast(raw_set); \
    size_t elem_size; \
    if (set == NULL) { osty_rt_abort("set is null"); } \
    if (osty_rt_set_find_index(set, &item) >= 0) { return false; } \
    elem_size = osty_rt_kind_size(set->elem_kind); \
    osty_rt_set_reserve(set, set->len + 1); \
    memcpy(set->items + ((size_t)set->len * elem_size), &item, elem_size); \
    set->len += 1; \
    /* Phase E follow-up: post-write barrier for managed elements. \
     * Same rationale as Map.insert — without this cheney has to walk \
     * every pinned Set's full payload looking for OLD→YOUNG edges, \
     * paying O(set.len) per Set per minor cycle. Scalar elem kinds \
     * (i64/i1/f64) skip; STRING/PTR feed remembered_edges. */ \
    if (set->elem_kind == OSTY_RT_ABI_STRING || set->elem_kind == OSTY_RT_ABI_PTR) { \
        void *item_value = NULL; \
        memcpy(&item_value, &item, sizeof(item_value)); \
        if (item_value != NULL) { \
            osty_gc_post_write_v1(set, item_value, OSTY_GC_KIND_SET); \
        } \
    } \
    return true; \
} \
bool osty_rt_set_remove_##suffix(void *raw_set, ctype item) { \
    osty_rt_set *set = osty_rt_set_cast(raw_set); \
    int64_t index; \
    size_t elem_size; \
    if (set == NULL) { osty_rt_abort("set is null"); } \
    index = osty_rt_set_find_index(set, &item); \
    if (index < 0) { return false; } \
    elem_size = osty_rt_kind_size(set->elem_kind); \
    if (index + 1 < set->len) { \
        memmove(set->items + ((size_t)index * elem_size), set->items + ((size_t)(index + 1) * elem_size), (size_t)(set->len - index - 1) * elem_size); \
    } \
    set->len -= 1; \
    return true; \
}

OSTY_RT_DEFINE_SET_KEY_OPS(i64, int64_t)
OSTY_RT_DEFINE_SET_KEY_OPS(i1, bool)
OSTY_RT_DEFINE_SET_KEY_OPS(f64, double)
OSTY_RT_DEFINE_SET_KEY_OPS(ptr, void *)
OSTY_RT_DEFINE_SET_KEY_OPS(string, const char *)

/*
 * Bytes primitive ABI. Values flow as `osty_rt_bytes *` — an opaque
 * pointer to a `{unsigned char *data; int64_t len}` struct. Length /
 * emptiness queries, concatenation, indexed loads, and UTF-8
 * String-conversion helpers are wired up at the LLVM layer.
 * String.toBytes() constructs the value through the strings runtime
 * family, and Bytes.from(List<Byte>) copies the list payload into the
 * same owned layout.
 */
static bool osty_rt_bytes_validate_utf8_data(const unsigned char *data, size_t len) {
    size_t i = 0;

    while (i < len) {
        unsigned char b1 = data[i];
        int continuations = 0;
        unsigned char min2 = 0x80;
        unsigned char max2 = 0xBF;

        /* Osty Strings are NUL-terminated C strings, so embedded NUL
         * bytes would truncate the value if we accepted them here. */
        if (b1 == 0) {
            return false;
        }
        if (b1 < 0x80) {
            i++;
            continue;
        }

        if (b1 >= 0xC2 && b1 <= 0xDF) {
            continuations = 1;
        } else if (b1 == 0xE0) {
            continuations = 2;
            min2 = 0xA0;
        } else if ((b1 >= 0xE1 && b1 <= 0xEC) || b1 == 0xEE || b1 == 0xEF) {
            continuations = 2;
        } else if (b1 == 0xED) {
            continuations = 2;
            max2 = 0x9F;
        } else if (b1 == 0xF0) {
            continuations = 3;
            min2 = 0x90;
        } else if (b1 >= 0xF1 && b1 <= 0xF3) {
            continuations = 3;
        } else if (b1 == 0xF4) {
            continuations = 3;
            max2 = 0x8F;
        } else {
            return false;
        }

        if ((size_t)continuations > len - i - 1) {
            return false;
        }
        for (int j = 0; j < continuations; j++) {
            unsigned char bn = data[i + 1 + (size_t)j];
            unsigned char lo = (j == 0) ? min2 : 0x80;
            unsigned char hi = (j == 0) ? max2 : 0xBF;
            if (bn < lo || bn > hi) {
                return false;
            }
        }
        i += (size_t)(continuations + 1);
    }

    return true;
}

static bool osty_rt_bytes_contains_byte(const unsigned char *data, size_t len, unsigned char needle) {
    size_t i;

    for (i = 0; i < len; i++) {
        if (data[i] == needle) {
            return true;
        }
    }
    return false;
}

static bool osty_rt_bytes_is_space(unsigned char c) {
    return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f';
}

static int osty_rt_hex_nibble(unsigned char c) {
    if (c >= '0' && c <= '9') {
        return (int)(c - '0');
    }
    if (c >= 'a' && c <= 'f') {
        return (int)(c - 'a') + 10;
    }
    if (c >= 'A' && c <= 'F') {
        return (int)(c - 'A') + 10;
    }
    return -1;
}

static void *osty_rt_bytes_dup_site(const unsigned char *start, size_t len, const char *site) {
    size_t total;
    osty_rt_bytes *out;
    unsigned char *data;

    if (len > SIZE_MAX - sizeof(osty_rt_bytes)) {
        osty_rt_abort("runtime.bytes.dup: size overflow");
    }
    total = sizeof(osty_rt_bytes) + len;
    out = (osty_rt_bytes *)osty_gc_allocate_managed(total, OSTY_GC_KIND_BYTES, site, NULL, NULL);
    data = (unsigned char *)(out + 1);
    out->data = data;
    out->len = (int64_t)len;
    if (len != 0) {
        memcpy(data, start, len);
    }
    return out;
}

static unsigned char *osty_rt_compress_grow_buffer(unsigned char *buf, size_t *cap, size_t min_cap, const char *site) {
    unsigned char *grown;
    size_t next = (*cap == 0) ? 256 : *cap;

    if (min_cap <= *cap) {
        return buf;
    }
    while (next < min_cap) {
        if (next > SIZE_MAX / 2) {
            next = min_cap;
            break;
        }
        next *= 2;
    }
    grown = (unsigned char *)realloc(buf, next);
    if (grown == NULL) {
        free(buf);
        osty_rt_abort(site);
    }
    *cap = next;
    return grown;
}

static bool osty_rt_compress_all_zero(const unsigned char *data, size_t len) {
    size_t i;

    for (i = 0; i < len; i++) {
        if (data[i] != 0) {
            return false;
        }
    }
    return true;
}

void *osty_rt_compress_gzip_encode(void *raw_bytes) {
#if defined(_WIN32)
    (void)raw_bytes;
    osty_rt_abort("runtime.compress.gzip.encode: zlib unavailable on windows");
    return NULL;
#else
    osty_rt_bytes *input = (osty_rt_bytes *)raw_bytes;
    const unsigned char *input_data = NULL;
    size_t input_len = 0;
    size_t input_off = 0;
    unsigned char *out = NULL;
    size_t out_cap = 0;
    z_stream stream;
    int rc;

    if (input != NULL) {
        if (input->len < 0) {
            osty_rt_abort("runtime.compress.gzip.encode: negative length");
        }
        input_data = input->data;
        input_len = (size_t)input->len;
    }

    memset(&stream, 0, sizeof(stream));
    rc = deflateInit2(&stream, Z_DEFAULT_COMPRESSION, Z_DEFLATED, 15 + 16, 8, Z_DEFAULT_STRATEGY);
    if (rc != Z_OK) {
        osty_rt_abort("runtime.compress.gzip.encode: deflateInit2 failed");
    }

    out = osty_rt_compress_grow_buffer(NULL, &out_cap, 256, "runtime.compress.gzip.encode: out of memory");
    stream.next_out = out;
    stream.avail_out = (uInt)((out_cap > (size_t)UINT_MAX) ? (size_t)UINT_MAX : out_cap);

    for (;;) {
        if (stream.avail_in == 0 && input_off < input_len) {
            size_t chunk = input_len - input_off;
            if (chunk > (size_t)UINT_MAX) {
                chunk = (size_t)UINT_MAX;
            }
            stream.next_in = (Bytef *)(input_data + input_off);
            stream.avail_in = (uInt)chunk;
            input_off += chunk;
        }

        rc = deflate(&stream, (input_off == input_len && stream.avail_in == 0) ? Z_FINISH : Z_NO_FLUSH);
        if (rc == Z_STREAM_END) {
            size_t produced = (size_t)(stream.next_out - out);
            void *managed = osty_rt_bytes_dup_site(produced == 0 ? NULL : out, produced, "runtime.compress.gzip.encode");
            free(out);
            deflateEnd(&stream);
            return managed;
        }
        if (rc != Z_OK) {
            free(out);
            deflateEnd(&stream);
            osty_rt_abort("runtime.compress.gzip.encode: deflate failed");
        }
        if (stream.avail_out == 0) {
            size_t produced = (size_t)(stream.next_out - out);
            size_t min_cap = out_cap + ((out_cap < 4096) ? out_cap : 4096);
            if (min_cap <= out_cap) {
                min_cap = out_cap + 1;
            }
            out = osty_rt_compress_grow_buffer(out, &out_cap, min_cap, "runtime.compress.gzip.encode: out of memory");
            stream.next_out = out + produced;
            stream.avail_out = (uInt)(((out_cap - produced) > (size_t)UINT_MAX) ? (size_t)UINT_MAX : (out_cap - produced));
        }
    }
#endif
}

void *osty_rt_compress_gzip_decode(void *raw_bytes) {
#if defined(_WIN32)
    (void)raw_bytes;
    return NULL;
#else
    osty_rt_bytes *input = (osty_rt_bytes *)raw_bytes;
    const unsigned char *input_data = NULL;
    size_t input_len = 0;
    size_t input_off = 0;
    unsigned char *out = NULL;
    size_t out_cap = 0;
    z_stream stream;
    int rc;

    if (input != NULL) {
        if (input->len < 0) {
            osty_rt_abort("runtime.compress.gzip.decode: negative length");
        }
        input_data = input->data;
        input_len = (size_t)input->len;
    }
    if (input_len == 0) {
        return NULL;
    }

    memset(&stream, 0, sizeof(stream));
    rc = inflateInit2(&stream, 15 + 16);
    if (rc != Z_OK) {
        osty_rt_abort("runtime.compress.gzip.decode: inflateInit2 failed");
    }

    out = osty_rt_compress_grow_buffer(NULL, &out_cap, 256, "runtime.compress.gzip.decode: out of memory");
    stream.next_out = out;
    stream.avail_out = (uInt)((out_cap > (size_t)UINT_MAX) ? (size_t)UINT_MAX : out_cap);

    for (;;) {
        if (stream.avail_in == 0 && input_off < input_len) {
            size_t chunk = input_len - input_off;
            if (chunk > (size_t)UINT_MAX) {
                chunk = (size_t)UINT_MAX;
            }
            stream.next_in = (Bytef *)(input_data + input_off);
            stream.avail_in = (uInt)chunk;
            input_off += chunk;
        }

        rc = inflate(&stream, Z_NO_FLUSH);
        if (rc == Z_STREAM_END) {
            size_t consumed = input_off - (size_t)stream.avail_in;
            size_t remaining = input_len - consumed;

            if (remaining == 0 || osty_rt_compress_all_zero(input_data + consumed, remaining)) {
                size_t produced = (size_t)(stream.next_out - out);
                void *managed = osty_rt_bytes_dup_site(produced == 0 ? NULL : out, produced, "runtime.compress.gzip.decode");
                free(out);
                inflateEnd(&stream);
                return managed;
            }
            rc = inflateReset(&stream);
            if (rc != Z_OK) {
                free(out);
                inflateEnd(&stream);
                return NULL;
            }
            continue;
        }
        if (rc == Z_BUF_ERROR) {
            if (stream.avail_out == 0) {
                size_t produced = (size_t)(stream.next_out - out);
                size_t min_cap = out_cap + ((out_cap < 4096) ? out_cap : 4096);
                if (min_cap <= out_cap) {
                    min_cap = out_cap + 1;
                }
                out = osty_rt_compress_grow_buffer(out, &out_cap, min_cap, "runtime.compress.gzip.decode: out of memory");
                stream.next_out = out + produced;
                stream.avail_out = (uInt)(((out_cap - produced) > (size_t)UINT_MAX) ? (size_t)UINT_MAX : (out_cap - produced));
                continue;
            }
            if (stream.avail_in == 0 && input_off == input_len) {
                free(out);
                inflateEnd(&stream);
                return NULL;
            }
        } else if (rc != Z_OK) {
            free(out);
            inflateEnd(&stream);
            return NULL;
        }

        if (stream.avail_out == 0) {
            size_t produced = (size_t)(stream.next_out - out);
            size_t min_cap = out_cap + ((out_cap < 4096) ? out_cap : 4096);
            if (min_cap <= out_cap) {
                min_cap = out_cap + 1;
            }
            out = osty_rt_compress_grow_buffer(out, &out_cap, min_cap, "runtime.compress.gzip.decode: out of memory");
            stream.next_out = out + produced;
            stream.avail_out = (uInt)(((out_cap - produced) > (size_t)UINT_MAX) ? (size_t)UINT_MAX : (out_cap - produced));
        }
    }
#endif
}

void *osty_rt_bytes_from_list(void *raw_list) {
    osty_rt_list *list = (osty_rt_list *)raw_list;
    size_t len;
    size_t total;
    osty_rt_bytes *out;
    unsigned char *data;

    if (list == NULL || list->len <= 0) {
        len = 0;
    } else {
        if (list->len < 0) {
            osty_rt_abort("runtime.bytes.from_list: negative length");
        }
        if (list->elem_size != 0 && list->elem_size != sizeof(uint8_t)) {
            osty_rt_abort("runtime.bytes.from_list: list element size is not Byte");
        }
        len = (size_t)list->len;
    }
    if (len > SIZE_MAX - sizeof(osty_rt_bytes)) {
        osty_rt_abort("runtime.bytes.from_list: size overflow");
    }
    total = sizeof(osty_rt_bytes) + len;
    out = (osty_rt_bytes *)osty_gc_allocate_managed(total, OSTY_GC_KIND_BYTES, "runtime.bytes.from_list", NULL, NULL);
    data = (unsigned char *)(out + 1);
    out->data = data;
    out->len = (int64_t)len;
    if (len != 0) {
        if (list->data == NULL) {
            osty_rt_abort("runtime.bytes.from_list: missing list storage");
        }
        memcpy(data, list->data, len);
    }
    return out;
}

void *osty_rt_bytes_concat(void *raw_left, void *raw_right) {
    osty_rt_bytes *left = (osty_rt_bytes *)raw_left;
    osty_rt_bytes *right = (osty_rt_bytes *)raw_right;
    size_t left_len = 0;
    size_t right_len = 0;
    size_t total_len;
    size_t total;
    osty_rt_bytes *out;
    unsigned char *data;

    if (left != NULL) {
        if (left->len < 0) {
            osty_rt_abort("runtime.bytes.concat: negative left length");
        }
        left_len = (size_t)left->len;
    }
    if (right != NULL) {
        if (right->len < 0) {
            osty_rt_abort("runtime.bytes.concat: negative right length");
        }
        right_len = (size_t)right->len;
    }
    if (left_len > SIZE_MAX - right_len || left_len + right_len > SIZE_MAX - sizeof(osty_rt_bytes)) {
        osty_rt_abort("runtime.bytes.concat: size overflow");
    }
    total_len = left_len + right_len;
    total = sizeof(osty_rt_bytes) + total_len;
    out = (osty_rt_bytes *)osty_gc_allocate_managed(total, OSTY_GC_KIND_BYTES, "runtime.bytes.concat", NULL, NULL);
    data = (unsigned char *)(out + 1);
    out->data = data;
    out->len = (int64_t)total_len;
    if (left_len != 0) {
        if (left->data == NULL) {
            osty_rt_abort("runtime.bytes.concat: missing left storage");
        }
        memcpy(data, left->data, left_len);
    }
    if (right_len != 0) {
        if (right->data == NULL) {
            osty_rt_abort("runtime.bytes.concat: missing right storage");
        }
        memcpy(data + left_len, right->data, right_len);
    }
    return out;
}

void *osty_rt_bytes_repeat(void *raw_bytes, int64_t n) {
    osty_rt_bytes *b = (osty_rt_bytes *)raw_bytes;
    size_t value_len = 0;
    size_t total_len;
    size_t total;
    osty_rt_bytes *out;
    unsigned char *data;
    unsigned char *cursor;
    int64_t i;

    if (b != NULL) {
        if (b->len < 0) {
            osty_rt_abort("runtime.bytes.repeat: negative length");
        }
        value_len = (size_t)b->len;
    }
    if (n <= 0 || value_len == 0) {
        total_len = 0;
    } else {
        if (b->data == NULL) {
            osty_rt_abort("runtime.bytes.repeat: missing storage");
        }
        if ((uint64_t)n > (uint64_t)(SIZE_MAX - sizeof(osty_rt_bytes)) / (uint64_t)value_len) {
            osty_rt_abort("runtime.bytes.repeat: size overflow");
        }
        total_len = value_len * (size_t)n;
    }

    total = sizeof(osty_rt_bytes) + total_len;
    out = (osty_rt_bytes *)osty_gc_allocate_managed(total, OSTY_GC_KIND_BYTES, "runtime.bytes.repeat", NULL, NULL);
    data = (unsigned char *)(out + 1);
    out->data = data;
    out->len = (int64_t)total_len;
    if (total_len == 0) {
        return out;
    }
    cursor = data;
    for (i = 0; i < n; i++) {
        memcpy(cursor, b->data, value_len);
        cursor += value_len;
    }
    return out;
}

int64_t osty_rt_bytes_index_of(void *raw_bytes, void *raw_sub) {
    osty_rt_bytes *b = (osty_rt_bytes *)raw_bytes;
    osty_rt_bytes *sub = (osty_rt_bytes *)raw_sub;
    size_t value_len = 0;
    size_t sub_len = 0;
    size_t i;

    if (b != NULL) {
        if (b->len < 0) {
            osty_rt_abort("runtime.bytes.index_of: negative value length");
        }
        value_len = (size_t)b->len;
    }
    if (sub != NULL) {
        if (sub->len < 0) {
            osty_rt_abort("runtime.bytes.index_of: negative sub length");
        }
        sub_len = (size_t)sub->len;
    }
    if (sub_len == 0) {
        return 0;
    }
    if (sub_len > value_len) {
        return -1;
    }
    if (b->data == NULL) {
        osty_rt_abort("runtime.bytes.index_of: missing value storage");
    }
    if (sub->data == NULL) {
        osty_rt_abort("runtime.bytes.index_of: missing sub storage");
    }
    for (i = 0; i + sub_len <= value_len; i++) {
        if (memcmp(b->data + i, sub->data, sub_len) == 0) {
            return (int64_t)i;
        }
    }
    return -1;
}

int64_t osty_rt_bytes_last_index_of(void *raw_bytes, void *raw_sub) {
    osty_rt_bytes *b = (osty_rt_bytes *)raw_bytes;
    osty_rt_bytes *sub = (osty_rt_bytes *)raw_sub;
    size_t value_len = 0;
    size_t sub_len = 0;
    size_t i;

    if (b != NULL) {
        if (b->len < 0) {
            osty_rt_abort("runtime.bytes.last_index_of: negative value length");
        }
        value_len = (size_t)b->len;
    }
    if (sub != NULL) {
        if (sub->len < 0) {
            osty_rt_abort("runtime.bytes.last_index_of: negative sub length");
        }
        sub_len = (size_t)sub->len;
    }
    if (sub_len == 0) {
        return (int64_t)value_len;
    }
    if (sub_len > value_len) {
        return -1;
    }
    if (b->data == NULL) {
        osty_rt_abort("runtime.bytes.last_index_of: missing value storage");
    }
    if (sub->data == NULL) {
        osty_rt_abort("runtime.bytes.last_index_of: missing sub storage");
    }
    for (i = value_len - sub_len + 1; i > 0; i--) {
        size_t at = i - 1;
        if (memcmp(b->data + at, sub->data, sub_len) == 0) {
            return (int64_t)at;
        }
    }
    return -1;
}

void *osty_rt_bytes_split(void *raw_bytes, void *raw_sep) {
    osty_rt_bytes *b = (osty_rt_bytes *)raw_bytes;
    osty_rt_bytes *sep = (osty_rt_bytes *)raw_sep;
    size_t value_len = 0;
    size_t sep_len = 0;
    size_t start = 0;
    size_t i;
    void *out = osty_rt_list_new();

    if (b != NULL) {
        if (b->len < 0) {
            osty_rt_abort("runtime.bytes.split: negative value length");
        }
        value_len = (size_t)b->len;
    }
    if (sep != NULL) {
        if (sep->len < 0) {
            osty_rt_abort("runtime.bytes.split: negative sep length");
        }
        sep_len = (size_t)sep->len;
    }
    if (value_len != 0 && b->data == NULL) {
        osty_rt_abort("runtime.bytes.split: missing value storage");
    }
    if (sep_len != 0 && sep->data == NULL) {
        osty_rt_abort("runtime.bytes.split: missing sep storage");
    }
    if (sep_len == 0) {
        for (i = 0; i < value_len; i++) {
            osty_rt_list_push_ptr(out, osty_rt_bytes_dup_site(b->data + i, 1, "runtime.bytes.split.byte"));
        }
        return out;
    }
    for (i = 0; i + sep_len <= value_len;) {
        if (memcmp(b->data + i, sep->data, sep_len) == 0) {
            osty_rt_list_push_ptr(out, osty_rt_bytes_dup_site((start < i) ? (b->data + start) : NULL, i - start, "runtime.bytes.split.part"));
            i += sep_len;
            start = i;
            continue;
        }
        i += 1;
    }
    osty_rt_list_push_ptr(out, osty_rt_bytes_dup_site((start < value_len) ? (b->data + start) : NULL, value_len - start, "runtime.bytes.split.part"));
    return out;
}

void *osty_rt_bytes_join(void *raw_parts, void *raw_sep) {
    osty_rt_list *parts = NULL;
    osty_rt_bytes *sep = (osty_rt_bytes *)raw_sep;
    size_t sep_len = 0;
    size_t total_len = 0;
    int64_t count;
    int64_t i;
    osty_rt_bytes *out;
    unsigned char *cursor;

    if (sep != NULL) {
        if (sep->len < 0) {
            osty_rt_abort("runtime.bytes.join: negative sep length");
        }
        sep_len = (size_t)sep->len;
        if (sep_len != 0 && sep->data == NULL) {
            osty_rt_abort("runtime.bytes.join: missing sep storage");
        }
    }
    if (raw_parts == NULL) {
        return osty_rt_bytes_dup_site(NULL, 0, "runtime.bytes.join.empty");
    }
    parts = osty_rt_list_cast(raw_parts);
    if (parts->len == 0) {
        return osty_rt_bytes_dup_site(NULL, 0, "runtime.bytes.join.empty");
    }
    osty_rt_list_ensure_layout(parts, sizeof(void *), osty_gc_mark_slot_v1);
    count = parts->len;
    for (i = 0; i < count; i++) {
        osty_rt_bytes *piece = ((osty_rt_bytes **)parts->data)[i];
        size_t piece_len = 0;
        if (piece != NULL) {
            if (piece->len < 0) {
                osty_rt_abort("runtime.bytes.join: negative piece length");
            }
            piece_len = (size_t)piece->len;
            if (piece_len != 0 && piece->data == NULL) {
                osty_rt_abort("runtime.bytes.join: missing piece storage");
            }
        }
        if (piece_len > SIZE_MAX - total_len) {
            osty_rt_abort("runtime.bytes.join: size overflow");
        }
        total_len += piece_len;
        if (i + 1 < count) {
            if (sep_len > SIZE_MAX - total_len) {
                osty_rt_abort("runtime.bytes.join: size overflow");
            }
            total_len += sep_len;
        }
    }
    out = (osty_rt_bytes *)osty_rt_bytes_dup_site(NULL, total_len, total_len == 0 ? "runtime.bytes.join.empty" : "runtime.bytes.join");
    cursor = out->data;
    for (i = 0; i < count; i++) {
        osty_rt_bytes *piece = ((osty_rt_bytes **)parts->data)[i];
        size_t piece_len = 0;
        if (piece != NULL) {
            piece_len = (size_t)piece->len;
            if (piece_len != 0) {
                memcpy(cursor, piece->data, piece_len);
                cursor += piece_len;
            }
        }
        if (i + 1 < count && sep_len != 0) {
            memcpy(cursor, sep->data, sep_len);
            cursor += sep_len;
        }
    }
    return out;
}

void *osty_rt_bytes_replace(void *raw_bytes, void *raw_old, void *raw_new_value) {
    osty_rt_bytes *b = (osty_rt_bytes *)raw_bytes;
    osty_rt_bytes *old = (osty_rt_bytes *)raw_old;
    osty_rt_bytes *new_value = (osty_rt_bytes *)raw_new_value;
    size_t value_len = 0;
    size_t old_len = 0;
    size_t new_len = 0;
    size_t index;
    size_t prefix_len;
    size_t suffix_len;
    size_t total_len;
    size_t total;
    osty_rt_bytes *out;
    unsigned char *data;

    if (b != NULL) {
        if (b->len < 0) {
            osty_rt_abort("runtime.bytes.replace: negative value length");
        }
        value_len = (size_t)b->len;
    }
    if (old != NULL) {
        if (old->len < 0) {
            osty_rt_abort("runtime.bytes.replace: negative old length");
        }
        old_len = (size_t)old->len;
    }
    if (new_value != NULL) {
        if (new_value->len < 0) {
            osty_rt_abort("runtime.bytes.replace: negative new length");
        }
        new_len = (size_t)new_value->len;
    }
    if (value_len != 0 && b->data == NULL) {
        osty_rt_abort("runtime.bytes.replace: missing value storage");
    }
    if (old_len != 0 && old->data == NULL) {
        osty_rt_abort("runtime.bytes.replace: missing old storage");
    }
    if (new_len != 0 && new_value->data == NULL) {
        osty_rt_abort("runtime.bytes.replace: missing new storage");
    }
    if (old_len == 0) {
        if (new_len > SIZE_MAX - value_len) {
            osty_rt_abort("runtime.bytes.replace: size overflow");
        }
        total_len = new_len + value_len;
        total = sizeof(osty_rt_bytes) + total_len;
        out = (osty_rt_bytes *)osty_gc_allocate_managed(total, OSTY_GC_KIND_BYTES, "runtime.bytes.replace", NULL, NULL);
        data = (unsigned char *)(out + 1);
        out->data = data;
        out->len = (int64_t)total_len;
        if (new_len != 0) {
            memcpy(data, new_value->data, new_len);
        }
        if (value_len != 0) {
            memcpy(data + new_len, b->data, value_len);
        }
        return out;
    }
    if (old_len > value_len) {
        return osty_rt_bytes_dup_site(value_len == 0 ? NULL : b->data, value_len, "runtime.bytes.replace.copy");
    }
    for (index = 0; index + old_len <= value_len; index++) {
        if (memcmp(b->data + index, old->data, old_len) == 0) {
            prefix_len = index;
            suffix_len = value_len - prefix_len - old_len;
            if (prefix_len > SIZE_MAX - new_len || prefix_len + new_len > SIZE_MAX - suffix_len) {
                osty_rt_abort("runtime.bytes.replace: size overflow");
            }
            total_len = prefix_len + new_len + suffix_len;
            total = sizeof(osty_rt_bytes) + total_len;
            out = (osty_rt_bytes *)osty_gc_allocate_managed(total, OSTY_GC_KIND_BYTES, "runtime.bytes.replace", NULL, NULL);
            data = (unsigned char *)(out + 1);
            out->data = data;
            out->len = (int64_t)total_len;
            if (prefix_len != 0) {
                memcpy(data, b->data, prefix_len);
            }
            if (new_len != 0) {
                memcpy(data + prefix_len, new_value->data, new_len);
            }
            if (suffix_len != 0) {
                memcpy(data + prefix_len + new_len, b->data + index + old_len, suffix_len);
            }
            return out;
        }
    }
    return osty_rt_bytes_dup_site(value_len == 0 ? NULL : b->data, value_len, "runtime.bytes.replace.copy");
}

void *osty_rt_bytes_replace_all(void *raw_bytes, void *raw_old, void *raw_new_value) {
    osty_rt_bytes *b = (osty_rt_bytes *)raw_bytes;
    osty_rt_bytes *old = (osty_rt_bytes *)raw_old;
    osty_rt_bytes *new_value = (osty_rt_bytes *)raw_new_value;
    size_t value_len = 0;
    size_t old_len = 0;
    size_t new_len = 0;
    size_t total_len;
    size_t total;
    int64_t count = 0;
    size_t cursor = 0;
    osty_rt_bytes *out;
    unsigned char *data;
    unsigned char *dst;
    size_t i;

    if (b != NULL) {
        if (b->len < 0) {
            osty_rt_abort("runtime.bytes.replace_all: negative value length");
        }
        value_len = (size_t)b->len;
    }
    if (old != NULL) {
        if (old->len < 0) {
            osty_rt_abort("runtime.bytes.replace_all: negative old length");
        }
        old_len = (size_t)old->len;
    }
    if (new_value != NULL) {
        if (new_value->len < 0) {
            osty_rt_abort("runtime.bytes.replace_all: negative new length");
        }
        new_len = (size_t)new_value->len;
    }
    if (value_len != 0 && b->data == NULL) {
        osty_rt_abort("runtime.bytes.replace_all: missing value storage");
    }
    if (old_len != 0 && old->data == NULL) {
        osty_rt_abort("runtime.bytes.replace_all: missing old storage");
    }
    if (new_len != 0 && new_value->data == NULL) {
        osty_rt_abort("runtime.bytes.replace_all: missing new storage");
    }
    if (old_len == 0) {
        if (new_len != 0 && value_len + 1 > SIZE_MAX / new_len) {
            osty_rt_abort("runtime.bytes.replace_all: size overflow");
        }
        total_len = value_len + (value_len + 1) * new_len;
        total = sizeof(osty_rt_bytes) + total_len;
        out = (osty_rt_bytes *)osty_gc_allocate_managed(total, OSTY_GC_KIND_BYTES, "runtime.bytes.replace_all", NULL, NULL);
        data = (unsigned char *)(out + 1);
        out->data = data;
        out->len = (int64_t)total_len;
        dst = data;
        if (new_len != 0) {
            memcpy(dst, new_value->data, new_len);
            dst += new_len;
        }
        for (i = 0; i < value_len; i++) {
            *dst++ = b->data[i];
            if (new_len != 0) {
                memcpy(dst, new_value->data, new_len);
                dst += new_len;
            }
        }
        return out;
    }
    if (old_len > value_len) {
        return osty_rt_bytes_dup_site(value_len == 0 ? NULL : b->data, value_len, "runtime.bytes.replace_all.copy");
    }
    for (i = 0; i + old_len <= value_len;) {
        if (memcmp(b->data + i, old->data, old_len) == 0) {
            count += 1;
            i += old_len;
            continue;
        }
        i += 1;
    }
    if (count == 0) {
        return osty_rt_bytes_dup_site(value_len == 0 ? NULL : b->data, value_len, "runtime.bytes.replace_all.copy");
    }
    if (new_len >= old_len) {
        size_t delta = new_len - old_len;
        if (delta != 0 && (size_t)count > (SIZE_MAX - value_len) / delta) {
            osty_rt_abort("runtime.bytes.replace_all: size overflow");
        }
        total_len = value_len + (size_t)count * delta;
    } else {
        total_len = value_len - (size_t)count * (old_len - new_len);
    }
    if (total_len > SIZE_MAX - sizeof(osty_rt_bytes)) {
        osty_rt_abort("runtime.bytes.replace_all: size overflow");
    }
    total = sizeof(osty_rt_bytes) + total_len;
    out = (osty_rt_bytes *)osty_gc_allocate_managed(total, OSTY_GC_KIND_BYTES, "runtime.bytes.replace_all", NULL, NULL);
    data = (unsigned char *)(out + 1);
    out->data = data;
    out->len = (int64_t)total_len;
    dst = data;
    while (cursor < value_len) {
        if (cursor + old_len <= value_len && memcmp(b->data + cursor, old->data, old_len) == 0) {
            if (new_len != 0) {
                memcpy(dst, new_value->data, new_len);
                dst += new_len;
            }
            cursor += old_len;
            continue;
        }
        *dst++ = b->data[cursor++];
    }
    return out;
}

void *osty_rt_bytes_trim_left(void *raw_bytes, void *raw_strip) {
    osty_rt_bytes *b = (osty_rt_bytes *)raw_bytes;
    osty_rt_bytes *strip = (osty_rt_bytes *)raw_strip;
    size_t value_len = 0;
    size_t strip_len = 0;
    size_t start = 0;

    if (b != NULL) {
        if (b->len < 0) {
            osty_rt_abort("runtime.bytes.trim_left: negative value length");
        }
        value_len = (size_t)b->len;
    }
    if (strip != NULL) {
        if (strip->len < 0) {
            osty_rt_abort("runtime.bytes.trim_left: negative strip length");
        }
        strip_len = (size_t)strip->len;
    }
    if (value_len != 0 && b->data == NULL) {
        osty_rt_abort("runtime.bytes.trim_left: missing value storage");
    }
    if (strip_len != 0 && strip->data == NULL) {
        osty_rt_abort("runtime.bytes.trim_left: missing strip storage");
    }
    while (start < value_len && strip_len != 0 && osty_rt_bytes_contains_byte(strip->data, strip_len, b->data[start])) {
        start++;
    }
    return osty_rt_bytes_dup_site((start < value_len) ? (b->data + start) : NULL, value_len - start, "runtime.bytes.trim_left");
}

void *osty_rt_bytes_trim_right(void *raw_bytes, void *raw_strip) {
    osty_rt_bytes *b = (osty_rt_bytes *)raw_bytes;
    osty_rt_bytes *strip = (osty_rt_bytes *)raw_strip;
    size_t value_len = 0;
    size_t strip_len = 0;
    size_t end;

    if (b != NULL) {
        if (b->len < 0) {
            osty_rt_abort("runtime.bytes.trim_right: negative value length");
        }
        value_len = (size_t)b->len;
    }
    if (strip != NULL) {
        if (strip->len < 0) {
            osty_rt_abort("runtime.bytes.trim_right: negative strip length");
        }
        strip_len = (size_t)strip->len;
    }
    if (value_len != 0 && b->data == NULL) {
        osty_rt_abort("runtime.bytes.trim_right: missing value storage");
    }
    if (strip_len != 0 && strip->data == NULL) {
        osty_rt_abort("runtime.bytes.trim_right: missing strip storage");
    }
    end = value_len;
    while (end > 0 && strip_len != 0 && osty_rt_bytes_contains_byte(strip->data, strip_len, b->data[end - 1])) {
        end--;
    }
    return osty_rt_bytes_dup_site((end == 0) ? NULL : b->data, end, "runtime.bytes.trim_right");
}

void *osty_rt_bytes_trim(void *raw_bytes, void *raw_strip) {
    osty_rt_bytes *b = (osty_rt_bytes *)raw_bytes;
    osty_rt_bytes *strip = (osty_rt_bytes *)raw_strip;
    size_t value_len = 0;
    size_t strip_len = 0;
    size_t start = 0;
    size_t end;

    if (b != NULL) {
        if (b->len < 0) {
            osty_rt_abort("runtime.bytes.trim: negative value length");
        }
        value_len = (size_t)b->len;
    }
    if (strip != NULL) {
        if (strip->len < 0) {
            osty_rt_abort("runtime.bytes.trim: negative strip length");
        }
        strip_len = (size_t)strip->len;
    }
    if (value_len != 0 && b->data == NULL) {
        osty_rt_abort("runtime.bytes.trim: missing value storage");
    }
    if (strip_len != 0 && strip->data == NULL) {
        osty_rt_abort("runtime.bytes.trim: missing strip storage");
    }
    end = value_len;
    while (start < value_len && strip_len != 0 && osty_rt_bytes_contains_byte(strip->data, strip_len, b->data[start])) {
        start++;
    }
    while (end > start && strip_len != 0 && osty_rt_bytes_contains_byte(strip->data, strip_len, b->data[end - 1])) {
        end--;
    }
    return osty_rt_bytes_dup_site((start < end) ? (b->data + start) : NULL, end - start, "runtime.bytes.trim");
}

void *osty_rt_bytes_trim_space(void *raw_bytes) {
    osty_rt_bytes *b = (osty_rt_bytes *)raw_bytes;
    size_t value_len = 0;
    size_t start = 0;
    size_t end;

    if (b != NULL) {
        if (b->len < 0) {
            osty_rt_abort("runtime.bytes.trim_space: negative value length");
        }
        value_len = (size_t)b->len;
    }
    if (value_len != 0 && b->data == NULL) {
        osty_rt_abort("runtime.bytes.trim_space: missing value storage");
    }
    end = value_len;
    while (start < value_len && osty_rt_bytes_is_space(b->data[start])) {
        start++;
    }
    while (end > start && osty_rt_bytes_is_space(b->data[end - 1])) {
        end--;
    }
    return osty_rt_bytes_dup_site((start < end) ? (b->data + start) : NULL, end - start, "runtime.bytes.trim_space");
}

void *osty_rt_bytes_to_upper(void *raw_bytes) {
    osty_rt_bytes *b = (osty_rt_bytes *)raw_bytes;
    size_t value_len = 0;
    osty_rt_bytes *out;
    unsigned char *data;
    size_t i;

    if (b != NULL) {
        if (b->len < 0) {
            osty_rt_abort("runtime.bytes.to_upper: negative value length");
        }
        value_len = (size_t)b->len;
    }
    if (value_len != 0 && b->data == NULL) {
        osty_rt_abort("runtime.bytes.to_upper: missing value storage");
    }
    out = (osty_rt_bytes *)osty_rt_bytes_dup_site((value_len == 0) ? NULL : b->data, value_len, "runtime.bytes.to_upper");
    data = out->data;
    for (i = 0; i < value_len; i++) {
        unsigned char c = data[i];
        if (c >= 'a' && c <= 'z') {
            data[i] = (unsigned char)(c - ('a' - 'A'));
        }
    }
    return out;
}

void *osty_rt_bytes_to_lower(void *raw_bytes) {
    osty_rt_bytes *b = (osty_rt_bytes *)raw_bytes;
    size_t value_len = 0;
    osty_rt_bytes *out;
    unsigned char *data;
    size_t i;

    if (b != NULL) {
        if (b->len < 0) {
            osty_rt_abort("runtime.bytes.to_lower: negative value length");
        }
        value_len = (size_t)b->len;
    }
    if (value_len != 0 && b->data == NULL) {
        osty_rt_abort("runtime.bytes.to_lower: missing value storage");
    }
    out = (osty_rt_bytes *)osty_rt_bytes_dup_site((value_len == 0) ? NULL : b->data, value_len, "runtime.bytes.to_lower");
    data = out->data;
    for (i = 0; i < value_len; i++) {
        unsigned char c = data[i];
        if (c >= 'A' && c <= 'Z') {
            data[i] = (unsigned char)(c + ('a' - 'A'));
        }
    }
    return out;
}

const char *osty_rt_bytes_to_hex(void *raw_bytes) {
    osty_rt_bytes *b = (osty_rt_bytes *)raw_bytes;
    size_t value_len = 0;
    size_t total_len;
    char *out;
    size_t i;
    static const char hex[] = "0123456789abcdef";

    if (b != NULL) {
        if (b->len < 0) {
            osty_rt_abort("runtime.bytes.to_hex: negative value length");
        }
        value_len = (size_t)b->len;
    }
    if (value_len != 0 && b->data == NULL) {
        osty_rt_abort("runtime.bytes.to_hex: missing value storage");
    }
    if (value_len == 0) {
        return osty_rt_string_dup_site("", 0, "runtime.bytes.to_hex.empty");
    }
    if (value_len > (SIZE_MAX - 1) / 2) {
        osty_rt_abort("runtime.bytes.to_hex: size overflow");
    }
    total_len = value_len * 2;
    out = (char *)osty_gc_allocate_managed(total_len + 1, OSTY_GC_KIND_STRING, "runtime.bytes.to_hex", NULL, NULL);
    for (i = 0; i < value_len; i++) {
        unsigned char c = b->data[i];
        out[i * 2] = hex[c >> 4];
        out[i * 2 + 1] = hex[c & 0x0F];
    }
    out[total_len] = '\0';
    return out;
}

bool osty_rt_bytes_is_valid_hex(const char *value) {
    size_t len;
    size_t i;

    value = (value == NULL) ? "" : value;
    len = strlen(value);
    if ((len & 1U) != 0) {
        return false;
    }
    for (i = 0; i < len; i++) {
        if (osty_rt_hex_nibble((unsigned char)value[i]) < 0) {
            return false;
        }
    }
    return true;
}

void *osty_rt_bytes_from_hex(const char *value) {
    size_t len;
    size_t byte_len;
    size_t i;
    osty_rt_bytes *out;
    unsigned char *data;

    value = (value == NULL) ? "" : value;
    len = strlen(value);
    if ((len & 1U) != 0) {
        osty_rt_abort("runtime.bytes.from_hex: odd hex length");
    }
    byte_len = len / 2;
    out = (osty_rt_bytes *)osty_rt_bytes_dup_site(NULL, byte_len, byte_len == 0 ? "runtime.bytes.from_hex.empty" : "runtime.bytes.from_hex");
    data = out->data;
    for (i = 0; i < byte_len; i++) {
        int hi = osty_rt_hex_nibble((unsigned char)value[i * 2]);
        int lo = osty_rt_hex_nibble((unsigned char)value[i * 2 + 1]);
        if (hi < 0 || lo < 0) {
            osty_rt_abort("runtime.bytes.from_hex: invalid hex digit");
        }
        data[i] = (unsigned char)((hi << 4) | lo);
    }
    return out;
}

int64_t osty_rt_bytes_len(void *raw_bytes) {
    osty_rt_bytes *b = (osty_rt_bytes *)raw_bytes;
    if (b == NULL) {
        return 0;
    }
    return b->len;
}

bool osty_rt_bytes_is_empty(void *raw_bytes) {
    osty_rt_bytes *b = (osty_rt_bytes *)raw_bytes;
    if (b == NULL) {
        return true;
    }
    return b->len == 0;
}

bool osty_rt_bytes_is_valid_utf8(void *raw_bytes) {
    osty_rt_bytes *b = (osty_rt_bytes *)raw_bytes;
    size_t len = 0;

    if (b != NULL) {
        if (b->len < 0) {
            osty_rt_abort("runtime.bytes.is_valid_utf8: negative length");
        }
        len = (size_t)b->len;
    }
    if (len == 0) {
        return true;
    }
    if (b->data == NULL) {
        osty_rt_abort("runtime.bytes.is_valid_utf8: missing storage");
    }
    return osty_rt_bytes_validate_utf8_data(b->data, len);
}

const char *osty_rt_bytes_to_string(void *raw_bytes) {
    osty_rt_bytes *b = (osty_rt_bytes *)raw_bytes;
    size_t len = 0;
    char *out;

    if (b != NULL) {
        if (b->len < 0) {
            osty_rt_abort("runtime.bytes.to_string: negative length");
        }
        len = (size_t)b->len;
    }
    if (len == 0) {
        out = (char *)osty_gc_allocate_managed(1, OSTY_GC_KIND_STRING, "runtime.bytes.to_string.empty", NULL, NULL);
        out[0] = '\0';
        return out;
    }
    if (b->data == NULL) {
        osty_rt_abort("runtime.bytes.to_string: missing storage");
    }
    if (!osty_rt_bytes_validate_utf8_data(b->data, len)) {
        osty_rt_abort("runtime.bytes.to_string: invalid UTF-8");
    }
    if (len == SIZE_MAX) {
        osty_rt_abort("runtime.bytes.to_string: size overflow");
    }
    out = (char *)osty_gc_allocate_managed(len + 1, OSTY_GC_KIND_STRING, "runtime.bytes.to_string", NULL, NULL);
    memcpy(out, b->data, len);
    out[len] = '\0';
    return out;
}

uint8_t osty_rt_bytes_get(void *raw_bytes, int64_t index) {
    osty_rt_bytes *b = (osty_rt_bytes *)raw_bytes;
    if (b == NULL) {
        osty_rt_abort("runtime.bytes.get: nil bytes");
    }
    if (index < 0 || index >= b->len) {
        osty_rt_abort("runtime.bytes.get: index out of range");
    }
    return b->data[index];
}

void *osty_rt_bytes_slice(void *raw_bytes, int64_t start, int64_t end) {
    osty_rt_bytes *b = (osty_rt_bytes *)raw_bytes;
    size_t value_len = 0;
    size_t slice_len;
    size_t total;
    osty_rt_bytes *out;
    unsigned char *data;

    if (b != NULL) {
        if (b->len < 0) {
            osty_rt_abort("runtime.bytes.slice: negative length");
        }
        value_len = (size_t)b->len;
    }
    if (start < 0 || end < start) {
        osty_rt_abort("runtime.bytes.slice: invalid bounds");
    }
    if ((uint64_t)end > (uint64_t)value_len) {
        osty_rt_abort("runtime.bytes.slice: end out of range");
    }
    slice_len = (size_t)(end - start);
    if (slice_len > SIZE_MAX - sizeof(osty_rt_bytes)) {
        osty_rt_abort("runtime.bytes.slice: size overflow");
    }
    total = sizeof(osty_rt_bytes) + slice_len;
    out = (osty_rt_bytes *)osty_gc_allocate_managed(total, OSTY_GC_KIND_BYTES, "runtime.bytes.slice", NULL, NULL);
    data = (unsigned char *)(out + 1);
    out->data = data;
    out->len = (int64_t)slice_len;
    if (slice_len != 0) {
        if (b == NULL || b->data == NULL) {
            osty_rt_abort("runtime.bytes.slice: missing storage");
        }
        memcpy(data, b->data + (size_t)start, slice_len);
    }
    return out;
}

static inline uint32_t osty_rt_crypto_rotr32(uint32_t x, unsigned n) {
    return (x >> n) | (x << (32U - n));
}

static inline uint64_t osty_rt_crypto_rotr64(uint64_t x, unsigned n) {
    return (x >> n) | (x << (64U - n));
}

static inline uint32_t osty_rt_crypto_rotl32(uint32_t x, unsigned n) {
    return (x << n) | (x >> (32U - n));
}

static inline uint32_t osty_rt_crypto_load_be32(const unsigned char *p) {
    return ((uint32_t)p[0] << 24) |
           ((uint32_t)p[1] << 16) |
           ((uint32_t)p[2] << 8) |
           (uint32_t)p[3];
}

static inline uint64_t osty_rt_crypto_load_be64(const unsigned char *p) {
    return ((uint64_t)p[0] << 56) |
           ((uint64_t)p[1] << 48) |
           ((uint64_t)p[2] << 40) |
           ((uint64_t)p[3] << 32) |
           ((uint64_t)p[4] << 24) |
           ((uint64_t)p[5] << 16) |
           ((uint64_t)p[6] << 8) |
           (uint64_t)p[7];
}

static inline uint32_t osty_rt_crypto_load_le32(const unsigned char *p) {
    return (uint32_t)p[0] |
           ((uint32_t)p[1] << 8) |
           ((uint32_t)p[2] << 16) |
           ((uint32_t)p[3] << 24);
}

static inline void osty_rt_crypto_store_be32(unsigned char *p, uint32_t x) {
    p[0] = (unsigned char)(x >> 24);
    p[1] = (unsigned char)(x >> 16);
    p[2] = (unsigned char)(x >> 8);
    p[3] = (unsigned char)x;
}

static inline void osty_rt_crypto_store_be64(unsigned char *p, uint64_t x) {
    p[0] = (unsigned char)(x >> 56);
    p[1] = (unsigned char)(x >> 48);
    p[2] = (unsigned char)(x >> 40);
    p[3] = (unsigned char)(x >> 32);
    p[4] = (unsigned char)(x >> 24);
    p[5] = (unsigned char)(x >> 16);
    p[6] = (unsigned char)(x >> 8);
    p[7] = (unsigned char)x;
}

static inline void osty_rt_crypto_store_le32(unsigned char *p, uint32_t x) {
    p[0] = (unsigned char)x;
    p[1] = (unsigned char)(x >> 8);
    p[2] = (unsigned char)(x >> 16);
    p[3] = (unsigned char)(x >> 24);
}

static void osty_rt_crypto_bytes_view(void *raw_bytes,
                                      const unsigned char **data,
                                      size_t *len,
                                      const char *op) {
    osty_rt_bytes *b = (osty_rt_bytes *)raw_bytes;
    *data = NULL;
    *len = 0;
    if (b == NULL) {
        return;
    }
    if (b->len < 0) {
        osty_rt_abort(op);
    }
    *len = (size_t)b->len;
    if (*len != 0 && b->data == NULL) {
        osty_rt_abort(op);
    }
    *data = b->data;
}

static osty_rt_bytes *osty_rt_crypto_alloc_bytes(size_t len, const char *site) {
    size_t total;
    osty_rt_bytes *out;
    unsigned char *data;

    if (len > SIZE_MAX - sizeof(osty_rt_bytes)) {
        osty_rt_abort("runtime.crypto.alloc_bytes: size overflow");
    }
    total = sizeof(osty_rt_bytes) + len;
    out = (osty_rt_bytes *)osty_gc_allocate_managed(total, OSTY_GC_KIND_BYTES, site, NULL, NULL);
    data = (unsigned char *)(out + 1);
    out->data = data;
    out->len = (int64_t)len;
    return out;
}

static void osty_rt_crypto_secure_zero(void *ptr, size_t len) {
    volatile unsigned char *p = (volatile unsigned char *)ptr;
    while (len-- != 0) {
        *p++ = 0U;
    }
}

static bool osty_rt_crypto_digest_matches_hex(const unsigned char *digest,
                                              size_t digest_len,
                                              const char *hex) {
    size_t i;
    if (hex == NULL) {
        return false;
    }
    if (strlen(hex) != digest_len * 2U) {
        return false;
    }
    for (i = 0; i < digest_len; i++) {
        int hi = osty_rt_hex_nibble((unsigned char)hex[i * 2U]);
        int lo = osty_rt_hex_nibble((unsigned char)hex[i * 2U + 1U]);
        if (hi < 0 || lo < 0) {
            return false;
        }
        if (digest[i] != (unsigned char)((hi << 4) | lo)) {
            return false;
        }
    }
    return true;
}

static const uint32_t osty_rt_crypto_sha256_k[64] = {
    0x428a2f98U, 0x71374491U, 0xb5c0fbcfU, 0xe9b5dba5U,
    0x3956c25bU, 0x59f111f1U, 0x923f82a4U, 0xab1c5ed5U,
    0xd807aa98U, 0x12835b01U, 0x243185beU, 0x550c7dc3U,
    0x72be5d74U, 0x80deb1feU, 0x9bdc06a7U, 0xc19bf174U,
    0xe49b69c1U, 0xefbe4786U, 0x0fc19dc6U, 0x240ca1ccU,
    0x2de92c6fU, 0x4a7484aaU, 0x5cb0a9dcU, 0x76f988daU,
    0x983e5152U, 0xa831c66dU, 0xb00327c8U, 0xbf597fc7U,
    0xc6e00bf3U, 0xd5a79147U, 0x06ca6351U, 0x14292967U,
    0x27b70a85U, 0x2e1b2138U, 0x4d2c6dfcU, 0x53380d13U,
    0x650a7354U, 0x766a0abbU, 0x81c2c92eU, 0x92722c85U,
    0xa2bfe8a1U, 0xa81a664bU, 0xc24b8b70U, 0xc76c51a3U,
    0xd192e819U, 0xd6990624U, 0xf40e3585U, 0x106aa070U,
    0x19a4c116U, 0x1e376c08U, 0x2748774cU, 0x34b0bcb5U,
    0x391c0cb3U, 0x4ed8aa4aU, 0x5b9cca4fU, 0x682e6ff3U,
    0x748f82eeU, 0x78a5636fU, 0x84c87814U, 0x8cc70208U,
    0x90befffaU, 0xa4506cebU, 0xbef9a3f7U, 0xc67178f2U,
};

static const uint64_t osty_rt_crypto_sha512_k[80] = {
    0x428a2f98d728ae22ULL, 0x7137449123ef65cdULL,
    0xb5c0fbcfec4d3b2fULL, 0xe9b5dba58189dbbcULL,
    0x3956c25bf348b538ULL, 0x59f111f1b605d019ULL,
    0x923f82a4af194f9bULL, 0xab1c5ed5da6d8118ULL,
    0xd807aa98a3030242ULL, 0x12835b0145706fbeULL,
    0x243185be4ee4b28cULL, 0x550c7dc3d5ffb4e2ULL,
    0x72be5d74f27b896fULL, 0x80deb1fe3b1696b1ULL,
    0x9bdc06a725c71235ULL, 0xc19bf174cf692694ULL,
    0xe49b69c19ef14ad2ULL, 0xefbe4786384f25e3ULL,
    0x0fc19dc68b8cd5b5ULL, 0x240ca1cc77ac9c65ULL,
    0x2de92c6f592b0275ULL, 0x4a7484aa6ea6e483ULL,
    0x5cb0a9dcbd41fbd4ULL, 0x76f988da831153b5ULL,
    0x983e5152ee66dfabULL, 0xa831c66d2db43210ULL,
    0xb00327c898fb213fULL, 0xbf597fc7beef0ee4ULL,
    0xc6e00bf33da88fc2ULL, 0xd5a79147930aa725ULL,
    0x06ca6351e003826fULL, 0x142929670a0e6e70ULL,
    0x27b70a8546d22ffcULL, 0x2e1b21385c26c926ULL,
    0x4d2c6dfc5ac42aedULL, 0x53380d139d95b3dfULL,
    0x650a73548baf63deULL, 0x766a0abb3c77b2a8ULL,
    0x81c2c92e47edaee6ULL, 0x92722c851482353bULL,
    0xa2bfe8a14cf10364ULL, 0xa81a664bbc423001ULL,
    0xc24b8b70d0f89791ULL, 0xc76c51a30654be30ULL,
    0xd192e819d6ef5218ULL, 0xd69906245565a910ULL,
    0xf40e35855771202aULL, 0x106aa07032bbd1b8ULL,
    0x19a4c116b8d2d0c8ULL, 0x1e376c085141ab53ULL,
    0x2748774cdf8eeb99ULL, 0x34b0bcb5e19b48a8ULL,
    0x391c0cb3c5c95a63ULL, 0x4ed8aa4ae3418acbULL,
    0x5b9cca4f7763e373ULL, 0x682e6ff3d6b2b8a3ULL,
    0x748f82ee5defb2fcULL, 0x78a5636f43172f60ULL,
    0x84c87814a1f0ab72ULL, 0x8cc702081a6439ecULL,
    0x90befffa23631e28ULL, 0xa4506cebde82bde9ULL,
    0xbef9a3f7b2c67915ULL, 0xc67178f2e372532bULL,
    0xca273eceea26619cULL, 0xd186b8c721c0c207ULL,
    0xeada7dd6cde0eb1eULL, 0xf57d4f7fee6ed178ULL,
    0x06f067aa72176fbaULL, 0x0a637dc5a2c898a6ULL,
    0x113f9804bef90daeULL, 0x1b710b35131c471bULL,
    0x28db77f523047d84ULL, 0x32caab7b40c72493ULL,
    0x3c9ebe0a15c9bebcULL, 0x431d67c49c100d4cULL,
    0x4cc5d4becb3e42b6ULL, 0x597f299cfc657e2aULL,
    0x5fcb6fab3ad6faecULL, 0x6c44198c4a475817ULL,
};

static const uint32_t osty_rt_crypto_md5_k[64] = {
    0xd76aa478U, 0xe8c7b756U, 0x242070dbU, 0xc1bdceeeU,
    0xf57c0fafU, 0x4787c62aU, 0xa8304613U, 0xfd469501U,
    0x698098d8U, 0x8b44f7afU, 0xffff5bb1U, 0x895cd7beU,
    0x6b901122U, 0xfd987193U, 0xa679438eU, 0x49b40821U,
    0xf61e2562U, 0xc040b340U, 0x265e5a51U, 0xe9b6c7aaU,
    0xd62f105dU, 0x02441453U, 0xd8a1e681U, 0xe7d3fbc8U,
    0x21e1cde6U, 0xc33707d6U, 0xf4d50d87U, 0x455a14edU,
    0xa9e3e905U, 0xfcefa3f8U, 0x676f02d9U, 0x8d2a4c8aU,
    0xfffa3942U, 0x8771f681U, 0x6d9d6122U, 0xfde5380cU,
    0xa4beea44U, 0x4bdecfa9U, 0xf6bb4b60U, 0xbebfbc70U,
    0x289b7ec6U, 0xeaa127faU, 0xd4ef3085U, 0x04881d05U,
    0xd9d4d039U, 0xe6db99e5U, 0x1fa27cf8U, 0xc4ac5665U,
    0xf4292244U, 0x432aff97U, 0xab9423a7U, 0xfc93a039U,
    0x655b59c3U, 0x8f0ccc92U, 0xffeff47dU, 0x85845dd1U,
    0x6fa87e4fU, 0xfe2ce6e0U, 0xa3014314U, 0x4e0811a1U,
    0xf7537e82U, 0xbd3af235U, 0x2ad7d2bbU, 0xeb86d391U,
};

static const uint32_t osty_rt_crypto_md5_s[64] = {
    7U, 12U, 17U, 22U, 7U, 12U, 17U, 22U,
    7U, 12U, 17U, 22U, 7U, 12U, 17U, 22U,
    5U, 9U, 14U, 20U, 5U, 9U, 14U, 20U,
    5U, 9U, 14U, 20U, 5U, 9U, 14U, 20U,
    4U, 11U, 16U, 23U, 4U, 11U, 16U, 23U,
    4U, 11U, 16U, 23U, 4U, 11U, 16U, 23U,
    6U, 10U, 15U, 21U, 6U, 10U, 15U, 21U,
    6U, 10U, 15U, 21U, 6U, 10U, 15U, 21U,
};

static void osty_rt_crypto_sha256_compress(uint32_t state[8], const unsigned char block[64]) {
    uint32_t w[64];
    uint32_t a, b, c, d, e, f, g, h;
    int i;

    for (i = 0; i < 16; i++) {
        w[i] = osty_rt_crypto_load_be32(block + ((size_t)i * 4U));
    }
    for (i = 16; i < 64; i++) {
        uint32_t s0 = osty_rt_crypto_rotr32(w[i - 15], 7) ^
                      osty_rt_crypto_rotr32(w[i - 15], 18) ^
                      (w[i - 15] >> 3);
        uint32_t s1 = osty_rt_crypto_rotr32(w[i - 2], 17) ^
                      osty_rt_crypto_rotr32(w[i - 2], 19) ^
                      (w[i - 2] >> 10);
        w[i] = w[i - 16] + s0 + w[i - 7] + s1;
    }

    a = state[0];
    b = state[1];
    c = state[2];
    d = state[3];
    e = state[4];
    f = state[5];
    g = state[6];
    h = state[7];

    for (i = 0; i < 64; i++) {
        uint32_t s1 = osty_rt_crypto_rotr32(e, 6) ^
                      osty_rt_crypto_rotr32(e, 11) ^
                      osty_rt_crypto_rotr32(e, 25);
        uint32_t ch = (e & f) ^ ((~e) & g);
        uint32_t temp1 = h + s1 + ch + osty_rt_crypto_sha256_k[i] + w[i];
        uint32_t s0 = osty_rt_crypto_rotr32(a, 2) ^
                      osty_rt_crypto_rotr32(a, 13) ^
                      osty_rt_crypto_rotr32(a, 22);
        uint32_t maj = (a & b) ^ (a & c) ^ (b & c);
        uint32_t temp2 = s0 + maj;

        h = g;
        g = f;
        f = e;
        e = d + temp1;
        d = c;
        c = b;
        b = a;
        a = temp1 + temp2;
    }

    state[0] += a;
    state[1] += b;
    state[2] += c;
    state[3] += d;
    state[4] += e;
    state[5] += f;
    state[6] += g;
    state[7] += h;
}

typedef struct osty_rt_crypto_sha256_ctx {
    uint32_t state[8];
    uint64_t total_len;
    size_t block_len;
    unsigned char block[64];
} osty_rt_crypto_sha256_ctx;

static void osty_rt_crypto_sha256_init(osty_rt_crypto_sha256_ctx *ctx) {
    ctx->state[0] = 0x6a09e667U;
    ctx->state[1] = 0xbb67ae85U;
    ctx->state[2] = 0x3c6ef372U;
    ctx->state[3] = 0xa54ff53aU;
    ctx->state[4] = 0x510e527fU;
    ctx->state[5] = 0x9b05688cU;
    ctx->state[6] = 0x1f83d9abU;
    ctx->state[7] = 0x5be0cd19U;
    ctx->total_len = 0;
    ctx->block_len = 0;
}

static void osty_rt_crypto_sha256_update(osty_rt_crypto_sha256_ctx *ctx,
                                         const unsigned char *data,
                                         size_t len) {
    if (len == 0) {
        return;
    }
    if (ctx->total_len > UINT64_MAX - (uint64_t)len) {
        osty_rt_abort("runtime.crypto.sha256: input too large");
    }
    ctx->total_len += (uint64_t)len;
    if (ctx->block_len != 0) {
        size_t want = 64U - ctx->block_len;
        if (want > len) {
            want = len;
        }
        memcpy(ctx->block + ctx->block_len, data, want);
        ctx->block_len += want;
        data += want;
        len -= want;
        if (ctx->block_len == 64U) {
            osty_rt_crypto_sha256_compress(ctx->state, ctx->block);
            ctx->block_len = 0;
        }
    }
    while (len >= 64U) {
        osty_rt_crypto_sha256_compress(ctx->state, data);
        data += 64U;
        len -= 64U;
    }
    if (len != 0) {
        memcpy(ctx->block, data, len);
        ctx->block_len = len;
    }
}

static void osty_rt_crypto_sha256_final(osty_rt_crypto_sha256_ctx *ctx,
                                        unsigned char out[32]) {
    unsigned char final[128];
    uint64_t bit_len;
    size_t padded;
    int i;

    if (ctx->total_len > (UINT64_MAX >> 3)) {
        osty_rt_abort("runtime.crypto.sha256: input too large");
    }
    bit_len = ctx->total_len * 8ULL;
    memset(final, 0, sizeof(final));
    if (ctx->block_len != 0) {
        memcpy(final, ctx->block, ctx->block_len);
    }
    final[ctx->block_len] = 0x80U;
    padded = (ctx->block_len + 1U + 8U <= 64U) ? 64U : 128U;
    osty_rt_crypto_store_be64(final + padded - 8U, bit_len);
    osty_rt_crypto_sha256_compress(ctx->state, final);
    if (padded == 128U) {
        osty_rt_crypto_sha256_compress(ctx->state, final + 64U);
    }
    for (i = 0; i < 8; i++) {
        osty_rt_crypto_store_be32(out + ((size_t)i * 4U), ctx->state[i]);
    }
    osty_rt_crypto_secure_zero(final, sizeof(final));
    osty_rt_crypto_secure_zero(ctx, sizeof(*ctx));
}

static void osty_rt_crypto_sha256_digest(const unsigned char *data, size_t len, unsigned char out[32]) {
    osty_rt_crypto_sha256_ctx ctx;
    osty_rt_crypto_sha256_init(&ctx);
    osty_rt_crypto_sha256_update(&ctx, data, len);
    osty_rt_crypto_sha256_final(&ctx, out);
}

static void osty_rt_crypto_sha512_compress(uint64_t state[8], const unsigned char block[128]) {
    uint64_t w[80];
    uint64_t a, b, c, d, e, f, g, h;
    int i;

    for (i = 0; i < 16; i++) {
        w[i] = osty_rt_crypto_load_be64(block + ((size_t)i * 8U));
    }
    for (i = 16; i < 80; i++) {
        uint64_t s0 = osty_rt_crypto_rotr64(w[i - 15], 1) ^
                      osty_rt_crypto_rotr64(w[i - 15], 8) ^
                      (w[i - 15] >> 7);
        uint64_t s1 = osty_rt_crypto_rotr64(w[i - 2], 19) ^
                      osty_rt_crypto_rotr64(w[i - 2], 61) ^
                      (w[i - 2] >> 6);
        w[i] = w[i - 16] + s0 + w[i - 7] + s1;
    }

    a = state[0];
    b = state[1];
    c = state[2];
    d = state[3];
    e = state[4];
    f = state[5];
    g = state[6];
    h = state[7];

    for (i = 0; i < 80; i++) {
        uint64_t s1 = osty_rt_crypto_rotr64(e, 14) ^
                      osty_rt_crypto_rotr64(e, 18) ^
                      osty_rt_crypto_rotr64(e, 41);
        uint64_t ch = (e & f) ^ ((~e) & g);
        uint64_t temp1 = h + s1 + ch + osty_rt_crypto_sha512_k[i] + w[i];
        uint64_t s0 = osty_rt_crypto_rotr64(a, 28) ^
                      osty_rt_crypto_rotr64(a, 34) ^
                      osty_rt_crypto_rotr64(a, 39);
        uint64_t maj = (a & b) ^ (a & c) ^ (b & c);
        uint64_t temp2 = s0 + maj;

        h = g;
        g = f;
        f = e;
        e = d + temp1;
        d = c;
        c = b;
        b = a;
        a = temp1 + temp2;
    }

    state[0] += a;
    state[1] += b;
    state[2] += c;
    state[3] += d;
    state[4] += e;
    state[5] += f;
    state[6] += g;
    state[7] += h;
}

typedef struct osty_rt_crypto_sha512_ctx {
    uint64_t state[8];
    uint64_t total_lo;
    uint64_t total_hi;
    size_t block_len;
    unsigned char block[128];
} osty_rt_crypto_sha512_ctx;

static void osty_rt_crypto_sha512_init(osty_rt_crypto_sha512_ctx *ctx) {
    ctx->state[0] = 0x6a09e667f3bcc908ULL;
    ctx->state[1] = 0xbb67ae8584caa73bULL;
    ctx->state[2] = 0x3c6ef372fe94f82bULL;
    ctx->state[3] = 0xa54ff53a5f1d36f1ULL;
    ctx->state[4] = 0x510e527fade682d1ULL;
    ctx->state[5] = 0x9b05688c2b3e6c1fULL;
    ctx->state[6] = 0x1f83d9abfb41bd6bULL;
    ctx->state[7] = 0x5be0cd19137e2179ULL;
    ctx->total_lo = 0;
    ctx->total_hi = 0;
    ctx->block_len = 0;
}

static void osty_rt_crypto_sha512_add_bytes(osty_rt_crypto_sha512_ctx *ctx,
                                            size_t len) {
    uint64_t add = (uint64_t)len;
    uint64_t prev = ctx->total_lo;
    ctx->total_lo += add;
    if (ctx->total_lo < prev) {
        if (ctx->total_hi == UINT64_MAX) {
            osty_rt_abort("runtime.crypto.sha512: input too large");
        }
        ctx->total_hi += 1U;
    }
}

static void osty_rt_crypto_sha512_update(osty_rt_crypto_sha512_ctx *ctx,
                                         const unsigned char *data,
                                         size_t len) {
    if (len == 0) {
        return;
    }
    osty_rt_crypto_sha512_add_bytes(ctx, len);
    if (ctx->block_len != 0) {
        size_t want = 128U - ctx->block_len;
        if (want > len) {
            want = len;
        }
        memcpy(ctx->block + ctx->block_len, data, want);
        ctx->block_len += want;
        data += want;
        len -= want;
        if (ctx->block_len == 128U) {
            osty_rt_crypto_sha512_compress(ctx->state, ctx->block);
            ctx->block_len = 0;
        }
    }
    while (len >= 128U) {
        osty_rt_crypto_sha512_compress(ctx->state, data);
        data += 128U;
        len -= 128U;
    }
    if (len != 0) {
        memcpy(ctx->block, data, len);
        ctx->block_len = len;
    }
}

static void osty_rt_crypto_sha512_final(osty_rt_crypto_sha512_ctx *ctx,
                                        unsigned char out[64]) {
    unsigned char final[256];
    uint64_t bit_hi = (ctx->total_hi << 3) | (ctx->total_lo >> 61);
    uint64_t bit_lo = ctx->total_lo << 3;
    size_t padded;
    int i;

    memset(final, 0, sizeof(final));
    if (ctx->block_len != 0) {
        memcpy(final, ctx->block, ctx->block_len);
    }
    final[ctx->block_len] = 0x80U;
    padded = (ctx->block_len + 1U + 16U <= 128U) ? 128U : 256U;
    osty_rt_crypto_store_be64(final + padded - 16U, bit_hi);
    osty_rt_crypto_store_be64(final + padded - 8U, bit_lo);
    osty_rt_crypto_sha512_compress(ctx->state, final);
    if (padded == 256U) {
        osty_rt_crypto_sha512_compress(ctx->state, final + 128U);
    }
    for (i = 0; i < 8; i++) {
        osty_rt_crypto_store_be64(out + ((size_t)i * 8U), ctx->state[i]);
    }
    osty_rt_crypto_secure_zero(final, sizeof(final));
    osty_rt_crypto_secure_zero(ctx, sizeof(*ctx));
}

static void osty_rt_crypto_sha512_digest(const unsigned char *data, size_t len, unsigned char out[64]) {
    osty_rt_crypto_sha512_ctx ctx;
    osty_rt_crypto_sha512_init(&ctx);
    osty_rt_crypto_sha512_update(&ctx, data, len);
    osty_rt_crypto_sha512_final(&ctx, out);
}

static void osty_rt_crypto_sha1_compress(uint32_t state[5], const unsigned char block[64]) {
    uint32_t w[80];
    uint32_t a, b, c, d, e;
    int i;

    for (i = 0; i < 16; i++) {
        w[i] = osty_rt_crypto_load_be32(block + ((size_t)i * 4U));
    }
    for (i = 16; i < 80; i++) {
        w[i] = osty_rt_crypto_rotl32(w[i - 3] ^ w[i - 8] ^ w[i - 14] ^ w[i - 16], 1);
    }

    a = state[0];
    b = state[1];
    c = state[2];
    d = state[3];
    e = state[4];

    for (i = 0; i < 80; i++) {
        uint32_t f;
        uint32_t k;
        uint32_t temp;
        if (i < 20) {
            f = (b & c) | ((~b) & d);
            k = 0x5a827999U;
        } else if (i < 40) {
            f = b ^ c ^ d;
            k = 0x6ed9eba1U;
        } else if (i < 60) {
            f = (b & c) | (b & d) | (c & d);
            k = 0x8f1bbcdcU;
        } else {
            f = b ^ c ^ d;
            k = 0xca62c1d6U;
        }
        temp = osty_rt_crypto_rotl32(a, 5) + f + e + k + w[i];
        e = d;
        d = c;
        c = osty_rt_crypto_rotl32(b, 30);
        b = a;
        a = temp;
    }

    state[0] += a;
    state[1] += b;
    state[2] += c;
    state[3] += d;
    state[4] += e;
}

static void osty_rt_crypto_sha1_digest(const unsigned char *data, size_t len, unsigned char out[20]) {
    uint32_t state[5] = {
        0x67452301U, 0xefcdab89U, 0x98badcfeU, 0x10325476U, 0xc3d2e1f0U,
    };
    unsigned char final[128];
    uint64_t bit_len;
    size_t rem;
    size_t padded;
    int i;

    if (len > (size_t)(UINT64_MAX >> 3)) {
        osty_rt_abort("runtime.crypto.sha1: input too large");
    }
    bit_len = (uint64_t)len * 8ULL;

    while (len >= 64U) {
        osty_rt_crypto_sha1_compress(state, data);
        data += 64U;
        len -= 64U;
    }

    rem = len;
    memset(final, 0, sizeof(final));
    if (rem != 0) {
        memcpy(final, data, rem);
    }
    final[rem] = 0x80U;
    padded = (rem + 1U + 8U <= 64U) ? 64U : 128U;
    osty_rt_crypto_store_be64(final + padded - 8U, bit_len);
    osty_rt_crypto_sha1_compress(state, final);
    if (padded == 128U) {
        osty_rt_crypto_sha1_compress(state, final + 64U);
    }

    for (i = 0; i < 5; i++) {
        osty_rt_crypto_store_be32(out + ((size_t)i * 4U), state[i]);
    }
    osty_rt_crypto_secure_zero(final, sizeof(final));
    osty_rt_crypto_secure_zero(state, sizeof(state));
}

static void osty_rt_crypto_md5_compress(uint32_t state[4], const unsigned char block[64]) {
    uint32_t m[16];
    uint32_t a, b, c, d;
    int i;

    for (i = 0; i < 16; i++) {
        m[i] = osty_rt_crypto_load_le32(block + ((size_t)i * 4U));
    }

    a = state[0];
    b = state[1];
    c = state[2];
    d = state[3];

    for (i = 0; i < 64; i++) {
        uint32_t f;
        uint32_t g;
        uint32_t temp;
        if (i < 16) {
            f = (b & c) | ((~b) & d);
            g = (uint32_t)i;
        } else if (i < 32) {
            f = (d & b) | ((~d) & c);
            g = (uint32_t)((5 * i + 1) & 15);
        } else if (i < 48) {
            f = b ^ c ^ d;
            g = (uint32_t)((3 * i + 5) & 15);
        } else {
            f = c ^ (b | (~d));
            g = (uint32_t)((7 * i) & 15);
        }
        temp = d;
        d = c;
        c = b;
        b = b + osty_rt_crypto_rotl32(a + f + osty_rt_crypto_md5_k[i] + m[g], osty_rt_crypto_md5_s[i]);
        a = temp;
    }

    state[0] += a;
    state[1] += b;
    state[2] += c;
    state[3] += d;
}

static void osty_rt_crypto_md5_digest(const unsigned char *data, size_t len, unsigned char out[16]) {
    uint32_t state[4] = {
        0x67452301U, 0xefcdab89U, 0x98badcfeU, 0x10325476U,
    };
    unsigned char final[128];
    uint64_t bit_len;
    size_t rem;
    size_t padded;
    int i;

    if (len > (size_t)(UINT64_MAX >> 3)) {
        osty_rt_abort("runtime.crypto.md5: input too large");
    }
    bit_len = (uint64_t)len * 8ULL;

    while (len >= 64U) {
        osty_rt_crypto_md5_compress(state, data);
        data += 64U;
        len -= 64U;
    }

    rem = len;
    memset(final, 0, sizeof(final));
    if (rem != 0) {
        memcpy(final, data, rem);
    }
    final[rem] = 0x80U;
    padded = (rem + 1U + 8U <= 64U) ? 64U : 128U;
    osty_rt_crypto_store_le32(final + padded - 8U, (uint32_t)bit_len);
    osty_rt_crypto_store_le32(final + padded - 4U, (uint32_t)(bit_len >> 32));
    osty_rt_crypto_md5_compress(state, final);
    if (padded == 128U) {
        osty_rt_crypto_md5_compress(state, final + 64U);
    }

    for (i = 0; i < 4; i++) {
        osty_rt_crypto_store_le32(out + ((size_t)i * 4U), state[i]);
    }
    osty_rt_crypto_secure_zero(final, sizeof(final));
    osty_rt_crypto_secure_zero(state, sizeof(state));
}

static void osty_rt_crypto_hmac_sha256_digest(const unsigned char *key,
                                              size_t key_len,
                                              const unsigned char *message,
                                              size_t message_len,
                                              unsigned char out[32]) {
    unsigned char key_block[64];
    unsigned char pad[64];
    unsigned char inner_digest[32];
    osty_rt_crypto_sha256_ctx ctx;
    size_t i;

    memset(key_block, 0, sizeof(key_block));
    if (key_len > sizeof(key_block)) {
        osty_rt_crypto_sha256_digest(key, key_len, key_block);
    } else if (key_len != 0) {
        memcpy(key_block, key, key_len);
    }

    for (i = 0; i < sizeof(key_block); i++) {
        pad[i] = (unsigned char)(key_block[i] ^ 0x36U);
    }
    osty_rt_crypto_sha256_init(&ctx);
    osty_rt_crypto_sha256_update(&ctx, pad, sizeof(pad));
    osty_rt_crypto_sha256_update(&ctx, message, message_len);
    osty_rt_crypto_sha256_final(&ctx, inner_digest);

    for (i = 0; i < sizeof(key_block); i++) {
        pad[i] = (unsigned char)(key_block[i] ^ 0x5cU);
    }
    osty_rt_crypto_sha256_init(&ctx);
    osty_rt_crypto_sha256_update(&ctx, pad, sizeof(pad));
    osty_rt_crypto_sha256_update(&ctx, inner_digest, sizeof(inner_digest));
    osty_rt_crypto_sha256_final(&ctx, out);

    osty_rt_crypto_secure_zero(key_block, sizeof(key_block));
    osty_rt_crypto_secure_zero(pad, sizeof(pad));
    osty_rt_crypto_secure_zero(inner_digest, sizeof(inner_digest));
}

static void osty_rt_crypto_hmac_sha512_digest(const unsigned char *key,
                                              size_t key_len,
                                              const unsigned char *message,
                                              size_t message_len,
                                              unsigned char out[64]) {
    unsigned char key_block[128];
    unsigned char pad[128];
    unsigned char inner_digest[64];
    osty_rt_crypto_sha512_ctx ctx;
    size_t i;

    memset(key_block, 0, sizeof(key_block));
    if (key_len > sizeof(key_block)) {
        osty_rt_crypto_sha512_digest(key, key_len, key_block);
    } else if (key_len != 0) {
        memcpy(key_block, key, key_len);
    }

    for (i = 0; i < sizeof(key_block); i++) {
        pad[i] = (unsigned char)(key_block[i] ^ 0x36U);
    }
    osty_rt_crypto_sha512_init(&ctx);
    osty_rt_crypto_sha512_update(&ctx, pad, sizeof(pad));
    osty_rt_crypto_sha512_update(&ctx, message, message_len);
    osty_rt_crypto_sha512_final(&ctx, inner_digest);

    for (i = 0; i < sizeof(key_block); i++) {
        pad[i] = (unsigned char)(key_block[i] ^ 0x5cU);
    }
    osty_rt_crypto_sha512_init(&ctx);
    osty_rt_crypto_sha512_update(&ctx, pad, sizeof(pad));
    osty_rt_crypto_sha512_update(&ctx, inner_digest, sizeof(inner_digest));
    osty_rt_crypto_sha512_final(&ctx, out);

    osty_rt_crypto_secure_zero(key_block, sizeof(key_block));
    osty_rt_crypto_secure_zero(pad, sizeof(pad));
    osty_rt_crypto_secure_zero(inner_digest, sizeof(inner_digest));
}

static osty_rt_once_t osty_rt_crypto_selftest_once = OSTY_RT_ONCE_INIT;

static void osty_rt_crypto_selftest(void) {
    static const unsigned char abc[] = {'a', 'b', 'c'};
    static const unsigned char quick_msg[] = "The quick brown fox jumps over the lazy dog";
    static const unsigned char quick_key[] = "key";
    unsigned char long_key[200];
    unsigned char long_msg_local[3000];
    unsigned char digest[64];
    size_t i;

    memset(long_key, 'a', sizeof(long_key));
    for (i = 0; i < sizeof(long_msg_local); i += 3U) {
        long_msg_local[i] = 'a';
        long_msg_local[i + 1U] = 'b';
        long_msg_local[i + 2U] = 'c';
    }

    osty_rt_crypto_sha256_digest(abc, sizeof(abc), digest);
    if (!osty_rt_crypto_digest_matches_hex(digest, 32U, "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad")) {
        osty_rt_abort("runtime.crypto.selftest: sha256 failed");
    }
    osty_rt_crypto_sha512_digest(abc, sizeof(abc), digest);
    if (!osty_rt_crypto_digest_matches_hex(digest, 64U, "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f")) {
        osty_rt_abort("runtime.crypto.selftest: sha512 failed");
    }
    osty_rt_crypto_sha1_digest(abc, sizeof(abc), digest);
    if (!osty_rt_crypto_digest_matches_hex(digest, 20U, "a9993e364706816aba3e25717850c26c9cd0d89d")) {
        osty_rt_abort("runtime.crypto.selftest: sha1 failed");
    }
    osty_rt_crypto_md5_digest(abc, sizeof(abc), digest);
    if (!osty_rt_crypto_digest_matches_hex(digest, 16U, "900150983cd24fb0d6963f7d28e17f72")) {
        osty_rt_abort("runtime.crypto.selftest: md5 failed");
    }
    osty_rt_crypto_hmac_sha256_digest(
        quick_key, sizeof(quick_key) - 1U,
        quick_msg, sizeof(quick_msg) - 1U,
        digest);
    if (!osty_rt_crypto_digest_matches_hex(digest, 32U, "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8")) {
        osty_rt_abort("runtime.crypto.selftest: hmac-sha256 failed");
    }
    osty_rt_crypto_hmac_sha512_digest(
        quick_key, sizeof(quick_key) - 1U,
        quick_msg, sizeof(quick_msg) - 1U,
        digest);
    if (!osty_rt_crypto_digest_matches_hex(digest, 64U, "b42af09057bac1e2d41708e48a902e09b5ff7f12ab428a4fe86653c73dd248fb82f948a549f7b791a5b41915ee4d1ec3935357e4e2317250d0372afa2ebeeb3a")) {
        osty_rt_abort("runtime.crypto.selftest: hmac-sha512 failed");
    }
    osty_rt_crypto_sha256_digest(long_msg_local, sizeof(long_msg_local), digest);
    if (!osty_rt_crypto_digest_matches_hex(digest, 32U, "328de8f1895f8bb09f6e6b4c2012ef2b2a6f067cd002794b750aa040a6f6d8bd")) {
        osty_rt_abort("runtime.crypto.selftest: long sha256 failed");
    }
    osty_rt_crypto_sha512_digest(long_msg_local, sizeof(long_msg_local), digest);
    if (!osty_rt_crypto_digest_matches_hex(digest, 64U, "14e615e6e7d4cf8cc75df5f408c558ddc98ad6eace44cabff9b4dcc9c8a4c22c0d772680f0267eb2595847c4dae7ecae06fc374d37f9f65db8502b0b7c048db1")) {
        osty_rt_abort("runtime.crypto.selftest: long sha512 failed");
    }
    osty_rt_crypto_hmac_sha256_digest(long_key, sizeof(long_key), long_msg_local, sizeof(long_msg_local), digest);
    if (!osty_rt_crypto_digest_matches_hex(digest, 32U, "7068ec7fdc9547b40e98e4bd5f7213d3c31088b993dfe87067c7636c786fc8b7")) {
        osty_rt_abort("runtime.crypto.selftest: long hmac-sha256 failed");
    }
    osty_rt_crypto_hmac_sha512_digest(long_key, sizeof(long_key), long_msg_local, sizeof(long_msg_local), digest);
    if (!osty_rt_crypto_digest_matches_hex(digest, 64U, "c1d9c61a8a67c829edae908bd74acf67d556379eabb0fa52d63cde5fa016dc339e8f323bb3d11c699a6feeb6189370744f7ef33565b180ae0395d574497fdd5b")) {
        osty_rt_abort("runtime.crypto.selftest: long hmac-sha512 failed");
    }
    osty_rt_crypto_secure_zero(digest, sizeof(digest));
    osty_rt_crypto_secure_zero(long_key, sizeof(long_key));
    osty_rt_crypto_secure_zero(long_msg_local, sizeof(long_msg_local));
}

static void osty_rt_crypto_require_selftest(void) {
    osty_rt_once(&osty_rt_crypto_selftest_once, osty_rt_crypto_selftest);
}

static void osty_rt_crypto_fill_random(unsigned char *out, size_t len) {
    size_t offset = 0;

    if (len == 0) {
        return;
    }
#if defined(OSTY_RT_PLATFORM_WIN32)
    while (offset < len) {
        unsigned int word = 0;
        size_t chunk = len - offset;
        if (rand_s(&word) != 0) {
            osty_rt_abort("runtime.crypto.random_bytes: rand_s failed");
        }
        if (chunk > sizeof(word)) {
            chunk = sizeof(word);
        }
        memcpy(out + offset, &word, chunk);
        offset += chunk;
    }
#elif defined(__APPLE__) || defined(__FreeBSD__) || defined(__OpenBSD__) || defined(__NetBSD__)
    arc4random_buf(out, len);
#else
#  if defined(__linux__)
    while (offset < len) {
        ssize_t n = getrandom(out + offset, len - offset, 0);
        if (n < 0) {
            if (errno == EINTR) {
                continue;
            }
            if (errno == ENOSYS) {
                break;
            }
            osty_rt_abort("runtime.crypto.random_bytes: getrandom failed");
        }
        offset += (size_t)n;
    }
    if (offset == len) {
        return;
    }
#  endif
    {
        int fd = open("/dev/urandom", O_RDONLY);
        if (fd < 0) {
            osty_rt_abort("runtime.crypto.random_bytes: open /dev/urandom failed");
        }
        while (offset < len) {
            ssize_t n = read(fd, out + offset, len - offset);
            if (n < 0) {
                if (errno == EINTR) {
                    continue;
                }
                close(fd);
                osty_rt_abort("runtime.crypto.random_bytes: read /dev/urandom failed");
            }
            if (n == 0) {
                close(fd);
                osty_rt_abort("runtime.crypto.random_bytes: short read from /dev/urandom");
            }
            offset += (size_t)n;
        }
        close(fd);
    }
#endif
}

void *osty_rt_crypto_sha256(void *raw_data) {
    const unsigned char *data;
    size_t len;
    unsigned char digest[32];
    void *out;

    osty_rt_crypto_require_selftest();
    osty_rt_crypto_bytes_view(raw_data, &data, &len, "runtime.crypto.sha256: invalid bytes");
    osty_rt_crypto_sha256_digest(data, len, digest);
    out = osty_rt_bytes_dup_site(digest, sizeof(digest), "runtime.crypto.sha256");
    osty_rt_crypto_secure_zero(digest, sizeof(digest));
    return out;
}

void *osty_rt_crypto_sha512(void *raw_data) {
    const unsigned char *data;
    size_t len;
    unsigned char digest[64];
    void *out;

    osty_rt_crypto_require_selftest();
    osty_rt_crypto_bytes_view(raw_data, &data, &len, "runtime.crypto.sha512: invalid bytes");
    osty_rt_crypto_sha512_digest(data, len, digest);
    out = osty_rt_bytes_dup_site(digest, sizeof(digest), "runtime.crypto.sha512");
    osty_rt_crypto_secure_zero(digest, sizeof(digest));
    return out;
}

void *osty_rt_crypto_sha1(void *raw_data) {
    const unsigned char *data;
    size_t len;
    unsigned char digest[20];
    void *out;

    osty_rt_crypto_require_selftest();
    osty_rt_crypto_bytes_view(raw_data, &data, &len, "runtime.crypto.sha1: invalid bytes");
    osty_rt_crypto_sha1_digest(data, len, digest);
    out = osty_rt_bytes_dup_site(digest, sizeof(digest), "runtime.crypto.sha1");
    osty_rt_crypto_secure_zero(digest, sizeof(digest));
    return out;
}

void *osty_rt_crypto_md5(void *raw_data) {
    const unsigned char *data;
    size_t len;
    unsigned char digest[16];
    void *out;

    osty_rt_crypto_require_selftest();
    osty_rt_crypto_bytes_view(raw_data, &data, &len, "runtime.crypto.md5: invalid bytes");
    osty_rt_crypto_md5_digest(data, len, digest);
    out = osty_rt_bytes_dup_site(digest, sizeof(digest), "runtime.crypto.md5");
    osty_rt_crypto_secure_zero(digest, sizeof(digest));
    return out;
}

void *osty_rt_crypto_hmac_sha256(void *raw_key, void *raw_message) {
    const unsigned char *key;
    const unsigned char *message;
    size_t key_len;
    size_t message_len;
    unsigned char digest[32];
    void *out;

    osty_rt_crypto_require_selftest();
    osty_rt_crypto_bytes_view(raw_key, &key, &key_len, "runtime.crypto.hmac_sha256: invalid key");
    osty_rt_crypto_bytes_view(raw_message, &message, &message_len, "runtime.crypto.hmac_sha256: invalid message");
    osty_rt_crypto_hmac_sha256_digest(key, key_len, message, message_len, digest);
    out = osty_rt_bytes_dup_site(digest, sizeof(digest), "runtime.crypto.hmac_sha256");
    osty_rt_crypto_secure_zero(digest, sizeof(digest));
    return out;
}

void *osty_rt_crypto_hmac_sha512(void *raw_key, void *raw_message) {
    const unsigned char *key;
    const unsigned char *message;
    size_t key_len;
    size_t message_len;
    unsigned char digest[64];
    void *out;

    osty_rt_crypto_require_selftest();
    osty_rt_crypto_bytes_view(raw_key, &key, &key_len, "runtime.crypto.hmac_sha512: invalid key");
    osty_rt_crypto_bytes_view(raw_message, &message, &message_len, "runtime.crypto.hmac_sha512: invalid message");
    osty_rt_crypto_hmac_sha512_digest(key, key_len, message, message_len, digest);
    out = osty_rt_bytes_dup_site(digest, sizeof(digest), "runtime.crypto.hmac_sha512");
    osty_rt_crypto_secure_zero(digest, sizeof(digest));
    return out;
}

void *osty_rt_crypto_random_bytes(int64_t n) {
    size_t len;
    osty_rt_bytes *out;

    osty_rt_crypto_require_selftest();
    if (n < 0) {
        osty_rt_abort("runtime.crypto.random_bytes: negative length");
    }
    len = (size_t)n;
    out = osty_rt_crypto_alloc_bytes(len, len == 0 ? "runtime.crypto.random_bytes.empty" : "runtime.crypto.random_bytes");
    if (len != 0) {
        osty_rt_crypto_fill_random(out->data, len);
    }
    return out;
}

bool osty_rt_crypto_constant_time_eq(void *raw_a, void *raw_b) {
    const unsigned char *a;
    const unsigned char *b;
    size_t a_len;
    size_t b_len;
    size_t max_len;
    size_t i;
    size_t diff;

    osty_rt_crypto_require_selftest();
    osty_rt_crypto_bytes_view(raw_a, &a, &a_len, "runtime.crypto.constant_time_eq: invalid left bytes");
    osty_rt_crypto_bytes_view(raw_b, &b, &b_len, "runtime.crypto.constant_time_eq: invalid right bytes");
    max_len = a_len > b_len ? a_len : b_len;
    diff = a_len ^ b_len;
    for (i = 0; i < max_len; i++) {
        unsigned char av = i < a_len ? a[i] : 0U;
        unsigned char bv = i < b_len ? b[i] : 0U;
        diff |= (size_t)(av ^ bv);
    }
    return diff == 0;
}

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void *osty_gc_alloc_pinned_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_pinned_v1"));
void osty_gc_pre_write_v1(void *owner, void *old_value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.pre_write_v1"));
void osty_gc_post_write_v1(void *owner, void *value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.post_write_v1"));
void *osty_gc_load_v1(void *value) __asm__(OSTY_GC_SYMBOL("osty.gc.load_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));
void osty_gc_root_release_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_release_v1"));
void osty_gc_pin_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.pin_v1"));
void osty_gc_unpin_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.unpin_v1"));
void osty_gc_global_root_register_v1(void *slot) __asm__(OSTY_GC_SYMBOL("osty.gc.global_root_register_v1"));
void osty_gc_global_root_unregister_v1(void *slot) __asm__(OSTY_GC_SYMBOL("osty.gc.global_root_unregister_v1"));
void osty_gc_safepoint_v1(int64_t safepoint_id, void *const *root_slots, int64_t root_slot_count) __asm__(OSTY_GC_SYMBOL("osty.gc.safepoint_v1"));

/* Shadow-stack frame descriptor. Each callee with managed locals allocas
 * one of these on its own stack and registers it via root_frame_enter_v1
 * at function entry, then unregisters via root_frame_leave_v1 before
 * every return. Ordering forms a thread-local linked list: the deepest
 * caller is the head, root frames threaded back through `prev`.
 *
 * Why this exists: safepoint_v1 only ever sees the *current* frame's
 * roots — not the caller's. Without a chain, an inner call's safepoint
 * would mark only its own locals, sweep the caller's Map / List
 * payloads as unreachable, and free them out from under the caller.
 * The chain is what makes cross-frame root tracking work. The struct
 * itself is forward-declared above (alongside the chain head); only
 * the runtime entry / leave symbols live here. */
void osty_gc_root_frame_enter_v1(struct osty_gc_root_frame *frame, void *const *slots, int64_t count) __asm__(OSTY_GC_SYMBOL("osty.gc.root_frame_enter_v1"));
void osty_gc_root_frame_leave_v1(struct osty_gc_root_frame *frame) __asm__(OSTY_GC_SYMBOL("osty.gc.root_frame_leave_v1"));
void *osty_rt_enum_alloc_ptr_v1(const char *site) __asm__(OSTY_GC_SYMBOL("osty.rt.enum_alloc_ptr_v1"));
void *osty_rt_enum_alloc_scalar_v1(const char *site) __asm__(OSTY_GC_SYMBOL("osty.rt.enum_alloc_scalar_v1"));

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) {
    if (byte_size < 0) {
        osty_rt_abort("negative GC allocation size");
    }
    return osty_gc_allocate_managed((size_t)byte_size, object_kind == 0 ? OSTY_GC_KIND_GENERIC : object_kind, site, NULL, NULL);
}

void *osty_gc_alloc_pinned_v1(int64_t object_kind, int64_t byte_size, const char *site) {
    if (byte_size < 0) {
        osty_rt_abort("negative GC pinned allocation size");
    }
    return osty_gc_allocate_pinned_managed(
        (size_t)byte_size,
        object_kind == 0 ? OSTY_GC_KIND_GENERIC : object_kind, site, NULL,
        NULL);
}

static void osty_rt_enum_ptr_payload_trace(void *payload) {
    /* `payload` IS the slot — the box's payload is a single
     * pointer-sized slot holding the inner managed pointer.
     * `mark_slot_v1` dispatches by current trace_slot_mode so the
     * cheney path can forward young inners + rewrite the slot. */
    osty_gc_mark_slot_v1(payload);
}

/* Phase A4 depth (RUNTIME_GC_DELTA §2.4). Trace callback for closure
 * envs: walks the capture array so any managed pointer stored in a
 * closure capture survives GC while the env itself is reachable.
 *
 * The env layout is self-describing (see `osty_rt_closure_env`) so the
 * trace needs no external descriptor — it reads `capture_count` and
 * `pointer_bitmap`, then calls `osty_gc_mark_slot_v1` only for slots
 * whose bitmap bit is set.
 *
 * This is a structural guarantee against scalar false-retention: the
 * frontend records which capture slots hold managed pointers at alloc
 * time (`osty_rt_closure_env_alloc_v2`), so scalar bit patterns that
 * happen to alias a live payload address can never keep that payload
 * alive through a closure env. This closes the probabilistic caveat
 * the v1 layout carried (find_header would have safely skipped
 * invalid pointers, but the guarantee was statistical, not
 * structural).
 */
static void osty_rt_closure_env_trace(void *payload) {
    osty_rt_closure_env *env = (osty_rt_closure_env *)payload;
    uint64_t bitmap;
    int64_t n;
    int64_t i;
    if (env == NULL || env->capture_count <= 0) {
        return;
    }
    bitmap = env->pointer_bitmap;
    n = env->capture_count;
    for (i = 0; i < n; i++) {
        if ((bitmap >> (uint64_t)i) & 1ULL) {
            osty_gc_mark_slot_v1((void *)&env->captures[i]);
        }
    }
}

/* Phase A4 depth: dedicated allocator for closure envs. Takes the
 * capture count up front so the layout and trace callback are set
 * together — there is no post-alloc mutation window where a
 * collection could see an env without its trace installed.
 *
 * `pointer_bitmap` (RUNTIME_GC §2.4): bit i is 1 iff capture slot i
 * holds a managed pointer. Scalar slots leave the bit clear so the
 * tracer skips them entirely, giving a structural (not probabilistic)
 * guarantee against scalar false-retention. The bitmap is 64 bits
 * wide so capture_count is capped at 64 — closures in practice never
 * approach this limit.
 *
 * Exported as `osty.rt.closure_env_alloc_v2` so the LLVM emitter can
 * call it with a single `call ptr @osty.rt.closure_env_alloc_v2(i64 N,
 * ptr %site, i64 %bitmap)` at the fn-value materialisation site.
 */
void *osty_rt_closure_env_alloc_v2(int64_t capture_count, const char *site, uint64_t pointer_bitmap)
    __asm__(OSTY_GC_SYMBOL("osty.rt.closure_env_alloc_v2"));

void *osty_rt_closure_env_alloc_v2(int64_t capture_count, const char *site, uint64_t pointer_bitmap) {
    size_t byte_size;
    osty_rt_closure_env *env;

    if (capture_count < 0) {
        osty_rt_abort("negative closure env capture count");
    }
    if (capture_count > 64) {
        osty_rt_abort("closure env capture count exceeds bitmap width (max 64)");
    }
    if ((size_t)capture_count > (SIZE_MAX - sizeof(osty_rt_closure_env)) / sizeof(void *)) {
        osty_rt_abort("closure env capture count overflow");
    }
    byte_size = sizeof(osty_rt_closure_env) + (size_t)capture_count * sizeof(void *);
    env = (osty_rt_closure_env *)osty_gc_allocate_managed(
        byte_size, OSTY_GC_KIND_CLOSURE_ENV, site,
        osty_rt_closure_env_trace, NULL);
    env->capture_count = capture_count;
    env->pointer_bitmap = pointer_bitmap;
    return env;
}

void *osty_rt_enum_alloc_ptr_v1(const char *site) {
    return osty_gc_allocate_managed(8, OSTY_GC_KIND_GENERIC_ENUM_PTR, site, osty_rt_enum_ptr_payload_trace, NULL);
}

void *osty_rt_enum_alloc_scalar_v1(const char *site) {
    return osty_gc_allocate_managed(8, OSTY_GC_KIND_GENERIC, site, NULL, NULL);
}

static void osty_gc_satb_log_grow(void) {
    int64_t new_cap;
    void **new_buf;

    new_cap = osty_gc_satb_log_cap == 0 ? 16 : osty_gc_satb_log_cap * 2;
    new_buf = (void **)realloc(osty_gc_satb_log, (size_t)new_cap * sizeof(void *));
    if (new_buf == NULL) {
        osty_rt_abort("out of memory (SATB log)");
    }
    osty_gc_satb_log = new_buf;
    osty_gc_satb_log_cap = new_cap;
}

static void osty_gc_satb_log_append(void *payload) {
    if (osty_gc_satb_log_count == osty_gc_satb_log_cap) {
        osty_gc_satb_log_grow();
    }
    osty_gc_satb_log[osty_gc_satb_log_count] = payload;
    osty_gc_satb_log_count += 1;
}

static void osty_gc_remembered_edges_grow(void) {
    int64_t new_cap;
    osty_gc_remembered_edge *new_buf;

    new_cap = osty_gc_remembered_edge_cap == 0 ? 16 : osty_gc_remembered_edge_cap * 2;
    new_buf = (osty_gc_remembered_edge *)realloc(osty_gc_remembered_edges, (size_t)new_cap * sizeof(osty_gc_remembered_edge));
    if (new_buf == NULL) {
        osty_rt_abort("out of memory (remembered edges)");
    }
    osty_gc_remembered_edges = new_buf;
    osty_gc_remembered_edge_cap = new_cap;
}

static void osty_gc_dirty_old_grow(void) {
    int64_t new_cap;
    osty_gc_header **new_buf;

    new_cap = osty_gc_dirty_old_cap == 0 ? 16 : osty_gc_dirty_old_cap * 2;
    new_buf = (osty_gc_header **)realloc(
        osty_gc_dirty_old_headers, (size_t)new_cap * sizeof(osty_gc_header *));
    if (new_buf == NULL) {
        osty_rt_abort("out of memory (dirty old list)");
    }
    osty_gc_dirty_old_headers = new_buf;
    osty_gc_dirty_old_cap = new_cap;
}

/* Push `header` onto the dirty list. Caller must have just transitioned
 * `header->card_dirty` from 0 to 1, which doubles as the membership
 * flag — the array therefore never holds duplicates. */
static OSTY_HOT_INLINE void osty_gc_dirty_old_push(osty_gc_header *header) {
    if (osty_gc_dirty_old_count == osty_gc_dirty_old_cap) {
        osty_gc_dirty_old_grow();
    }
    osty_gc_dirty_old_headers[osty_gc_dirty_old_count++] = header;
}

/* Phase F barrier helper: flip `card_dirty` 0→1 once per major cycle
 * and push the owner onto the dirty list. The bit doubles as the
 * membership flag, so subsequent stores from the same owner short-
 * circuit on the `if (!card_dirty)` test without re-pushing. Inlined
 * at every call site so the hot path stays branch-free post-codegen. */
static OSTY_HOT_INLINE void osty_gc_card_dirty_set(
    osty_gc_header *owner_header) {
    if (!owner_header->card_dirty) {
        owner_header->card_dirty = 1;
        osty_gc_dirty_old_push(owner_header);
        osty_gc_cards_dirtied_total += 1;
    }
}

static bool osty_gc_remembered_edges_contains(void *owner, void *value) {
    int64_t i;
    for (i = 0; i < osty_gc_remembered_edge_count; i++) {
        if (osty_gc_remembered_edges[i].owner == owner && osty_gc_remembered_edges[i].value == value) {
            return true;
        }
    }
    return false;
}

static void osty_gc_remembered_edges_append(void *owner, void *value) {
    if (osty_gc_remembered_edges_contains(owner, value)) {
        return;
    }
    if (osty_gc_remembered_edge_count == osty_gc_remembered_edge_cap) {
        osty_gc_remembered_edges_grow();
    }
    osty_gc_remembered_edges[osty_gc_remembered_edge_count].owner = owner;
    osty_gc_remembered_edges[osty_gc_remembered_edge_count].value = value;
    osty_gc_remembered_edge_count += 1;
}

static void osty_gc_barrier_logs_clear(void) {
    osty_gc_satb_log_count = 0;
    osty_gc_remembered_edge_count = 0;
}

void osty_gc_pre_write_v1(void *owner, void *old_value, int64_t slot_kind) {
    osty_gc_header *owner_header;
    osty_gc_header *old_header;

    (void)slot_kind;
    osty_gc_acquire();
    osty_gc_pre_write_count += 1;
    if (old_value == NULL) {
        osty_gc_release();
        return;
    }
    old_header = osty_gc_find_header_or_forwarded(old_value);
    if (old_header == NULL) {
        osty_gc_release();
        return;
    }
    osty_gc_pre_write_managed_count += 1;
    old_value = old_header->payload;
    osty_gc_satb_log_append(old_value);
    /* Phase C (RUNTIME_GC_DELTA §2.7, §4.3): SATB consumption. While
     * the incremental collector is in MARK_INCREMENTAL the mutator can
     * overwrite a slot whose old value has not been reached yet — a
     * classic "lost object" hazard for any non-STW marker. Greying the
     * old value here preserves the snapshot-at-the-beginning guarantee:
     * every pointer that was live at start-of-mark survives to the
     * end, even if the mutator rewrites the slot mid-cycle. Outside
     * MARK_INCREMENTAL the barrier is a no-op (STW cycles don't need
     * SATB). */
    if (osty_gc_state == OSTY_GC_STATE_MARK_INCREMENTAL) {
        /* Phase 0g: same WHITE→GREY CAS as `mark_header`. The barrier
         * runs under `osty_gc_lock`, so today it can't lose the race
         * to a concurrent tracer — but once the lock drops around
         * `dispatch_trace` (Phase 0h) two paths grey the same header:
         * SATB on a slot overwrite, and the tracer that just popped
         * this header off the mark stack. CAS keeps either path
         * idempotent and ensures exactly one push per WHITE→GREY
         * transition. */
        uint8_t expected = OSTY_GC_COLOR_WHITE;
        osty_gc_mark_cas_attempts_total += 1;
        if (__atomic_compare_exchange_n(&old_header->color, &expected,
                                        OSTY_GC_COLOR_GREY,
                                        false /* strong */,
                                        __ATOMIC_ACQ_REL,
                                        __ATOMIC_ACQUIRE)) {
            __atomic_store_n(&old_header->marked, true, __ATOMIC_RELAXED);
            osty_gc_mark_stack_push(old_header);
            osty_gc_satb_barrier_greyed_total += 1;
        } else {
            osty_gc_mark_cas_failures_total += 1;
        }
    }
    owner_header = osty_gc_find_header_or_forwarded(owner);
    if (owner_header != NULL && osty_gc_header_is_pinned(owner_header)) {
        osty_gc_collection_requested = true;
    }
    osty_gc_release();
}

void osty_gc_post_write_v1(void *owner, void *value, int64_t slot_kind) {
    osty_gc_header *owner_header;

    (void)slot_kind;

    /* Single-mutator fast path. STW collection means no other thread can be
     * mutating GC state concurrently while we run, so the recursive mutex
     * acquire/release pair is pure overhead — and the profile says it's the
     * single dominant cost in pointer-store-heavy workloads (log-aggregator
     * spends ~99% of its CPU time inside this helper, all of which traces
     * back to the lock cycle in `pthread_mutex_lock` /
     * `_pthread_mutex_firstfit_lock_slow`).
     *
     * Phase F: when card marking is enabled, the per-edge log append is
     * replaced by a single dirty-bit set + dirty-array push (the bit
     * doubles as membership flag, so the push fires only on the 0→1
     * transition). The cleared edge-log path stays available via
     * `OSTY_GC_CARD_MARKING=0`.
     *
     * `osty_gc_card_marking_now()` is loaded after the unmanaged early
     * exits so the YOUNG-skip / NULL-store paths don't pay for the
     * cached-env load.
     *
     * When a worker thread is live (`osty_concurrent_workers > 0`) we fall
     * through to the locked path so concurrent task-group programs keep the
     * old single-writer guarantee. Phase 0a: `osty_gc_serialized_now()`
     * also gates on the bg marker — its tracer reads the same
     * payload→header index this fast path probes, so concurrent reads
     * vs the unlocked appends here would race. */
    if (osty_gc_serialized_now()) {
        osty_gc_post_write_count += 1;
        if (owner == NULL || value == NULL) {
            return;
        }
        /* Tinytag-young owner fast path. Pin / root-bind on a young
         * payload promotes it out of the no-header arena (Phase 7
         * step 2 contract), so any address we observe inside the
         * young arena range is by construction unpinned. The minor
         * collector scans every YOUNG-generation owner and forwards
         * its children, so a YOUNG→* write doesn't need a
         * remembered-set entry — and we don't even need to
         * reconstruct a header to know the generation. The 4-compare
         * `is_young_page` range check replaces the hash + arena
         * fast-path inside `find_header`, which used to take ~10%
         * of CPU on intToStr-heavy workloads (every list_push on a
         * young List paid for it). */
        if (osty_gc_arena_is_young_page(owner)) {
            return;
        }
        owner_header = osty_gc_find_header(owner);
        if (owner_header == NULL) {
            /* Owner forwarded by a recent compaction. Take the slow path so
             * `osty_gc_find_header_or_forwarded` can resolve the indirection
             * — rare enough not to hurt the hot path. */
            goto locked_path;
        }
        /* Headerful-young owner skip — pre-#977 this was the only
         * young-skip path. Headerful YOUNG owners (Maps before they
         * promoted, etc.) need the same skip as tinytag young: the
         * minor scans them via the normal mark/trace pipeline. The
         * tinytag fast path above sidesteps `find_header` for the
         * common case; this one keeps the same semantic for
         * objects that stayed headerful. */
        if (owner_header->generation == OSTY_GC_GEN_YOUNG &&
            !osty_gc_header_is_pinned(owner_header)) {
            return;
        }
        if (osty_gc_card_marking_now()) {
            /* Cards-on young-arena fast path. Most cross-gen edges
             * target a value in the young arena (the headerful YOUNG
             * route only handles humongous allocations); the same 4-
             * compare range check that owner-side already uses confirms
             * managed + YOUNG without a hash probe. Skipping
             * `forward_payload` + `find_header` here lets the cards
             * barrier collapse to "range-check value, set bit, push"
             * for every Map.insert / List.push into a long-lived
             * collection — exactly the pattern the lru-sim and
             * dedup-stress wins ride. */
            if (osty_gc_arena_is_young_page(value)) {
                osty_gc_post_write_managed_count += 1;
                osty_gc_card_dirty_set(owner_header);
                if (osty_gc_header_is_pinned(owner_header)) {
                    osty_gc_collection_requested = true;
                }
                return;
            }
            /* Slow fallback: humongous YOUNG, OLD, or unmanaged value.
             * Filter OLD→OLD eagerly — the legacy edge log relied on
             * `compact_after_minor` to drop them post-cycle, but the
             * dirty list has no equivalent compaction so the only way
             * to keep the next minor focused on real cross-gen work is
             * to never insert OLD→OLD entries. */
            value = osty_gc_forward_payload(value);
            osty_gc_header *value_header = osty_gc_find_header(value);
            if (value_header == NULL) {
                return;
            }
            osty_gc_post_write_managed_count += 1;
            if (value_header->generation == OSTY_GC_GEN_YOUNG) {
                osty_gc_card_dirty_set(owner_header);
            }
            if (osty_gc_header_is_pinned(owner_header)) {
                osty_gc_collection_requested = true;
            }
            return;
        }
        value = osty_gc_forward_payload(value);
        if (osty_gc_find_header(value) == NULL) {
            return;
        }
        osty_gc_post_write_managed_count += 1;
        osty_gc_remembered_edges_append(owner_header->payload, value);
        if (osty_gc_header_is_pinned(owner_header)) {
            osty_gc_collection_requested = true;
        }
        return;
    }

locked_path:

    osty_gc_acquire();
    osty_gc_post_write_count += 1;
    if (owner == NULL || value == NULL) {
        osty_gc_release();
        return;
    }
    owner_header = osty_gc_find_header_or_forwarded(owner);
    if (owner_header == NULL) {
        osty_gc_release();
        return;
    }
    if (osty_gc_card_marking_now()) {
        if (osty_gc_arena_is_young_page(value)) {
            osty_gc_post_write_managed_count += 1;
            osty_gc_card_dirty_set(owner_header);
            if (osty_gc_header_is_pinned(owner_header)) {
                osty_gc_collection_requested = true;
            }
            osty_gc_release();
            return;
        }
        value = osty_gc_forward_payload(value);
        {
            osty_gc_header *value_header = osty_gc_find_header(value);
            if (value_header == NULL) {
                osty_gc_release();
                return;
            }
            osty_gc_post_write_managed_count += 1;
            if (value_header->generation == OSTY_GC_GEN_YOUNG) {
                osty_gc_card_dirty_set(owner_header);
            }
        }
        if (osty_gc_header_is_pinned(owner_header)) {
            osty_gc_collection_requested = true;
        }
        osty_gc_release();
        return;
    }
    value = osty_gc_forward_payload(value);
    if (osty_gc_find_header(value) == NULL) {
        osty_gc_release();
        return;
    }
    osty_gc_post_write_managed_count += 1;
    osty_gc_remembered_edges_append(owner_header->payload, value);
    if (osty_gc_header_is_pinned(owner_header)) {
        osty_gc_collection_requested = true;
    }
    osty_gc_release();
}

void *osty_gc_load_v1(void *value) {
    void *out = value;

    /* Pre-compaction fast path. The only thing the slow path can do
     * differently from "return value" is route forwarded payloads to
     * their relocated header, which can only fire after a major
     * compaction has actually moved at least one object — i.e., when
     * `osty_gc_forwarding_count > 0`. Until then every load resolves
     * to its input verbatim, so the hash + forwarding-table probes
     * are pure overhead. log-aggregator-style code (read-heavy lists,
     * no compaction) spent ~10% of CPU here post-#872 just hashing
     * pointers to confirm "yes, that's the address you already had".
     *
     * Counters: load_count still steps every call (test observability).
     * load_managed_count was previously "value resolved to a managed
     * header"; with the fast path it counts non-NULL loads instead,
     * which equals the previous semantic on every test program in
     * the suite (none of them mix unmanaged sentinels into pointer
     * fields the GC sees). The slow path keeps the original
     * accounting for forwarded loads. */
    /* SSO inline strings have all their content in the pointer; no
     * header to look up, no forwarding to follow. Pass through
     * before either the fast or slow path interprets the encoded
     * content as an address. They are NOT GC-managed (no header,
     * no payload allocation), so `load_managed_count` is not bumped
     * — that counter pins what the live-set tests check. The
     * `live_count` semantic in `llvm_runtime_gc_test` already
     * reflects this (split pieces drop from contributing to the
     * live count once they're inline). */
    if (osty_rt_string_is_inline((const char *)value)) {
        osty_gc_load_count += 1;
        return value;
    }
    /* Phase E follow-up: cheney PROMOTED / FORWARDED tag follow.
     * When `osty_gc_promote_young_to_old` (root_bind / pin path)
     * moves a young payload to OLD, callers that retained the
     * pre-promote young pointer would otherwise read dead bytes
     * on subsequent `osty_rt_list_cast` / `_map_cast` / `_set_cast`
     * (every list/map/set op funnels through here). Following the
     * tag here keeps the cast site cheap (no per-typed-cast check)
     * and centralises the "stale young addr → live OLD addr"
     * redirect. The arena range check is a 4-compare on the young
     * mmap; only addresses inside young arena pay the additional
     * micro-header read. Non-young loads (the dominant case) take
     * the cheap fast path below. */
    if (value != NULL && osty_gc_arena_is_young_page(value)) {
        osty_gc_micro_header *micro =
            osty_gc_micro_header_for_payload(value);
        uint64_t fom = micro->forward_or_meta;
        uint64_t tag = fom & OSTY_GC_FORWARD_TAG_MASK;
        if (tag == OSTY_GC_FORWARD_TAG_PROMOTED ||
            tag == OSTY_GC_FORWARD_TAG_FORWARDED) {
            void *redirected = (void *)(uintptr_t)(fom & ~OSTY_GC_FORWARD_TAG_MASK);
            osty_gc_load_count += 1;
            osty_gc_load_managed_count += 1;
            if (redirected != value) {
                osty_gc_load_forwarded_count += 1;
            }
            return redirected;
        }
    }
    if (osty_gc_serialized_now() &&
        osty_gc_forwarding_count == 0) {
        osty_gc_load_count += 1;
        if (value != NULL) {
            osty_gc_load_managed_count += 1;
        }
        return value;
    }

    /* Single-mutator path with forwarding active: skip the lock but
     * keep the full hash + forwarding lookup. */
    if (osty_gc_serialized_now()) {
        osty_gc_load_count += 1;
        if (osty_gc_find_header(value) != NULL) {
            osty_gc_load_managed_count += 1;
        } else {
            out = osty_gc_forward_payload(value);
            if (out != value) {
                osty_gc_load_managed_count += 1;
                osty_gc_load_forwarded_count += 1;
            }
        }
        return out;
    }

    osty_gc_acquire();
    osty_gc_load_count += 1;
    if (osty_gc_find_header(value) != NULL) {
        osty_gc_load_managed_count += 1;
    } else {
        out = osty_gc_forward_payload(value);
        if (out != value) {
            osty_gc_load_managed_count += 1;
            osty_gc_load_forwarded_count += 1;
        }
    }
    osty_gc_release();
    return out;
}

/* Phase 6 step 2 + Phase E follow-up: Cheney slot action.
 *
 * For every child pointer the tracer encounters:
 *   - Young from-space: forward to to-space, rewrite slot with new
 *     address. Standard Cheney mechanic.
 *   - OLD: enqueue the OLD header for owner-tracing in CHENEY mode.
 *     Without this an OLD owner that holds young children (e.g.,
 *     `Map<String, _>` whose write barrier doesn't fire) is invisible
 *     to the cheney scan.
 *   - Anything else (NULL, SSO, OLD-but-no-tracer, stale-young
 *     above-cursor, etc.): pass-through, no-op.
 *
 * Sharing the existing `mark_slot_v1` dispatch lets every kind's
 * tracer participate in Cheney without any per-tracer changes. */
static void osty_gc_cheney_slot(void *slot_addr) {
    void *payload = NULL;
    void *forwarded;
    osty_gc_header *header;

    if (slot_addr == NULL) {
        return;
    }
    memcpy(&payload, slot_addr, sizeof(payload));
    if (payload == NULL) {
        return;
    }
    /* Young from-space → forward + rewrite. cheney_forward returns
     * pass-through for non-young, so the OLD path doesn't change
     * the slot. */
    forwarded = osty_gc_cheney_forward(payload);
    if (forwarded != payload) {
        memcpy(slot_addr, &forwarded, sizeof(forwarded));
        payload = forwarded;
    }
    /* Enqueue the (possibly-forwarded) target for transitive trace.
     *
     * Originally gated on generation == OLD, on the assumption that
     * young objects already get walked by `cheney_scan_loop`. That's
     * true for tinytag young (no header) — they live in young from-
     * space and the scan loop visits them all. But HEADERFUL YOUNG
     * objects (Map / Set / Channel / Closure-env / GENERIC) are NOT
     * visited by the scan loop (scan walks young to-space which only
     * contains tinytag survivors, never headerful payloads). So if a
     * Map is headerful-young (i.e., never been promoted) and holds
     * young-arena String keys, those keys went un-forwarded — the
     * Map's slots kept stale from-space addresses, content vanished
     * on the next swap, and `containsKey` started returning false
     * positives because empty-len cached lookups on dead memory
     * silently succeeded. Pushing for any non-NULL header covers all
     * cases; the BLACK-colour dedup keeps the queue O(reachable). */
    header = osty_gc_find_header(payload);
    if (header != NULL) {
        osty_gc_cheney_old_stack_push(header);
    }
}

void osty_gc_mark_slot_v1(void *slot_addr) {
    /* Only reachable inside `osty_gc_collect_now_with_stack_roots`,
     * which runs under `osty_gc_lock` (see safepoint / debug collect).
     * No additional lock required. */
    if (osty_gc_trace_slot_mode_current == OSTY_GC_TRACE_SLOT_MODE_REMAP) {
        osty_gc_remap_slot(slot_addr);
        return;
    }
    if (osty_gc_trace_slot_mode_current == OSTY_GC_TRACE_SLOT_MODE_CHENEY) {
        osty_gc_cheney_slot(slot_addr);
        return;
    }
    osty_gc_mark_root_slot(slot_addr);
}

static osty_gc_header *osty_gc_root_binding_header(void *root) {
    osty_gc_header *header = osty_gc_find_header_or_forwarded(root);
    if (header == NULL && root != NULL) {
        // LLVM locals root managed values by passing the address of the
        // stack slot that stores the payload. Runtime helpers sometimes
        // root the payload directly. Accept both shapes here so GC-pin
        // semantics stay stable across generated and hand-written paths.
        void *payload = NULL;
        memcpy(&payload, root, sizeof(payload));
        header = osty_gc_find_header_or_forwarded(payload);
    }
    return header;
}

/* Phase E follow-up: auto-promote a young payload before binding
 * so root_count lands on the persistent OLD header. Two shapes
 * accepted (matching `osty_gc_root_binding_header`):
 *   1. `root` is a payload — promote in place, return new addr.
 *   2. `root` is a stack slot whose first 8 bytes are a payload
 *      (LLVM convention) — deref, promote, rewrite the slot.
 * The slot-rewriting case is critical: without it the LLVM-emitted
 * release call later hits `osty_gc_root_binding_header` and reads
 * the same slot to find a matching header. If the slot still
 * pointed at the dead young payload, find_header would return the
 * read-only shim, root_count writes from bind would have gone
 * nowhere, and release would underflow. */
static void *osty_gc_root_bind_promote_if_young(void *root) {
    if (root == NULL) {
        return root;
    }
    if (osty_gc_arena_is_young_page(root)) {
        return osty_gc_promote_young_to_old(root);
    }
    /* Slot-addr path: only chase if root isn't already a recognised
     * managed payload (otherwise dereferencing reads the payload's
     * first 8 bytes as if they were a child pointer). */
    if (osty_gc_find_header(root) != NULL) {
        return root;
    }
    void *payload = NULL;
    memcpy(&payload, root, sizeof(payload));
    if (payload != NULL && osty_gc_arena_is_young_page(payload)) {
        void *promoted = osty_gc_promote_young_to_old(payload);
        if (promoted != payload) {
            memcpy(root, &promoted, sizeof(promoted));
        }
    }
    return root;
}

void osty_gc_root_bind_v1(void *root) {
    osty_gc_header *header;
    /* Phase 7 step 2 (extended in Phase E follow-up): a young
     * payload can't carry a persistent root_count, so binding
     * promotes it to OLD first. Done outside the lock since
     * `osty_gc_allocate_managed` runs its own acquire/release
     * internally. */
    root = osty_gc_root_bind_promote_if_young(root);
    osty_gc_acquire();
    header = osty_gc_root_binding_header(root);
    if (header == NULL) {
        osty_gc_release();
        return;
    }
    if (header->root_count == INT32_MAX) {
        osty_gc_release();
        osty_rt_abort("GC root count overflow");
    }
    header->root_count += 1;
    osty_gc_release();
}

void osty_gc_root_release_v1(void *root) {
    osty_gc_header *header;
    osty_gc_acquire();
    header = osty_gc_root_binding_header(root);
    if (header == NULL) {
        osty_gc_release();
        return;
    }
    if (header->root_count <= 0) {
        osty_gc_release();
        osty_rt_abort("GC root release underflow");
    }
    header->root_count -= 1;
    osty_gc_release();
}

void osty_gc_pin_v1(void *root) {
    osty_gc_header *header;
    /* Phase 7 step 2 (extended in Phase E follow-up): pin
     * auto-promotes young payloads (and slot-addr-to-young) to
     * OLD so pin_count survives the next minor cycle. Same
     * promote helper as `root_bind_v1` for consistency. */
    root = osty_gc_root_bind_promote_if_young(root);
    osty_gc_acquire();
    header = osty_gc_find_header_or_forwarded(root);
    if (header == NULL) {
        osty_gc_release();
        return;
    }
    if (header->pin_count == INT32_MAX) {
        osty_gc_release();
        osty_rt_abort("GC pin count overflow");
    }
    header->pin_count += 1;
    osty_gc_note_pin_acquire(header);
    osty_gc_release();
}

void osty_gc_unpin_v1(void *root) {
    osty_gc_header *header;
    osty_gc_acquire();
    header = osty_gc_find_header_or_forwarded(root);
    if (header == NULL) {
        osty_gc_release();
        return;
    }
    if (header->pin_count <= 0) {
        osty_gc_release();
        osty_rt_abort("GC pin release underflow");
    }
    header->pin_count -= 1;
    osty_gc_note_pin_release(header);
    osty_gc_release();
}

static void osty_gc_global_roots_grow(void) {
    int64_t new_cap;
    void **new_slots;

    new_cap = osty_gc_global_root_cap == 0 ? 8 : osty_gc_global_root_cap * 2;
    new_slots = (void **)realloc(osty_gc_global_root_slots, (size_t)new_cap * sizeof(void *));
    if (new_slots == NULL) {
        osty_rt_abort("out of memory (global roots)");
    }
    osty_gc_global_root_slots = new_slots;
    osty_gc_global_root_cap = new_cap;
}

void osty_gc_global_root_register_v1(void *slot) {
    if (slot == NULL) {
        return;
    }
    if (osty_gc_global_root_count == osty_gc_global_root_cap) {
        osty_gc_global_roots_grow();
    }
    osty_gc_global_root_slots[osty_gc_global_root_count] = slot;
    osty_gc_global_root_count += 1;
}

void osty_gc_global_root_unregister_v1(void *slot) {
    int64_t i;

    if (slot == NULL) {
        return;
    }
    for (i = osty_gc_global_root_count - 1; i >= 0; i--) {
        if (osty_gc_global_root_slots[i] == slot) {
            memmove(&osty_gc_global_root_slots[i], &osty_gc_global_root_slots[i + 1],
                    (size_t)(osty_gc_global_root_count - i - 1) * sizeof(void *));
            osty_gc_global_root_count -= 1;
            return;
        }
    }
}

/* Forward declaration — definition ships with the scheduler block
 * further down. Kept here so `osty_gc_safepoint_v1` (above the
 * scheduler) can invoke the preemption hook. Phase 3 step 2 passes
 * the caller's root slots through so a collector-induced park can
 * publish them for cross-thread scan. */
static void osty_rt_sched_preempt_observe(void *const *root_slots,
                                          int64_t root_slot_count);

void osty_gc_safepoint_v1(int64_t safepoint_id, void *const *root_slots, int64_t root_slot_count) {
    /* Phase 3 cooperative preemption: every safepoint participates in
     * scheduler-level yield. Does a handful of relaxed atomic loads
     * and bails on the common case (no cancel, no collector stop, no
     * starved queue). The cost is dominated by the existing safepoint
     * kind decode + allocation check below, so the added cycles are
     * in the noise for programs without pool saturation. */
    osty_rt_sched_preempt_observe(root_slots, root_slot_count);

    /* Phase A5: decode the kind from the high byte. Legacy callers pass
     * pure serial ids (kind byte == 0 == UNSPECIFIED) so the dispatch is
     * backwards compatible. Unknown kinds fall through to UNSPECIFIED
     * rather than aborting — the kind byte is advisory, not a contract. */
    uint64_t raw_id = (uint64_t)safepoint_id;
    int kind = (int)((raw_id >> 56) & 0xffu);
    if (kind < 0 || kind >= OSTY_GC_SAFEPOINT_KIND_COUNT) {
        kind = OSTY_GC_SAFEPOINT_KIND_UNSPECIFIED;
    }
    osty_gc_safepoint_counts_by_kind[kind] += 1;
    if (root_slot_count < 0) {
        osty_rt_abort("negative safepoint root slot count");
    }
    if (root_slot_count > OSTY_GC_SAFEPOINT_MAX_ROOTS) {
        /* Phase A6: lowering emitted a frame whose root slot array
         * exceeds the runtime cap. Abort with a message that names
         * both the emitted count and the cap so the generator can be
         * audited. The id byte may help narrow the site (ENTRY vs
         * LOOP vs CALL). */
        char msg[160];
        snprintf(msg, sizeof(msg),
                 "safepoint root slot count %lld exceeds cap %lld (kind=%d)",
                 (long long)root_slot_count,
                 (long long)OSTY_GC_SAFEPOINT_MAX_ROOTS,
                 kind);
        osty_rt_abort(msg);
    }
    if (root_slot_count > osty_gc_safepoint_max_roots_seen) {
        osty_gc_safepoint_max_roots_seen = root_slot_count;
    }
    if (!osty_gc_safepoint_stress_enabled_now() && !osty_gc_collection_requested) {
        return;
    }
    /* While worker threads are live we can only see the current stack's
     * roots. A naive collection here would free objects that sibling
     * threads still reference. Defer until workers drain; a concurrent
     * collector is Phase 3. */
    osty_gc_acquire();
    if (osty_rt_concurrent_workers_load() > 0) {
        osty_gc_release();
        return;
    }
    osty_gc_collect_now_with_stack_roots(root_slots, root_slot_count);
    osty_gc_release();
}

/* Shadow-stack push: register the current frame's root chunk so the
 * GC can see it when a callee triggers a safepoint. The frame itself is
 * an alloca'd `osty_gc_root_frame` in the caller's stack — the linked
 * list lives off thread-local storage. */
void osty_gc_root_frame_enter_v1(struct osty_gc_root_frame *frame, void *const *slots, int64_t count) {
    if (frame == NULL) {
        return;
    }
    frame->prev = osty_gc_root_chain_top;
    frame->slots = slots;
    frame->count = count;
    osty_gc_root_chain_top = frame;
}

/* Shadow-stack pop: walk back to `frame->prev`. Defensive — if some
 * exotic flow pops out of order we don't underflow, just no-op. The
 * chain is thread-local so no locking required. */
void osty_gc_root_frame_leave_v1(struct osty_gc_root_frame *frame) {
    if (frame == NULL) {
        return;
    }
    if (osty_gc_root_chain_top == frame) {
        osty_gc_root_chain_top = frame->prev;
        return;
    }
    /* Out-of-order pop. Try to splice frame out of the chain. */
    struct osty_gc_root_frame *cur = osty_gc_root_chain_top;
    while (cur != NULL && cur->prev != frame) {
        cur = cur->prev;
    }
    if (cur != NULL) {
        cur->prev = frame->prev;
    }
}

void osty_gc_debug_collect(void) {
    osty_gc_acquire();
    if (osty_rt_concurrent_workers_load() > 0) {
        /* Same cross-thread root safety gate as safepoint_v1. */
        osty_gc_release();
        return;
    }
    osty_gc_collect_now();
    osty_gc_release();
}

/* Phase B test-access entry points. `debug_collect` above stays a
 * forced-major call so existing Phase A tests (which assume "one
 * collection frees every unrooted object") keep their semantics.
 * `debug_collect_minor` / `debug_collect_major` let Phase B tests pin
 * the tier explicitly regardless of pressure state. */
void osty_gc_debug_collect_minor(void) {
    osty_gc_acquire();
    if (osty_rt_concurrent_workers_load() > 0) {
        osty_gc_release();
        return;
    }
    if (osty_gc_tinytag_young_now()) {
        osty_gc_collect_minor_cheney_with_stack_roots(NULL, 0);
    }
    osty_gc_collect_minor_with_stack_roots(NULL, 0);
    osty_gc_release();
}

void osty_gc_debug_collect_major(void) {
    osty_gc_acquire();
    if (osty_rt_concurrent_workers_load() > 0) {
        osty_gc_release();
        return;
    }
    if (osty_gc_tinytag_young_now()) {
        osty_gc_collect_minor_cheney_with_stack_roots(NULL, 0);
    }
    osty_gc_collect_major_with_stack_roots(NULL, 0);
    osty_gc_release();
}

/* Phase B tier / generation accessors. */

int64_t osty_gc_debug_minor_count(void) {
    return osty_gc_minor_count;
}

int64_t osty_gc_debug_major_count(void) {
    return osty_gc_major_count;
}

int64_t osty_gc_debug_minor_nanos_total(void) {
    return osty_gc_minor_nanos_total;
}

int64_t osty_gc_debug_major_nanos_total(void) {
    return osty_gc_major_nanos_total;
}

int64_t osty_gc_debug_young_count(void) {
    return osty_gc_young_count;
}

int64_t osty_gc_debug_young_bytes(void) {
    return osty_gc_young_bytes;
}

int64_t osty_gc_debug_old_count(void) {
    return osty_gc_old_count;
}

int64_t osty_gc_debug_old_bytes(void) {
    return osty_gc_old_bytes;
}

int64_t osty_gc_debug_promoted_count_total(void) {
    return osty_gc_promoted_count_total;
}

int64_t osty_gc_debug_promoted_bytes_total(void) {
    return osty_gc_promoted_bytes_total;
}

int64_t osty_gc_debug_nursery_limit_bytes(void) {
    return osty_gc_nursery_limit_now();
}

/* Phase C incremental / tri-colour accessors. */

int64_t osty_gc_debug_state(void) {
    return (int64_t)osty_gc_state;
}

int64_t osty_gc_debug_mark_stack_count(void) {
    return osty_gc_mark_stack_count;
}

int64_t osty_gc_debug_incremental_steps_total(void) {
    return osty_gc_incremental_steps_total;
}

int64_t osty_gc_debug_incremental_work_total(void) {
    return osty_gc_incremental_work_total;
}

int64_t osty_gc_debug_satb_barrier_greyed_total(void) {
    return osty_gc_satb_barrier_greyed_total;
}

/* Phase 0a/0c background marker accessors. Tests assert these advance
 * when `OSTY_GC_BG_MARKER=1` is enabled and a cycle runs. Counters
 * are atomically updated by the marker threads (potentially N>=2)
 * so reads use ACQUIRE to pair with the writers' RELEASE/RELAXED
 * adds. Per-worker counters are single-writer so a plain load is
 * fine. */
int64_t osty_gc_debug_bg_marker_started(void) {
    return osty_gc_bg_marker_started ? 1 : 0;
}

int64_t osty_gc_debug_bg_marker_workers(void) {
    return (int64_t)osty_gc_bg_marker_worker_count;
}

int64_t osty_gc_debug_bg_marker_drained_total(void) {
    return __atomic_load_n(&osty_gc_bg_marker_drained_total,
                           __ATOMIC_ACQUIRE);
}

int64_t osty_gc_debug_bg_marker_loops_total(void) {
    return __atomic_load_n(&osty_gc_bg_marker_loops_total,
                           __ATOMIC_ACQUIRE);
}

int64_t osty_gc_debug_bg_marker_naps_total(void) {
    return __atomic_load_n(&osty_gc_bg_marker_naps_total,
                           __ATOMIC_ACQUIRE);
}

int64_t osty_gc_debug_bg_marker_kicks_total(void) {
    return __atomic_load_n(&osty_gc_bg_marker_kicks_total,
                           __ATOMIC_ACQUIRE);
}

int64_t osty_gc_debug_bg_marker_active(void) {
    return __atomic_load_n(&osty_gc_bg_marker_active, __ATOMIC_ACQUIRE) ? 1 : 0;
}

/* Per-worker drained — index 0..workers-1. Returns -1 for OOB. */
int64_t osty_gc_debug_bg_marker_per_worker_drained(int64_t worker_id) {
    if (worker_id < 0 || worker_id >= OSTY_GC_BG_MARKER_MAX_WORKERS) {
        return -1;
    }
    return osty_gc_bg_marker_per_worker_drained[(int)worker_id];
}

/* Phase 0e concurrent-sweep accessors. `swept_total` is what the bg
 * thread reclaimed (atomic add); `sweep_steps_total` counts every
 * `_sweep_step` call (bg + finish-side); `sweep_reclaim_done` mirrors
 * the flag the bg thread sets when its cursor hits NULL — tests use
 * it to assert sweep finished concurrently with the mutator rather
 * than during the finish handshake. */
int64_t osty_gc_debug_bg_marker_swept_total(void) {
    return __atomic_load_n(&osty_gc_bg_marker_swept_total,
                           __ATOMIC_ACQUIRE);
}

int64_t osty_gc_debug_sweep_steps_total(void) {
    return osty_gc_sweep_steps_total;
}

int64_t osty_gc_debug_sweep_reclaim_done(void) {
    return osty_gc_sweep_reclaim_done ? 1 : 0;
}

/* Phase 0e' TLS local mark stack accessors. `local_mark_pushes_total`
 * is the count of tracer pushes that landed in the TLS buffer (vs.
 * the central stack). `local_mark_overflow_total` is the count that
 * spilled to central because the local cap was hit — non-zero on
 * deep object graphs where the trace recursion outruns the local
 * 256-slot buffer. Tests use the ratio to confirm Phase 0e' is
 * actually routing work to the local path. */
int64_t osty_gc_debug_local_mark_pushes_total(void) {
    return __atomic_load_n(&osty_gc_local_mark_pushes_total,
                           __ATOMIC_ACQUIRE);
}

int64_t osty_gc_debug_local_mark_overflow_total(void) {
    return __atomic_load_n(&osty_gc_local_mark_overflow_total,
                           __ATOMIC_ACQUIRE);
}

/* Phase 0f index-resize accessors. `pregrow_total` counts cycle
 * starts that proactively grew the table; `grow_deferred_total`
 * counts insert sites that hit the load threshold inside a cycle
 * and set the pending flag instead of growing. A test that
 * exercises heavy in-cycle alloc can assert both move (pre-grow
 * fired at start, defer absorbed mid-cycle pressure). */
int64_t osty_gc_debug_index_pregrow_total(void) {
    return osty_gc_index_pregrow_total;
}

int64_t osty_gc_debug_index_grow_deferred_total(void) {
    return osty_gc_index_grow_deferred_total;
}

/* Phase 0g atomic-color CAS counters. `attempts` increments on
 * every WHITE→GREY transition site (mark_header + SATB pre_write),
 * `failures` only when another thread won the race. With the
 * trace lock still in place (Phase 0g) failures stay at 0; once
 * Phase 0h drops the lock around dispatch_trace, contention shows
 * up here and the ratio (failures/attempts) becomes the lock-free
 * trace overhead signal. */
int64_t osty_gc_debug_mark_cas_attempts_total(void) {
    return osty_gc_mark_cas_attempts_total;
}

int64_t osty_gc_debug_mark_cas_failures_total(void) {
    return osty_gc_mark_cas_failures_total;
}

/* Phase 0h trace-dispatch counters. `lockfree_trace_total` is the
 * count of `dispatch_trace` calls that ran with `osty_gc_lock`
 * released (closure_env, generic enum); `locked_trace_total` is the
 * count that kept the lock (list/map/set/channel). The ratio is the
 * fraction of trace work newly able to run concurrently with
 * mutator activity. */
int64_t osty_gc_debug_lockfree_trace_total(void) {
    return __atomic_load_n(&osty_gc_lockfree_trace_total,
                           __ATOMIC_ACQUIRE);
}

int64_t osty_gc_debug_locked_trace_total(void) {
    return __atomic_load_n(&osty_gc_locked_trace_total,
                           __ATOMIC_ACQUIRE);
}

int64_t osty_gc_debug_color_of(void *payload) {
    osty_gc_header *header = osty_gc_find_header(payload);
    if (header == NULL) {
        return -1;
    }
    return (int64_t)header->color;
}

/* Phase C3 mutator assist accessors. */

int64_t osty_gc_debug_mutator_assist_work_total(void) {
    return osty_gc_mutator_assist_work_total;
}

int64_t osty_gc_debug_mutator_assist_calls_total(void) {
    return osty_gc_mutator_assist_calls_total;
}

int64_t osty_gc_debug_assist_bytes_per_unit(void) {
    return osty_gc_assist_bytes_per_unit_now();
}

int64_t osty_gc_debug_promote_age(void) {
    return osty_gc_promote_age_now();
}

/* Phase 2 dispatch counters — used by the test that proves the descriptor
 * route fires on registered kinds. Header-fallback counter exposes the
 * GENERIC path so tests can also confirm the residual fallback still
 * works while Phase 4 removes the per-header trace/destroy fields. */
int64_t osty_gc_debug_dispatch_via_descriptor_total(void) {
    return osty_gc_dispatch_via_descriptor_total;
}

int64_t osty_gc_debug_dispatch_via_header_total(void) {
    return osty_gc_dispatch_via_header_total;
}

/* Phase 3 scaffolding accessors. The flag is opt-in via
 * `OSTY_GC_TINYTAG_YOUNG=1`; today it gates nothing observable but the
 * hook predicate exists so the eventual Phase 5 cutover is a one-line
 * change inside `osty_gc_arena_is_young_page`. */
int64_t osty_gc_debug_tinytag_young_enabled(void) {
    return osty_gc_tinytag_young_now() ? 1 : 0;
}

int64_t osty_gc_debug_arena_is_young_page(void *payload) {
    return osty_gc_arena_is_young_page(payload) ? 1 : 0;
}

/* Phase 5 entry points + counters. The young arena is dormant in
 * production until Phase 6 starts routing allocs through it; tests
 * exercise it directly via these accessors so the infrastructure
 * (mmap, micro-header layout, find_header shim) can be validated
 * before the cutover. */
void *osty_gc_debug_allocate_young(int64_t byte_size, int64_t object_kind) {
    if (byte_size <= 0) {
        return NULL;
    }
    return osty_gc_allocate_young((size_t)byte_size, object_kind);
}

int64_t osty_gc_debug_young_alloc_count_total(void) {
    return osty_gc_young_alloc_count_total;
}

int64_t osty_gc_debug_young_alloc_bytes_total(void) {
    return osty_gc_young_alloc_bytes_total;
}

/* Returns a snapshot of the shim's `object_kind` / `byte_size` /
 * `generation` after reconstruction so tests can confirm the
 * micro-header's data flows through correctly. The shim is per-thread
 * scratch — the test must read it before any other find_header call
 * overwrites it. */
int64_t osty_gc_debug_young_header_object_kind(void *payload) {
    osty_gc_header *header = osty_gc_reconstruct_young_header(payload);
    return header->object_kind;
}

int64_t osty_gc_debug_young_header_byte_size(void *payload) {
    osty_gc_header *header = osty_gc_reconstruct_young_header(payload);
    return header->byte_size;
}

int64_t osty_gc_debug_young_header_generation(void *payload) {
    osty_gc_header *header = osty_gc_reconstruct_young_header(payload);
    return (int64_t)header->generation;
}

/* Phase 6 step 1 accessors. Tests exercise the Cheney forward/swap
 * mechanic directly without needing a minor GC entry point — that
 * comes in step 3 once the descriptor tracers learn the Cheney
 * action and stack roots feed into the scan. */
void *osty_gc_debug_cheney_forward(void *from_payload) {
    return osty_gc_cheney_forward(from_payload);
}

void osty_gc_debug_cheney_swap_arenas(void) {
    osty_gc_cheney_swap_arenas();
}

int64_t osty_gc_debug_arena_is_young_from_page(void *payload) {
    return osty_gc_arena_is_young_from_page(payload) ? 1 : 0;
}

int64_t osty_gc_debug_arena_is_young_to_page(void *payload) {
    return osty_gc_arena_is_young_to_page(payload) ? 1 : 0;
}

int64_t osty_gc_debug_young_cheney_forwarded_count_total(void) {
    return osty_gc_young_cheney_forwarded_count_total;
}

int64_t osty_gc_debug_young_cheney_forwarded_bytes_total(void) {
    return osty_gc_young_cheney_forwarded_bytes_total;
}

int64_t osty_gc_debug_young_cheney_swap_count_total(void) {
    return osty_gc_young_cheney_swap_count_total;
}

int64_t osty_gc_debug_young_cheney_dead_lists_freed_total(void) {
    return osty_gc_young_cheney_dead_lists_freed_total;
}

int64_t osty_gc_debug_young_cheney_dead_sets_freed_total(void) {
    return osty_gc_young_cheney_dead_sets_freed_total;
}

int64_t osty_gc_debug_young_cheney_dead_maps_freed_total(void) {
    return osty_gc_young_cheney_dead_maps_freed_total;
}

int64_t osty_gc_debug_young_cheney_dead_buffer_bytes_freed_total(void) {
    return osty_gc_young_cheney_dead_buffer_bytes_freed_total;
}

/* Decode the forwarding tag for testing. Returns the tag value
 * (UNFORWARDED=0, FORWARDED=1, PROMOTED=2). */
int64_t osty_gc_debug_young_forward_tag(void *from_payload) {
    if (!osty_gc_arena_is_young_page(from_payload)) {
        return -1;
    }
    osty_gc_micro_header *micro =
        osty_gc_micro_header_for_payload(from_payload);
    return (int64_t)(micro->forward_or_meta & OSTY_GC_FORWARD_TAG_MASK);
}

/* Phase 6 step 2: scoped CHENEY-mode entry/exit for tests + the
 * eventual minor GC scan loop. The mode is global state read by
 * `osty_gc_mark_slot_v1`, so callers must always pair set+restore in
 * a stack-discipline fashion. The previous-mode return lets nested
 * calls (e.g. trace fns that call other tracers) compose. */
int64_t osty_gc_debug_set_trace_slot_mode(int64_t mode) {
    osty_gc_trace_slot_mode previous = osty_gc_trace_slot_mode_current;
    if (mode == OSTY_GC_TRACE_SLOT_MODE_MARK ||
        mode == OSTY_GC_TRACE_SLOT_MODE_REMAP ||
        mode == OSTY_GC_TRACE_SLOT_MODE_CHENEY) {
        osty_gc_trace_slot_mode_current = (osty_gc_trace_slot_mode)mode;
    }
    return (int64_t)previous;
}

int64_t osty_gc_debug_trace_slot_mode_current(void) {
    return (int64_t)osty_gc_trace_slot_mode_current;
}

/* Phase 6 step 3: minor GC Cheney entry. Tests pass the stack-root
 * vector directly so we can exercise root forwarding + scan loop
 * + arena swap end-to-end without involving the safepoint dispatcher.
 * Phase 7 will fold this into the existing minor selector under the
 * tinytag-young feature flag. */
void osty_gc_debug_collect_minor_cheney(void *const *root_slots,
                                        int64_t root_slot_count) {
    osty_gc_collect_minor_cheney_with_stack_roots(root_slots,
                                                  root_slot_count);
}

int64_t osty_gc_debug_young_cheney_scanned_count_total(void) {
    return osty_gc_young_cheney_scanned_count_total;
}

/* Phase 7 step 1 accessors. The eligibility predicate gates every
 * young alloc decision, so a test that exercises the four rejection
 * reasons (flag off, GENERIC, has-destroy, oversized) keeps those
 * paths from rotting independently. The `_if_eligible` entry returns
 * NULL on rejection and a payload pointer on accept — same contract
 * Phase 8's allocator routing will rely on. */
int64_t osty_gc_debug_young_eligible(int64_t byte_size, int64_t object_kind) {
    if (byte_size < 0) {
        return 0;
    }
    /* Debug entry assumes the GENERIC NONE pattern (NULL trace +
     * NULL destroy) — the per-instance trace/destroy aren't part of
     * this filter's surface, and NONE is the only GENERIC pattern
     * the eligibility filter can admit. */
    return osty_gc_young_eligible(object_kind, (size_t)byte_size, NULL, NULL)
               ? 1 : 0;
}

void *osty_gc_debug_allocate_young_if_eligible(int64_t byte_size,
                                               int64_t object_kind) {
    if (byte_size < 0) {
        return NULL;
    }
    return osty_gc_allocate_young_if_eligible((size_t)byte_size, object_kind,
                                              NULL, NULL);
}

/* Phase 7 step 2 accessors. The promote path is exercised via the
 * public `osty.gc.pin_v1` / `osty.gc.root_bind_v1` ABIs, but the
 * counter readouts let tests confirm exactly one promote fired and
 * the byte-size attribution lines up. */
int64_t osty_gc_debug_young_promoted_to_old_count_total(void) {
    return osty_gc_young_promoted_to_old_count_total;
}

int64_t osty_gc_debug_young_promoted_to_old_bytes_total(void) {
    return osty_gc_young_promoted_to_old_bytes_total;
}

void *osty_gc_debug_promote_young_to_old(void *young_payload) {
    return osty_gc_promote_young_to_old(young_payload);
}

/* Expose the generation tag of a specific payload so tests can assert
 * on promotion semantics directly. Returns -1 if the payload is not
 * managed. */
int64_t osty_gc_debug_generation_of(void *payload) {
    osty_gc_header *header = osty_gc_find_header_or_forwarded(payload);
    if (header == NULL) {
        return -1;
    }
    return (int64_t)header->generation;
}

int64_t osty_gc_debug_age_of(void *payload) {
    osty_gc_header *header = osty_gc_find_header_or_forwarded(payload);
    if (header == NULL) {
        return -1;
    }
    return (int64_t)header->age;
}

int64_t osty_gc_debug_live_count(void) {
    return osty_gc_live_count;
}

/* Phase 4: header-size invariant accessor. Tests pin the size to the
 * post-shrink value so a future regression that reintroduces the
 * trace/destroy fn-pointer fields shows up immediately. */
int64_t osty_gc_debug_header_size_bytes(void) {
    return (int64_t)sizeof(osty_gc_header);
}

int64_t osty_gc_debug_generic_pattern_of(void *payload) {
    osty_gc_header *header = osty_gc_find_header(payload);
    if (header == NULL) {
        return -1;
    }
    return (int64_t)header->generic_pattern;
}

int64_t osty_gc_debug_collection_count(void) {
    return osty_gc_collection_count;
}

int64_t osty_gc_debug_live_bytes(void) {
    return osty_gc_live_bytes;
}

int64_t osty_gc_debug_pre_write_count(void) {
    return osty_gc_pre_write_count;
}

int64_t osty_gc_debug_pre_write_managed_count(void) {
    return osty_gc_pre_write_managed_count;
}

int64_t osty_gc_debug_post_write_count(void) {
    return osty_gc_post_write_count;
}

int64_t osty_gc_debug_post_write_managed_count(void) {
    return osty_gc_post_write_managed_count;
}

int64_t osty_gc_debug_load_count(void) {
    return osty_gc_load_count;
}

int64_t osty_gc_debug_load_managed_count(void) {
    return osty_gc_load_managed_count;
}

int64_t osty_gc_debug_load_forwarded_count(void) {
    return osty_gc_load_forwarded_count;
}

int64_t osty_gc_debug_forwarding_count(void) {
    return osty_gc_forwarding_count;
}

int64_t osty_gc_debug_pinned_count(void) {
    return osty_gc_pinned_count;
}

int64_t osty_gc_debug_pinned_bytes(void) {
    return osty_gc_pinned_bytes;
}

int64_t osty_gc_debug_pin_count_of(void *payload) {
    osty_gc_header *header = osty_gc_find_header_or_forwarded(payload);
    if (header == NULL) {
        return -1;
    }
    return header->pin_count;
}

int64_t osty_gc_debug_compaction_count_total(void) {
    return osty_gc_compaction_count_total;
}

int64_t osty_gc_debug_forwarded_objects_last(void) {
    return osty_gc_forwarded_objects_last;
}

int64_t osty_gc_debug_forwarded_bytes_last(void) {
    return osty_gc_forwarded_bytes_last;
}

int64_t osty_gc_debug_global_root_count(void) {
    return osty_gc_global_root_count;
}

int64_t osty_gc_debug_satb_log_count(void) {
    return osty_gc_satb_log_count;
}

int64_t osty_gc_debug_remembered_edge_count(void) {
    return osty_gc_remembered_edge_count;
}

int64_t osty_gc_debug_cards_dirtied_total(void) {
    return osty_gc_cards_dirtied_total;
}

int64_t osty_gc_debug_cards_scanned_total(void) {
    return osty_gc_cards_scanned_total;
}

int64_t osty_gc_debug_old_dirty_count(void) {
    return osty_gc_dirty_old_count;
}

int osty_gc_debug_remembered_edge_contains(void *owner, void *value) {
    return osty_gc_remembered_edges_contains(owner, value) ? 1 : 0;
}

int osty_gc_debug_satb_log_contains(void *payload) {
    int64_t i;
    for (i = 0; i < osty_gc_satb_log_count; i++) {
        if (osty_gc_satb_log[i] == payload) {
            return 1;
        }
    }
    return 0;
}

/* Phase A2 lifetime totals (RUNTIME_GC_DELTA §9.3). */

int64_t osty_gc_debug_allocated_bytes_total(void) {
    return osty_gc_allocated_bytes_total;
}

int64_t osty_gc_debug_swept_count_total(void) {
    return osty_gc_swept_count_total;
}

int64_t osty_gc_debug_swept_bytes_total(void) {
    return osty_gc_swept_bytes_total;
}

int64_t osty_gc_debug_free_list_count(void) {
    return osty_gc_free_list_count;
}

int64_t osty_gc_debug_free_list_bytes(void) {
    return osty_gc_free_list_bytes;
}

int64_t osty_gc_debug_free_list_reused_count_total(void) {
    return osty_gc_free_list_reused_count_total;
}

int64_t osty_gc_debug_free_list_reused_bytes_total(void) {
    return osty_gc_free_list_reused_bytes_total;
}

int64_t osty_gc_debug_bump_block_bytes(void) {
    return OSTY_GC_BUMP_BLOCK_BYTES;
}

int64_t osty_gc_debug_bump_block_count(void) {
    return osty_gc_bump_block_count;
}

int64_t osty_gc_debug_bump_block_bytes_total(void) {
    return osty_gc_bump_block_bytes_total;
}

int64_t osty_gc_debug_bump_alloc_count_total(void) {
    return osty_gc_bump_alloc_count_total;
}

int64_t osty_gc_debug_bump_alloc_bytes_total(void) {
    return osty_gc_bump_alloc_bytes_total;
}

int64_t osty_gc_debug_tlab_refill_count_total(void) {
    return osty_gc_tlab_refill_count_total;
}

int64_t osty_gc_debug_bump_recycled_block_count_total(void) {
    return osty_gc_bump_recycled_block_count_total;
}

int64_t osty_gc_debug_bump_recycled_bytes_total(void) {
    return osty_gc_bump_recycled_bytes_total;
}

int64_t osty_gc_debug_survivor_bump_block_count(void) {
    return osty_gc_survivor_bump_block_count;
}

int64_t osty_gc_debug_survivor_bump_block_bytes_total(void) {
    return osty_gc_survivor_bump_block_bytes_total;
}

int64_t osty_gc_debug_survivor_bump_alloc_count_total(void) {
    return osty_gc_survivor_bump_alloc_count_total;
}

int64_t osty_gc_debug_survivor_bump_alloc_bytes_total(void) {
    return osty_gc_survivor_bump_alloc_bytes_total;
}

int64_t osty_gc_debug_survivor_tlab_refill_count_total(void) {
    return osty_gc_survivor_tlab_refill_count_total;
}

int64_t osty_gc_debug_survivor_bump_recycled_block_count_total(void) {
    return osty_gc_survivor_bump_recycled_block_count_total;
}

int64_t osty_gc_debug_survivor_bump_recycled_bytes_total(void) {
    return osty_gc_survivor_bump_recycled_bytes_total;
}

int64_t osty_gc_debug_old_bump_block_count(void) {
    return osty_gc_old_bump_block_count;
}

int64_t osty_gc_debug_old_bump_block_bytes_total(void) {
    return osty_gc_old_bump_block_bytes_total;
}

int64_t osty_gc_debug_old_bump_alloc_count_total(void) {
    return osty_gc_old_bump_alloc_count_total;
}

int64_t osty_gc_debug_old_bump_alloc_bytes_total(void) {
    return osty_gc_old_bump_alloc_bytes_total;
}

int64_t osty_gc_debug_old_tlab_refill_count_total(void) {
    return osty_gc_old_tlab_refill_count_total;
}

int64_t osty_gc_debug_old_bump_recycled_block_count_total(void) {
    return osty_gc_old_bump_recycled_block_count_total;
}

int64_t osty_gc_debug_old_bump_recycled_bytes_total(void) {
    return osty_gc_old_bump_recycled_bytes_total;
}

int64_t osty_gc_debug_pinned_bump_block_count(void) {
    return osty_gc_pinned_bump_block_count;
}

int64_t osty_gc_debug_pinned_bump_block_bytes_total(void) {
    return osty_gc_pinned_bump_block_bytes_total;
}

int64_t osty_gc_debug_pinned_bump_alloc_count_total(void) {
    return osty_gc_pinned_bump_alloc_count_total;
}

int64_t osty_gc_debug_pinned_bump_alloc_bytes_total(void) {
    return osty_gc_pinned_bump_alloc_bytes_total;
}

int64_t osty_gc_debug_pinned_tlab_refill_count_total(void) {
    return osty_gc_pinned_tlab_refill_count_total;
}

int64_t osty_gc_debug_pinned_bump_recycled_block_count_total(void) {
    return osty_gc_pinned_bump_recycled_block_count_total;
}

int64_t osty_gc_debug_pinned_bump_recycled_bytes_total(void) {
    return osty_gc_pinned_bump_recycled_bytes_total;
}

int64_t osty_gc_debug_humongous_threshold_bytes(void) {
    return OSTY_GC_HUMONGOUS_THRESHOLD_BYTES;
}

int64_t osty_gc_debug_humongous_alloc_count_total(void) {
    return osty_gc_humongous_alloc_count_total;
}

int64_t osty_gc_debug_humongous_alloc_bytes_total(void) {
    return osty_gc_humongous_alloc_bytes_total;
}

int64_t osty_gc_debug_humongous_swept_count_total(void) {
    return osty_gc_humongous_swept_count_total;
}

int64_t osty_gc_debug_humongous_swept_bytes_total(void) {
    return osty_gc_humongous_swept_bytes_total;
}

int64_t osty_gc_debug_allocated_since_collect(void) {
    return osty_gc_allocated_since_collect;
}

int64_t osty_gc_debug_pressure_limit_bytes(void) {
    return osty_gc_pressure_limit_now();
}

int64_t osty_gc_debug_mark_stack_max_depth(void) {
    return osty_gc_mark_stack_max_depth;
}

/* Phase A2 depth: collection timing accessors. Nanoseconds relative to
 * CLOCK_MONOTONIC — zero means "no measurement recorded" (either no
 * collections yet, or clock_gettime failed on the host). */

int64_t osty_gc_debug_collection_nanos_total(void) {
    return osty_gc_collection_nanos_total;
}

int64_t osty_gc_debug_collection_nanos_last(void) {
    return osty_gc_collection_nanos_last;
}

int64_t osty_gc_debug_collection_nanos_max(void) {
    return osty_gc_collection_nanos_max;
}

/* Phase A3 depth: hash index observability. Exposes capacity and
 * occupancy so tuning scripts can verify the probe sequences stay
 * short. `find_ops_total` counts lookups across the program — pairs
 * with `mark_stack_max_depth` to describe the mark pass's cost
 * profile. (The counter itself lives near the index state globals
 * because `osty_gc_find_header` bumps it.) */

int64_t osty_gc_debug_index_capacity(void) {
    return osty_gc_index_capacity;
}

int64_t osty_gc_debug_index_count(void) {
    return osty_gc_index_count;
}

int64_t osty_gc_debug_index_tombstones(void) {
    return osty_gc_index_tombstones;
}

int64_t osty_gc_debug_index_find_ops(void) {
    return osty_gc_index_find_ops_total;
}

/* Phase D observability. `stable_id` is the logical object identity
 * preserved across relocation; the forwarding table and pin counters
 * let tests assert that major collection actually evacuated movable
 * objects and skipped explicitly pinned ones. */
int64_t osty_gc_debug_stable_id(void *payload) {
    osty_gc_header *header;
    int64_t out = 0;

    osty_gc_acquire();
    header = osty_gc_find_header_or_forwarded(payload);
    if (header != NULL && header->stable_id <= INT64_MAX) {
        out = (int64_t)header->stable_id;
    }
    osty_gc_release();
    return out;
}

void *osty_gc_debug_payload_for_stable_id(int64_t stable_id) {
    osty_gc_header *header;
    void *payload = NULL;

    if (stable_id <= 0) {
        return NULL;
    }
    osty_gc_acquire();
    header = osty_gc_identity_lookup((uint64_t)stable_id);
    if (header != NULL) {
        payload = header->payload;
    }
    osty_gc_release();
    return payload;
}

/* Phase A5 per-kind safepoint counters. `kind` values map to the
 * OSTY_GC_SAFEPOINT_KIND_* constants; out-of-range queries return -1 so
 * callers can distinguish from a zero count. */
int64_t osty_gc_debug_safepoint_count_by_kind(int64_t kind) {
    if (kind < 0 || kind >= OSTY_GC_SAFEPOINT_KIND_COUNT) {
        return -1;
    }
    return osty_gc_safepoint_counts_by_kind[kind];
}

int64_t osty_gc_debug_safepoint_count_total(void) {
    int64_t total = 0;
    int i;
    for (i = 0; i < OSTY_GC_SAFEPOINT_KIND_COUNT; i++) {
        total += osty_gc_safepoint_counts_by_kind[i];
    }
    return total;
}

/* Phase A6 observability: largest root slot array seen since program
 * start. `osty_gc_debug_safepoint_max_roots_cap` returns the runtime
 * limit so tuning scripts can compute headroom. */
int64_t osty_gc_debug_safepoint_max_roots_seen(void) {
    return osty_gc_safepoint_max_roots_seen;
}

int64_t osty_gc_debug_safepoint_max_roots_cap(void) {
    return OSTY_GC_SAFEPOINT_MAX_ROOTS;
}

/* Aggregate snapshot. Fields mirror the individual debug_* accessors so that
 * test harnesses written against the scalar ABI keep working; the struct is
 * for callers that want one atomic read under the GC lock. Field order is
 * load bearing — clients declare a compatible struct and blit directly. */
typedef struct osty_gc_stats {
    int64_t collection_count;
    int64_t live_count;
    int64_t live_bytes;
    int64_t allocated_since_collect;
    int64_t allocated_bytes_total;
    int64_t swept_count_total;
    int64_t swept_bytes_total;
    int64_t pre_write_count;
    int64_t pre_write_managed_count;
    int64_t post_write_count;
    int64_t post_write_managed_count;
    int64_t load_count;
    int64_t load_managed_count;
    int64_t satb_log_count;
    int64_t remembered_edge_count;
    int64_t global_root_count;
    int64_t pressure_limit_bytes;
    int64_t mark_stack_max_depth;
    /* Phase A2 depth — collection timing. */
    int64_t collection_nanos_total;
    int64_t collection_nanos_last;
    int64_t collection_nanos_max;
    /* Phase A3 depth — hash index state. */
    int64_t index_capacity;
    int64_t index_count;
    int64_t index_tombstones;
    int64_t index_find_ops_total;
    /* Phase B — generational tiers and timing. */
    int64_t minor_count;
    int64_t major_count;
    int64_t minor_nanos_total;
    int64_t major_nanos_total;
    int64_t young_count;
    int64_t young_bytes;
    int64_t old_count;
    int64_t old_bytes;
    int64_t promoted_count_total;
    int64_t promoted_bytes_total;
    int64_t allocated_since_minor;
    int64_t nursery_limit_bytes;
    int64_t promote_age;
    /* Phase D / §6.5 — fragmentation instrumentation. All individual
     * counters already have scalar accessors (osty_gc_debug_free_list_*,
     * osty_gc_debug_bump_*, osty_gc_debug_humongous_*); consolidating
     * them into the stats snapshot lets a single atomic read capture
     * the whole fragmentation picture without taking the GC lock per
     * field. Fields grouped by tier so tuning scripts can compute
     * utilization ratios (alloc / block_bytes_total) and recycle rates
     * (recycled_bytes_total / swept_bytes_total) directly. */
    int64_t free_list_count;
    int64_t free_list_bytes;
    int64_t free_list_reused_count_total;
    int64_t free_list_reused_bytes_total;
    int64_t humongous_alloc_count_total;
    int64_t humongous_alloc_bytes_total;
    int64_t humongous_swept_count_total;
    int64_t humongous_swept_bytes_total;
    /* bump_block_count is a live gauge — sum of currently allocated
     * bump blocks across tiers. The per-tier statics decrement on
     * recycle, so this reflects "blocks in flight", not a cumulative
     * total. The remaining bump fields are monotonic totals (see
     * `*_total` suffix); they only grow. */
    int64_t bump_block_count;
    int64_t bump_block_bytes_total;
    int64_t bump_alloc_count_total;
    int64_t bump_alloc_bytes_total;
    int64_t bump_recycled_block_count_total;
    int64_t bump_recycled_bytes_total;
} osty_gc_stats;

void osty_gc_debug_stats(osty_gc_stats *out) {
    if (out == NULL) {
        return;
    }
    osty_gc_acquire();
    out->collection_count = osty_gc_collection_count;
    out->live_count = osty_gc_live_count;
    out->live_bytes = osty_gc_live_bytes;
    out->allocated_since_collect = osty_gc_allocated_since_collect;
    out->allocated_bytes_total = osty_gc_allocated_bytes_total;
    out->swept_count_total = osty_gc_swept_count_total;
    out->swept_bytes_total = osty_gc_swept_bytes_total;
    out->pre_write_count = osty_gc_pre_write_count;
    out->pre_write_managed_count = osty_gc_pre_write_managed_count;
    out->post_write_count = osty_gc_post_write_count;
    out->post_write_managed_count = osty_gc_post_write_managed_count;
    out->load_count = osty_gc_load_count;
    out->load_managed_count = osty_gc_load_managed_count;
    out->satb_log_count = osty_gc_satb_log_count;
    out->remembered_edge_count = osty_gc_remembered_edge_count;
    out->global_root_count = osty_gc_global_root_count;
    out->pressure_limit_bytes = osty_gc_pressure_limit_now();
    out->mark_stack_max_depth = osty_gc_mark_stack_max_depth;
    out->collection_nanos_total = osty_gc_collection_nanos_total;
    out->collection_nanos_last = osty_gc_collection_nanos_last;
    out->collection_nanos_max = osty_gc_collection_nanos_max;
    out->index_capacity = osty_gc_index_capacity;
    out->index_count = osty_gc_index_count;
    out->index_tombstones = osty_gc_index_tombstones;
    out->index_find_ops_total = osty_gc_index_find_ops_total;
    out->minor_count = osty_gc_minor_count;
    out->major_count = osty_gc_major_count;
    out->minor_nanos_total = osty_gc_minor_nanos_total;
    out->major_nanos_total = osty_gc_major_nanos_total;
    out->young_count = osty_gc_young_count;
    out->young_bytes = osty_gc_young_bytes;
    out->old_count = osty_gc_old_count;
    out->old_bytes = osty_gc_old_bytes;
    out->promoted_count_total = osty_gc_promoted_count_total;
    out->promoted_bytes_total = osty_gc_promoted_bytes_total;
    out->allocated_since_minor = osty_gc_allocated_since_minor;
    out->nursery_limit_bytes = osty_gc_nursery_limit_now();
    out->promote_age = osty_gc_promote_age_now();
    /* §6.5 fragmentation. Bump aggregates sum across tiers (young /
     * survivor / old / pinned) so a single utilization ratio covers
     * the whole bump population; callers that want per-tier breakdown
     * use the existing scalar accessors. */
    out->free_list_count = osty_gc_free_list_count;
    out->free_list_bytes = osty_gc_free_list_bytes;
    out->free_list_reused_count_total = osty_gc_free_list_reused_count_total;
    out->free_list_reused_bytes_total = osty_gc_free_list_reused_bytes_total;
    out->humongous_alloc_count_total = osty_gc_humongous_alloc_count_total;
    out->humongous_alloc_bytes_total = osty_gc_humongous_alloc_bytes_total;
    out->humongous_swept_count_total = osty_gc_humongous_swept_count_total;
    out->humongous_swept_bytes_total = osty_gc_humongous_swept_bytes_total;
    out->bump_block_count =
        osty_gc_bump_block_count +
        osty_gc_survivor_bump_block_count +
        osty_gc_old_bump_block_count +
        osty_gc_pinned_bump_block_count;
    out->bump_block_bytes_total =
        osty_gc_bump_block_bytes_total +
        osty_gc_survivor_bump_block_bytes_total +
        osty_gc_old_bump_block_bytes_total +
        osty_gc_pinned_bump_block_bytes_total;
    out->bump_alloc_count_total =
        osty_gc_bump_alloc_count_total +
        osty_gc_survivor_bump_alloc_count_total +
        osty_gc_old_bump_alloc_count_total +
        osty_gc_pinned_bump_alloc_count_total;
    out->bump_alloc_bytes_total =
        osty_gc_bump_alloc_bytes_total +
        osty_gc_survivor_bump_alloc_bytes_total +
        osty_gc_old_bump_alloc_bytes_total +
        osty_gc_pinned_bump_alloc_bytes_total;
    out->bump_recycled_block_count_total =
        osty_gc_bump_recycled_block_count_total +
        osty_gc_survivor_bump_recycled_block_count_total +
        osty_gc_old_bump_recycled_block_count_total +
        osty_gc_pinned_bump_recycled_block_count_total;
    out->bump_recycled_bytes_total =
        osty_gc_bump_recycled_bytes_total +
        osty_gc_survivor_bump_recycled_bytes_total +
        osty_gc_old_bump_recycled_bytes_total +
        osty_gc_pinned_bump_recycled_bytes_total;
    osty_gc_release();
}

/* Phase A1 depth: corruption injectors for `validate_heap` negative
 * tests. Each injector flips exactly one invariant so tests can pair
 * it with `osty_gc_debug_validate_heap()` and assert on a specific
 * error code. The symbols are UNSAFE-prefixed to discourage any use
 * outside the runtime's own tests — they intentionally leave the heap
 * in a broken state.
 *
 * Injectors are idempotent where possible (e.g. bumping live_count by
 * 1 multiple times compounds) — tests should reset state by calling a
 * matching clear before moving on, or simply exit the process.
 */

void osty_gc_debug_unsafe_bump_live_count(void) {
    osty_gc_live_count += 1;
}

void osty_gc_debug_unsafe_bump_live_bytes(void) {
    osty_gc_live_bytes += 1;
}

void osty_gc_debug_unsafe_break_first_prev(void) {
    if (osty_gc_objects != NULL) {
        osty_gc_objects->prev = (osty_gc_header *)(uintptr_t)0xdeadbeef;
    }
}

void osty_gc_debug_unsafe_break_next_link(void) {
    if (osty_gc_objects != NULL && osty_gc_objects->next != NULL) {
        osty_gc_objects->next->prev = (osty_gc_header *)(uintptr_t)0xdeadbeef;
    }
}

void osty_gc_debug_unsafe_set_stale_mark(void) {
    if (osty_gc_objects != NULL) {
        osty_gc_objects->marked = true;
    }
}

void osty_gc_debug_unsafe_negative_root_count(void) {
    if (osty_gc_objects != NULL) {
        osty_gc_objects->root_count = -1;
    }
}

void osty_gc_debug_unsafe_dirty_mark_stack(void) {
    if (osty_gc_objects != NULL) {
        osty_gc_mark_stack_push(osty_gc_objects);
    }
}

void osty_gc_debug_unsafe_append_null_global_slot(void) {
    /* Register a slot whose address is NULL — this is not a managed
     * pointer, it's literally the pointer 0 in the slot table, which
     * the validate pass flags as invalid. */
    if (osty_gc_global_root_count == osty_gc_global_root_cap) {
        osty_gc_global_roots_grow();
    }
    osty_gc_global_root_slots[osty_gc_global_root_count] = NULL;
    osty_gc_global_root_count += 1;
}

void osty_gc_debug_unsafe_satb_dangling(void) {
    /* Append a bogus pointer that has no matching header. */
    osty_gc_satb_log_append((void *)(uintptr_t)0xbadc0ffee0ddf00d);
}

void osty_gc_debug_unsafe_remembered_edge_dangling(void) {
    /* Skip the dedup check to force-insert a dangling edge. */
    if (osty_gc_remembered_edge_count == osty_gc_remembered_edge_cap) {
        osty_gc_remembered_edges_grow();
    }
    osty_gc_remembered_edges[osty_gc_remembered_edge_count].owner =
        (void *)(uintptr_t)0xdeadbeef;
    osty_gc_remembered_edges[osty_gc_remembered_edge_count].value =
        (void *)(uintptr_t)0xfeedface;
    osty_gc_remembered_edge_count += 1;
}

void osty_gc_debug_unsafe_negative_cumulative(void) {
    osty_gc_allocated_bytes_total = -1;
}

/* Phase B depth — corruption injectors for the generational invariants
 * (-12, -13, -14, -15, -16). Each flips exactly one field so tests can
 * pair it with `osty_gc_debug_validate_heap()` and assert on the
 * specific error code. */

void osty_gc_debug_unsafe_bump_young_count(void) {
    osty_gc_young_count += 1;
}

void osty_gc_debug_unsafe_bump_young_bytes(void) {
    osty_gc_young_bytes += 1;
}

void osty_gc_debug_unsafe_set_invalid_generation(void) {
    if (osty_gc_objects != NULL) {
        osty_gc_objects->generation = 42;
    }
}

void osty_gc_debug_unsafe_corrupt_young_head_gen(void) {
    /* Flip the young-head's generation to an out-of-range value. The
     * global walk hits the `-14 invalid generation` check first, which
     * is stronger than the gen-list membership check (-16) — we'd get
     * -12 instead of -16 with a plain YOUNG→OLD flip because the
     * count bookkeeping goes wrong before the list walk. Going to an
     * invalid value skips that intermediate failure. */
    if (osty_gc_young_head != NULL) {
        osty_gc_young_head->generation = 42;
    }
}

void osty_gc_debug_unsafe_detach_from_young_list(void) {
    /* Remove the young-list head but leave the header's global list
     * membership and gen-count intact — gen list count no longer
     * matches `osty_gc_young_count` (-15). */
    osty_gc_header *h = osty_gc_young_head;
    if (h == NULL) {
        return;
    }
    osty_gc_young_head = h->next_gen;
    if (osty_gc_young_head != NULL) {
        osty_gc_young_head->prev_gen = NULL;
    }
    h->next_gen = NULL;
    h->prev_gen = NULL;
}

/* Phase C depth — negative injectors for the tri-colour invariants
 * (-17 invalid colour, -18 colour/marked mismatch, -19 non-white at
 * rest). Each trips exactly one validate_heap code. */

void osty_gc_debug_unsafe_set_invalid_color(void) {
    /* 42 is outside {WHITE, GREY, BLACK} — validate returns -17
     * before it has a chance to look at the marked bit. */
    if (osty_gc_objects != NULL) {
        osty_gc_objects->color = 42;
        osty_gc_objects->marked = true;
    }
}

void osty_gc_debug_unsafe_desync_color_marked(void) {
    /* color = GREY but marked = false → validate returns -18. We need
     * state=IDLE (default) and a live object. Avoid the -9 legacy
     * path by keeping `marked = false` alongside the non-white
     * colour. */
    if (osty_gc_objects != NULL) {
        osty_gc_objects->color = OSTY_GC_COLOR_GREY;
        osty_gc_objects->marked = false;
    }
}

void osty_gc_debug_unsafe_nonwhite_at_rest(void) {
    /* Both fields coherent (BLACK, marked=true) but state=IDLE so
     * -19 NONWHITE_OUTSIDE_MARK fires. */
    if (osty_gc_objects != NULL) {
        osty_gc_objects->color = OSTY_GC_COLOR_BLACK;
        osty_gc_objects->marked = true;
    }
}

/* Phase D groundwork — stable identity invariants. */

void osty_gc_debug_unsafe_zero_stable_id(void) {
    if (osty_gc_objects != NULL) {
        osty_gc_objects->stable_id = 0;
    }
}

void osty_gc_debug_unsafe_remove_identity_index_live(void) {
    if (osty_gc_objects != NULL) {
        osty_gc_identity_remove(osty_gc_objects->stable_id);
    }
}

void osty_gc_debug_stats_dump(FILE *out) {
    osty_gc_stats s;
    if (out == NULL) {
        return;
    }
    osty_gc_debug_stats(&s);
    fprintf(out,
            "osty_gc_stats {\n"
            "  collections:             %lld\n"
            "  live:                    %lld objects, %lld bytes\n"
            "  allocated:               %lld since last collect, %lld total\n"
            "  swept total:             %lld objects, %lld bytes\n"
            "  pre_write / managed:     %lld / %lld\n"
            "  post_write / managed:    %lld / %lld\n"
            "  load / managed:          %lld / %lld\n"
            "  satb log:                %lld entries\n"
            "  remembered edges:        %lld entries\n"
            "  global roots:            %lld slots\n"
            "  pressure limit:          %lld bytes\n"
            "  mark stack peak:         %lld\n"
            "  collect nanos:           total %lld, last %lld, max %lld\n"
            "  index:                   count %lld, tombstones %lld, cap %lld, finds %lld\n"
            "  tiers:                   minor %lld (%lld ns), major %lld (%lld ns)\n"
            "  young / old:             %lld objs %lld B / %lld objs %lld B\n"
            "  promoted total:          %lld objs %lld B\n"
            "  nursery:                 %lld / %lld bytes, promote_age=%lld\n"
            "  free list:               %lld chunks, %lld bytes (reused %lld / %lld B total)\n"
            "  humongous:               alloc %lld / %lld B, swept %lld / %lld B\n"
            "  bump (all tiers):        %lld blocks live, %lld B allocated for blocks (cumul), alloc %lld / %lld B, recycled %lld blocks / %lld B\n"
            "}\n",
            (long long)s.collection_count,
            (long long)s.live_count, (long long)s.live_bytes,
            (long long)s.allocated_since_collect, (long long)s.allocated_bytes_total,
            (long long)s.swept_count_total, (long long)s.swept_bytes_total,
            (long long)s.pre_write_count, (long long)s.pre_write_managed_count,
            (long long)s.post_write_count, (long long)s.post_write_managed_count,
            (long long)s.load_count, (long long)s.load_managed_count,
            (long long)s.satb_log_count,
            (long long)s.remembered_edge_count,
            (long long)s.global_root_count,
            (long long)s.pressure_limit_bytes,
            (long long)s.mark_stack_max_depth,
            (long long)s.collection_nanos_total, (long long)s.collection_nanos_last,
            (long long)s.collection_nanos_max,
            (long long)s.index_count, (long long)s.index_tombstones,
            (long long)s.index_capacity, (long long)s.index_find_ops_total,
            (long long)s.minor_count, (long long)s.minor_nanos_total,
            (long long)s.major_count, (long long)s.major_nanos_total,
            (long long)s.young_count, (long long)s.young_bytes,
            (long long)s.old_count, (long long)s.old_bytes,
            (long long)s.promoted_count_total, (long long)s.promoted_bytes_total,
            (long long)s.allocated_since_minor,
            (long long)s.nursery_limit_bytes, (long long)s.promote_age,
            (long long)s.free_list_count, (long long)s.free_list_bytes,
            (long long)s.free_list_reused_count_total,
            (long long)s.free_list_reused_bytes_total,
            (long long)s.humongous_alloc_count_total,
            (long long)s.humongous_alloc_bytes_total,
            (long long)s.humongous_swept_count_total,
            (long long)s.humongous_swept_bytes_total,
            (long long)s.bump_block_count,
            (long long)s.bump_block_bytes_total,
            (long long)s.bump_alloc_count_total,
            (long long)s.bump_alloc_bytes_total,
            (long long)s.bump_recycled_block_count_total,
            (long long)s.bump_recycled_bytes_total);
}

/* Phase A1 heap validation oracle (RUNTIME_GC_DELTA §9.5).
 *
 * Checks invariants that must hold outside of an active mark phase:
 *
 *   - the `osty_gc_objects` list is a correctly linked doubly-linked list
 *   - `live_count` / `live_bytes` match the actual contents
 *   - no header has negative `root_count`
 *   - no header has `marked == true` (colour is cleared at the start of
 *     every collect and again before sweep, so outside a collection the
 *     survivors from the previous cycle should all have been cleared on
 *     entry of the next — we check the stronger invariant that between
 *     collections no header is stuck grey)
 *   - every payload in the SATB log still has a matching header
 *   - every (owner, value) pair in the remembered set still has headers
 *   - cumulative allocation / sweep counters are non-negative
 *
 * Returns 0 on success, or a negative invariant identifier on the first
 * failure encountered. The identifiers are stable — tests assert on them
 * directly. Callable while the heap is quiescent (no other thread holds
 * the GC lock mid-collection); it acquires the lock for its own walk.
 */
enum {
    OSTY_GC_VALIDATE_OK = 0,
    OSTY_GC_VALIDATE_LIST_FIRST_PREV_NOT_NULL = -1,
    OSTY_GC_VALIDATE_LIST_LINK_BROKEN = -2,
    OSTY_GC_VALIDATE_LIVE_COUNT_MISMATCH = -3,
    OSTY_GC_VALIDATE_LIVE_BYTES_MISMATCH = -4,
    OSTY_GC_VALIDATE_NEGATIVE_ROOT_COUNT = -5,
    OSTY_GC_VALIDATE_SATB_DANGLING = -6,
    OSTY_GC_VALIDATE_REMEMBERED_EDGE_DANGLING = -7,
    OSTY_GC_VALIDATE_NEGATIVE_CUMULATIVE = -8,
    OSTY_GC_VALIDATE_STALE_MARK = -9,
    OSTY_GC_VALIDATE_MARK_STACK_NON_EMPTY = -10,
    OSTY_GC_VALIDATE_NULL_GLOBAL_SLOT = -11,
    /* Phase B: generational bookkeeping invariants. */
    OSTY_GC_VALIDATE_GEN_COUNT_MISMATCH = -12,
    OSTY_GC_VALIDATE_GEN_BYTES_MISMATCH = -13,
    OSTY_GC_VALIDATE_INVALID_GENERATION = -14,
    /* Phase B2 depth — segregated list consistency. */
    OSTY_GC_VALIDATE_GEN_LIST_COUNT_MISMATCH = -15,
    OSTY_GC_VALIDATE_GEN_LIST_MEMBERSHIP = -16,
    /* Phase C tri-colour coherence. */
    OSTY_GC_VALIDATE_INVALID_COLOR = -17,
    OSTY_GC_VALIDATE_COLOR_MARKED_MISMATCH = -18,
    OSTY_GC_VALIDATE_NONWHITE_OUTSIDE_MARK = -19,
    /* Phase D groundwork — stable identity table coherence. */
    OSTY_GC_VALIDATE_INVALID_STABLE_ID = -20,
    OSTY_GC_VALIDATE_IDENTITY_INDEX_MISMATCH = -21,
};

int64_t osty_gc_debug_validate_heap(void) {
    osty_gc_header *header;
    int64_t walked_count = 0;
    int64_t walked_bytes = 0;
    int64_t walked_young_count = 0;
    int64_t walked_young_bytes = 0;
    int64_t walked_old_count = 0;
    int64_t walked_old_bytes = 0;
    int64_t i;
    int64_t status = OSTY_GC_VALIDATE_OK;

    osty_gc_acquire();

    header = osty_gc_objects;
    if (header != NULL && header->prev != NULL) {
        status = OSTY_GC_VALIDATE_LIST_FIRST_PREV_NOT_NULL;
        goto done;
    }
    while (header != NULL) {
        if (header->next != NULL && header->next->prev != header) {
            status = OSTY_GC_VALIDATE_LIST_LINK_BROKEN;
            goto done;
        }
        if (header->prev != NULL && header->prev->next != header) {
            status = OSTY_GC_VALIDATE_LIST_LINK_BROKEN;
            goto done;
        }
        if (header->root_count < 0) {
            status = OSTY_GC_VALIDATE_NEGATIVE_ROOT_COUNT;
            goto done;
        }
        if (header->stable_id == 0 ||
            header->stable_id == OSTY_GC_IDENTITY_TOMBSTONE) {
            status = OSTY_GC_VALIDATE_INVALID_STABLE_ID;
            goto done;
        }
        /* Per-header identity check is a no-op under the lazy
         * identity-insert model: the hash starts empty and only
         * carries entries that compaction explicitly relocated.
         * `osty_gc_identity_lookup` walks the gen lists when the hash
         * misses — but this validate function ALSO walks `osty_gc_objects`
         * in this very loop, so duplicating that walk here would only
         * report gen-list corruption ahead of the dedicated
         * GEN_LIST_COUNT / GEN_MEMBERSHIP error codes that exist
         * specifically for that case. The forged-stable_id case (the
         * surviving Phase D negative test) is caught by the
         * `stable_id == 0` check above. */
        /* Phase C tri-colour coherence. Ordered so the legacy Phase A1
         * stale-mark shape (marked=true flipped without touching
         * color, state=IDLE) keeps returning -9 and older corruption
         * harnesses don't need to know about the new codes:
         *   - legacy -9: marked=true, color=WHITE, state=IDLE
         *   - -17 INVALID_COLOR: `color` is out of range
         *   - -18 COLOR_MARKED_MISMATCH: color/marked desynchronised
         *     in any other way
         *   - -19 NONWHITE_OUTSIDE_MARK: both fields agree but the
         *     header is non-WHITE while no mark phase is running
         */
        if (header->marked && header->color == OSTY_GC_COLOR_WHITE &&
            osty_gc_state == OSTY_GC_STATE_IDLE) {
            status = OSTY_GC_VALIDATE_STALE_MARK;
            goto done;
        }
        if (header->color != OSTY_GC_COLOR_WHITE &&
            header->color != OSTY_GC_COLOR_GREY &&
            header->color != OSTY_GC_COLOR_BLACK) {
            status = OSTY_GC_VALIDATE_INVALID_COLOR;
            goto done;
        }
        {
            bool expected_marked = header->color != OSTY_GC_COLOR_WHITE;
            if (header->marked != expected_marked) {
                status = OSTY_GC_VALIDATE_COLOR_MARKED_MISMATCH;
                goto done;
            }
        }
        if (osty_gc_state == OSTY_GC_STATE_IDLE &&
            header->color != OSTY_GC_COLOR_WHITE) {
            status = OSTY_GC_VALIDATE_NONWHITE_OUTSIDE_MARK;
            goto done;
        }
        if (header->generation == OSTY_GC_GEN_YOUNG) {
            walked_young_count += 1;
            walked_young_bytes += header->byte_size;
        } else if (header->generation == OSTY_GC_GEN_OLD) {
            walked_old_count += 1;
            walked_old_bytes += header->byte_size;
        } else {
            status = OSTY_GC_VALIDATE_INVALID_GENERATION;
            goto done;
        }
        walked_count += 1;
        walked_bytes += header->byte_size;
        header = header->next;
    }
    if (walked_count != osty_gc_live_count) {
        status = OSTY_GC_VALIDATE_LIVE_COUNT_MISMATCH;
        goto done;
    }
    if (walked_bytes != osty_gc_live_bytes) {
        status = OSTY_GC_VALIDATE_LIVE_BYTES_MISMATCH;
        goto done;
    }
    /* Identity-table count vs live-count check retired alongside the
     * lazy identity-insert change. The alloc path no longer mirrors
     * every header into the hash, so the equality invariant no longer
     * holds — the table is empty in steady state and only grows when
     * compaction or debug paths actually call lookup. The per-header
     * lookup check above (line ~9126) still detects stable-id
     * corruption: lookup walks the gen lists when the hash misses, so
     * a forged stable_id surfaces as a mismatch there. */
    if (walked_young_count != osty_gc_young_count ||
        walked_old_count != osty_gc_old_count) {
        status = OSTY_GC_VALIDATE_GEN_COUNT_MISMATCH;
        goto done;
    }
    if (walked_young_bytes != osty_gc_young_bytes ||
        walked_old_bytes != osty_gc_old_bytes) {
        status = OSTY_GC_VALIDATE_GEN_BYTES_MISMATCH;
        goto done;
    }
    /* Phase B2 depth: per-gen list consistency. Walk each gen head,
     * count, and verify it equals the tallied count. Also verify every
     * node in the young list has generation == YOUNG (and vice versa)
     * to catch drift where a promote forgot to re-link. */
    {
        int64_t gen_walked_young = 0;
        int64_t gen_walked_old = 0;
        osty_gc_header *gh = osty_gc_young_head;
        while (gh != NULL) {
            if (gh->generation != OSTY_GC_GEN_YOUNG) {
                status = OSTY_GC_VALIDATE_GEN_LIST_MEMBERSHIP;
                goto done;
            }
            gen_walked_young += 1;
            gh = gh->next_gen;
        }
        gh = osty_gc_old_head;
        while (gh != NULL) {
            if (gh->generation != OSTY_GC_GEN_OLD) {
                status = OSTY_GC_VALIDATE_GEN_LIST_MEMBERSHIP;
                goto done;
            }
            gen_walked_old += 1;
            gh = gh->next_gen;
        }
        if (gen_walked_young != osty_gc_young_count ||
            gen_walked_old != osty_gc_old_count) {
            status = OSTY_GC_VALIDATE_GEN_LIST_COUNT_MISMATCH;
            goto done;
        }
    }
    /* Phase C: mark stack is expected to be empty only at rest.
     * During MARK_INCREMENTAL the stack is the live grey set; during
     * SWEEPING it has been drained but state hasn't flipped yet. */
    if (osty_gc_state == OSTY_GC_STATE_IDLE &&
        osty_gc_mark_stack_count != 0) {
        status = OSTY_GC_VALIDATE_MARK_STACK_NON_EMPTY;
        goto done;
    }
    for (i = 0; i < osty_gc_global_root_count; i++) {
        if (osty_gc_global_root_slots[i] == NULL) {
            status = OSTY_GC_VALIDATE_NULL_GLOBAL_SLOT;
            goto done;
        }
    }
    for (i = 0; i < osty_gc_satb_log_count; i++) {
        if (osty_gc_find_header(osty_gc_satb_log[i]) == NULL) {
            status = OSTY_GC_VALIDATE_SATB_DANGLING;
            goto done;
        }
    }
    for (i = 0; i < osty_gc_remembered_edge_count; i++) {
        if (osty_gc_find_header(osty_gc_remembered_edges[i].owner) == NULL ||
            osty_gc_find_header(osty_gc_remembered_edges[i].value) == NULL) {
            status = OSTY_GC_VALIDATE_REMEMBERED_EDGE_DANGLING;
            goto done;
        }
    }
    if (osty_gc_allocated_since_minor < 0 ||
        osty_gc_minor_count < 0 ||
        osty_gc_major_count < 0 ||
        osty_gc_promoted_count_total < 0 ||
        osty_gc_promoted_bytes_total < 0 ||
        osty_gc_free_list_count < 0 ||
        osty_gc_free_list_bytes < 0 ||
        osty_gc_free_list_reused_count_total < 0 ||
        osty_gc_free_list_reused_bytes_total < 0 ||
        osty_gc_bump_block_count < 0 ||
        osty_gc_bump_block_bytes_total < 0 ||
        osty_gc_bump_alloc_count_total < 0 ||
        osty_gc_bump_alloc_bytes_total < 0 ||
        osty_gc_tlab_refill_count_total < 0 ||
        osty_gc_bump_recycled_block_count_total < 0 ||
        osty_gc_bump_recycled_bytes_total < 0 ||
        osty_gc_survivor_bump_block_count < 0 ||
        osty_gc_survivor_bump_block_bytes_total < 0 ||
        osty_gc_survivor_bump_alloc_count_total < 0 ||
        osty_gc_survivor_bump_alloc_bytes_total < 0 ||
        osty_gc_survivor_tlab_refill_count_total < 0 ||
        osty_gc_survivor_bump_recycled_block_count_total < 0 ||
        osty_gc_survivor_bump_recycled_bytes_total < 0 ||
        osty_gc_old_bump_block_count < 0 ||
        osty_gc_old_bump_block_bytes_total < 0 ||
        osty_gc_old_bump_alloc_count_total < 0 ||
        osty_gc_old_bump_alloc_bytes_total < 0 ||
        osty_gc_old_tlab_refill_count_total < 0 ||
        osty_gc_old_bump_recycled_block_count_total < 0 ||
        osty_gc_old_bump_recycled_bytes_total < 0 ||
        osty_gc_pinned_bump_block_count < 0 ||
        osty_gc_pinned_bump_block_bytes_total < 0 ||
        osty_gc_pinned_bump_alloc_count_total < 0 ||
        osty_gc_pinned_bump_alloc_bytes_total < 0 ||
        osty_gc_pinned_tlab_refill_count_total < 0 ||
        osty_gc_pinned_bump_recycled_block_count_total < 0 ||
        osty_gc_pinned_bump_recycled_bytes_total < 0 ||
        osty_gc_humongous_alloc_count_total < 0 ||
        osty_gc_humongous_alloc_bytes_total < 0 ||
        osty_gc_humongous_swept_count_total < 0 ||
        osty_gc_humongous_swept_bytes_total < 0 ||
        osty_gc_young_count < 0 ||
        osty_gc_young_bytes < 0 ||
        osty_gc_old_count < 0 ||
        osty_gc_old_bytes < 0 ||
        osty_gc_allocated_since_collect < 0 ||
        osty_gc_allocated_bytes_total < 0 ||
        osty_gc_swept_count_total < 0 ||
        osty_gc_swept_bytes_total < 0 ||
        osty_gc_live_count < 0 ||
        osty_gc_live_bytes < 0 ||
        osty_gc_collection_count < 0) {
        status = OSTY_GC_VALIDATE_NEGATIVE_CUMULATIVE;
        goto done;
    }

done:
    osty_gc_release();
    return status;
}

/* ======================================================================
 * Scheduler: worker pool + Chase-Lev work-stealing deques
 * (RUNTIME_SCHEDULER.md Phase 2)
 *
 * Replaces the thread-per-task model with a bounded pool of worker
 * threads that consume tasks from per-worker Chase-Lev deques (Lê
 * et al., "Correct and Efficient Work-Stealing for Weak Memory
 * Models", PPoPP'13 — the weak-memory-safe variant). Tasks submitted
 * from non-worker threads land on a global FIFO inject queue; workers
 * first pop their own deque, then steal from a random peer, then
 * drain the inject queue, and finally park on a shared cv until more
 * work arrives.
 *
 * ABI unchanged (RUNTIME_SCHEDULER.md §ABI contract): spawn and
 * group_spawn still return a GC-managed Handle; handle_join still
 * blocks until the body publishes its result. Callers that polled
 * `handle->done` under the old layout (race tie-break) continue to
 * observe the atomic release store published by the worker thread.
 *
 * Join-helping: a worker that calls handle_join on a not-yet-done
 * handle drains its own deque and steals, making progress toward any
 * task the target depends on. This keeps the pool deadlock-free under
 * dependency depth greater than the worker count. Non-worker joiners
 * (the main thread) park on the handle's cv — the pool is sized to
 * make progress without the main thread helping.
 *
 * GC coupling (spec §19.1): `osty_concurrent_workers` counts
 * *in-flight tasks*, not pool threads. Parked workers are not
 * touching Osty state, so they do not gate collection. The counter
 * is incremented on enqueue and decremented after the body returns,
 * matching Phase 1B semantics so the existing gating in
 * allocate_managed / write barriers / root bind stays correct.
 *
 * Worker count: default `min(CPUs, 8)`. Override via `OSTY_SCHED_WORKERS`
 * in [1, 256]. The pool is initialized lazily on first spawn and
 * shut down at process exit via atexit (workers drain remaining
 * tasks, then thread_join).
 *
 * ABI abuse notice (unchanged from Phase 1B): task bodies and
 * handle_join travel their 8-byte result through int64_t. Scalar/ptr
 * returns up to 8 bytes are supported directly; float returns are
 * raw bits through int64_t; anything wider still requires a separate
 * ABI pass.
 * ====================================================================== */

#if defined(__APPLE__)
/* Forward-declare the sysctl entry we need instead of pulling in
 * <sys/sysctl.h>, which on macOS transitively requires BSD types
 * (u_int, u_char) that are hidden under our strict _POSIX_C_SOURCE
 * feature macros. The signature is stable in the Darwin libc. */
extern int sysctlbyname(const char *name, void *oldp, size_t *oldlenp,
                        void *newp, size_t newlen);
#endif

typedef int64_t (*osty_task_group_body_fn)(void *env, void *group);
typedef int64_t (*osty_task_spawn_body_fn)(void *env);

/* `osty_rt_task_handle_impl` typedef + struct tag are
 * forward-declared earlier (near `osty_rt_chan_impl`) so cheney's
 * safe-handoff helpers can cast `void *` payloads. Full struct
 * body follows below. */

typedef struct osty_rt_task_handle_node {
    osty_rt_task_handle_impl *handle;
    struct osty_rt_task_handle_node *next;
} osty_rt_task_handle_node;

typedef struct osty_rt_task_group_impl {
    volatile int64_t cancelled;          /* 0 = live, 1 = cancelled */
    void *cause;                         /* reserved for error propagation */
    osty_rt_mu_t children_mu;
    osty_rt_task_handle_node *children;  /* all handles spawned into group */
} osty_rt_task_group_impl;

struct osty_rt_task_handle_impl_ {
    osty_rt_task_group_impl *group;      /* inherited group, may be NULL */
    int64_t result;                      /* published before `done` store */
    volatile int32_t done;               /* 0 = pending, 1 = completed */
    osty_rt_mu_t mu;                     /* guards cv for non-worker joiners */
    osty_rt_cond_t cv;
    int sync_live;                       /* 1 iff mu/cv initialised */
};

/* TASK_HANDLE safe-handoff (forward-declared earlier with the
 * cheney young-arena machinery). Mutex + condvar; no external
 * buffers. Same pattern as osty_gc_chan_safe_handoff. */
static void osty_gc_task_handle_safe_handoff(osty_rt_task_handle_impl *src,
                                             osty_rt_task_handle_impl *dst) {
    if (src->sync_live) {
        if (osty_rt_mu_init(&dst->mu) != 0 ||
            osty_rt_cond_init(&dst->cv) != 0) {
            osty_rt_abort("task_handle safe-handoff: sync init failed");
        }
        dst->sync_live = 1;
        osty_rt_mu_destroy(&src->mu);
        osty_rt_cond_destroy(&src->cv);
        src->sync_live = 0;
    }
}

typedef struct osty_rt_task_item {
    void *body_env;
    osty_rt_task_group_impl *group;
    osty_rt_task_handle_impl *handle;
} osty_rt_task_item;

static OSTY_RT_TLS osty_rt_task_group_impl *osty_sched_current_group = NULL;

/* ---- Per-worker Chase-Lev deque.
 *
 * Single producer (owner worker) pushes/pops at the bottom; any
 * thread may steal from the top. Implements the weak-memory-safe
 * variant from Lê et al. 2013:
 *   - owner stores the task pointer, then release fence, then a
 *     relaxed store to `bottom`;
 *   - owner's pop does relaxed --bottom, seq-cst fence, relaxed load
 *     of top, and a seq-cst CAS on the final-element race;
 *   - thieves acquire-load top and bottom (with a seq-cst fence
 *     between) and use a seq-cst CAS to claim the slot.
 *
 * Deque size is a fixed power of two. Local overflow falls through
 * to the inject queue so oversubscribed workers never lose work. */

#define OSTY_SCHED_DEQUE_CAP   4096
#define OSTY_SCHED_DEQUE_MASK  (OSTY_SCHED_DEQUE_CAP - 1)

typedef struct osty_rt_deque {
    volatile int64_t top;
    volatile int64_t bottom;
    osty_rt_task_item *slots[OSTY_SCHED_DEQUE_CAP];
} osty_rt_deque;

static void osty_rt_deque_init(osty_rt_deque *dq) {
    __atomic_store_n(&dq->top, 0, __ATOMIC_RELAXED);
    __atomic_store_n(&dq->bottom, 0, __ATOMIC_RELAXED);
    memset(dq->slots, 0, sizeof(dq->slots));
}

/* Owner-only. 0 on success, -1 on overflow (caller falls back to inject).
 * Slot is published with a release store so a thief's acquire load
 * of the same slot observes the fully-initialized task item (and,
 * transitively, every field written before push). */
static int osty_rt_deque_push(osty_rt_deque *dq, osty_rt_task_item *t) {
    int64_t b = __atomic_load_n(&dq->bottom, __ATOMIC_RELAXED);
    int64_t tp = __atomic_load_n(&dq->top, __ATOMIC_ACQUIRE);
    if (b - tp >= OSTY_SCHED_DEQUE_CAP) {
        return -1;
    }
    __atomic_store_n(&dq->slots[b & OSTY_SCHED_DEQUE_MASK], t,
                     __ATOMIC_RELEASE);
    __atomic_store_n(&dq->bottom, b + 1, __ATOMIC_RELEASE);
    return 0;
}

/* Owner-only. NULL when the deque is empty. The slot load uses
 * acquire so that the matching release store in push (and everything
 * that happened-before it on the writer) is visible to the caller. */
static osty_rt_task_item *osty_rt_deque_pop(osty_rt_deque *dq) {
    int64_t b = __atomic_load_n(&dq->bottom, __ATOMIC_RELAXED) - 1;
    __atomic_store_n(&dq->bottom, b, __ATOMIC_RELAXED);
    __atomic_thread_fence(__ATOMIC_SEQ_CST);
    int64_t tp = __atomic_load_n(&dq->top, __ATOMIC_RELAXED);
    osty_rt_task_item *x = NULL;
    if (tp <= b) {
        x = __atomic_load_n(&dq->slots[b & OSTY_SCHED_DEQUE_MASK],
                            __ATOMIC_ACQUIRE);
        if (tp == b) {
            /* Final element — race with a concurrent thief. */
            int64_t expected = tp;
            if (!__atomic_compare_exchange_n(&dq->top, &expected, tp + 1,
                                             false,
                                             __ATOMIC_SEQ_CST,
                                             __ATOMIC_RELAXED)) {
                x = NULL;
            }
            __atomic_store_n(&dq->bottom, b + 1, __ATOMIC_RELAXED);
        }
    } else {
        __atomic_store_n(&dq->bottom, b + 1, __ATOMIC_RELAXED);
    }
    return x;
}

/* Thief. NULL on empty or CAS-lost; caller retries by picking another
 * victim or falling through to the inject queue. */
static osty_rt_task_item *osty_rt_deque_steal(osty_rt_deque *dq) {
    int64_t tp = __atomic_load_n(&dq->top, __ATOMIC_ACQUIRE);
    __atomic_thread_fence(__ATOMIC_SEQ_CST);
    int64_t b = __atomic_load_n(&dq->bottom, __ATOMIC_ACQUIRE);
    if (tp >= b) {
        return NULL;
    }
    osty_rt_task_item *x =
        __atomic_load_n(&dq->slots[tp & OSTY_SCHED_DEQUE_MASK],
                        __ATOMIC_ACQUIRE);
    int64_t expected = tp;
    if (!__atomic_compare_exchange_n(&dq->top, &expected, tp + 1,
                                     false,
                                     __ATOMIC_SEQ_CST,
                                     __ATOMIC_RELAXED)) {
        return NULL;
    }
    return x;
}

/* ---- Global inject queue (mutex-guarded FIFO linked list).
 *
 * Non-worker submitters enqueue here; workers drain it when their own
 * deque and all steal attempts come up empty. Implemented as a
 * singly-linked list rather than a ring so the queue can absorb
 * arbitrarily large spawn bursts without spurious overflow. Nodes
 * are malloc'd per task (one cacheline each) and freed by the
 * popper — task items themselves are freed in osty_sched_run_task. */

typedef struct osty_rt_inject_node {
    osty_rt_task_item *task;
    struct osty_rt_inject_node *next;
} osty_rt_inject_node;

typedef struct osty_rt_inject {
    osty_rt_inject_node *head;  /* oldest queued */
    osty_rt_inject_node *tail;  /* newest queued */
    int64_t count;
} osty_rt_inject;

static osty_rt_inject osty_sched_inject;

/* ---- Pool state. */

static osty_rt_mu_t osty_sched_pool_mu;
static osty_rt_cond_t osty_sched_pool_cv;
static int64_t osty_sched_pool_parked = 0;    /* guarded by pool_mu */
static volatile int osty_sched_pool_shutdown = 0;
static int osty_sched_worker_count = 0;
static osty_rt_thread_t *osty_sched_worker_threads = NULL;
static osty_rt_deque *osty_sched_worker_deques = NULL;
static OSTY_RT_TLS int osty_sched_worker_id = -1;
/* Set to 1 by both pool workers and elastic workers while they are
 * executing a task body. `worker_id >= 0` identifies pool workers
 * (owners of a deque); elastic workers keep `worker_id = -1` because
 * they do not own a deque. `is_worker` unifies "will eventually
 * return to the scheduler loop" for blocking-wait instrumentation. */
static OSTY_RT_TLS int osty_sched_is_worker = 0;
static osty_rt_once_t osty_sched_pool_once = OSTY_RT_ONCE_INIT;

/* Elastic pool cap. Temp workers are spawned on demand when the fixed
 * pool is saturated (every worker running, no one parked) so that
 * blocking-op workloads cannot deadlock the pool. Bounded to keep
 * runaway programs from fork-bombing the scheduler. */
#define OSTY_SCHED_ELASTIC_MAX 256
static int64_t osty_sched_elastic_count = 0;  /* guarded by pool_mu */

/* Per-thread PRNG for steal-victim selection. Seeded from worker id
 * so every worker draws from a different sequence. SplitMix64 variant. */
static OSTY_RT_TLS uint64_t osty_sched_rand_state = 0;

static uint64_t osty_sched_rand_next(void) {
    uint64_t x = osty_sched_rand_state + 0x9E3779B97F4A7C15ULL;
    osty_sched_rand_state = x;
    x ^= x >> 30;
    x *= 0xBF58476D1CE4E5B9ULL;
    x ^= x >> 27;
    x *= 0x94D049BB133111EBULL;
    x ^= x >> 31;
    return x;
}

static void *osty_sched_worker_main(void *arg);
static void *osty_sched_elastic_main(void *arg);
static void osty_sched_shutdown_atexit(void);
static void osty_sched_spawn_elastic(void);
/* Concurrent-collector stop handshake + SIGURG preemption. Defined
 * further down alongside `osty_rt_sched_preempt_observe` but
 * forward-declared here so `osty_sched_pool_init` can initialise
 * them without a reordering headache. */
static osty_rt_mu_t osty_gc_stop_mu;
static osty_rt_cond_t osty_gc_stop_cv;
static volatile int osty_gc_stop_mu_live;
static volatile int osty_gc_concurrent_stop_requested;
static void osty_rt_install_sigurg_handler(void);

static int osty_sched_default_worker_count(void) {
    const char *env = getenv("OSTY_SCHED_WORKERS");
    if (env != NULL && env[0] != '\0') {
        char *endp = NULL;
        long n = strtol(env, &endp, 10);
        if (endp != env && n >= 1 && n <= 256) {
            return (int)n;
        }
    }
#if defined(OSTY_RT_PLATFORM_WIN32)
    SYSTEM_INFO si;
    GetSystemInfo(&si);
    long cpu = (long)si.dwNumberOfProcessors;
#elif defined(__APPLE__)
    /* Darwin hides _SC_NPROCESSORS_ONLN under _DARWIN_C_SOURCE; the
     * sysctl path is always available and returns the current online
     * core count directly. */
    int ncpu = 0;
    size_t sz = sizeof(ncpu);
    if (sysctlbyname("hw.ncpu", &ncpu, &sz, NULL, 0) != 0 || ncpu < 1) {
        ncpu = 4;
    }
    long cpu = (long)ncpu;
#elif defined(_SC_NPROCESSORS_ONLN)
    long cpu = sysconf(_SC_NPROCESSORS_ONLN);
#else
    long cpu = 4;
#endif
    if (cpu < 1) cpu = 1;
    if (cpu > 8) cpu = 8;
    return (int)cpu;
}

static void osty_sched_pool_init(void) {
    if (osty_rt_mu_init(&osty_sched_pool_mu) != 0 ||
        osty_rt_cond_init(&osty_sched_pool_cv) != 0) {
        osty_rt_abort("scheduler: pool mutex/cv init failed");
    }
    /* Concurrent-collector stop handshake primitives. Initialised
     * here because they share the pool's lifetime: preempt_observe
     * can be called from any thread after pool init, and atexit
     * tears down after all workers are joined. */
    if (osty_rt_mu_init(&osty_gc_stop_mu) != 0 ||
        osty_rt_cond_init(&osty_gc_stop_cv) != 0) {
        osty_rt_abort("scheduler: stop handshake mutex/cv init failed");
    }
    osty_gc_stop_mu_live = 1;
    /* Install the SIGURG handler once. `kick_worker_v1` uses it to
     * nudge compute-bound workers to their next safepoint without
     * waiting for the safepoint stride to roll over on its own. */
    osty_rt_install_sigurg_handler();
    osty_sched_inject.head = NULL;
    osty_sched_inject.tail = NULL;
    osty_sched_inject.count = 0;

    int n = osty_sched_default_worker_count();
    osty_sched_worker_count = n;
    osty_sched_worker_threads =
        (osty_rt_thread_t *)calloc((size_t)n, sizeof(osty_rt_thread_t));
    osty_sched_worker_deques =
        (osty_rt_deque *)calloc((size_t)n, sizeof(osty_rt_deque));
    if (osty_sched_worker_threads == NULL || osty_sched_worker_deques == NULL) {
        osty_rt_abort("scheduler: pool alloc failed");
    }
    for (int i = 0; i < n; i++) {
        osty_rt_deque_init(&osty_sched_worker_deques[i]);
    }
    /* atexit first so an early worker_start failure still teardown-
     * safes any workers that did start. */
    atexit(osty_sched_shutdown_atexit);
    for (int i = 0; i < n; i++) {
        if (osty_rt_thread_start(&osty_sched_worker_threads[i],
                                 osty_sched_worker_main,
                                 (void *)(intptr_t)i) != 0) {
            osty_rt_abort("scheduler: worker thread start failed");
        }
    }
}

static void osty_sched_pool_lazy_init(void) {
    osty_rt_once(&osty_sched_pool_once, osty_sched_pool_init);
}

static void osty_sched_shutdown_atexit(void) {
    /* Best-effort shutdown. Workers finish any in-flight task, then
     * exit. Don't touch GC state here — atexit order vs. other
     * libc destructors is undefined. */
    osty_rt_mu_lock(&osty_sched_pool_mu);
    osty_sched_pool_shutdown = 1;
    osty_rt_cond_broadcast(&osty_sched_pool_cv);
    osty_rt_mu_unlock(&osty_sched_pool_mu);
    for (int i = 0; i < osty_sched_worker_count; i++) {
        osty_rt_thread_join(osty_sched_worker_threads[i], NULL);
    }
}

/* Caller holds pool_mu. Returns 1 if any deque or the inject queue
 * has work for self_id to take. */
static int osty_sched_has_any_work_unlocked(int self_id) {
    if (osty_sched_inject.count > 0) {
        return 1;
    }
    for (int i = 0; i < osty_sched_worker_count; i++) {
        if (i == self_id) continue;
        osty_rt_deque *dq = &osty_sched_worker_deques[i];
        int64_t tp = __atomic_load_n(&dq->top, __ATOMIC_ACQUIRE);
        int64_t b = __atomic_load_n(&dq->bottom, __ATOMIC_ACQUIRE);
        if (b > tp) {
            return 1;
        }
    }
    return 0;
}

static osty_rt_task_item *osty_sched_pop_inject(void) {
    osty_rt_mu_lock(&osty_sched_pool_mu);
    osty_rt_task_item *t = NULL;
    osty_rt_inject_node *node = osty_sched_inject.head;
    if (node != NULL) {
        t = node->task;
        osty_sched_inject.head = node->next;
        if (osty_sched_inject.head == NULL) {
            osty_sched_inject.tail = NULL;
        }
        osty_sched_inject.count -= 1;
    }
    osty_rt_mu_unlock(&osty_sched_pool_mu);
    free(node);
    return t;
}

/* 0 on success, -1 on allocation failure. Signals a parked worker
 * if any are available. Elastic workers are NOT spawned here — if
 * the fixed pool is CPU-bound, oversubscribing on every push would
 * destroy scaling. Oversubscription only happens at actual blocking
 * points via `osty_sched_notify_blocking_wait`. */
static int osty_sched_push_inject(osty_rt_task_item *t) {
    osty_rt_inject_node *node =
        (osty_rt_inject_node *)calloc(1, sizeof(*node));
    if (node == NULL) {
        return -1;
    }
    node->task = t;
    node->next = NULL;
    osty_rt_mu_lock(&osty_sched_pool_mu);
    if (osty_sched_inject.tail != NULL) {
        osty_sched_inject.tail->next = node;
    } else {
        osty_sched_inject.head = node;
    }
    osty_sched_inject.tail = node;
    osty_sched_inject.count += 1;
    if (osty_sched_pool_parked > 0) {
        osty_rt_cond_signal(&osty_sched_pool_cv);
    }
    osty_rt_mu_unlock(&osty_sched_pool_mu);
    return 0;
}

/* Signal that a worker (pool or elastic) is about to enter a
 * blocking cv_wait (channel full/empty, handle completion). If the
 * inject queue has pending work, spawn an elastic helper so the
 * queued task isn't stranded while this worker is off-CPU. No-op
 * when the caller isn't a worker (main thread) or when the elastic
 * cap would be breached. */
static void osty_sched_notify_blocking_wait(void) {
    if (!osty_sched_is_worker) {
        return;
    }
    osty_rt_mu_lock(&osty_sched_pool_mu);
    int want = (osty_sched_inject.count > 0 &&
                osty_sched_elastic_count < OSTY_SCHED_ELASTIC_MAX);
    if (want) {
        osty_sched_elastic_count += 1;
    }
    osty_rt_mu_unlock(&osty_sched_pool_mu);
    if (want) {
        osty_sched_spawn_elastic();
    }
}

/* Phase 3 — cooperative preemption hook.
 *
 * Called from `osty_gc_safepoint_v1` on every safepoint (entry / loop
 * backedge / alloc / call / yield). Does four cheap things, in order
 * of observability:
 *
 *   1. Observes the current group's cancel flag and yields the OS
 *      scheduler slot so a sibling worker can make progress. Tasks
 *      that check `osty_rt_cancel_is_cancelled` at the next user
 *      yield point get a fast turnaround; tasks that don't still
 *      benefit from the OS yield.
 *
 *   2. Observes a thread-local SIGURG async-preemption flag. Set by
 *      `osty_rt_sigurg_handler` (installed from `osty_sched_pool_init`
 *      on POSIX). The flag is reset when we arrive at the safepoint,
 *      so external kicks from `osty_rt_sched_kick_worker` take effect
 *      here rather than crashing inside the async-signal handler.
 *
 *   3. Observes the concurrent-collector `stop_requested` flag. When
 *      set, the mutator parks on `osty_gc_stop_cv` until the
 *      collector releases it — proper cv/broadcast wake-up rather
 *      than busy-wait so a 50ms STW window doesn't burn 50ms of CPU.
 *      Collector itself is Phase-3-future work, but mutator side of
 *      the handshake is complete.
 *
 *   4. When the pool is saturated (no parked worker) *and* the
 *      inject queue has pending tasks, spawn an elastic worker to
 *      drain them. A pure-CPU task in a tight loop can otherwise
 *      starve queued siblings on a pool sized smaller than the
 *      workload: the worker never reaches a blocking cv_wait, so
 *      `osty_sched_notify_blocking_wait` never fires. This is the
 *      compute-bound analogue, paying for the elastic spawn at
 *      loop-backedge granularity (stride-scaled by the emitter, so
 *      not per-iteration) rather than per-block.
 */
/* The three globals above are forward-declared near the other
 * scheduler forward decls so pool_init can initialise them. Keep
 * this comment block as the anchor for the documentation without
 * re-declaring them (that would be a duplicate definition with
 * the forward decls). */

/* Thread-local SIGURG preemption flag. Set by the handler (async-
 * signal context), cleared at the safepoint. Using `sig_atomic_t`
 * keeps writes from the handler well-defined per POSIX; cleared as
 * a relaxed atomic so the compiler doesn't hoist the load. */
static OSTY_RT_TLS volatile sig_atomic_t osty_rt_sigurg_flag = 0;

/* Phase 3 step 2 — root publication for concurrent GC.
 *
 * When a mutator parks for a stop-request window, it publishes its
 * current safepoint root array to a shared registry so the collector
 * can scan every parked mutator's stack without racing with the
 * mutators themselves. The registry is a linked list under
 * `osty_gc_stop_mu`; each parked mutator appends its entry on park
 * and removes it on resume. The slot pointer remains stable for the
 * lifetime of the park because the caller's safepoint frame is
 * blocked in `osty_rt_cond_wait` — the stack memory backing the
 * root array cannot move or be reclaimed while we hold the park.
 *
 * The collector side (`osty_rt_gc_collect_concurrent_v1`) sets the
 * stop flag, kicks every worker to accelerate safepoint arrival,
 * waits until the parked-mutator count matches the in-flight-task
 * count, then walks the registry unioning root arrays into a single
 * stack-root snapshot and drives a standard STW collection over it.
 * Release clears the stop flag and broadcasts; mutators wake and
 * unpublish. */
typedef struct osty_gc_parked_roots_entry {
    void *const *slots;
    int64_t count;
    struct osty_gc_parked_roots_entry *next;
} osty_gc_parked_roots_entry;

static osty_gc_parked_roots_entry *osty_gc_parked_roots_head = NULL;
static int64_t osty_gc_parked_mutator_count = 0;  /* guarded by osty_gc_stop_mu */

static void osty_rt_sched_preempt_park_for_collector(void *const *roots,
                                                     int64_t count) {
    /* Publish + park atomically under the stop mutex. The collector
     * reads the registry under the same mutex, so the publication
     * is observable by the time parked_mutator_count is bumped. */
    osty_gc_parked_roots_entry entry;
    entry.slots = roots;
    entry.count = (count < 0) ? 0 : count;

    osty_rt_mu_lock(&osty_gc_stop_mu);
    entry.next = osty_gc_parked_roots_head;
    osty_gc_parked_roots_head = &entry;
    osty_gc_parked_mutator_count += 1;
    /* Broadcast so a collector blocked on "all mutators parked"
     * observes this arrival. */
    osty_rt_cond_broadcast(&osty_gc_stop_cv);
    while (__atomic_load_n(&osty_gc_concurrent_stop_requested,
                           __ATOMIC_ACQUIRE)) {
        osty_rt_cond_wait(&osty_gc_stop_cv, &osty_gc_stop_mu);
    }
    /* Unpublish on the way out. Walk the list and splice our entry.
     * The list is tiny (bounded by parked mutator count, i.e. at
     * most `osty_concurrent_workers`) so the O(n) removal is
     * cheaper than any per-entry indirection. */
    osty_gc_parked_roots_entry **pp = &osty_gc_parked_roots_head;
    while (*pp != NULL && *pp != &entry) {
        pp = &(*pp)->next;
    }
    if (*pp == &entry) {
        *pp = entry.next;
    }
    osty_gc_parked_mutator_count -= 1;
    osty_rt_cond_broadcast(&osty_gc_stop_cv);
    osty_rt_mu_unlock(&osty_gc_stop_mu);
}

static void osty_rt_sched_preempt_observe(void *const *roots,
                                          int64_t count) {
    if (!osty_sched_is_worker) {
        return;
    }
    /* (1) Cancel observation + OS yield. Relaxed load — we don't
     * need acquire because cancel is a liveness hint, not a
     * memory-ordering gate: the worker will see the store at the
     * latest on the next pool_mu acquire. */
    osty_rt_task_group_impl *g = osty_sched_current_group;
    if (g != NULL &&
        __atomic_load_n(&g->cancelled, __ATOMIC_RELAXED) != 0) {
        osty_rt_plat_yield();
    }
    /* (2) SIGURG async-preemption flag. TLS so the load is a plain
     * memory access; the handler's store is sig_atomic_t which is
     * well-defined across async delivery on POSIX. Clear-on-observe
     * so one kick == one preempt tick. */
    if (osty_rt_sigurg_flag) {
        osty_rt_sigurg_flag = 0;
        osty_rt_plat_yield();
    }
    /* (3) Concurrent-collector handshake. Cheap relaxed-load gate;
     * park on the cv only on the slow path. Roots-passthrough lets
     * the collector scan parked mutators' live references. */
    if (__atomic_load_n(&osty_gc_concurrent_stop_requested,
                        __ATOMIC_RELAXED)) {
        osty_rt_sched_preempt_park_for_collector(roots, count);
    }
    /* (4) Elastic drain. Only enter the pool mutex when an elastic
     * is warranted — fast path is a single atomic-less count read,
     * which is safe to race (the mutex-guarded recheck catches
     * stale observations). */
    if (osty_sched_inject.count == 0) {
        return;
    }
    osty_rt_mu_lock(&osty_sched_pool_mu);
    int want = (osty_sched_inject.count > 0 &&
                osty_sched_pool_parked == 0 &&
                osty_sched_elastic_count < OSTY_SCHED_ELASTIC_MAX);
    if (want) {
        osty_sched_elastic_count += 1;
    }
    osty_rt_mu_unlock(&osty_sched_pool_mu);
    if (want) {
        osty_sched_spawn_elastic();
    }
}

#if !defined(OSTY_RT_PLATFORM_WIN32)
/* SIGURG handler. Async-signal-safe: writes only to `sig_atomic_t`
 * TLS. The next safepoint (or explicit preempt_check_v1) observes
 * the flag and yields cooperatively — we deliberately do NOT do
 * any real preemption work here (no mutex, no malloc, no stdio)
 * because async-signal context forbids most libc. */
static void osty_rt_sigurg_handler(int signo) {
    (void)signo;
    osty_rt_sigurg_flag = 1;
}

static void osty_rt_install_sigurg_handler(void) {
    struct sigaction sa;
    memset(&sa, 0, sizeof(sa));
    sa.sa_handler = osty_rt_sigurg_handler;
    sigemptyset(&sa.sa_mask);
    /* SA_RESTART so a blocked syscall (e.g. nanosleep) resumes after
     * the handler returns rather than failing with EINTR, which the
     * existing sleep/park paths would have to special-case. */
    sa.sa_flags = SA_RESTART;
    (void)sigaction(SIGURG, &sa, NULL);
}
#else
static void osty_rt_install_sigurg_handler(void) { /* Windows: no SIGURG */ }
#endif

/* Try to steal from a random non-self victim. Caller retries via the
 * outer take-work loop; a single pass is "good enough" per iteration. */
static osty_rt_task_item *osty_sched_try_steal(int self_id) {
    int n = osty_sched_worker_count;
    if (n <= 1) {
        return NULL;
    }
    int start = (int)(osty_sched_rand_next() % (uint64_t)n);
    for (int i = 0; i < n; i++) {
        int victim = (start + i) % n;
        if (victim == self_id) continue;
        osty_rt_task_item *t =
            osty_rt_deque_steal(&osty_sched_worker_deques[victim]);
        if (t != NULL) {
            return t;
        }
    }
    return NULL;
}

/* Own deque → steal → inject. NULL when every source is empty. */
static osty_rt_task_item *osty_sched_take_work(int self_id) {
    if (self_id >= 0) {
        osty_rt_task_item *t =
            osty_rt_deque_pop(&osty_sched_worker_deques[self_id]);
        if (t != NULL) return t;
    }
    osty_rt_task_item *t = osty_sched_try_steal(self_id);
    if (t != NULL) return t;
    return osty_sched_pop_inject();
}

static void osty_sched_park_until_work_or_shutdown(int self_id) {
    osty_rt_mu_lock(&osty_sched_pool_mu);
    while (!osty_sched_pool_shutdown &&
           !osty_sched_has_any_work_unlocked(self_id)) {
        osty_sched_pool_parked += 1;
        osty_rt_cond_wait(&osty_sched_pool_cv, &osty_sched_pool_mu);
        osty_sched_pool_parked -= 1;
    }
    osty_rt_mu_unlock(&osty_sched_pool_mu);
}

static void osty_rt_task_handle_complete(osty_rt_task_handle_impl *h,
                                         int64_t result) {
    /* Publish result before `done` so any concurrent acquire-load of
     * `done` sees a coherent result. The cv broadcast is strictly for
     * sleeping joiners; worker joiners (help-drain) re-check `done`
     * between steals. */
    h->result = result;
    __atomic_store_n(&h->done, 1, __ATOMIC_RELEASE);
    osty_rt_mu_lock(&h->mu);
    osty_rt_cond_broadcast(&h->cv);
    osty_rt_mu_unlock(&h->mu);
}

static void osty_sched_run_task(osty_rt_task_item *t) {
    osty_rt_task_group_impl *prev = osty_sched_current_group;
    osty_sched_current_group = t->group;
    osty_task_spawn_body_fn fn =
        (osty_task_spawn_body_fn)(*(void **)t->body_env);
    int64_t result = fn(t->body_env);
    osty_sched_current_group = prev;

    osty_rt_task_handle_complete(t->handle, result);
    osty_sched_workers_dec();
    free(t);
}

static void *osty_sched_worker_main(void *arg) {
    int self = (int)(intptr_t)arg;
    osty_sched_worker_id = self;
    osty_sched_is_worker = 1;
    osty_sched_rand_state = (uint64_t)self * 0x9E3779B97F4A7C15ULL + 1;
    for (;;) {
        osty_rt_task_item *t = osty_sched_take_work(self);
        if (t != NULL) {
            osty_sched_run_task(t);
            continue;
        }
        if (osty_sched_pool_shutdown) {
            /* Drain any last-second work so nothing is left with
             * done=0. If we then still see nothing, exit. */
            t = osty_sched_take_work(self);
            if (t != NULL) {
                osty_sched_run_task(t);
                continue;
            }
            break;
        }
        osty_sched_park_until_work_or_shutdown(self);
    }
    osty_sched_worker_id = -1;
    osty_sched_is_worker = 0;
    return NULL;
}

/* Elastic worker: spawned on demand when a pool worker is about to
 * block and the inject queue still has pending work. Not a pool
 * member (worker_id = -1) — cannot be the target of steals, but can
 * steal from the pool's deques and drain the inject queue. Exits as
 * soon as work dries up so oversubscription decays back to the
 * baseline pool size when the burst finishes. */
static void *osty_sched_elastic_main(void *arg) {
    (void)arg;
    osty_sched_worker_id = -1;
    osty_sched_is_worker = 1;
    osty_sched_rand_state =
        (uint64_t)(uintptr_t)&osty_sched_elastic_count +
        osty_rt_monotonic_ns();
    for (;;) {
        osty_rt_task_item *t = osty_sched_take_work(-1);
        if (t == NULL) {
            break;
        }
        osty_sched_run_task(t);
    }
    osty_sched_is_worker = 0;
    osty_rt_mu_lock(&osty_sched_pool_mu);
    osty_sched_elastic_count -= 1;
    osty_rt_mu_unlock(&osty_sched_pool_mu);
    return NULL;
}

static void osty_sched_spawn_elastic(void) {
    osty_rt_thread_t thr;
    if (osty_rt_thread_start(&thr, osty_sched_elastic_main, NULL) != 0) {
        /* Spawn failure is non-fatal: the pushed task still lives in
         * the inject queue and a pool worker will pick it up once it
         * finishes its current body. Undo the elastic reservation. */
        osty_rt_mu_lock(&osty_sched_pool_mu);
        osty_sched_elastic_count -= 1;
        osty_rt_mu_unlock(&osty_sched_pool_mu);
        return;
    }
    /* Detach so the OS reaps the thread on exit — we never join
     * elastics explicitly. Pool workers retain their joinable handle
     * because the atexit shutdown must wait on them. */
#if defined(OSTY_RT_PLATFORM_WIN32)
    CloseHandle(thr);
#else
    pthread_detach(thr);
#endif
}

/* ---- Handle + group. */

static void osty_rt_task_handle_destroy(void *payload) {
    osty_rt_task_handle_impl *h = (osty_rt_task_handle_impl *)payload;
    if (h == NULL || !h->sync_live) {
        return;
    }
    osty_rt_mu_destroy(&h->mu);
    osty_rt_cond_destroy(&h->cv);
    h->sync_live = 0;
}

/* Handle is GC-managed: reclaimed when no Osty Handle<T> reference
 * remains. While a task is in-flight, osty_concurrent_workers > 0
 * pauses collection so the pointer the task item carries cannot be
 * freed underneath it. */
static osty_rt_task_handle_impl *osty_sched_alloc_handle(void) {
    /* Use the dedicated KIND_GENERIC_TASK_HANDLE so cheney's
     * young dispatch can find the destroy + safe-handoff via the
     * kind table. The headerful path resolves the same destroy
     * via the descriptor too, so behavior is identical. */
    osty_rt_task_handle_impl *h =
        (osty_rt_task_handle_impl *)osty_gc_allocate_managed(
            sizeof(osty_rt_task_handle_impl),
            OSTY_GC_KIND_GENERIC_TASK_HANDLE,
            "runtime.task.handle",
            NULL, osty_rt_task_handle_destroy);
    if (osty_rt_mu_init(&h->mu) != 0 ||
        osty_rt_cond_init(&h->cv) != 0) {
        osty_rt_abort("task_spawn: handle sync init failed");
    }
    h->sync_live = 1;
    return h;
}

static void osty_rt_task_group_attach(osty_rt_task_group_impl *g,
                                      osty_rt_task_handle_impl *h) {
    osty_rt_task_handle_node *node =
        (osty_rt_task_handle_node *)calloc(1, sizeof(*node));
    if (node == NULL) {
        osty_rt_abort("task_group_spawn: out of memory");
    }
    node->handle = h;
    osty_rt_mu_lock(&g->children_mu);
    node->next = g->children;
    g->children = node;
    osty_rt_mu_unlock(&g->children_mu);
}

/* Wait for a single handle. Worker callers help-drain so that nested
 * joins cannot deadlock a pool smaller than the dependency depth. */
static void osty_rt_task_handle_wait(osty_rt_task_handle_impl *h) {
    if (__atomic_load_n(&h->done, __ATOMIC_ACQUIRE)) {
        return;
    }
    int self = osty_sched_worker_id;
    if (self >= 0) {
        while (!__atomic_load_n(&h->done, __ATOMIC_ACQUIRE)) {
            osty_rt_task_item *t = osty_sched_take_work(self);
            if (t != NULL) {
                osty_sched_run_task(t);
                continue;
            }
            /* No work available and handle still pending. Before
             * parking, let a helper spawn if the inject queue has
             * anything waiting — some of that work may be what the
             * completing worker is currently blocked on. */
            osty_sched_notify_blocking_wait();
            osty_rt_mu_lock(&h->mu);
            if (!__atomic_load_n(&h->done, __ATOMIC_ACQUIRE)) {
                osty_rt_cond_wait(&h->cv, &h->mu);
            }
            osty_rt_mu_unlock(&h->mu);
        }
        return;
    }
    osty_rt_mu_lock(&h->mu);
    while (!__atomic_load_n(&h->done, __ATOMIC_ACQUIRE)) {
        osty_rt_cond_wait(&h->cv, &h->mu);
    }
    osty_rt_mu_unlock(&h->mu);
}

static void osty_rt_task_group_reap(osty_rt_task_group_impl *g) {
    /* Spec §8.1: no child outlives its group scope. Walk the child
     * list and wait on every handle. Workers may help-drain; the
     * main thread just parks on the cv. Nodes are freed because the
     * group's stack frame is about to disappear. */
    osty_rt_mu_lock(&g->children_mu);
    osty_rt_task_handle_node *node = g->children;
    g->children = NULL;
    osty_rt_mu_unlock(&g->children_mu);

    while (node != NULL) {
        osty_rt_task_handle_node *next = node->next;
        if (node->handle != NULL) {
            osty_rt_task_handle_wait(node->handle);
        }
        free(node);
        node = next;
    }
}

int64_t osty_rt_task_group(void *body_env) {
    if (body_env == NULL) {
        osty_rt_abort("task_group: null body env");
    }
    osty_rt_task_group_impl group;
    group.cancelled = 0;
    group.cause = NULL;
    group.children = NULL;
    if (osty_rt_mu_init(&group.children_mu) != 0) {
        osty_rt_abort("task_group: mutex init failed");
    }

    osty_rt_task_group_impl *prev = osty_sched_current_group;
    osty_sched_current_group = &group;

    osty_task_group_body_fn fn = (osty_task_group_body_fn)(*(void **)body_env);
    int64_t result = fn(body_env, (void *)&group);

    osty_rt_task_group_reap(&group);
    osty_rt_mu_destroy(&group.children_mu);

    osty_sched_current_group = prev;
    return result;
}

static void *osty_rt_task_spawn_internal(void *group, void *body_env) {
    if (body_env == NULL) {
        osty_rt_abort("task_spawn: null body env");
    }
    osty_rt_enable_concurrency_runtime();
    osty_sched_pool_lazy_init();

    osty_rt_task_handle_impl *h = osty_sched_alloc_handle();
    h->group = (osty_rt_task_group_impl *)group;

    osty_rt_task_item *item =
        (osty_rt_task_item *)calloc(1, sizeof(*item));
    if (item == NULL) {
        osty_rt_abort("task_spawn: out of memory");
    }
    item->body_env = body_env;
    item->group = (osty_rt_task_group_impl *)group;
    item->handle = h;

    /* Inc BEFORE publishing so GC can never run while the env / handle
     * would be visible only through unscanned task-item memory. The
     * matching dec happens in osty_sched_run_task after the body
     * returns. */
    osty_sched_workers_inc();

    int self = osty_sched_worker_id;
    int routed = 0;
    if (self >= 0) {
        if (osty_rt_deque_push(&osty_sched_worker_deques[self], item) == 0) {
            routed = 1;
            /* Wake a parked peer so the newly pushed task isn't
             * stranded if we block before the next safepoint. */
            osty_rt_mu_lock(&osty_sched_pool_mu);
            if (osty_sched_pool_parked > 0) {
                osty_rt_cond_signal(&osty_sched_pool_cv);
            }
            osty_rt_mu_unlock(&osty_sched_pool_mu);
        }
    }
    if (!routed) {
        if (osty_sched_push_inject(item) != 0) {
            osty_sched_workers_dec();
            free(item);
            osty_rt_abort("task_spawn: inject queue alloc failed (OOM)");
        }
    }

    if (h->group != NULL) {
        osty_rt_task_group_attach(h->group, h);
    }
    return h;
}

void *osty_rt_task_spawn(void *body_env) {
    return osty_rt_task_spawn_internal(NULL, body_env);
}

void *osty_rt_task_group_spawn(void *group, void *body_env) {
    return osty_rt_task_spawn_internal(group, body_env);
}

int64_t osty_rt_task_handle_join(void *handle) {
    if (handle == NULL) {
        osty_rt_abort("task_handle_join: null handle");
    }
    /* Handles are GC-managed; `osty_gc_load_v1` is the read barrier
     * that mirrors the handle's tracing obligations (Phase 2 pool
     * uses the barrier for the same reason the earlier pthread path
     * did — the forwarding address is valid for the scope of this
     * call, not the raw pointer stored on the caller's stack). */
    osty_rt_task_handle_impl *h =
        (osty_rt_task_handle_impl *)osty_gc_load_v1(handle);
    osty_rt_task_handle_wait(h);
    return h->result;
}

void osty_rt_task_group_cancel(void *group) {
    if (group == NULL) {
        return;
    }
    osty_rt_task_group_impl *g = (osty_rt_task_group_impl *)group;
    __atomic_store_n(&g->cancelled, 1, __ATOMIC_RELEASE);
}

bool osty_rt_task_group_is_cancelled(void *group) {
    if (group == NULL) {
        return false;
    }
    osty_rt_task_group_impl *g = (osty_rt_task_group_impl *)group;
    return __atomic_load_n(&g->cancelled, __ATOMIC_ACQUIRE) != 0;
}

bool osty_rt_cancel_is_cancelled(void) {
    osty_rt_task_group_impl *g = osty_sched_current_group;
    if (g == NULL) {
        return false;
    }
    return __atomic_load_n(&g->cancelled, __ATOMIC_ACQUIRE) != 0;
}

void osty_rt_thread_yield(void) {
    /* Hand the OS scheduler a chance to run other threads. */
    osty_rt_plat_yield();
}

/* Phase 3 public entry points.
 *
 * `osty_rt_sched_preempt_check_v1` is the compiler-emittable name for
 * the preemption safepoint. The GC safepoint already calls
 * `osty_rt_sched_preempt_observe` transitively, so emitters that
 * already use `osty.gc.safepoint_v1` get preemption for free; this
 * symbol is for sites that need preemption without the full GC
 * safepoint (e.g. hot inner loops where the allocator check +
 * root-slot walk is too expensive). ABI is intentionally minimal —
 * no args, no return — so the compiler emits a single bare call.
 *
 * `osty_rt_sched_concurrent_stop_request_v1` /
 * `osty_rt_sched_concurrent_stop_release_v1` flip the flag the
 * preempt hook parks on. A Phase-3 concurrent collector drives
 * these from its STW windows; tests can drive them directly to
 * exercise the park path. */
void osty_rt_sched_preempt_check_v1(void) {
    /* No-arg preempt entry: cancel + SIGURG + elastic signals only.
     * We deliberately pass NULL roots: a collector-induced park
     * would otherwise publish a rootless slot and leak reachable
     * objects. Call sites that need collector-safe parking must go
     * through the full `osty_gc_safepoint_v1` (which threads the
     * live root array). */
    osty_rt_sched_preempt_observe(NULL, 0);
}

void osty_rt_sched_concurrent_stop_request_v1(void) {
    /* Flag flip under the stop mutex so parking mutators can't race
     * on the acquire-load → cond_wait interlock. The atomic store is
     * kept for the relaxed fast-path in preempt_observe, but the
     * broadcast requires the lock anyway. */
    if (!osty_gc_stop_mu_live) {
        /* Pool not yet started — no mutators can be parking. Set the
         * flag so future safepoints see it; no cv signalling needed. */
        __atomic_store_n(&osty_gc_concurrent_stop_requested, 1,
                         __ATOMIC_RELEASE);
        return;
    }
    osty_rt_mu_lock(&osty_gc_stop_mu);
    __atomic_store_n(&osty_gc_concurrent_stop_requested, 1,
                     __ATOMIC_RELEASE);
    osty_rt_mu_unlock(&osty_gc_stop_mu);
}

void osty_rt_sched_concurrent_stop_release_v1(void) {
    if (!osty_gc_stop_mu_live) {
        __atomic_store_n(&osty_gc_concurrent_stop_requested, 0,
                         __ATOMIC_RELEASE);
        return;
    }
    osty_rt_mu_lock(&osty_gc_stop_mu);
    __atomic_store_n(&osty_gc_concurrent_stop_requested, 0,
                     __ATOMIC_RELEASE);
    osty_rt_cond_broadcast(&osty_gc_stop_cv);
    osty_rt_mu_unlock(&osty_gc_stop_mu);
}

/* Forward decl for the helper below; full definition lives further
 * down with the rest of the scheduler-side preempt machinery. */
void osty_rt_sched_kick_worker_v1(int64_t worker_id);

/* Phase 0b: STW root handshake helper. Definitions of the forward
 * declarations near the top of the file. Used by the multi-thread
 * branch of `incremental_start_with_stack_roots` and `incremental_finish*`
 * to bring every pool worker to a safepoint and union published roots
 * with the caller's. */
static void osty_gc_stw_handshake_acquire(
    void *const *caller_roots, int64_t caller_count,
    osty_gc_stw_handshake_state *out) {
    out->flat_roots = NULL;
    out->total_count = 0;
    out->held = 0;
    if (caller_count < 0) {
        caller_count = 0;
    }

    osty_rt_sched_concurrent_stop_request_v1();
    osty_rt_sched_kick_worker_v1(-1);

    osty_rt_mu_lock(&osty_gc_stop_mu);
    for (;;) {
        int64_t inflight =
            __atomic_load_n(&osty_concurrent_workers, __ATOMIC_ACQUIRE);
        if (osty_gc_parked_mutator_count >= inflight) {
            break;
        }
        osty_rt_cond_wait(&osty_gc_stop_cv, &osty_gc_stop_mu);
    }

    int64_t total = caller_count;
    for (osty_gc_parked_roots_entry *e = osty_gc_parked_roots_head;
         e != NULL; e = e->next) {
        total += e->count;
    }
    void **flat = NULL;
    if (total > 0) {
        flat = (void **)calloc((size_t)total, sizeof(void *));
        if (flat == NULL) {
            osty_rt_mu_unlock(&osty_gc_stop_mu);
            osty_rt_sched_concurrent_stop_release_v1();
            osty_rt_abort("stw handshake: flat union OOM");
        }
        int64_t idx = 0;
        for (int64_t i = 0; i < caller_count; i++) {
            flat[idx++] = (void *)caller_roots[i];
        }
        for (osty_gc_parked_roots_entry *e = osty_gc_parked_roots_head;
             e != NULL; e = e->next) {
            for (int64_t i = 0; i < e->count; i++) {
                flat[idx++] = (void *)e->slots[i];
            }
        }
    }
    osty_rt_mu_unlock(&osty_gc_stop_mu);

    out->flat_roots = flat;
    out->total_count = total;
    out->held = 1;
}

static void osty_gc_stw_handshake_release(
    osty_gc_stw_handshake_state *state) {
    if (!state->held) {
        return;
    }
    if (state->flat_roots != NULL) {
        free(state->flat_roots);
        state->flat_roots = NULL;
    }
    state->total_count = 0;
    state->held = 0;
    osty_rt_sched_concurrent_stop_release_v1();
}

/* Phase 3 step 2 — concurrent collection driver.
 *
 * Runs a full STW collection while mutator workers are live, using
 * the stop-request handshake + SIGURG kick to bring every mutator
 * to a safepoint and publish its stack roots. This is a "STW
 * during workers" path rather than a true concurrent marker — the
 * pause window is bounded by mark + sweep duration — but it
 * unblocks the biggest Phase 2 limitation: `osty_gc_collect_*`
 * was hard-gated to return early whenever `osty_concurrent_workers
 * > 0`, meaning long-running programs with always-live workers
 * could never collect.
 *
 * The concurrent-marker full-fat variant is Phase 3 step 2b: it
 * would reuse this same handshake for the start (root scan) and
 * finish (final drain) phases, with marking running between them
 * while mutators keep executing. The infrastructure here — root
 * publication registry + parked-mutator counter + cv broadcast
 * — is exactly what step 2b needs; the delta is a new collector
 * path that yields the gc_lock during the mark loop.
 *
 * `caller_roots` / `caller_count` come from the caller's own
 * safepoint array (main-thread root scan, typically empty if the
 * caller pins everything via `osty_gc_root_bind_v1`).
 */
/* Forward decl: kick_worker_v1 is defined just below but called from
 * collect_concurrent_v1 so C's single-pass compile needs a hint. */
void osty_rt_sched_kick_worker_v1(int64_t worker_id);

void osty_rt_gc_collect_concurrent_v1(void *const *caller_roots,
                                      int64_t caller_count) {
    if (caller_count < 0) {
        caller_count = 0;
    }
    /* 1. Request stop. This also takes the stop_mu, but we release
     *    it before observing the parked count so mutators can
     *    acquire it to publish roots. */
    osty_rt_sched_concurrent_stop_request_v1();

    /* 2. Kick every pool worker so they reach a safepoint fast
     *    (compute-bound workers otherwise wait for the 1024-stride
     *    to roll over on their own). No-op on Windows. */
    osty_rt_sched_kick_worker_v1(-1);

    /* 3. Wait for all in-flight tasks to park. `osty_concurrent_workers`
     *    is the number of active bodies; `osty_gc_parked_mutator_count`
     *    is how many are currently blocked in park_for_collector.
     *    Both are read under the same mutex so the condition is
     *    consistent. A task body that completes between signal and
     *    park decrements `osty_concurrent_workers`; we handle that
     *    by re-reading the counter each iteration. */
    osty_rt_mu_lock(&osty_gc_stop_mu);
    for (;;) {
        int64_t inflight = __atomic_load_n(&osty_concurrent_workers,
                                           __ATOMIC_ACQUIRE);
        if (osty_gc_parked_mutator_count >= inflight) {
            break;
        }
        osty_rt_cond_wait(&osty_gc_stop_cv, &osty_gc_stop_mu);
    }

    /* 4. Build a flat union of all parked mutators' root slots
     *    plus the caller's own roots. We copy into a heap buffer
     *    rather than chasing the linked list from inside the
     *    collector because the collect path re-enters gc_lock and
     *    we don't want to hold stop_mu across it (deadlock risk
     *    if the collector's own traces need stop_mu transitively). */
    int64_t total = caller_count;
    for (osty_gc_parked_roots_entry *e = osty_gc_parked_roots_head;
         e != NULL; e = e->next) {
        total += e->count;
    }
    void **flat = NULL;
    if (total > 0) {
        flat = (void **)calloc((size_t)total, sizeof(void *));
        if (flat == NULL) {
            osty_rt_mu_unlock(&osty_gc_stop_mu);
            osty_rt_sched_concurrent_stop_release_v1();
            osty_rt_abort("gc.collect_concurrent: flat union OOM");
        }
        int64_t idx = 0;
        for (int64_t i = 0; i < caller_count; i++) {
            flat[idx++] = (void *)caller_roots[i];
        }
        for (osty_gc_parked_roots_entry *e = osty_gc_parked_roots_head;
             e != NULL; e = e->next) {
            for (int64_t i = 0; i < e->count; i++) {
                flat[idx++] = (void *)e->slots[i];
            }
        }
    }
    osty_rt_mu_unlock(&osty_gc_stop_mu);

    /* 5. Run the collection against the unioned snapshot. The
     *    gc_lock serializes against mutator write barriers, but
     *    all mutators are parked at safepoints so the barriers
     *    are not firing. */
    osty_gc_acquire();
    osty_gc_collect_now_with_stack_roots((void *const *)flat, total);
    osty_gc_release();

    if (flat != NULL) {
        free(flat);
    }

    /* 6. Release the stop. Mutators unpublish their roots and
     *    resume execution from the next line of the park. */
    osty_rt_sched_concurrent_stop_release_v1();
}

/* Per-phase pause instrumentation for the concurrent-incremental
 * cycle. Updated on each call to `collect_concurrent_incremental_v1`;
 * accessors below expose them to tests and to future metrics sinks
 * (tracing, benchmarks). The "pause" figures are what user-visible
 * latency sees: Phase A (start) + Phase C (finish + sweep). Phase B
 * is concurrent so it doesn't count toward stop-the-world time even
 * when the collector is running. Default zero until the first cycle
 * completes. */
static volatile int64_t osty_gc_concurrent_incremental_start_nanos = 0;
static volatile int64_t osty_gc_concurrent_incremental_mark_nanos = 0;
static volatile int64_t osty_gc_concurrent_incremental_finish_nanos = 0;
static volatile int64_t osty_gc_concurrent_incremental_cycles = 0;

/* Phase 3 step 2b — concurrent incremental collection driver.
 *
 * Upgrade of `osty_rt_gc_collect_concurrent_v1` that lets the MARK
 * phase overlap with mutator execution. The pause windows shrink to
 * just root-scan (start) and final-drain (finish); the bulk of the
 * cycle (actually walking the heap via the mark stack) happens
 * while mutators keep running.
 *
 * Phases, each numbered to match the existing `osty_gc_state` enum:
 *
 *   Phase A — STW START → MARK_INCREMENTAL
 *     stop_request + kick + wait_all_parked.
 *     Union parked roots + caller roots into a flat array.
 *     Under gc_lock: transition state IDLE → MARK_INCREMENTAL and
 *     seed the mark stack with all roots.
 *     stop_release. Mutators resume; SATB write barrier
 *     (`osty_gc_pre_write_v1`) is now active and greys any
 *     overwritten white value.
 *
 *   Phase B — concurrent MARK drain
 *     Loop calling `osty_gc_collect_incremental_step(budget)`
 *     until it reports the stack empty. The step function takes
 *     gc_lock for each drain batch, so mutators that take
 *     gc_lock (allocations, write barriers, root bind) interleave
 *     naturally. Mutator-assist in the allocator also pays down
 *     the queue in proportion to allocation pressure.
 *
 *   Phase C — STW FINISH → SWEEPING → IDLE
 *     stop_request + kick + wait_all_parked. The parked window
 *     is important: it closes the SATB "last drop" race where a
 *     mutator could grey one last value between our final step
 *     and the sweep.
 *     Call `osty_gc_collect_incremental_finish` which drains
 *     residual greys, flips state to SWEEPING, sweeps, returns
 *     to IDLE. Then stop_release.
 *
 * If the concurrent cycle has a reason to abort mid-flight (e.g.
 * another collect_concurrent request arrives from a different
 * thread), `osty_gc_collect_incremental_finish` is idempotent
 * enough to clean up from any MARK_INCREMENTAL state.
 */
void osty_rt_gc_collect_concurrent_incremental_v1(
    void *const *caller_roots, int64_t caller_count) {
    if (caller_count < 0) {
        caller_count = 0;
    }

    /* ---- Phase A: STW start ---- */
    int64_t t_phase_a_start = osty_gc_now_nanos();
    osty_rt_sched_concurrent_stop_request_v1();
    osty_rt_sched_kick_worker_v1(-1);

    osty_rt_mu_lock(&osty_gc_stop_mu);
    for (;;) {
        int64_t inflight =
            __atomic_load_n(&osty_concurrent_workers, __ATOMIC_ACQUIRE);
        if (osty_gc_parked_mutator_count >= inflight) {
            break;
        }
        osty_rt_cond_wait(&osty_gc_stop_cv, &osty_gc_stop_mu);
    }
    int64_t total = caller_count;
    for (osty_gc_parked_roots_entry *e = osty_gc_parked_roots_head;
         e != NULL; e = e->next) {
        total += e->count;
    }
    void **flat = NULL;
    if (total > 0) {
        flat = (void **)calloc((size_t)total, sizeof(void *));
        if (flat == NULL) {
            osty_rt_mu_unlock(&osty_gc_stop_mu);
            osty_rt_sched_concurrent_stop_release_v1();
            osty_rt_abort("gc.collect_concurrent_incremental: flat union OOM");
        }
        int64_t idx = 0;
        for (int64_t i = 0; i < caller_count; i++) {
            flat[idx++] = (void *)caller_roots[i];
        }
        for (osty_gc_parked_roots_entry *e = osty_gc_parked_roots_head;
             e != NULL; e = e->next) {
            for (int64_t i = 0; i < e->count; i++) {
                flat[idx++] = (void *)e->slots[i];
            }
        }
    }
    osty_rt_mu_unlock(&osty_gc_stop_mu);

    /* Seed the mark — deliberately bypasses the
     * `osty_concurrent_workers > 0` gate inside
     * `osty_gc_collect_incremental_start_with_stack_roots`, because
     * the stop handshake above already guarantees no mutator is
     * observing state. */
    osty_gc_acquire();
    if (osty_gc_state != OSTY_GC_STATE_IDLE) {
        /* Another concurrent cycle is mid-flight. Back off: we
         * cannot start a second. Release everything and return so
         * the caller can retry. */
        osty_gc_release();
        if (flat != NULL) free(flat);
        osty_rt_sched_concurrent_stop_release_v1();
        return;
    }
    osty_gc_state = OSTY_GC_STATE_MARK_INCREMENTAL;
    osty_gc_incremental_seed_roots((void *const *)flat, total);
    osty_gc_release();
    if (flat != NULL) free(flat);

    osty_rt_sched_concurrent_stop_release_v1();
    int64_t t_phase_a_end = osty_gc_now_nanos();

    /* ---- Phase B: concurrent mark drain ----
     *
     * Mutators are running again; SATB greys overwrites; mutator
     * assist drains at allocation time. We drain in budgeted
     * steps, yielding between each so mutators make progress. */
    const int64_t mark_budget = 1024;
    for (int64_t safety = 0; safety < (int64_t)1 << 30; safety++) {
        bool more = osty_gc_collect_incremental_step(mark_budget);
        if (!more) break;
        osty_rt_plat_yield();
    }
    int64_t t_phase_b_end = osty_gc_now_nanos();

    /* ---- Phase C: STW finish + sweep ---- */
    int64_t t_phase_c_start = osty_gc_now_nanos();
    osty_rt_sched_concurrent_stop_request_v1();
    osty_rt_sched_kick_worker_v1(-1);

    osty_rt_mu_lock(&osty_gc_stop_mu);
    for (;;) {
        int64_t inflight =
            __atomic_load_n(&osty_concurrent_workers, __ATOMIC_ACQUIRE);
        if (osty_gc_parked_mutator_count >= inflight) {
            break;
        }
        osty_rt_cond_wait(&osty_gc_stop_cv, &osty_gc_stop_mu);
    }
    osty_rt_mu_unlock(&osty_gc_stop_mu);

    osty_gc_collect_incremental_finish();

    osty_rt_sched_concurrent_stop_release_v1();
    int64_t t_phase_c_end = osty_gc_now_nanos();

    /* Publish per-phase timings. Overflows are impossible in
     * practice (int64 nanoseconds = ~292 years) so plain subtraction
     * is safe. Phase A + Phase C is the user-visible pause; Phase B
     * is the concurrent mark duration. */
    __atomic_store_n(&osty_gc_concurrent_incremental_start_nanos,
                     t_phase_a_end - t_phase_a_start,
                     __ATOMIC_RELEASE);
    __atomic_store_n(&osty_gc_concurrent_incremental_mark_nanos,
                     t_phase_b_end - t_phase_a_end,
                     __ATOMIC_RELEASE);
    __atomic_store_n(&osty_gc_concurrent_incremental_finish_nanos,
                     t_phase_c_end - t_phase_c_start,
                     __ATOMIC_RELEASE);
    __atomic_fetch_add(&osty_gc_concurrent_incremental_cycles, 1,
                       __ATOMIC_RELEASE);
}

/* Phase 3 step 2b — per-phase timing accessors.
 *
 * Return the nanoseconds spent in each phase of the *most recent*
 * concurrent-incremental cycle. Zero before the first cycle has
 * run; updated atomically at cycle end so a test can sample
 * outside the cycle safely. */
int64_t osty_gc_debug_concurrent_incremental_start_nanos(void) {
    return __atomic_load_n(&osty_gc_concurrent_incremental_start_nanos,
                           __ATOMIC_ACQUIRE);
}
int64_t osty_gc_debug_concurrent_incremental_mark_nanos(void) {
    return __atomic_load_n(&osty_gc_concurrent_incremental_mark_nanos,
                           __ATOMIC_ACQUIRE);
}
int64_t osty_gc_debug_concurrent_incremental_finish_nanos(void) {
    return __atomic_load_n(&osty_gc_concurrent_incremental_finish_nanos,
                           __ATOMIC_ACQUIRE);
}
int64_t osty_gc_debug_concurrent_incremental_cycles(void) {
    return __atomic_load_n(&osty_gc_concurrent_incremental_cycles,
                           __ATOMIC_ACQUIRE);
}

/* Async preemption kick. Sends SIGURG to pool worker `worker_id`
 * (or to every pool worker when `worker_id < 0`). The handler sets
 * a per-thread flag that the next safepoint observes and yields on.
 * Useful when a concurrent collector (or any external controller)
 * wants to force a fleet of busy workers to reach a safepoint
 * sooner than the emitter's stride would naturally allow — e.g. the
 * STW root-scan window wants every mutator parked as quickly as
 * possible.
 *
 * POSIX-only; Windows falls back to a no-op because there is no
 * SIGURG analogue. Windows builds can still rely on the normal
 * safepoint cadence to observe `stop_requested`; the mutators just
 * take up to `loopSafepointStride` extra iterations to notice. */
void osty_rt_sched_kick_worker_v1(int64_t worker_id) {
#if defined(OSTY_RT_PLATFORM_WIN32)
    (void)worker_id;
#else
    if (osty_sched_worker_threads == NULL) {
        return;
    }
    if (worker_id < 0) {
        for (int i = 0; i < osty_sched_worker_count; i++) {
            pthread_kill(osty_sched_worker_threads[i], SIGURG);
        }
        return;
    }
    if ((int)worker_id >= osty_sched_worker_count) {
        return;
    }
    pthread_kill(osty_sched_worker_threads[worker_id], SIGURG);
#endif
}

void osty_rt_thread_sleep(int64_t nanos) {
    if (nanos <= 0) {
        return;
    }
    osty_rt_sleep_ns((uint64_t)nanos);
}

/* ---- Channels: bounded ring buffer, mutex + two condition variables.
 *
 * Every channel slot is an int64_t holding either a scalar (i64/i1/f64
 * bits / ptr bits) or a GC-managed pointer copy of the sent bytes. The
 * element width encoding is the caller's responsibility — they reach
 * here through one of the typed suffixes (send_i64, send_f64, ...) so
 * the slot layout is uniform.
 */

typedef struct osty_rt_chan_recv_result {
    int64_t value;
    int64_t ok;
} osty_rt_chan_recv_result;

static void osty_rt_chan_sync_init_or_abort(osty_rt_chan_impl *ch,
                                            const char *op) {
    if (ch == NULL) {
        osty_rt_abort("thread.chan: null channel");
    }
    if (osty_rt_mu_init(&ch->mu) != 0 ||
        osty_rt_cond_init(&ch->not_full) != 0 ||
        osty_rt_cond_init(&ch->not_empty) != 0) {
        char buf[96];
        snprintf(buf, sizeof(buf), "%s: sync init failed", op);
        osty_rt_abort(buf);
    }
    ch->sync_init = 1;
}

static void osty_rt_chan_ensure_elem_kind(osty_rt_chan_impl *ch,
                                          int64_t elem_kind,
                                          const char *op) {
    if (ch == NULL) {
        osty_rt_abort("thread.chan: null channel");
    }
    if (ch->elem_kind == 0) {
        ch->elem_kind = elem_kind;
        return;
    }
    if (ch->elem_kind != elem_kind) {
        char buf[96];
        snprintf(buf, sizeof(buf), "%s: element kind mismatch", op);
        osty_rt_abort(buf);
    }
}

static void osty_rt_chan_trace(void *payload) {
    osty_rt_chan_impl *ch = (osty_rt_chan_impl *)payload;
    int64_t i;

    if (ch == NULL || ch->slots == NULL ||
        !osty_gc_chan_elem_kind_is_managed(ch->elem_kind)) {
        return;
    }
    /* Pass slot ADDRESSES to mark_slot_v1 so the CHENEY path can
     * forward young children + rewrite the slot. Slots are int64_t
     * holding ptr-cast values; mark_slot_v1's `memcpy(sizeof(void *))`
     * reads the pointer back. */
    for (i = 0; i < ch->count; i++) {
        int64_t idx = (ch->head + i) % ch->cap;
        osty_gc_mark_slot_v1((void *)&ch->slots[idx]);
    }
}

static osty_rt_chan_impl *osty_rt_chan_cast(void *ch, const char *op) {
    if (ch == NULL) {
        char buf[64];
        snprintf(buf, sizeof(buf), "%s: null channel", op);
        osty_rt_abort(buf);
    }
    return (osty_rt_chan_impl *)osty_gc_load_v1(ch);
}

static void osty_rt_chan_destroy(void *payload) {
    /* Invoked by the GC sweep when no Osty reference remains. Closing
     * is idempotent but we don't require the channel to be closed
     * first — unreachable implies no live sender or receiver. */
    osty_rt_chan_impl *ch = (osty_rt_chan_impl *)payload;
    if (ch == NULL) {
        return;
    }
    if (ch->sync_init) {
        osty_rt_mu_destroy(&ch->mu);
        osty_rt_cond_destroy(&ch->not_full);
        osty_rt_cond_destroy(&ch->not_empty);
        ch->sync_init = 0;
    }
    free(ch->slots);
    ch->slots = NULL;
}

void *osty_rt_thread_chan_make(int64_t capacity) {
    if (capacity < 0) {
        osty_rt_abort("thread.chan.make: negative capacity");
    }
    if (capacity == 0) {
        /* Rendezvous semantics (cap=0) require fiber handoff, Phase 1B.
         * Clamp to 1 so single-step producer/consumer still works. */
        capacity = 1;
    }
    /* Channel lives on the GC heap so it is reclaimed when no Osty
     * reference remains (including captures in spawned closures).
     * The `slots` buffer and the pthread sync objects are owned by
     * the channel and freed by osty_rt_chan_destroy on sweep. */
    osty_rt_chan_impl *ch = (osty_rt_chan_impl *)osty_gc_allocate_managed(
        sizeof(osty_rt_chan_impl), OSTY_GC_KIND_CHANNEL,
        "runtime.thread.chan", osty_rt_chan_trace, osty_rt_chan_destroy);
    ch->slots = (int64_t *)calloc((size_t)capacity, sizeof(int64_t));
    if (ch->slots == NULL) {
        osty_rt_abort("thread.chan.make: out of memory");
    }
    ch->cap = capacity;
    osty_rt_chan_sync_init_or_abort(ch, "thread.chan.make");
    return ch;
}

void osty_rt_thread_chan_close(void *raw) {
    osty_rt_chan_impl *ch = osty_rt_chan_cast(raw, "thread.chan.close");
    osty_rt_mu_lock(&ch->mu);
    ch->closed = 1;
    osty_rt_cond_broadcast(&ch->not_empty);
    osty_rt_cond_broadcast(&ch->not_full);
    osty_rt_mu_unlock(&ch->mu);
}

bool osty_rt_thread_chan_is_closed(void *raw) {
    osty_rt_chan_impl *ch = osty_rt_chan_cast(raw, "thread.chan.is_closed");
    osty_rt_mu_lock(&ch->mu);
    bool closed = ch->closed != 0;
    osty_rt_mu_unlock(&ch->mu);
    return closed;
}

static void osty_rt_chan_send_raw(osty_rt_chan_impl *ch, int64_t slot) {
    osty_rt_mu_lock(&ch->mu);
    if (ch->count == ch->cap && !ch->closed) {
        /* About to park this worker for an indeterminate time. If
         * we're a pool worker and the inject queue has work, hand
         * off to an elastic helper so queued tasks aren't stranded
         * behind our blocked stack. */
        osty_rt_mu_unlock(&ch->mu);
        osty_sched_notify_blocking_wait();
        osty_rt_mu_lock(&ch->mu);
    }
    while (ch->count == ch->cap && !ch->closed) {
        osty_rt_cond_wait(&ch->not_full, &ch->mu);
    }
    if (ch->closed) {
        osty_rt_mu_unlock(&ch->mu);
        osty_rt_abort("thread.chan.send: send on closed channel");
    }
    ch->slots[ch->tail] = slot;
    ch->tail = (ch->tail + 1) % ch->cap;
    ch->count += 1;
    osty_rt_cond_signal(&ch->not_empty);
    osty_rt_mu_unlock(&ch->mu);
}

static osty_rt_chan_recv_result osty_rt_chan_recv_raw(osty_rt_chan_impl *ch) {
    osty_rt_chan_recv_result r = {0, 0};
    osty_rt_mu_lock(&ch->mu);
    if (ch->count == 0 && !ch->closed) {
        osty_rt_mu_unlock(&ch->mu);
        osty_sched_notify_blocking_wait();
        osty_rt_mu_lock(&ch->mu);
    }
    while (ch->count == 0 && !ch->closed) {
        osty_rt_cond_wait(&ch->not_empty, &ch->mu);
    }
    if (ch->count == 0 && ch->closed) {
        osty_rt_mu_unlock(&ch->mu);
        return r;
    }
    r.value = ch->slots[ch->head];
    r.ok = 1;
    ch->head = (ch->head + 1) % ch->cap;
    ch->count -= 1;
    osty_rt_cond_signal(&ch->not_full);
    osty_rt_mu_unlock(&ch->mu);
    return r;
}

void osty_rt_thread_chan_send_i64(void *raw, int64_t value) {
    osty_rt_chan_impl *ch = osty_rt_chan_cast(raw, "thread.chan.send");
    osty_rt_chan_ensure_elem_kind(ch, OSTY_RT_ABI_I64, "thread.chan.send");
    osty_rt_chan_send_raw(ch, value);
}

void osty_rt_thread_chan_send_i1(void *raw, bool value) {
    osty_rt_chan_impl *ch = osty_rt_chan_cast(raw, "thread.chan.send");
    osty_rt_chan_ensure_elem_kind(ch, OSTY_RT_ABI_I1, "thread.chan.send");
    osty_rt_chan_send_raw(ch, (int64_t)(value ? 1 : 0));
}

void osty_rt_thread_chan_send_f64(void *raw, double value) {
    int64_t bits = 0;
    osty_rt_chan_impl *ch = osty_rt_chan_cast(raw, "thread.chan.send");
    memcpy(&bits, &value, sizeof(bits));
    osty_rt_chan_ensure_elem_kind(ch, OSTY_RT_ABI_F64, "thread.chan.send");
    osty_rt_chan_send_raw(ch, bits);
}

void osty_rt_thread_chan_send_ptr(void *raw, void *value) {
    osty_rt_chan_impl *ch = osty_rt_chan_cast(raw, "thread.chan.send");
    osty_rt_chan_ensure_elem_kind(ch, OSTY_RT_ABI_PTR, "thread.chan.send");
    osty_rt_chan_send_raw(ch, (int64_t)(uintptr_t)value);
}

void osty_rt_thread_chan_send_bytes_v1(void *raw, const void *src, int64_t sz) {
    osty_rt_chan_impl *ch = osty_rt_chan_cast(raw, "thread.chan.send");
    if (sz < 0) {
        osty_rt_abort("thread.chan.send: negative byte size");
    }
    /* Copy into a GC-managed allocation so receiver sees a stable owner
     * even after the producer's frame is gone. The collector will reap
     * it once the receiver drops the pointer. */
    void *copy = osty_gc_allocate_managed((size_t)sz, OSTY_GC_KIND_GENERIC,
                                          "runtime.chan.bytes", NULL, NULL);
    if (sz > 0 && src != NULL) {
        memcpy(copy, src, (size_t)sz);
    }
    osty_rt_chan_ensure_elem_kind(ch, OSTY_RT_ABI_PTR, "thread.chan.send");
    osty_rt_chan_send_raw(ch, (int64_t)(uintptr_t)copy);
}

osty_rt_chan_recv_result osty_rt_thread_chan_recv_i64(void *raw) {
    osty_rt_chan_impl *ch = osty_rt_chan_cast(raw, "thread.chan.recv");
    osty_rt_chan_ensure_elem_kind(ch, OSTY_RT_ABI_I64, "thread.chan.recv");
    return osty_rt_chan_recv_raw(ch);
}

osty_rt_chan_recv_result osty_rt_thread_chan_recv_i1(void *raw) {
    osty_rt_chan_impl *ch = osty_rt_chan_cast(raw, "thread.chan.recv");
    osty_rt_chan_ensure_elem_kind(ch, OSTY_RT_ABI_I1, "thread.chan.recv");
    osty_rt_chan_recv_result r = osty_rt_chan_recv_raw(ch);
    if (r.ok) {
        r.value = r.value != 0 ? 1 : 0;
    }
    return r;
}

osty_rt_chan_recv_result osty_rt_thread_chan_recv_f64(void *raw) {
    osty_rt_chan_impl *ch = osty_rt_chan_cast(raw, "thread.chan.recv");
    osty_rt_chan_ensure_elem_kind(ch, OSTY_RT_ABI_F64, "thread.chan.recv");
    return osty_rt_chan_recv_raw(ch);
}

osty_rt_chan_recv_result osty_rt_thread_chan_recv_ptr(void *raw) {
    osty_rt_chan_impl *ch = osty_rt_chan_cast(raw, "thread.chan.recv");
    osty_rt_chan_recv_result r;
    osty_rt_chan_ensure_elem_kind(ch, OSTY_RT_ABI_PTR, "thread.chan.recv");
    r = osty_rt_chan_recv_raw(ch);
    if (r.ok) {
        r.value = (int64_t)(uintptr_t)osty_gc_load_v1((void *)(uintptr_t)r.value);
    }
    return r;
}

osty_rt_chan_recv_result osty_rt_thread_chan_recv_bytes_v1(void *raw) {
    osty_rt_chan_impl *ch = osty_rt_chan_cast(raw, "thread.chan.recv");
    osty_rt_chan_recv_result r;
    osty_rt_chan_ensure_elem_kind(ch, OSTY_RT_ABI_PTR, "thread.chan.recv");
    r = osty_rt_chan_recv_raw(ch);
    if (r.ok) {
        r.value = (int64_t)(uintptr_t)osty_gc_load_v1((void *)(uintptr_t)r.value);
    }
    return r;
}

/* ---- Select: polling builder with recv / send / timeout / default arms.
 *
 * Surface contract:
 *   osty_rt_select(body_env)                            // 1-arg
 *   osty_rt_select_recv(s, ch, arm)                     // 3-arg
 *   osty_rt_select_send_<suffix>(s, ch, value, arm)     // 4-arg scalar; suffix ∈ i64/i1/f64/ptr
 *   osty_rt_select_send_bytes_v1(s, ch, src, size, arm) // 5-arg composite
 *   osty_rt_select_timeout(s, ns, arm)                  // 3-arg
 *   osty_rt_select_default(s, arm)                      // 2-arg
 *
 * `body_env` is a closure env whose slot-0 is the body function. The
 * body is invoked as `body(env, s)` where `s` is a stack-allocated
 * builder this function owns. Arms the body registers are evaluated
 * non-blockingly in their registration order; if none is ready we
 * sleep 500μs and retry, firing the first timeout arm whose deadline
 * elapses. A default arm fires immediately if present and no other
 * arm is ready on the first pass.
 *
 * Arm closures are void-returning; the Osty frontend is expected to
 * capture the result slot inside the closure (same pattern as
 * taskGroup body's result propagation). Recv-arm closures receive
 * the drained value as their second argument. Send-arm closures take
 * no args — they are invoked after the value has been enqueued, the
 * "Go-case-body-after-arrow" moral equivalent of `case ch <- v: body`.
 *
 * The scalar variants funnel through a single int64 slot (matching
 * the `osty_rt_chan_send_*` lane); composite values go through the
 * bytes_v1 route which copies into a GC-managed buffer before
 * queueing — same contract as `osty_rt_thread_chan_send_bytes_v1`.
 */

typedef enum {
    OSTY_RT_SELECT_ARM_RECV = 0,
    OSTY_RT_SELECT_ARM_TIMEOUT = 1,
    OSTY_RT_SELECT_ARM_DEFAULT = 2,
    OSTY_RT_SELECT_ARM_SEND = 3,
} osty_rt_select_arm_kind;

typedef struct osty_rt_select_arm {
    osty_rt_select_arm_kind kind;
    void *ch;              /* recv/send: channel ptr; otherwise NULL */
    int64_t timeout_ns;    /* timeout arm only */
    int64_t send_value;    /* send arm only — scalar bits or ptr to GC copy */
    void *arm_env;         /* closure env; send arms run it plain after the enqueue */
} osty_rt_select_arm;

#define OSTY_RT_SELECT_ARM_CAPACITY 32

typedef struct osty_rt_select_impl {
    osty_rt_mu_t mu;
    osty_rt_select_arm arms[OSTY_RT_SELECT_ARM_CAPACITY];
    int count;
} osty_rt_select_impl;

typedef void (*osty_rt_select_body_fn)(void *env, void *s);
typedef void (*osty_rt_select_recv_arm_fn)(void *env, int64_t value);
typedef void (*osty_rt_select_plain_arm_fn)(void *env);

static osty_rt_select_impl *osty_rt_select_cast(void *s, const char *op) {
    if (s == NULL) {
        char buf[64];
        snprintf(buf, sizeof(buf), "%s: null builder", op);
        osty_rt_abort(buf);
    }
    return (osty_rt_select_impl *)s;
}

static void osty_rt_select_arm_push(osty_rt_select_impl *sel,
                                    osty_rt_select_arm_kind kind,
                                    void *ch, int64_t timeout_ns,
                                    int64_t send_value,
                                    void *arm_env) {
    osty_rt_mu_lock(&sel->mu);
    if (sel->count >= OSTY_RT_SELECT_ARM_CAPACITY) {
        osty_rt_mu_unlock(&sel->mu);
        osty_rt_abort("thread.select: arm capacity exceeded");
    }
    sel->arms[sel->count].kind = kind;
    sel->arms[sel->count].ch = ch;
    sel->arms[sel->count].timeout_ns = timeout_ns;
    sel->arms[sel->count].send_value = send_value;
    sel->arms[sel->count].arm_env = arm_env;
    sel->count += 1;
    osty_rt_mu_unlock(&sel->mu);
}

void osty_rt_select_recv(void *s, void *ch, void *arm) {
    osty_rt_select_impl *sel = osty_rt_select_cast(s, "thread.select.recv");
    if (ch == NULL) {
        osty_rt_abort("thread.select.recv: null channel");
    }
    osty_rt_select_arm_push(sel, OSTY_RT_SELECT_ARM_RECV, ch, 0, 0, arm);
}

static void osty_rt_select_send_common(void *s, void *ch, int64_t value,
                                       int64_t elem_kind, void *arm) {
    osty_rt_select_impl *sel = osty_rt_select_cast(s, "thread.select.send");
    osty_rt_chan_impl *chan;
    if (ch == NULL) {
        osty_rt_abort("thread.select.send: null channel");
    }
    chan = osty_rt_chan_cast(ch, "thread.select.send");
    osty_rt_chan_ensure_elem_kind(chan, elem_kind, "thread.select.send");
    osty_rt_select_arm_push(sel, OSTY_RT_SELECT_ARM_SEND, ch, 0, value, arm);
}

void osty_rt_select_send_i64(void *s, void *ch, int64_t value, void *arm) {
    osty_rt_select_send_common(s, ch, value, OSTY_RT_ABI_I64, arm);
}

void osty_rt_select_send_i1(void *s, void *ch, bool value, void *arm) {
    osty_rt_select_send_common(s, ch, (int64_t)(value ? 1 : 0),
                               OSTY_RT_ABI_I1, arm);
}

void osty_rt_select_send_f64(void *s, void *ch, double value, void *arm) {
    int64_t bits = 0;
    memcpy(&bits, &value, sizeof(bits));
    osty_rt_select_send_common(s, ch, bits, OSTY_RT_ABI_F64, arm);
}

void osty_rt_select_send_ptr(void *s, void *ch, void *value, void *arm) {
    osty_rt_select_send_common(s, ch, (int64_t)(uintptr_t)value,
                               OSTY_RT_ABI_PTR, arm);
}

void osty_rt_select_send_bytes_v1(void *s, void *ch, const void *src,
                                  int64_t sz, void *arm) {
    if (sz < 0) {
        osty_rt_abort("thread.select.send: negative byte size");
    }
    /* Same contract as osty_rt_thread_chan_send_bytes_v1 — copy into a
     * GC-managed buffer so the receiver's pointer survives the sender's
     * frame exit. */
    void *copy = osty_gc_allocate_managed((size_t)sz, OSTY_GC_KIND_GENERIC,
                                          "runtime.select.bytes", NULL, NULL);
    if (sz > 0 && src != NULL) {
        memcpy(copy, src, (size_t)sz);
    }
    osty_rt_select_send_common(s, ch, (int64_t)(uintptr_t)copy,
                               OSTY_RT_ABI_PTR, arm);
}

void osty_rt_select_timeout(void *s, int64_t ns, void *arm) {
    osty_rt_select_impl *sel = osty_rt_select_cast(s, "thread.select.timeout");
    if (ns < 0) {
        osty_rt_abort("thread.select.timeout: negative duration");
    }
    osty_rt_select_arm_push(sel, OSTY_RT_SELECT_ARM_TIMEOUT, NULL, ns, 0, arm);
}

void osty_rt_select_default(void *s, void *arm) {
    osty_rt_select_impl *sel = osty_rt_select_cast(s, "thread.select.default");
    osty_rt_select_arm_push(sel, OSTY_RT_SELECT_ARM_DEFAULT, NULL, 0, 0, arm);
}

static int osty_rt_select_try_recv(osty_rt_chan_impl *ch, int64_t *out) {
    osty_rt_mu_lock(&ch->mu);
    if (ch->count > 0) {
        *out = ch->slots[ch->head];
        ch->head = (ch->head + 1) % ch->cap;
        ch->count -= 1;
        osty_rt_cond_signal(&ch->not_full);
        osty_rt_mu_unlock(&ch->mu);
        return 1;
    }
    osty_rt_mu_unlock(&ch->mu);
    return 0;
}

/* Non-blocking enqueue: enqueue into a channel if there is capacity and
 * the channel is not closed. Returns 1 on success, 0 if full. Aborts on
 * a closed channel — the same loudness the blocking send path gives,
 * because the Osty semantics treat send-after-close as a programmer
 * error regardless of the select arm it was registered on. */
static int osty_rt_select_try_send(osty_rt_chan_impl *ch, int64_t value) {
    osty_rt_mu_lock(&ch->mu);
    if (ch->closed) {
        osty_rt_mu_unlock(&ch->mu);
        osty_rt_abort("thread.select.send: send on closed channel");
    }
    if (ch->count < ch->cap) {
        ch->slots[ch->tail] = value;
        ch->tail = (ch->tail + 1) % ch->cap;
        ch->count += 1;
        osty_rt_cond_signal(&ch->not_empty);
        osty_rt_mu_unlock(&ch->mu);
        return 1;
    }
    osty_rt_mu_unlock(&ch->mu);
    return 0;
}

static void osty_rt_select_invoke_recv(osty_rt_select_arm *arm, int64_t value) {
    osty_rt_select_recv_arm_fn fn =
        (osty_rt_select_recv_arm_fn)(*(void **)arm->arm_env);
    fn(arm->arm_env, value);
}

static void osty_rt_select_invoke_plain(osty_rt_select_arm *arm) {
    osty_rt_select_plain_arm_fn fn =
        (osty_rt_select_plain_arm_fn)(*(void **)arm->arm_env);
    fn(arm->arm_env);
}

void osty_rt_select(void *body_env) {
    if (body_env == NULL) {
        osty_rt_abort("thread.select: null body env");
    }

    osty_rt_select_impl sel;
    sel.count = 0;
    if (osty_rt_mu_init(&sel.mu) != 0) {
        osty_rt_abort("thread.select: mutex init failed");
    }

    /* Invoke the body so it can register arms. Body signature matches
     * the taskGroup convention: body(env, builder). */
    osty_rt_select_body_fn fn =
        (osty_rt_select_body_fn)(*(void **)body_env);
    fn(body_env, (void *)&sel);

    if (sel.count == 0) {
        osty_rt_mu_destroy(&sel.mu);
        osty_rt_abort("thread.select: body registered no arms");
    }

    /* Classify arms. Shortest timeout wins; a default fires on the
     * first unsuccessful poll. */
    int default_idx = -1;
    int timeout_idx = -1;
    int64_t shortest_timeout_ns = INT64_MAX;
    for (int i = 0; i < sel.count; i++) {
        if (sel.arms[i].kind == OSTY_RT_SELECT_ARM_DEFAULT) {
            default_idx = i;
        } else if (sel.arms[i].kind == OSTY_RT_SELECT_ARM_TIMEOUT) {
            if (sel.arms[i].timeout_ns < shortest_timeout_ns) {
                shortest_timeout_ns = sel.arms[i].timeout_ns;
                timeout_idx = i;
            }
        }
    }

    uint64_t deadline_ns = 0;
    bool has_deadline = false;
    if (timeout_idx >= 0) {
        deadline_ns = osty_rt_monotonic_ns() +
                      (uint64_t)sel.arms[timeout_idx].timeout_ns;
        has_deadline = true;
    }

    for (;;) {
        /* Poll recv/send arms in registration order. A send arm fires
         * when the channel has room; a recv arm when there is at least
         * one queued value. The first arm that succeeds wins. */
        for (int i = 0; i < sel.count; i++) {
            osty_rt_select_arm *arm = &sel.arms[i];
            if (arm->kind == OSTY_RT_SELECT_ARM_RECV) {
                osty_rt_chan_impl *ch = (osty_rt_chan_impl *)arm->ch;
                int64_t value = 0;
                if (osty_rt_select_try_recv(ch, &value)) {
                    osty_rt_mu_destroy(&sel.mu);
                    osty_rt_select_invoke_recv(arm, value);
                    return;
                }
            } else if (arm->kind == OSTY_RT_SELECT_ARM_SEND) {
                osty_rt_chan_impl *ch = (osty_rt_chan_impl *)arm->ch;
                if (osty_rt_select_try_send(ch, arm->send_value)) {
                    osty_rt_mu_destroy(&sel.mu);
                    if (arm->arm_env != NULL) {
                        osty_rt_select_invoke_plain(arm);
                    }
                    return;
                }
            }
        }

        /* Default arm fires if no recv/send is ready on the first pass. */
        if (default_idx >= 0) {
            osty_rt_mu_destroy(&sel.mu);
            osty_rt_select_invoke_plain(&sel.arms[default_idx]);
            return;
        }

        /* Timeout check. */
        if (has_deadline && osty_rt_monotonic_ns() >= deadline_ns) {
            osty_rt_mu_destroy(&sel.mu);
            osty_rt_select_invoke_plain(&sel.arms[timeout_idx]);
            return;
        }

        /* 500μs backoff to avoid busy-spinning. A real fiber scheduler
         * would park on a multi-cond primitive; Phase 1B polling is
         * the cost of sharing the OS thread pool. Windows Sleep has
         * ms granularity, which osty_rt_sleep_ns rounds up to 1ms. */
        osty_rt_sleep_ns(500000ULL);
    }
}

/* ---- Concurrency helpers: collectAll / race / parallel --------------
 *
 * Closure ABI: the body's env slot 0 holds the fn pointer.
 *   - collectAll / race: body shape is
 *       `void *body(void *env, void *group)`
 *     returning a raw `List<Handle<T>>` pointer (i.e. the user's
 *     `fn(Group) -> List<Handle<T>>`).
 *   - parallel: items is a `List<T>` with elem_size == 8; f's env
 *     slot 0 holds a fn with signature
 *       `int64_t f(void *env, int64_t item_bits)`.
 *
 * Result layout: `{ i64 disc, i64 payload }` per §mir enum layout,
 * with Ok = 1 and Err = 0 (lowerer's variantIndexByName contract).
 * Output lists therefore carry elem_size = 16 and NULL trace_elem —
 * the conservative choice that matches `List<Result<scalar-T, Error>>`
 * for the 8-byte-wide T the rest of the Phase 1B/2 scheduler already
 * restricts itself to. Pointer-payload T relies on the returned list
 * being held via a regular GC root while the payload survives; this
 * matches the existing handle_join ABI assumption.
 */

#define OSTY_RT_RESULT_OK_DISC  1
#define OSTY_RT_RESULT_ERR_DISC 0

typedef struct osty_rt_result_enum_v1 {
    int64_t disc;
    int64_t payload;
} osty_rt_result_enum_v1;

typedef int64_t (*osty_rt_parallel_body_fn)(void *env, int64_t item);

static void osty_rt_group_init(osty_rt_task_group_impl *g) {
    g->cancelled = 0;
    g->cause = NULL;
    g->children = NULL;
    if (osty_rt_mu_init(&g->children_mu) != 0) {
        osty_rt_abort("concurrency helper: mutex init failed");
    }
}

/* collectAll: run body in a fresh group, join every handle the body
 * returned, package each join result as Ok(v), and return the
 * resulting List<Result<T, Error>>. Stray children (spawned into the
 * group but not surfaced in the returned list) are reaped on exit. */
void *osty_rt_task_collect_all(void *body_env) {
    if (body_env == NULL) {
        osty_rt_abort("collectAll: null body env");
    }
    osty_rt_task_group_impl group;
    osty_rt_group_init(&group);

    osty_rt_task_group_impl *prev = osty_sched_current_group;
    osty_sched_current_group = &group;

    osty_task_group_body_fn fn = (osty_task_group_body_fn)(*(void **)body_env);
    void *handles_list = (void *)(intptr_t)fn(body_env, (void *)&group);

    void *out = osty_rt_list_new();
    int64_t n = handles_list != NULL ? osty_rt_list_len(handles_list) : 0;
    for (int64_t i = 0; i < n; i++) {
        /* `osty_rt_list_get_ptr` folds the trace-kind check +
         * GC read barrier into a single helper; it replaces the
         * earlier memcpy/list_get_raw/mark_slot_v1 incantation. */
        void *handle = osty_rt_list_get_ptr(handles_list, i);
        osty_rt_result_enum_v1 r;
        r.disc = OSTY_RT_RESULT_OK_DISC;
        r.payload = osty_rt_task_handle_join(handle);
        osty_rt_list_push_raw(out, &r, sizeof(r), NULL);
    }

    /* Any child the body spawned into the group but did not surface
     * in the returned list (spec §8.1 structured lifetime). */
    osty_rt_task_group_reap(&group);
    osty_rt_mu_destroy(&group.children_mu);

    osty_sched_current_group = prev;
    return out;
}

/* race: run body in a fresh group, poll handle done flags, cancel
 * the group on first completion, reap stragglers cooperatively, and
 * return Ok(winner's result). Polling runs at a 500μs cadence (same
 * as select's recv loop) — real per-handle semaphore parking lands
 * with Phase 1B fibers. */
osty_rt_result_enum_v1 osty_rt_task_race(void *body_env) {
    if (body_env == NULL) {
        osty_rt_abort("race: null body env");
    }
    osty_rt_task_group_impl group;
    osty_rt_group_init(&group);

    osty_rt_task_group_impl *prev = osty_sched_current_group;
    osty_sched_current_group = &group;

    osty_task_group_body_fn fn = (osty_task_group_body_fn)(*(void **)body_env);
    void *handles_list = (void *)(intptr_t)fn(body_env, (void *)&group);

    int64_t n = handles_list != NULL ? osty_rt_list_len(handles_list) : 0;
    if (n <= 0) {
        osty_rt_task_group_reap(&group);
        osty_rt_mu_destroy(&group.children_mu);
        osty_sched_current_group = prev;
        osty_rt_abort("race: body returned no handles");
    }

    /* Snapshot the handle array — the list is GC-visible and we
     * don't want to re-read it in the polling loop. */
    osty_rt_task_handle_impl **handles =
        (osty_rt_task_handle_impl **)calloc((size_t)n, sizeof(*handles));
    if (handles == NULL) {
        osty_rt_abort("race: out of memory");
    }
    for (int64_t i = 0; i < n; i++) {
        handles[i] =
            (osty_rt_task_handle_impl *)osty_rt_list_get_ptr(handles_list, i);
    }

    int64_t winner_idx = -1;
    while (winner_idx < 0) {
        for (int64_t i = 0; i < n; i++) {
            osty_rt_task_handle_impl *h = handles[i];
            if (h != NULL && __atomic_load_n(&h->done, __ATOMIC_ACQUIRE)) {
                winner_idx = i;
                break;
            }
        }
        if (winner_idx < 0) {
            osty_rt_sleep_ns(500000ULL);
        }
    }

    /* Broadcast cancel: siblings observe at the next cooperative
     * yield (channel op, sleep, explicit checkCancelled, join). */
    __atomic_store_n(&group.cancelled, 1, __ATOMIC_RELEASE);

    osty_rt_result_enum_v1 winner;
    winner.disc = OSTY_RT_RESULT_OK_DISC;
    winner.payload = osty_rt_task_handle_join((void *)handles[winner_idx]);

    /* Reap reaps via pthread_join — guarantees no sibling outlives
     * the race() scope even if it ignores cancellation. */
    osty_rt_task_group_reap(&group);
    free(handles);
    osty_rt_mu_destroy(&group.children_mu);

    osty_sched_current_group = prev;
    return winner;
}

typedef struct osty_rt_parallel_ctx {
    void *items;                 /* raw List<T>, elem_size == 8 */
    void *out;                   /* raw List<Result<R, Error>>, elem_size 16 */
    void *f_env;                 /* closure env for f: fn(T) -> R */
    volatile int64_t next_index;
    int64_t total;
} osty_rt_parallel_ctx;

typedef struct osty_rt_parallel_worker_env {
    void *fn;                    /* osty_rt_parallel_worker_body */
    osty_rt_parallel_ctx *ctx;
} osty_rt_parallel_worker_env;

static void osty_rt_parallel_init_out_list(void *raw_list, int64_t count) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    size_t bytes = 0;

    if (count < 0) {
        osty_rt_abort("parallel: negative output length");
    }
    osty_rt_list_ensure_layout(list, sizeof(osty_rt_result_enum_v1), NULL);
    osty_rt_list_reserve(list, count);
    if (count > 0) {
        bytes = (size_t)count * sizeof(osty_rt_result_enum_v1);
        memset(list->data, 0, bytes);
    }
    list->len = count;
}

static int64_t osty_rt_parallel_worker_body(void *env) {
    osty_rt_parallel_worker_env *we = (osty_rt_parallel_worker_env *)env;
    osty_rt_parallel_ctx *ctx = we->ctx;
    osty_rt_parallel_body_fn f =
        (osty_rt_parallel_body_fn)(*(void **)ctx->f_env);
    for (;;) {
        /* Cooperative cancellation: if the enclosing group cancelled,
         * stop claiming work. Slots we skipped keep their zero sentinel
         * (disc=0 = Err, payload=0) so the caller sees an unfinished
         * entry rather than random memory. */
        if (osty_rt_cancel_is_cancelled()) {
            return 0;
        }
        int64_t idx = __atomic_fetch_add(&ctx->next_index, 1, __ATOMIC_RELAXED);
        if (idx >= ctx->total) {
            return 0;
        }
        osty_rt_list *items_list = osty_rt_list_cast(ctx->items);
        osty_rt_list *out_list = osty_rt_list_cast(ctx->out);
        int64_t item = 0;
        memcpy(&item,
               items_list->data + ((size_t)idx * sizeof(item)),
               sizeof(item));
        int64_t result_bits = f(ctx->f_env, item);
        osty_rt_result_enum_v1 r;
        r.disc = OSTY_RT_RESULT_OK_DISC;
        r.payload = result_bits;
        /* Concurrent writes at distinct indices are safe: the output
         * list is pre-sized up front, workers never reserve/append, and
         * the slots do not overlap. Re-casting each iteration keeps GC
         * forwarding / compaction semantics intact without re-running
         * the full list_get/list_set helper stack on every item. */
        memcpy(out_list->data + ((size_t)idx * sizeof(r)), &r, sizeof(r));
    }
}

/* parallel(items, concurrency, f): bounded-concurrency map. Workers
 * pull indices from a shared atomic counter and write Ok(f(item)) into
 * a pre-sized output list. Restrictions: items' element size must be
 * <= 8 bytes (same constraint the rest of the Phase 1B/2 scheduler
 * carries via the int64_t task body ABI). */
void *osty_rt_parallel(void *items, int64_t concurrency, void *f_env) {
    if (items == NULL) {
        osty_rt_abort("parallel: null items list");
    }
    if (f_env == NULL) {
        osty_rt_abort("parallel: null closure env");
    }
    osty_rt_list *items_list = osty_rt_list_cast(items);
    if (items_list->elem_size != 0 && items_list->elem_size != sizeof(int64_t)) {
        osty_rt_abort("parallel: element size > 8 bytes not supported yet");
    }
    int64_t n = items_list->len;

    void *out = osty_rt_list_new();
    osty_rt_parallel_init_out_list(out, n);
    if (n == 0) {
        return out;
    }

    int64_t workers = concurrency;
    if (workers <= 0) {
        workers = 1;
    }
    if (workers > n) {
        workers = n;
    }

    osty_rt_parallel_ctx ctx;
    ctx.items = items;
    ctx.out = out;
    ctx.f_env = f_env;
    ctx.next_index = 0;
    ctx.total = n;

    osty_rt_parallel_worker_env *envs =
        (osty_rt_parallel_worker_env *)calloc((size_t)workers, sizeof(*envs));
    void **handles = (void **)calloc((size_t)workers, sizeof(*handles));
    if (envs == NULL || handles == NULL) {
        free(envs);
        free(handles);
        osty_rt_abort("parallel: out of memory");
    }

    /* Workers inherit the caller's current group so that if parallel
     * runs inside a taskGroup / race / collectAll, cancellation from
     * that outer scope reaches them via osty_rt_cancel_is_cancelled.
     * NULL (caller is top-level) degrades to a detached spawn. */
    osty_rt_task_group_impl *inherited = osty_sched_current_group;
    for (int64_t i = 0; i < workers; i++) {
        envs[i].fn = (void *)osty_rt_parallel_worker_body;
        envs[i].ctx = &ctx;
        handles[i] = osty_rt_task_spawn_internal(inherited, &envs[i]);
    }
    for (int64_t i = 0; i < workers; i++) {
        (void)osty_rt_task_handle_join(handles[i]);
    }

    free(envs);
    free(handles);
    return out;
}

/* ============================================================
 * Snapshot / structural-diff helpers
 *
 * `osty_rt_strings_DiffLines` and `osty_rt_test_snapshot` implement
 * `testing.assertEq`'s String-vs-String diff and `testing.snapshot`
 * respectively. Both live in the runtime so the compiler only needs
 * to emit a single call instruction — no per-test boilerplate, and
 * the OSTY_UPDATE_SNAPSHOTS switch works without recompiling.
 * ============================================================ */

#define OSTY_UPDATE_SNAPSHOTS_ENV "OSTY_UPDATE_SNAPSHOTS"
#define OSTY_SNAPSHOT_DIR_ENV     "OSTY_SNAPSHOT_DIR"
#define OSTY_SNAPSHOT_SUBDIR      "__snapshots__"
#define OSTY_SNAPSHOT_SUFFIX      ".snap"
#define OSTY_SNAPSHOT_DEFAULT_STEM "snapshot"

/* osty_rt_xmalloc is the local OOM-or-abort shape used by the
 * snapshot helpers so each call site stays a single line. `site`
 * tags the abort for debuggability and matches the naming
 * convention of osty_rt_string_dup_site. */
static void *osty_rt_xmalloc(size_t n, const char *site) {
    void *p = malloc(n);
    if (p == NULL) {
        osty_rt_abort(site);
    }
    return p;
}

typedef struct osty_rt_diff_scratch {
    char  *data;
    size_t len;
    size_t cap;
} osty_rt_diff_scratch;

static void osty_rt_diff_scratch_init(osty_rt_diff_scratch *s) {
    s->data = NULL;
    s->len = 0;
    s->cap = 0;
}

static void osty_rt_diff_scratch_reserve(osty_rt_diff_scratch *s, size_t want) {
    if (s->cap >= want + 1) {
        return;
    }
    size_t cap = s->cap == 0 ? 64 : s->cap * 2;
    while (cap < want + 1) {
        cap *= 2;
    }
    char *next = (char *)realloc(s->data, cap);
    if (next == NULL) {
        osty_rt_abort("diff scratch: out of memory");
    }
    s->data = next;
    s->cap = cap;
}

static void osty_rt_diff_scratch_append(osty_rt_diff_scratch *s, const char *src, size_t n) {
    if (n == 0) {
        return;
    }
    osty_rt_diff_scratch_reserve(s, s->len + n);
    memcpy(s->data + s->len, src, n);
    s->len += n;
}

static void osty_rt_diff_scratch_append_cstr(osty_rt_diff_scratch *s, const char *src) {
    if (src == NULL) {
        return;
    }
    osty_rt_diff_scratch_append(s, src, strlen(src));
}

/* Split a '\n'-terminated string into (offset, length) pairs. A
 * trailing newline does NOT spawn a synthetic empty line, matching
 * how `diff` and Unified Patch render. Offsets are kept so the
 * suffix-trim walk stays O(n) rather than O(n²) via running-sum
 * recomputation. Caller frees both arrays. */
static void osty_rt_diff_grow_pair(size_t **offs, size_t **lens, size_t *cap) {
    *cap *= 2;
    size_t *grown_offs = (size_t *)realloc(*offs, *cap * sizeof(**offs));
    size_t *grown_lens = (size_t *)realloc(*lens, *cap * sizeof(**lens));
    if (grown_offs == NULL || grown_lens == NULL) {
        free(grown_offs != NULL ? grown_offs : *offs);
        free(grown_lens != NULL ? grown_lens : *lens);
        osty_rt_abort("diff split: grow failed");
    }
    *offs = grown_offs;
    *lens = grown_lens;
}

static void osty_rt_diff_split_lines(const char *s, size_t **out_offs, size_t **out_lens, size_t *out_count) {
    size_t n = (s == NULL) ? 0 : strlen(s);
    size_t cap = 16;
    size_t *offs = (size_t *)osty_rt_xmalloc(cap * sizeof(*offs), "diff split: out of memory");
    size_t *lens = (size_t *)osty_rt_xmalloc(cap * sizeof(*lens), "diff split: out of memory");
    size_t count = 0;
    size_t start = 0;
    for (size_t i = 0; i <= n; i++) {
        if (i == n || s[i] == '\n') {
            if (i == n && start == n) {
                break;
            }
            if (count == cap) {
                osty_rt_diff_grow_pair(&offs, &lens, &cap);
            }
            offs[count] = start;
            lens[count] = i - start;
            count++;
            start = i + 1;
        }
    }
    *out_offs = offs;
    *out_lens = lens;
    *out_count = count;
}

static bool osty_rt_diff_line_equal(const char *a, size_t a_off, size_t a_len, const char *b, size_t b_off, size_t b_len) {
    if (a_len != b_len) {
        return false;
    }
    if (a_len == 0) {
        return true;
    }
    return memcmp(a + a_off, b + b_off, a_len) == 0;
}

static void osty_rt_diff_emit_line(osty_rt_diff_scratch *s, const char *tag, const char *src, size_t off, size_t len) {
    osty_rt_diff_scratch_append_cstr(s, tag);
    osty_rt_diff_scratch_append(s, src + off, len);
    osty_rt_diff_scratch_append(s, "\n", 1);
}

/* osty_rt_strings_DiffLines renders a trim-prefix/suffix line diff
 * with up to 3 lines of shared context on either side. Returns "" if
 * the inputs are byte-equal (prefix consumes everything). NULL
 * inputs normalize to "". */
const char *osty_rt_strings_DiffLines(const char *actual, const char *expected) {
    const char *a = (actual == NULL) ? "" : actual;
    const char *e = (expected == NULL) ? "" : expected;

    size_t *a_off = NULL;
    size_t *a_len = NULL;
    size_t a_count = 0;
    size_t *e_off = NULL;
    size_t *e_len = NULL;
    size_t e_count = 0;
    osty_rt_diff_split_lines(a, &a_off, &a_len, &a_count);
    osty_rt_diff_split_lines(e, &e_off, &e_len, &e_count);

    size_t prefix = 0;
    while (prefix < a_count && prefix < e_count &&
           osty_rt_diff_line_equal(a, a_off[prefix], a_len[prefix], e, e_off[prefix], e_len[prefix])) {
        prefix++;
    }
    size_t suffix = 0;
    while (suffix + prefix < a_count && suffix + prefix < e_count &&
           osty_rt_diff_line_equal(a, a_off[a_count - 1 - suffix], a_len[a_count - 1 - suffix],
                                   e, e_off[e_count - 1 - suffix], e_len[e_count - 1 - suffix])) {
        suffix++;
    }

    osty_rt_diff_scratch scratch;
    osty_rt_diff_scratch_init(&scratch);

    /* No divergence → return "" so callers treat as "no diff". Equal
     * inputs (prefix consumed both sides) land here too. */
    bool has_diff = (prefix + suffix < a_count) || (prefix + suffix < e_count);
    if (!has_diff) {
        free(a_off);
        free(a_len);
        free(e_off);
        free(e_len);
        return osty_rt_string_dup_site("", 0, "runtime.test.diff.empty");
    }

    const size_t context = 3;
    size_t lead_start = prefix > context ? prefix - context : 0;
    for (size_t i = lead_start; i < prefix; i++) {
        osty_rt_diff_emit_line(&scratch, "  ", a, a_off[i], a_len[i]);
    }
    for (size_t i = prefix; i + suffix < a_count; i++) {
        osty_rt_diff_emit_line(&scratch, "- ", a, a_off[i], a_len[i]);
    }
    for (size_t i = prefix; i + suffix < e_count; i++) {
        osty_rt_diff_emit_line(&scratch, "+ ", e, e_off[i], e_len[i]);
    }
    size_t tail_count = suffix < context ? suffix : context;
    for (size_t i = 0; i < tail_count; i++) {
        size_t idx = a_count - suffix + i;
        osty_rt_diff_emit_line(&scratch, "  ", a, a_off[idx], a_len[idx]);
    }

    free(a_off);
    free(a_len);
    free(e_off);
    free(e_len);

    /* Strip the trailing newline so callers can splice a header like
     * "diff:\n" in front without an extra blank line at the bottom. */
    while (scratch.len > 0 && scratch.data[scratch.len - 1] == '\n') {
        scratch.len--;
    }

    const char *out = osty_rt_string_dup_site(scratch.data == NULL ? "" : scratch.data, scratch.len, "runtime.test.diff");
    free(scratch.data);
    return out;
}

/* osty_rt_strndup copies `n` bytes of `src` into a fresh malloc
 * buffer + NUL terminator. OOM aborts via the shared `site` tag. */
static char *osty_rt_strndup(const char *src, size_t n, const char *site) {
    char *out = (char *)osty_rt_xmalloc(n + 1, site);
    if (n != 0) {
        memcpy(out, src, n);
    }
    out[n] = '\0';
    return out;
}

/* osty_rt_list_primitive_to_string renders a List<T> with a primitive
 * element type as a multi-line String — one element per line so the
 * trim-prefix/suffix diff in osty_rt_strings_DiffLines surfaces the
 * divergence meaningfully:
 *
 *     [
 *       1,
 *       2,
 *       3,
 *     ]
 *
 * elem_kind:
 *   1 = Int    (i64,    osty_rt_list_get_i64)
 *   2 = Float  (double, osty_rt_list_get_f64)
 *   3 = Bool   (i1,     osty_rt_list_get_i1)
 *   4 = String (ptr,    osty_rt_list_get_ptr)
 *
 * Char (i32) and Byte (i8) go through the generic bytes path and are
 * intentionally omitted — they are rare in assertions and would bloat
 * this helper's dispatch without a proportional payoff. */
#define OSTY_LIST_KIND_INT    1
#define OSTY_LIST_KIND_FLOAT  2
#define OSTY_LIST_KIND_BOOL   3
#define OSTY_LIST_KIND_STRING 4

const char *osty_rt_list_primitive_to_string(void *list, int64_t elem_kind) {
    if (list == NULL) {
        return osty_rt_string_dup_site("[]", 2, "runtime.list.to_string.empty");
    }
    int64_t n = osty_rt_list_len(list);
    if (n == 0) {
        return osty_rt_string_dup_site("[]", 2, "runtime.list.to_string.empty");
    }

    osty_rt_diff_scratch s;
    osty_rt_diff_scratch_init(&s);
    osty_rt_diff_scratch_append(&s, "[\n", 2);
    char buf[64];
    for (int64_t i = 0; i < n; i++) {
        osty_rt_diff_scratch_append(&s, "  ", 2);
        switch (elem_kind) {
        case OSTY_LIST_KIND_INT: {
            int64_t v = osty_rt_list_get_i64(list, i);
            int w = snprintf(buf, sizeof(buf), "%lld", (long long)v);
            if (w > 0) osty_rt_diff_scratch_append(&s, buf, (size_t)w);
            break;
        }
        case OSTY_LIST_KIND_FLOAT: {
            double v = osty_rt_list_get_f64(list, i);
            int w = snprintf(buf, sizeof(buf), "%.17g", v);
            if (w > 0) osty_rt_diff_scratch_append(&s, buf, (size_t)w);
            break;
        }
        case OSTY_LIST_KIND_BOOL: {
            bool v = osty_rt_list_get_i1(list, i);
            osty_rt_diff_scratch_append_cstr(&s, v ? "true" : "false");
            break;
        }
        case OSTY_LIST_KIND_STRING: {
            const char *v = (const char *)osty_rt_list_get_ptr(list, i);
            osty_rt_diff_scratch_append(&s, "\"", 1);
            osty_rt_diff_scratch_append_cstr(&s, v == NULL ? "" : v);
            osty_rt_diff_scratch_append(&s, "\"", 1);
            break;
        }
        default:
            osty_rt_diff_scratch_append_cstr(&s, "<?>");
            break;
        }
        osty_rt_diff_scratch_append(&s, ",\n", 2);
    }
    osty_rt_diff_scratch_append(&s, "]", 1);

    const char *out = osty_rt_string_dup_site(s.data == NULL ? "" : s.data, s.len, "runtime.list.to_string");
    free(s.data);
    return out;
}

/* Sanitize a snapshot name into a filesystem-safe stem. Mirrors
 * `sanitizeNativeTestName` in toolchain/test_runner.osty: letters,
 * digits, underscore pass through; everything else collapses to `_`.
 * Empty/all-sanitized input falls back to OSTY_SNAPSHOT_DEFAULT_STEM
 * so the output path never ends up as `.snap`. Caller frees. */
static char *osty_rt_snapshot_sanitize(const char *name) {
    size_t n = (name == NULL) ? 0 : strlen(name);
    if (n == 0) {
        return osty_rt_strndup(OSTY_SNAPSHOT_DEFAULT_STEM, sizeof(OSTY_SNAPSHOT_DEFAULT_STEM) - 1, "snapshot sanitize: out of memory");
    }
    char *out = (char *)osty_rt_xmalloc(n + 1, "snapshot sanitize: out of memory");
    size_t j = 0;
    for (size_t i = 0; i < n; i++) {
        unsigned char c = (unsigned char)name[i];
        if ((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
            out[j++] = (char)c;
        } else {
            out[j++] = '_';
        }
    }
    out[j] = '\0';
    if (j == 0) {
        free(out);
        return osty_rt_strndup(OSTY_SNAPSHOT_DEFAULT_STEM, sizeof(OSTY_SNAPSHOT_DEFAULT_STEM) - 1, "snapshot sanitize: out of memory");
    }
    return out;
}

/* osty_rt_snapshot_dirname returns a malloc'd copy of the directory
 * component of `path` (everything up to the last `/` or `\`).
 * Returns "." on empty input or when `path` has no separator — that's
 * the right default for `osty test` runs where the source file is in
 * the test's working directory. A root-relative path like "/foo"
 * collapses to the single leading separator. Caller frees. */
static char *osty_rt_snapshot_dirname(const char *path) {
    size_t n = (path == NULL) ? 0 : strlen(path);
    size_t last = (size_t)-1;
    for (size_t i = 0; i < n; i++) {
        if (path[i] == '/' || path[i] == '\\') {
            last = i;
        }
    }
    if (last == (size_t)-1) {
        return osty_rt_strndup(".", 1, "snapshot dirname: out of memory");
    }
    if (last == 0) {
        return osty_rt_strndup(path, 1, "snapshot dirname: out of memory");
    }
    return osty_rt_strndup(path, last, "snapshot dirname: out of memory");
}

/* osty_rt_snapshot_mkdir_p creates `path` and every missing parent
 * segment. mkdir is best-effort — EEXIST, perm errors, or an
 * intermediate path that's actually a file all fall through to the
 * subsequent fopen in write_all, which reports the real error with
 * the offending path. */
static void osty_rt_snapshot_mkdir_p(const char *path) {
    if (path == NULL || path[0] == '\0') {
        return;
    }
    size_t n = strlen(path);
    char *buf = osty_rt_strndup(path, n, "snapshot mkdir_p: out of memory");
    for (size_t i = 1; i < n; i++) {
        char c = buf[i];
        if (c == '/' || c == '\\') {
            buf[i] = '\0';
            (void)OSTY_RT_MKDIR_ONE(buf);
            buf[i] = c;
        }
    }
    (void)OSTY_RT_MKDIR_ONE(buf);
    free(buf);
}

/* Read the entire file into a malloc'd, NUL-terminated buffer. On
 * success returns 0 and sets *out / *out_len. Missing file → 1.
 * Any other I/O error → -1. The three exit codes let the caller
 * distinguish "first run" from a real filesystem problem. */
static int osty_rt_snapshot_read_all(const char *path, char **out, size_t *out_len) {
    FILE *f = fopen(path, "rb");
    if (f == NULL) {
        *out = NULL;
        *out_len = 0;
        return 1;
    }
    if (fseek(f, 0, SEEK_END) != 0) {
        fclose(f);
        return -1;
    }
    long sz = ftell(f);
    if (sz < 0 || fseek(f, 0, SEEK_SET) != 0) {
        fclose(f);
        return -1;
    }
    char *buf = (char *)osty_rt_xmalloc((size_t)sz + 1, "snapshot read: out of memory");
    size_t got = fread(buf, 1, (size_t)sz, f);
    fclose(f);
    if (got != (size_t)sz) {
        free(buf);
        return -1;
    }
    buf[sz] = '\0';
    *out = buf;
    *out_len = (size_t)sz;
    return 0;
}

static int osty_rt_snapshot_write_all(const char *path, const char *data, size_t len) {
    FILE *f = fopen(path, "wb");
    if (f == NULL) {
        return -1;
    }
    if (len != 0) {
        size_t wrote = fwrite(data, 1, len, f);
        if (wrote != len) {
            fclose(f);
            return -1;
        }
    }
    if (fclose(f) != 0) {
        return -1;
    }
    return 0;
}

/* Mirrors the truthy rule used by `osty_gc_incremental_auto_now`: an
 * unset / empty / "0" / "false" / "FALSE" value reads as false,
 * anything else is true. Keep in sync with that site. */
static bool osty_rt_env_truthy(const char *name) {
    const char *v = getenv(name);
    if (v == NULL || v[0] == '\0' ||
        strcmp(v, "0") == 0 || strcmp(v, "false") == 0 || strcmp(v, "FALSE") == 0) {
        return false;
    }
    return true;
}

/* Build the `<dir>/__snapshots__/<stem>.snap` path for a given source
 * file + snapshot name. Honors OSTY_SNAPSHOT_DIR as an override used
 * by the test harness to redirect writes into a tempdir. Caller
 * frees. */
static char *osty_rt_snapshot_resolve_path(const char *source_path, const char *name) {
    const char *override_dir = getenv(OSTY_SNAPSHOT_DIR_ENV);
    char *base_dir = (override_dir != NULL && override_dir[0] != '\0')
        ? osty_rt_strndup(override_dir, strlen(override_dir), "snapshot path: out of memory")
        : osty_rt_snapshot_dirname(source_path);

    char *stem = osty_rt_snapshot_sanitize(name);
    const char *sep = "/" OSTY_SNAPSHOT_SUBDIR "/";
    size_t dir_len = strlen(base_dir);
    size_t sep_len = strlen(sep);
    size_t stem_len = strlen(stem);
    size_t sfx_len = sizeof(OSTY_SNAPSHOT_SUFFIX) - 1;
    char *out = (char *)osty_rt_xmalloc(dir_len + sep_len + stem_len + sfx_len + 1, "snapshot path: out of memory");
    size_t pos = 0;
    memcpy(out + pos, base_dir, dir_len); pos += dir_len;
    memcpy(out + pos, sep, sep_len);       pos += sep_len;
    memcpy(out + pos, stem, stem_len);     pos += stem_len;
    memcpy(out + pos, OSTY_SNAPSHOT_SUFFIX, sfx_len); pos += sfx_len;
    out[pos] = '\0';

    free(base_dir);
    free(stem);
    return out;
}

/* Fatal-exit shape shared by the three "cannot read / cannot write"
 * branches in osty_rt_test_snapshot. `existing` may be NULL when the
 * failure happens before the golden is read. */
static void osty_rt_snapshot_fatal(const char *verb, const char *name, char *snap_path, char *existing) {
    fprintf(stderr, "testing.snapshot(%s): cannot %s %s\n", name, verb, snap_path);
    free(existing);
    free(snap_path);
    exit(1);
}

/* osty_rt_test_snapshot is called from emitted code whenever a test
 * runs `testing.snapshot(name, output)`. `source_path` is hard-coded
 * at emit time so snapshot discovery is independent of the process
 * working directory. */
void osty_rt_test_snapshot(const char *name, const char *output, const char *source_path) {
    const char *name_s = (name == NULL) ? "" : name;
    const char *output_s = (output == NULL) ? "" : output;
    const char *src_s = (source_path == NULL) ? "" : source_path;
    char name_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
    char output_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
    char src_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];

    /* SSO: `name`/`output`/`source_path` flow into `fprintf %s`,
     * `strlen`, `memcmp`, and the resolver's path-builder, all of
     * which need real addresses. Decode at the door. */
    osty_rt_string_decode_to_buf_if_inline(&name_s, name_buf);
    osty_rt_string_decode_to_buf_if_inline(&output_s, output_buf);
    osty_rt_string_decode_to_buf_if_inline(&src_s, src_buf);

    char *snap_path = osty_rt_snapshot_resolve_path(src_s, name_s);
    char *snap_dir = osty_rt_snapshot_dirname(snap_path);
    osty_rt_snapshot_mkdir_p(snap_dir);
    free(snap_dir);

    size_t output_len = strlen(output_s);

    if (osty_rt_env_truthy(OSTY_UPDATE_SNAPSHOTS_ENV)) {
        if (osty_rt_snapshot_write_all(snap_path, output_s, output_len) != 0) {
            osty_rt_snapshot_fatal("write", name_s, snap_path, NULL);
        }
        fprintf(stdout, "snapshot: updated %s\n", snap_path);
        free(snap_path);
        return;
    }

    char *existing = NULL;
    size_t existing_len = 0;
    int rd = osty_rt_snapshot_read_all(snap_path, &existing, &existing_len);
    if (rd == 1) {
        if (osty_rt_snapshot_write_all(snap_path, output_s, output_len) != 0) {
            osty_rt_snapshot_fatal("write", name_s, snap_path, NULL);
        }
        fprintf(stdout, "snapshot: created %s\n", snap_path);
        free(snap_path);
        return;
    }
    if (rd != 0) {
        osty_rt_snapshot_fatal("read", name_s, snap_path, existing);
    }

    bool equal = (existing_len == output_len) && (output_len == 0 || memcmp(existing, output_s, output_len) == 0);
    if (equal) {
        free(existing);
        free(snap_path);
        return;
    }

    const char *diff = osty_rt_strings_DiffLines(output_s, existing);
    fprintf(stdout, "testing.snapshot(%s) mismatch: %s\n", name_s, snap_path);
    fprintf(stdout, "hint: re-run with " OSTY_UPDATE_SNAPSHOTS_ENV "=1 to accept the new output\n");
    if (diff != NULL && diff[0] != '\0') {
        fprintf(stdout, "%s\n", diff);
    }
    free(existing);
    free(snap_path);
    exit(1);
}

void osty_rt_io_write(const char *text, bool newline, bool to_stderr) {
    FILE *out = to_stderr ? stderr : stdout;
    const char *safe = text != NULL ? text : "";
    char inline_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
    /* SSO: an inline-tagged String would crash `fputs` (the pointer
     * is encoded content, not a real address). Decode to a stack
     * buffer first. */
    osty_rt_string_decode_to_buf_if_inline(&safe, inline_buf);
    fputs(safe, out);
    if (newline) {
        fputc('\n', out);
    }
    fflush(out);
}

void *osty_rt_io_read_line(void) {
    size_t cap = 128;
    size_t len = 0;
    char *buf = (char *)osty_rt_xmalloc(cap, "runtime.io.read_line.buf");
    for (;;) {
        int ch = fgetc(stdin);
        if (ch == EOF || ch == '\n') {
            break;
        }
        if (len + 1 >= cap) {
            size_t next_cap = cap * 2;
            char *grown = (char *)realloc(buf, next_cap);
            if (grown == NULL) {
                free(buf);
                osty_rt_abort("runtime.io.read_line: realloc failed");
            }
            buf = grown;
            cap = next_cap;
        }
        buf[len++] = (char)ch;
    }
    if (len > 0 && buf[len - 1] == '\r') {
        len--;
    }
    void *out = osty_rt_string_dup_site(buf, len, "runtime.io.read_line");
    free(buf);
    return out;
}

/* std.net TCP/DNS surface.
 *
 * The Osty `std.net` module keeps the public API in Osty source and
 * calls into these runtime helpers through `use c "osty_runtime"`.
 * Every helper clears the thread-local net status before work, and on
 * failure records a human-readable error string retrievable via
 * `osty_rt_net_last_error()`. */
static OSTY_RT_TLS int osty_rt_net_failed_flag = 0;
static OSTY_RT_TLS char *osty_rt_net_error_text = NULL;

static void osty_rt_net_clear_status(void) {
    osty_rt_net_failed_flag = 0;
    if (osty_rt_net_error_text != NULL) {
        free(osty_rt_net_error_text);
        osty_rt_net_error_text = NULL;
    }
}

static void osty_rt_net_set_error_text(const char *prefix, const char *detail, const char *site) {
    const char *lhs = prefix == NULL ? "network error" : prefix;
    const char *rhs = (detail != NULL && detail[0] != '\0') ? detail : "unknown error";
    size_t lhs_len = strlen(lhs);
    size_t rhs_len = strlen(rhs);
    size_t total = lhs_len + 2 + rhs_len;
    char *buf = (char *)osty_rt_xmalloc(total + 1, site);

    memcpy(buf, lhs, lhs_len);
    buf[lhs_len] = ':';
    buf[lhs_len + 1] = ' ';
    memcpy(buf + lhs_len + 2, rhs, rhs_len);
    buf[total] = '\0';

    osty_rt_net_clear_status();
    osty_rt_net_failed_flag = 1;
    osty_rt_net_error_text = buf;
}

static void osty_rt_net_set_errno_error(const char *prefix, const char *site) {
    osty_rt_net_set_error_text(prefix, strerror(errno), site);
}

static void *osty_rt_net_empty_string(void) {
    return osty_rt_string_dup_site("", 0, "runtime.net.empty_string");
}

static void *osty_rt_net_empty_bytes(void) {
    return osty_rt_bytes_dup_site(NULL, 0, "runtime.net.empty_bytes");
}

static void *osty_rt_net_empty_list(void) {
    return osty_rt_list_new();
}

static const char *osty_rt_net_decode_string_arg(const char *value, char *buf) {
    const char *out = value != NULL ? value : "";
    osty_rt_string_decode_to_buf_if_inline(&out, buf);
    return out;
}

bool osty_rt_net_failed(void) {
    return osty_rt_net_failed_flag != 0;
}

void *osty_rt_net_last_error(void) {
    const char *msg = osty_rt_net_error_text != NULL
        ? osty_rt_net_error_text
        : "network error";
    return osty_rt_string_dup_site(msg, strlen(msg), "runtime.net.last_error");
}

#if defined(OSTY_RT_PLATFORM_WIN32)

static void osty_rt_net_unsupported(const char *site) {
    osty_rt_net_set_error_text("network runtime is not available on this platform",
                               "Windows socket support is not wired into the current runtime yet",
                               site);
}

void *osty_rt_net_lookup_text(const char *host) {
    (void)host;
    osty_rt_net_clear_status();
    osty_rt_net_unsupported("runtime.net.lookup_text");
    return osty_rt_net_empty_list();
}

void *osty_rt_net_lookup_addr(const char *ip) {
    (void)ip;
    osty_rt_net_clear_status();
    osty_rt_net_unsupported("runtime.net.lookup_addr");
    return osty_rt_net_empty_list();
}

int64_t osty_rt_net_tcp_connect(const char *host, int64_t port) {
    (void)host;
    (void)port;
    osty_rt_net_clear_status();
    osty_rt_net_unsupported("runtime.net.tcp_connect");
    return 0;
}

int64_t osty_rt_net_tcp_connect_timeout(const char *host, int64_t port, int64_t timeout_ms) {
    (void)host;
    (void)port;
    (void)timeout_ms;
    osty_rt_net_clear_status();
    osty_rt_net_unsupported("runtime.net.tcp_connect_timeout");
    return 0;
}

int64_t osty_rt_net_tcp_listen(const char *host, int64_t port) {
    (void)host;
    (void)port;
    osty_rt_net_clear_status();
    osty_rt_net_unsupported("runtime.net.tcp_listen");
    return 0;
}

void *osty_rt_net_tcp_read(int64_t handle, int64_t max_bytes) {
    (void)handle;
    (void)max_bytes;
    osty_rt_net_clear_status();
    osty_rt_net_unsupported("runtime.net.tcp_read");
    return osty_rt_net_empty_bytes();
}

void *osty_rt_net_tcp_read_exact(int64_t handle, int64_t n) {
    (void)handle;
    (void)n;
    osty_rt_net_clear_status();
    osty_rt_net_unsupported("runtime.net.tcp_read_exact");
    return osty_rt_net_empty_bytes();
}

int64_t osty_rt_net_tcp_write(int64_t handle, void *data) {
    (void)handle;
    (void)data;
    osty_rt_net_clear_status();
    osty_rt_net_unsupported("runtime.net.tcp_write");
    return 0;
}

void osty_rt_net_tcp_write_all(int64_t handle, void *data) {
    (void)handle;
    (void)data;
    osty_rt_net_clear_status();
    osty_rt_net_unsupported("runtime.net.tcp_write_all");
}

void osty_rt_net_tcp_flush(int64_t handle) {
    (void)handle;
    osty_rt_net_clear_status();
}

void osty_rt_net_tcp_close(int64_t handle) {
    (void)handle;
    osty_rt_net_clear_status();
    osty_rt_net_unsupported("runtime.net.tcp_close");
}

void *osty_rt_net_tcp_local_addr(int64_t handle) {
    (void)handle;
    osty_rt_net_clear_status();
    osty_rt_net_unsupported("runtime.net.tcp_local_addr");
    return osty_rt_net_empty_string();
}

void *osty_rt_net_tcp_peer_addr(int64_t handle) {
    (void)handle;
    osty_rt_net_clear_status();
    osty_rt_net_unsupported("runtime.net.tcp_peer_addr");
    return osty_rt_net_empty_string();
}

void osty_rt_net_tcp_set_read_timeout(int64_t handle, int64_t timeout_ms) {
    (void)handle;
    (void)timeout_ms;
    osty_rt_net_clear_status();
    osty_rt_net_unsupported("runtime.net.tcp_set_read_timeout");
}

void osty_rt_net_tcp_set_write_timeout(int64_t handle, int64_t timeout_ms) {
    (void)handle;
    (void)timeout_ms;
    osty_rt_net_clear_status();
    osty_rt_net_unsupported("runtime.net.tcp_set_write_timeout");
}

void osty_rt_net_tcp_set_nodelay(int64_t handle, bool on) {
    (void)handle;
    (void)on;
    osty_rt_net_clear_status();
    osty_rt_net_unsupported("runtime.net.tcp_set_nodelay");
}

void osty_rt_net_tcp_set_keepalive(int64_t handle, bool on) {
    (void)handle;
    (void)on;
    osty_rt_net_clear_status();
    osty_rt_net_unsupported("runtime.net.tcp_set_keepalive");
}

int64_t osty_rt_net_tcp_accept(int64_t handle) {
    (void)handle;
    osty_rt_net_clear_status();
    osty_rt_net_unsupported("runtime.net.tcp_accept");
    return 0;
}

void *osty_rt_net_tcp_listener_local_addr(int64_t handle) {
    (void)handle;
    osty_rt_net_clear_status();
    osty_rt_net_unsupported("runtime.net.tcp_listener_local_addr");
    return osty_rt_net_empty_string();
}

void osty_rt_net_tcp_listener_close(int64_t handle) {
    (void)handle;
    osty_rt_net_clear_status();
    osty_rt_net_unsupported("runtime.net.tcp_listener_close");
}

#else

static void osty_rt_net_set_gai_error(const char *prefix, int code, const char *site) {
    osty_rt_net_set_error_text(prefix, gai_strerror(code), site);
}

static bool osty_rt_net_service_string(int64_t port, char *buf, size_t buf_size, const char *site) {
    int written;
    if (port < 0 || port > 65535) {
        osty_rt_net_set_error_text("invalid port", "port must be in 0..65535", site);
        return false;
    }
    written = snprintf(buf, buf_size, "%lld", (long long)port);
    if (written < 0 || (size_t)written >= buf_size) {
        osty_rt_net_set_error_text("failed to format port", "buffer overflow", site);
        return false;
    }
    return true;
}

static void osty_rt_net_prepare_stream_socket(int fd) {
#if defined(__APPLE__)
    int on = 1;
    if (fd >= 0) {
        setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &on, (socklen_t)sizeof(on));
    }
#else
    (void)fd;
#endif
}

static int osty_rt_net_send_flags(void) {
#if defined(MSG_NOSIGNAL)
    return MSG_NOSIGNAL;
#else
    return 0;
#endif
}

static int osty_rt_net_set_blocking(int fd, bool blocking) {
    int flags = fcntl(fd, F_GETFL, 0);
    if (flags < 0) {
        return -1;
    }
    if (blocking) {
        flags &= ~O_NONBLOCK;
    } else {
        flags |= O_NONBLOCK;
    }
    return fcntl(fd, F_SETFL, flags);
}

static void *osty_rt_net_sockaddr_text(const struct sockaddr *sa, socklen_t len, const char *site) {
    char host[NI_MAXHOST];
    char serv[NI_MAXSERV];
    char text[NI_MAXHOST + NI_MAXSERV + 8];
    int rc = getnameinfo(sa, len, host, sizeof(host), serv, sizeof(serv),
                         NI_NUMERICHOST | NI_NUMERICSERV);
    int written;
    if (rc != 0) {
        osty_rt_net_set_gai_error("failed to format socket address", rc, site);
        return osty_rt_net_empty_string();
    }
    if (sa->sa_family == AF_INET6) {
        written = snprintf(text, sizeof(text), "[%s]:%s", host, serv);
    } else {
        written = snprintf(text, sizeof(text), "%s:%s", host, serv);
    }
    if (written < 0 || (size_t)written >= sizeof(text)) {
        osty_rt_net_set_error_text("failed to format socket address", "buffer overflow", site);
        return osty_rt_net_empty_string();
    }
    return osty_rt_string_dup_site(text, (size_t)written, site);
}

static bool osty_rt_net_apply_timeout(int fd, int64_t timeout_ms, int optname, const char *site) {
    struct timeval tv;
    if (timeout_ms < 0) {
        tv.tv_sec = 0;
        tv.tv_usec = 0;
    } else {
        tv.tv_sec = (time_t)(timeout_ms / 1000);
        tv.tv_usec = (suseconds_t)((timeout_ms % 1000) * 1000);
    }
    if (setsockopt(fd, SOL_SOCKET, optname, &tv, (socklen_t)sizeof(tv)) != 0) {
        osty_rt_net_set_errno_error("failed to configure socket timeout", site);
        return false;
    }
    return true;
}

static int64_t osty_rt_net_connect_impl(const char *host, int64_t port, int64_t timeout_ms, const char *site) {
    char service[32];
    struct addrinfo hints;
    struct addrinfo *res = NULL;
    struct addrinfo *ai;
    int rc;
    int last_errno = 0;

    if (host == NULL || host[0] == '\0') {
        osty_rt_net_set_error_text("failed to connect", "host is empty", site);
        return 0;
    }
    if (!osty_rt_net_service_string(port, service, sizeof(service), site)) {
        return 0;
    }

    memset(&hints, 0, sizeof(hints));
    hints.ai_family = AF_UNSPEC;
    hints.ai_socktype = SOCK_STREAM;
    rc = getaddrinfo(host, service, &hints, &res);
    if (rc != 0) {
        osty_rt_net_set_gai_error("failed to resolve remote address", rc, site);
        return 0;
    }

    for (ai = res; ai != NULL; ai = ai->ai_next) {
        int fd = socket(ai->ai_family, ai->ai_socktype, ai->ai_protocol);
        if (fd < 0) {
            last_errno = errno;
            continue;
        }

        osty_rt_net_prepare_stream_socket(fd);
        if (timeout_ms >= 0) {
            struct pollfd pfd;
            int so_error = 0;
            socklen_t so_error_len = (socklen_t)sizeof(so_error);

            if (osty_rt_net_set_blocking(fd, false) != 0) {
                last_errno = errno;
                close(fd);
                continue;
            }

            do {
                rc = connect(fd, ai->ai_addr, ai->ai_addrlen);
            } while (rc != 0 && errno == EINTR);

            if (rc != 0) {
                if (errno != EINPROGRESS) {
                    last_errno = errno;
                    close(fd);
                    continue;
                }
                pfd.fd = fd;
                pfd.events = POLLOUT;
                do {
                    rc = poll(&pfd, 1, (int)timeout_ms);
                } while (rc < 0 && errno == EINTR);
                if (rc == 0) {
                    close(fd);
                    freeaddrinfo(res);
                    osty_rt_net_set_error_text("failed to connect", "connect timed out", site);
                    return 0;
                }
                if (rc < 0) {
                    last_errno = errno;
                    close(fd);
                    continue;
                }
                if (getsockopt(fd, SOL_SOCKET, SO_ERROR, &so_error, &so_error_len) != 0) {
                    last_errno = errno;
                    close(fd);
                    continue;
                }
                if (so_error != 0) {
                    last_errno = so_error;
                    close(fd);
                    continue;
                }
            }
            if (osty_rt_net_set_blocking(fd, true) != 0) {
                last_errno = errno;
                close(fd);
                continue;
            }
            freeaddrinfo(res);
            return (int64_t)fd;
        }

        do {
            rc = connect(fd, ai->ai_addr, ai->ai_addrlen);
        } while (rc != 0 && errno == EINTR);
        if (rc == 0) {
            freeaddrinfo(res);
            return (int64_t)fd;
        }
        last_errno = errno;
        close(fd);
    }

    freeaddrinfo(res);
    if (last_errno != 0) {
        errno = last_errno;
        osty_rt_net_set_errno_error("failed to connect", site);
    } else {
        osty_rt_net_set_error_text("failed to connect", "no reachable address", site);
    }
    return 0;
}

void *osty_rt_net_lookup_text(const char *host) {
    char host_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
    const char *decoded = osty_rt_net_decode_string_arg(host, host_buf);
    struct addrinfo hints;
    struct addrinfo *res = NULL;
    struct addrinfo *ai;
    int rc;
    void *out;

    osty_rt_net_clear_status();
    if (decoded[0] == '\0') {
        osty_rt_net_set_error_text("failed to resolve host", "host is empty", "runtime.net.lookup_text");
        return osty_rt_net_empty_list();
    }

    memset(&hints, 0, sizeof(hints));
    hints.ai_family = AF_UNSPEC;
    hints.ai_socktype = SOCK_STREAM;
    rc = getaddrinfo(decoded, NULL, &hints, &res);
    if (rc != 0) {
        osty_rt_net_set_gai_error("failed to resolve host", rc, "runtime.net.lookup_text");
        return osty_rt_net_empty_list();
    }

    out = osty_rt_list_new();
    for (ai = res; ai != NULL; ai = ai->ai_next) {
        char text[NI_MAXHOST];
        rc = getnameinfo(ai->ai_addr, ai->ai_addrlen, text, sizeof(text), NULL, 0, NI_NUMERICHOST);
        if (rc != 0) {
            continue;
        }
        osty_rt_list_push_ptr(out,
                              osty_rt_string_dup_site(text, strlen(text), "runtime.net.lookup_text.entry"));
    }
    freeaddrinfo(res);
    return out;
}

void *osty_rt_net_lookup_addr(const char *ip) {
    char ip_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
    const char *decoded = osty_rt_net_decode_string_arg(ip, ip_buf);
    struct addrinfo hints;
    struct addrinfo *res = NULL;
    struct addrinfo *ai;
    int rc;
    void *out;
    int64_t count = 0;

    osty_rt_net_clear_status();
    if (decoded[0] == '\0') {
        osty_rt_net_set_error_text("failed to reverse lookup address", "address is empty", "runtime.net.lookup_addr");
        return osty_rt_net_empty_list();
    }

    memset(&hints, 0, sizeof(hints));
    hints.ai_family = AF_UNSPEC;
    hints.ai_socktype = SOCK_STREAM;
    hints.ai_flags = AI_NUMERICHOST;
    rc = getaddrinfo(decoded, NULL, &hints, &res);
    if (rc != 0) {
        osty_rt_net_set_gai_error("failed to parse reverse-lookup address", rc, "runtime.net.lookup_addr");
        return osty_rt_net_empty_list();
    }

    out = osty_rt_list_new();
    for (ai = res; ai != NULL; ai = ai->ai_next) {
        char text[NI_MAXHOST];
        rc = getnameinfo(ai->ai_addr, ai->ai_addrlen, text, sizeof(text), NULL, 0, NI_NAMEREQD);
        if (rc != 0) {
            continue;
        }
        osty_rt_list_push_ptr(out,
                              osty_rt_string_dup_site(text, strlen(text), "runtime.net.lookup_addr.entry"));
        count++;
    }
    freeaddrinfo(res);

    if (count == 0) {
        osty_rt_net_set_error_text("failed to reverse lookup address", "no names found", "runtime.net.lookup_addr");
        return osty_rt_net_empty_list();
    }
    return out;
}

int64_t osty_rt_net_tcp_connect(const char *host, int64_t port) {
    char host_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
    const char *decoded = osty_rt_net_decode_string_arg(host, host_buf);
    osty_rt_net_clear_status();
    return osty_rt_net_connect_impl(decoded, port, -1, "runtime.net.tcp_connect");
}

int64_t osty_rt_net_tcp_connect_timeout(const char *host, int64_t port, int64_t timeout_ms) {
    char host_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
    const char *decoded = osty_rt_net_decode_string_arg(host, host_buf);
    osty_rt_net_clear_status();
    return osty_rt_net_connect_impl(decoded, port, timeout_ms, "runtime.net.tcp_connect_timeout");
}

int64_t osty_rt_net_tcp_listen(const char *host, int64_t port) {
    char host_buf[OSTY_RT_SSO_DECODE_BUF_BYTES];
    const char *decoded_host = osty_rt_net_decode_string_arg(host, host_buf);
    const char *bind_host = (decoded_host[0] == '\0' || strcmp(decoded_host, "*") == 0) ? NULL : decoded_host;
    char service[32];
    struct addrinfo hints;
    struct addrinfo *res = NULL;
    struct addrinfo *ai;
    int rc;
    int last_errno = 0;

    osty_rt_net_clear_status();
    if (!osty_rt_net_service_string(port, service, sizeof(service), "runtime.net.tcp_listen")) {
        return 0;
    }

    memset(&hints, 0, sizeof(hints));
    hints.ai_family = AF_UNSPEC;
    hints.ai_socktype = SOCK_STREAM;
    hints.ai_flags = AI_PASSIVE;
    rc = getaddrinfo(bind_host, service, &hints, &res);
    if (rc != 0) {
        osty_rt_net_set_gai_error("failed to resolve listen address", rc, "runtime.net.tcp_listen");
        return 0;
    }

    for (ai = res; ai != NULL; ai = ai->ai_next) {
        int on = 1;
        int fd = socket(ai->ai_family, ai->ai_socktype, ai->ai_protocol);
        if (fd < 0) {
            last_errno = errno;
            continue;
        }
        osty_rt_net_prepare_stream_socket(fd);
        setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &on, (socklen_t)sizeof(on));
        if (bind(fd, ai->ai_addr, ai->ai_addrlen) != 0) {
            last_errno = errno;
            close(fd);
            continue;
        }
        if (listen(fd, SOMAXCONN) != 0) {
            last_errno = errno;
            close(fd);
            continue;
        }
        freeaddrinfo(res);
        return (int64_t)fd;
    }

    freeaddrinfo(res);
    if (last_errno != 0) {
        errno = last_errno;
        osty_rt_net_set_errno_error("failed to listen", "runtime.net.tcp_listen");
    } else {
        osty_rt_net_set_error_text("failed to listen", "no bindable address", "runtime.net.tcp_listen");
    }
    return 0;
}

void *osty_rt_net_tcp_read(int64_t handle, int64_t max_bytes) {
    unsigned char *buf;
    ssize_t n;
    void *out;

    osty_rt_net_clear_status();
    if (max_bytes < 0) {
        osty_rt_net_set_error_text("failed to read from socket", "maxBytes must be non-negative", "runtime.net.tcp_read");
        return osty_rt_net_empty_bytes();
    }
    if (max_bytes == 0) {
        return osty_rt_net_empty_bytes();
    }

    buf = (unsigned char *)osty_rt_xmalloc((size_t)max_bytes, "runtime.net.tcp_read.buf");
    do {
        n = recv((int)handle, buf, (size_t)max_bytes, 0);
    } while (n < 0 && errno == EINTR);
    if (n < 0) {
        free(buf);
        osty_rt_net_set_errno_error("failed to read from socket", "runtime.net.tcp_read");
        return osty_rt_net_empty_bytes();
    }
    out = osty_rt_bytes_dup_site(n == 0 ? NULL : buf, (size_t)n, "runtime.net.tcp_read");
    free(buf);
    return out;
}

void *osty_rt_net_tcp_read_exact(int64_t handle, int64_t want) {
    unsigned char *buf;
    size_t off = 0;
    void *out;

    osty_rt_net_clear_status();
    if (want < 0) {
        osty_rt_net_set_error_text("failed to read from socket", "byte count must be non-negative", "runtime.net.tcp_read_exact");
        return osty_rt_net_empty_bytes();
    }
    if (want == 0) {
        return osty_rt_net_empty_bytes();
    }

    buf = (unsigned char *)osty_rt_xmalloc((size_t)want, "runtime.net.tcp_read_exact.buf");
    while (off < (size_t)want) {
        ssize_t n;
        do {
            n = recv((int)handle, buf + off, (size_t)want - off, 0);
        } while (n < 0 && errno == EINTR);
        if (n < 0) {
            free(buf);
            osty_rt_net_set_errno_error("failed to read from socket", "runtime.net.tcp_read_exact");
            return osty_rt_net_empty_bytes();
        }
        if (n == 0) {
            free(buf);
            osty_rt_net_set_error_text("failed to read from socket", "unexpected EOF", "runtime.net.tcp_read_exact");
            return osty_rt_net_empty_bytes();
        }
        off += (size_t)n;
    }
    out = osty_rt_bytes_dup_site(buf, (size_t)want, "runtime.net.tcp_read_exact");
    free(buf);
    return out;
}

int64_t osty_rt_net_tcp_write(int64_t handle, void *raw_data) {
    osty_rt_bytes *data = (osty_rt_bytes *)raw_data;
    ssize_t written;
    size_t len = (data == NULL || data->len <= 0) ? 0 : (size_t)data->len;

    osty_rt_net_clear_status();
    if (len == 0) {
        return 0;
    }

    do {
        written = send((int)handle, data->data, len, osty_rt_net_send_flags());
    } while (written < 0 && errno == EINTR);
    if (written < 0) {
        osty_rt_net_set_errno_error("failed to write to socket", "runtime.net.tcp_write");
        return 0;
    }
    return (int64_t)written;
}

void osty_rt_net_tcp_write_all(int64_t handle, void *raw_data) {
    osty_rt_bytes *data = (osty_rt_bytes *)raw_data;
    size_t len = (data == NULL || data->len <= 0) ? 0 : (size_t)data->len;
    size_t off = 0;

    osty_rt_net_clear_status();
    while (off < len) {
        ssize_t written;
        do {
            written = send((int)handle, data->data + off, len - off, osty_rt_net_send_flags());
        } while (written < 0 && errno == EINTR);
        if (written < 0) {
            osty_rt_net_set_errno_error("failed to write to socket", "runtime.net.tcp_write_all");
            return;
        }
        if (written == 0) {
            osty_rt_net_set_error_text("failed to write to socket", "socket write made no progress", "runtime.net.tcp_write_all");
            return;
        }
        off += (size_t)written;
    }
}

void osty_rt_net_tcp_flush(int64_t handle) {
    (void)handle;
    osty_rt_net_clear_status();
}

void osty_rt_net_tcp_close(int64_t handle) {
    osty_rt_net_clear_status();
    if (close((int)handle) != 0) {
        osty_rt_net_set_errno_error("failed to close socket", "runtime.net.tcp_close");
    }
}

void *osty_rt_net_tcp_local_addr(int64_t handle) {
    struct sockaddr_storage addr;
    socklen_t len = (socklen_t)sizeof(addr);
    osty_rt_net_clear_status();
    if (getsockname((int)handle, (struct sockaddr *)&addr, &len) != 0) {
        osty_rt_net_set_errno_error("failed to read local socket address", "runtime.net.tcp_local_addr");
        return osty_rt_net_empty_string();
    }
    return osty_rt_net_sockaddr_text((struct sockaddr *)&addr, len, "runtime.net.tcp_local_addr");
}

void *osty_rt_net_tcp_peer_addr(int64_t handle) {
    struct sockaddr_storage addr;
    socklen_t len = (socklen_t)sizeof(addr);
    osty_rt_net_clear_status();
    if (getpeername((int)handle, (struct sockaddr *)&addr, &len) != 0) {
        osty_rt_net_set_errno_error("failed to read peer socket address", "runtime.net.tcp_peer_addr");
        return osty_rt_net_empty_string();
    }
    return osty_rt_net_sockaddr_text((struct sockaddr *)&addr, len, "runtime.net.tcp_peer_addr");
}

void osty_rt_net_tcp_set_read_timeout(int64_t handle, int64_t timeout_ms) {
    osty_rt_net_clear_status();
    osty_rt_net_apply_timeout((int)handle, timeout_ms, SO_RCVTIMEO, "runtime.net.tcp_set_read_timeout");
}

void osty_rt_net_tcp_set_write_timeout(int64_t handle, int64_t timeout_ms) {
    osty_rt_net_clear_status();
    osty_rt_net_apply_timeout((int)handle, timeout_ms, SO_SNDTIMEO, "runtime.net.tcp_set_write_timeout");
}

void osty_rt_net_tcp_set_nodelay(int64_t handle, bool on) {
    int flag = on ? 1 : 0;
    osty_rt_net_clear_status();
    if (setsockopt((int)handle, IPPROTO_TCP, TCP_NODELAY, &flag, (socklen_t)sizeof(flag)) != 0) {
        osty_rt_net_set_errno_error("failed to configure TCP_NODELAY", "runtime.net.tcp_set_nodelay");
    }
}

void osty_rt_net_tcp_set_keepalive(int64_t handle, bool on) {
    int flag = on ? 1 : 0;
    osty_rt_net_clear_status();
    if (setsockopt((int)handle, SOL_SOCKET, SO_KEEPALIVE, &flag, (socklen_t)sizeof(flag)) != 0) {
        osty_rt_net_set_errno_error("failed to configure SO_KEEPALIVE", "runtime.net.tcp_set_keepalive");
    }
}

int64_t osty_rt_net_tcp_accept(int64_t handle) {
    int fd;
    osty_rt_net_clear_status();
    do {
        fd = accept((int)handle, NULL, NULL);
    } while (fd < 0 && errno == EINTR);
    if (fd < 0) {
        osty_rt_net_set_errno_error("failed to accept TCP connection", "runtime.net.tcp_accept");
        return 0;
    }
    osty_rt_net_prepare_stream_socket(fd);
    return (int64_t)fd;
}

void *osty_rt_net_tcp_listener_local_addr(int64_t handle) {
    return osty_rt_net_tcp_local_addr(handle);
}

void osty_rt_net_tcp_listener_close(int64_t handle) {
    osty_rt_net_tcp_close(handle);
}

#endif

static uint64_t osty_rt_test_gen_mix_u64(uint64_t x) {
    x += 0x9E3779B97F4A7C15ULL;
    x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9ULL;
    x = (x ^ (x >> 27)) * 0x94D049BB133111EBULL;
    return x ^ (x >> 31);
}

int64_t osty_rt_test_gen_int(int64_t seed) {
    return (int64_t)osty_rt_test_gen_mix_u64((uint64_t)seed);
}

int64_t osty_rt_test_gen_int_range(int64_t lo, int64_t hi, int64_t seed) {
    uint64_t span;
    uint64_t mixed;
    if (hi <= lo) {
        osty_rt_abort("testing.gen.intRange: hi must be greater than lo");
    }
    span = (uint64_t)(hi - lo);
    mixed = osty_rt_test_gen_mix_u64((uint64_t)seed);
    return lo + (int64_t)(mixed % span);
}

void *osty_rt_test_gen_ascii_string(int64_t max_len, int64_t seed) {
    uint64_t mixed;
    size_t len;
    size_t i;
    char *buf;
    if (max_len < 0) {
        osty_rt_abort("testing.gen.asciiString: maxLen must be non-negative");
    }
    mixed = osty_rt_test_gen_mix_u64((uint64_t)seed);
    len = (size_t)(mixed % (uint64_t)(max_len + 1));
    buf = (char *)osty_rt_xmalloc(len + 1, "runtime.test_gen.ascii_string");
    for (i = 0; i < len; i++) {
        uint64_t ch_seed = mixed + (uint64_t)i * 0x9E3779B97F4A7C15ULL;
        buf[i] = (char)(32 + (osty_rt_test_gen_mix_u64(ch_seed) % 95ULL));
    }
    buf[len] = '\0';
    return osty_rt_string_dup_site(buf, len, "runtime.test_gen.ascii_string");
}

/* std.env command-line argument surface.
 *
 * The emitter (see internal/llvmgen/stdlib_env_shim.go) routes the Osty
 * call `env.args()` to `osty_rt_env_args`, `env.get(name)` to
 * `osty_rt_env_get`, `env.vars()` to `osty_rt_env_vars`,
 * `env.currentDir()` to `osty_rt_env_current_dir`,
 * `env.setCurrentDir(path)` to `osty_rt_env_set_current_dir`, and
 * when a script/binary's package imports `std.env` the generated
 * `main` is widened to (i32 argc, ptr argv) and begins with a call to
 * `osty_rt_env_args_init`.
 *
 * The init call hands us the raw C argv; each `env.args()` invocation
 * materializes a fresh GC-managed List<String> by duplicating every
 * argv entry through the string allocator. Freshness matters because
 * the returned list is mutable from Osty and must not alias process
 * argv storage.
 *
 * `env.get(name)` similarly duplicates the C library getenv result so
 * the returned Osty String owns its own managed storage. Missing keys
 * surface as NULL, matching the ptr-backed `String?` lowering.
 *
 * `env.vars()` snapshots the current environment into a fresh
 * `Map<String, String>`. Keys beginning with '=' are skipped on
 * Windows to avoid the drive-current-directory pseudo-entries the CRT
 * exposes through GetEnvironmentStringsA.
 *
 * `env.set(name, value)` / `env.unset(name)` return NULL on success
 * and a freshly allocated error String on failure, matching the
 * `Result<(), Error>` lowering used for directory mutations too.
 *
 * `env.currentDir()` duplicates the process working directory into a
 * fresh managed String on success. On failure the helper returns NULL
 * and leaves the platform error intact so `osty_rt_env_current_dir_error`
 * can materialize a human-readable Error message.
 *
 * `env.setCurrentDir(path)` returns NULL on success and a freshly
 * allocated error String on failure so the emitter can materialize
 * `Result<(), Error>` without relying on process-global errno state
 * after the runtime call returns. */
static int64_t osty_rt_env_stored_argc = 0;
static char *const *osty_rt_env_stored_argv = NULL;

static void *osty_rt_env_error_message(const char *prefix, const char *detail, const char *site) {
    const char *lhs = prefix == NULL ? "environment error" : prefix;
    const char *rhs = (detail != NULL && detail[0] != '\0') ? detail : "unknown error";
    size_t lhs_len = strlen(lhs);
    size_t rhs_len = strlen(rhs);
    size_t total = lhs_len + 2 + rhs_len;
    char *buf = (char *)osty_rt_xmalloc(total + 1, site);
    memcpy(buf, lhs, lhs_len);
    buf[lhs_len] = ':';
    buf[lhs_len + 1] = ' ';
    memcpy(buf + lhs_len + 2, rhs, rhs_len);
    buf[total] = '\0';
    void *out = osty_rt_string_dup_site(buf, total, site);
    free(buf);
    return out;
}

void osty_rt_env_args_init(int64_t argc, void *argv) {
    osty_rt_env_stored_argc = argc < 0 ? 0 : argc;
    osty_rt_env_stored_argv = (char *const *)argv;
}

void *osty_rt_env_args(void) {
    void *out = osty_rt_list_new();
    int64_t i;
    for (i = 0; i < osty_rt_env_stored_argc; i++) {
        const char *raw = (osty_rt_env_stored_argv != NULL)
            ? osty_rt_env_stored_argv[i]
            : NULL;
        if (raw == NULL) {
            raw = "";
        }
        char *copy = osty_rt_string_dup_site(raw, strlen(raw), "runtime.env.args.entry");
        osty_rt_list_push_ptr(out, copy);
    }
    return out;
}

void *osty_rt_env_get(const char *name) {
    const char *raw;
    if (name == NULL) {
        return NULL;
    }
    raw = getenv(name);
    if (raw == NULL) {
        return NULL;
    }
    return osty_rt_string_dup_site(raw, strlen(raw), "runtime.env.get");
}

#if defined(OSTY_RT_PLATFORM_WIN32)
static LPCH osty_rt_env_entries_block(void) {
    return GetEnvironmentStringsA();
}
#else
static char *const *osty_rt_env_entries_posix(void) {
#  if defined(__APPLE__)
    char ***envp = _NSGetEnviron();
    return envp == NULL ? NULL : (char *const *)(*envp);
#  else
    extern char **environ;
    return (char *const *)environ;
#  endif
}
#endif

void *osty_rt_env_vars(void) {
    void *out = osty_rt_map_new(OSTY_RT_ABI_STRING, OSTY_RT_ABI_PTR,
                                (int64_t)sizeof(void *), NULL);
    osty_gc_root_bind_v1(out);
#if defined(OSTY_RT_PLATFORM_WIN32)
    LPCH block = osty_rt_env_entries_block();
    LPCH cursor;
    if (block != NULL) {
        for (cursor = block; cursor[0] != '\0'; cursor += strlen(cursor) + 1) {
            const char *entry = (const char *)cursor;
            const char *eq = strchr(entry, '=');
            size_t key_len;
            const char *value_text;
            char *key_copy;
            char *value_copy;
            if (eq == NULL || eq == entry) {
                continue;
            }
            key_len = (size_t)(eq - entry);
            value_text = eq + 1;
            key_copy = osty_rt_string_dup_site(entry, key_len, "runtime.env.vars.key");
            osty_gc_root_bind_v1(key_copy);
            value_copy = osty_rt_string_dup_site(value_text, strlen(value_text), "runtime.env.vars.value");
            osty_gc_root_bind_v1(value_copy);
            osty_rt_map_insert_string(out, key_copy, &value_copy);
            osty_gc_root_release_v1(value_copy);
            osty_gc_root_release_v1(key_copy);
        }
        FreeEnvironmentStringsA(block);
    }
#else
    char *const *entries = osty_rt_env_entries_posix();
    if (entries != NULL) {
        char *const *cursor;
        for (cursor = entries; *cursor != NULL; cursor++) {
            const char *entry = *cursor;
            const char *eq;
            size_t key_len;
            const char *value_text;
            char *key_copy;
            char *value_copy;
            if (entry == NULL) {
                continue;
            }
            eq = strchr(entry, '=');
            if (eq == NULL || eq == entry) {
                continue;
            }
            key_len = (size_t)(eq - entry);
            value_text = eq + 1;
            key_copy = osty_rt_string_dup_site(entry, key_len, "runtime.env.vars.key");
            osty_gc_root_bind_v1(key_copy);
            value_copy = osty_rt_string_dup_site(value_text, strlen(value_text), "runtime.env.vars.value");
            osty_gc_root_bind_v1(value_copy);
            osty_rt_map_insert_string(out, key_copy, &value_copy);
            osty_gc_root_release_v1(value_copy);
            osty_gc_root_release_v1(key_copy);
        }
    }
#endif
    osty_gc_root_release_v1(out);
    return out;
}

void *osty_rt_env_set(const char *name, const char *value) {
    if (name == NULL) {
        return osty_rt_env_error_message("failed to set environment variable", "name is null", "runtime.env.set.error");
    }
    if (value == NULL) {
        return osty_rt_env_error_message("failed to set environment variable", "value is null", "runtime.env.set.error");
    }
#if defined(OSTY_RT_PLATFORM_WIN32)
    if (SetEnvironmentVariableA(name, value)) {
        return NULL;
    }
    DWORD code = GetLastError();
    char buf[96];
    int written;
    if (code == 0) {
        return osty_rt_env_error_message("failed to set environment variable", "unknown error", "runtime.env.set.error");
    }
    written = snprintf(buf, sizeof(buf), "win32 error %lu", (unsigned long)code);
    if (written < 0) {
        return osty_rt_env_error_message("failed to set environment variable", "win32 error", "runtime.env.set.error");
    }
    return osty_rt_env_error_message("failed to set environment variable", buf, "runtime.env.set.error");
#else
    errno = 0;
    if (setenv(name, value, 1) == 0) {
        return NULL;
    }
    if (errno == 0) {
        return osty_rt_env_error_message("failed to set environment variable", "unknown error", "runtime.env.set.error");
    }
    return osty_rt_env_error_message("failed to set environment variable", strerror(errno), "runtime.env.set.error");
#endif
}

void *osty_rt_env_unset(const char *name) {
    if (name == NULL) {
        return osty_rt_env_error_message("failed to unset environment variable", "name is null", "runtime.env.unset.error");
    }
#if defined(OSTY_RT_PLATFORM_WIN32)
    if (SetEnvironmentVariableA(name, NULL)) {
        return NULL;
    }
    DWORD code = GetLastError();
    char buf[96];
    int written;
    if (code == 0) {
        return osty_rt_env_error_message("failed to unset environment variable", "unknown error", "runtime.env.unset.error");
    }
    written = snprintf(buf, sizeof(buf), "win32 error %lu", (unsigned long)code);
    if (written < 0) {
        return osty_rt_env_error_message("failed to unset environment variable", "win32 error", "runtime.env.unset.error");
    }
    return osty_rt_env_error_message("failed to unset environment variable", buf, "runtime.env.unset.error");
#else
    errno = 0;
    if (unsetenv(name) == 0) {
        return NULL;
    }
    if (errno == 0) {
        return osty_rt_env_error_message("failed to unset environment variable", "unknown error", "runtime.env.unset.error");
    }
    return osty_rt_env_error_message("failed to unset environment variable", strerror(errno), "runtime.env.unset.error");
#endif
}

void *osty_rt_env_current_dir(void) {
#if defined(OSTY_RT_PLATFORM_WIN32)
    DWORD need = GetCurrentDirectoryA(0, NULL);
    if (need == 0) {
        return NULL;
    }
    char *buf = (char *)osty_rt_xmalloc((size_t)need + 1, "runtime.env.current_dir.buf");
    DWORD written = GetCurrentDirectoryA(need + 1, buf);
    if (written == 0 || written > need) {
        DWORD code = GetLastError();
        free(buf);
        SetLastError(code);
        return NULL;
    }
    void *out = osty_rt_string_dup_site(buf, (size_t)written, "runtime.env.current_dir");
    free(buf);
    return out;
#else
    size_t cap = 256;
    for (;;) {
        char *buf = (char *)osty_rt_xmalloc(cap, "runtime.env.current_dir.buf");
        errno = 0;
        if (getcwd(buf, cap) != NULL) {
            void *out = osty_rt_string_dup_site(buf, strlen(buf), "runtime.env.current_dir");
            free(buf);
            return out;
        }
        int err = errno;
        free(buf);
        if (err != ERANGE) {
            errno = err;
            return NULL;
        }
        if (cap > ((size_t)-1) / 2) {
            errno = ERANGE;
            return NULL;
        }
        cap *= 2;
    }
#endif
}

void *osty_rt_env_current_dir_error(void) {
#if defined(OSTY_RT_PLATFORM_WIN32)
    DWORD code = GetLastError();
    char buf[96];
    int written;
    if (code == 0) {
        return osty_rt_env_error_message("failed to get current directory", "unknown error", "runtime.env.current_dir.error");
    }
    written = snprintf(buf, sizeof(buf), "win32 error %lu", (unsigned long)code);
    if (written < 0) {
        return osty_rt_env_error_message("failed to get current directory", "win32 error", "runtime.env.current_dir.error");
    }
    return osty_rt_env_error_message("failed to get current directory", buf, "runtime.env.current_dir.error");
#else
    int err = errno;
    if (err == 0) {
        return osty_rt_env_error_message("failed to get current directory", "unknown error", "runtime.env.current_dir.error");
    }
    return osty_rt_env_error_message("failed to get current directory", strerror(err), "runtime.env.current_dir.error");
#endif
}

void *osty_rt_env_set_current_dir(const char *path) {
    if (path == NULL) {
        return osty_rt_env_error_message("failed to set current directory", "path is null", "runtime.env.set_current_dir.error");
    }
#if defined(OSTY_RT_PLATFORM_WIN32)
    if (SetCurrentDirectoryA(path)) {
        return NULL;
    }
    DWORD code = GetLastError();
    char buf[96];
    int written;
    if (code == 0) {
        return osty_rt_env_error_message("failed to set current directory", "unknown error", "runtime.env.set_current_dir.error");
    }
    written = snprintf(buf, sizeof(buf), "win32 error %lu", (unsigned long)code);
    if (written < 0) {
        return osty_rt_env_error_message("failed to set current directory", "win32 error", "runtime.env.set_current_dir.error");
    }
    return osty_rt_env_error_message("failed to set current directory", buf, "runtime.env.set_current_dir.error");
#else
    errno = 0;
    if (chdir(path) == 0) {
        return NULL;
    }
    int err = errno;
    if (err == 0) {
        return osty_rt_env_error_message("failed to set current directory", "unknown error", "runtime.env.set_current_dir.error");
    }
    return osty_rt_env_error_message("failed to set current directory", strerror(err), "runtime.env.set_current_dir.error");
#endif
}

/* std.os process-control surface.
 *
 * `std.os.exec` / `execShell` lower to `osty_rt_os_exec(cmd, args, shell)`.
 * The helper returns a by-value aggregate so the emitter can distinguish
 * launch/runtime failures from ordinary non-zero child exit codes without
 * re-running the process:
 *
 *   tag = 0 -> success, exit_code/stdout/stderr populated, error_text NULL
 *   tag = 1 -> failure, error_text populated, output fields zeroed
 *
 * `std.os.hostname()` follows the same pattern with a narrower
 * `{tag, value, error}` result. `pid()` and `exit(code)` are direct
 * runtime calls. `onSignal` is intentionally not implemented here. */
typedef struct osty_rt_os_exec_result {
    int64_t tag;
    int64_t exit_code;
    void *stdout_text;
    void *stderr_text;
    void *error_text;
} osty_rt_os_exec_result;

typedef struct osty_rt_os_string_result {
    int64_t tag;
    void *value_text;
    void *error_text;
} osty_rt_os_string_result;

static void *osty_rt_os_error_message(const char *prefix, const char *detail, const char *site) {
    const char *lhs = prefix == NULL ? "process error" : prefix;
    const char *rhs = (detail != NULL && detail[0] != '\0') ? detail : "unknown error";
    size_t lhs_len = strlen(lhs);
    size_t rhs_len = strlen(rhs);
    size_t total = lhs_len + 2 + rhs_len;
    char *buf = (char *)osty_rt_xmalloc(total + 1, site);
    memcpy(buf, lhs, lhs_len);
    buf[lhs_len] = ':';
    buf[lhs_len + 1] = ' ';
    memcpy(buf + lhs_len + 2, rhs, rhs_len);
    buf[total] = '\0';
    void *out = osty_rt_string_dup_site(buf, total, site);
    free(buf);
    return out;
}

static void *osty_rt_os_empty_string(const char *site) {
    return osty_rt_string_dup_site("", 0, site);
}

static void *osty_rt_os_read_file_to_string(const char *path, const char *site) {
    FILE *fp;
    long size;
    size_t read_n;
    char *buf;
    void *out;
    if (path == NULL) {
        errno = EINVAL;
        return NULL;
    }
    fp = fopen(path, "rb");
    if (fp == NULL) {
        return NULL;
    }
    if (fseek(fp, 0, SEEK_END) != 0) {
        fclose(fp);
        return NULL;
    }
    size = ftell(fp);
    if (size < 0) {
        fclose(fp);
        return NULL;
    }
    if (fseek(fp, 0, SEEK_SET) != 0) {
        fclose(fp);
        return NULL;
    }
    buf = (char *)osty_rt_xmalloc((size_t)size + 1, site);
    read_n = fread(buf, 1, (size_t)size, fp);
    if (read_n != (size_t)size) {
        free(buf);
        fclose(fp);
        return NULL;
    }
    buf[size] = '\0';
    out = osty_rt_string_dup_site(buf, (size_t)size, site);
    free(buf);
    fclose(fp);
    return out;
}

static osty_rt_os_exec_result *osty_rt_os_exec_error_result(const char *prefix, const char *detail, const char *site) {
    osty_rt_os_exec_result *out =
        (osty_rt_os_exec_result *)osty_rt_xmalloc(sizeof(*out), site);
    out->tag = 1;
    out->exit_code = 0;
    out->stdout_text = NULL;
    out->stderr_text = NULL;
    out->error_text = osty_rt_os_error_message(prefix, detail, site);
    return out;
}

static osty_rt_os_string_result *osty_rt_os_string_error_result(const char *prefix, const char *detail, const char *site) {
    osty_rt_os_string_result *out =
        (osty_rt_os_string_result *)osty_rt_xmalloc(sizeof(*out), site);
    out->tag = 1;
    out->value_text = NULL;
    out->error_text = osty_rt_os_error_message(prefix, detail, site);
    return out;
}

#if defined(_WIN32)
static char *osty_rt_os_win_strdup(const char *text, const char *site) {
    size_t n = text == NULL ? 0 : strlen(text);
    char *out = (char *)osty_rt_xmalloc(n + 1, site);
    if (n != 0) {
        memcpy(out, text, n);
    }
    out[n] = '\0';
    return out;
}

static bool osty_rt_os_open_tempfile_win(char **out_path, HANDLE *out_handle) {
    char dir[MAX_PATH + 1];
    char name[MAX_PATH + 1];
    SECURITY_ATTRIBUTES sa;
    DWORD dir_len;
    if (out_path == NULL || out_handle == NULL) {
        return false;
    }
    dir_len = GetTempPathA((DWORD)sizeof(dir), dir);
    if (dir_len == 0 || dir_len >= sizeof(dir)) {
        return false;
    }
    if (GetTempFileNameA(dir, "ost", 0, name) == 0) {
        return false;
    }
    sa.nLength = sizeof(sa);
    sa.lpSecurityDescriptor = NULL;
    sa.bInheritHandle = TRUE;
    *out_handle = CreateFileA(
        name,
        GENERIC_READ | GENERIC_WRITE,
        FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
        &sa,
        OPEN_EXISTING,
        FILE_ATTRIBUTE_TEMPORARY | FILE_FLAG_SEQUENTIAL_SCAN,
        NULL
    );
    if (*out_handle == INVALID_HANDLE_VALUE) {
        DeleteFileA(name);
        return false;
    }
    *out_path = osty_rt_os_win_strdup(name, "runtime.os.tempfile.path");
    return true;
}

static size_t osty_rt_os_win_quoted_len(const char *arg) {
    size_t n = 0;
    size_t backslashes = 0;
    bool needs_quotes = false;
    const unsigned char *p = (const unsigned char *)(arg == NULL ? "" : arg);
    if (*p == '\0') {
        return 2;
    }
    for (; *p != '\0'; p++) {
        if (*p == ' ' || *p == '\t' || *p == '"') {
            needs_quotes = true;
        }
    }
    if (!needs_quotes) {
        return strlen(arg);
    }
    n = 2;
    p = (const unsigned char *)(arg == NULL ? "" : arg);
    for (; *p != '\0'; p++) {
        if (*p == '\\') {
            backslashes++;
            continue;
        }
        if (*p == '"') {
            n += backslashes * 2 + 2;
            backslashes = 0;
            continue;
        }
        n += backslashes + 1;
        backslashes = 0;
    }
    n += backslashes * 2;
    return n;
}

static char *osty_rt_os_win_append_quoted(char *dst, const char *arg) {
    size_t backslashes = 0;
    bool needs_quotes = false;
    const unsigned char *p = (const unsigned char *)(arg == NULL ? "" : arg);
    if (*p == '\0') {
        *dst++ = '"';
        *dst++ = '"';
        return dst;
    }
    for (; *p != '\0'; p++) {
        if (*p == ' ' || *p == '\t' || *p == '"') {
            needs_quotes = true;
        }
    }
    if (!needs_quotes) {
        size_t n = strlen(arg);
        memcpy(dst, arg, n);
        return dst + n;
    }
    *dst++ = '"';
    p = (const unsigned char *)(arg == NULL ? "" : arg);
    for (; *p != '\0'; p++) {
        if (*p == '\\') {
            backslashes++;
            continue;
        }
        if (*p == '"') {
            while (backslashes-- > 0) {
                *dst++ = '\\';
                *dst++ = '\\';
            }
            backslashes = 0;
            *dst++ = '\\';
            *dst++ = '"';
            continue;
        }
        while (backslashes-- > 0) {
            *dst++ = '\\';
        }
        backslashes = 0;
        *dst++ = (char)(*p);
    }
    while (backslashes-- > 0) {
        *dst++ = '\\';
        *dst++ = '\\';
    }
    *dst++ = '"';
    return dst;
}

static char *osty_rt_os_build_windows_command_line(const char *cmd, void *args, bool shell) {
    int64_t arg_count = args != NULL ? osty_rt_list_len(args) : 0;
    size_t total = 1;
    char *buf;
    char *cursor;
    int64_t i;
    if (shell) {
        total += osty_rt_os_win_quoted_len("cmd.exe") + 1;
        total += osty_rt_os_win_quoted_len("/d") + 1;
        total += osty_rt_os_win_quoted_len("/s") + 1;
        total += osty_rt_os_win_quoted_len("/c") + 1;
        total += osty_rt_os_win_quoted_len(cmd == NULL ? "" : cmd);
    } else {
        total += osty_rt_os_win_quoted_len(cmd == NULL ? "" : cmd);
        for (i = 0; i < arg_count; i++) {
            const char *arg = (const char *)osty_rt_list_get_ptr(args, i);
            total += 1 + osty_rt_os_win_quoted_len(arg == NULL ? "" : arg);
        }
    }
    buf = (char *)osty_rt_xmalloc(total, "runtime.os.exec.win.cmdline");
    cursor = buf;
    if (shell) {
        cursor = osty_rt_os_win_append_quoted(cursor, "cmd.exe");
        *cursor++ = ' ';
        cursor = osty_rt_os_win_append_quoted(cursor, "/d");
        *cursor++ = ' ';
        cursor = osty_rt_os_win_append_quoted(cursor, "/s");
        *cursor++ = ' ';
        cursor = osty_rt_os_win_append_quoted(cursor, "/c");
        *cursor++ = ' ';
        cursor = osty_rt_os_win_append_quoted(cursor, cmd == NULL ? "" : cmd);
    } else {
        cursor = osty_rt_os_win_append_quoted(cursor, cmd == NULL ? "" : cmd);
        for (i = 0; i < arg_count; i++) {
            const char *arg = (const char *)osty_rt_list_get_ptr(args, i);
            *cursor++ = ' ';
            cursor = osty_rt_os_win_append_quoted(cursor, arg == NULL ? "" : arg);
        }
    }
    *cursor = '\0';
    return buf;
}

static osty_rt_os_exec_result *osty_rt_os_exec_windows(const char *cmd, void *args, bool shell) {
    char *stdout_path = NULL;
    char *stderr_path = NULL;
    char *cmdline = NULL;
    void *stdout_text = NULL;
    void *stderr_text = NULL;
    HANDLE stdout_handle = INVALID_HANDLE_VALUE;
    HANDLE stderr_handle = INVALID_HANDLE_VALUE;
    HANDLE stdin_handle = INVALID_HANDLE_VALUE;
    PROCESS_INFORMATION pi;
    STARTUPINFOA si;
    DWORD exit_code = 0;
    osty_rt_os_exec_result *out;
    if (cmd == NULL || cmd[0] == '\0') {
        return osty_rt_os_exec_error_result("failed to launch process", "command is empty", "runtime.os.exec.error");
    }
    if (!osty_rt_os_open_tempfile_win(&stdout_path, &stdout_handle)) {
        return osty_rt_os_exec_error_result("failed to launch process", "could not create stdout temp file", "runtime.os.exec.error");
    }
    if (!osty_rt_os_open_tempfile_win(&stderr_path, &stderr_handle)) {
        CloseHandle(stdout_handle);
        DeleteFileA(stdout_path);
        free(stdout_path);
        return osty_rt_os_exec_error_result("failed to launch process", "could not create stderr temp file", "runtime.os.exec.error");
    }
    {
        SECURITY_ATTRIBUTES sa;
        sa.nLength = sizeof(sa);
        sa.lpSecurityDescriptor = NULL;
        sa.bInheritHandle = TRUE;
        stdin_handle = CreateFileA("NUL", GENERIC_READ, FILE_SHARE_READ | FILE_SHARE_WRITE, &sa, OPEN_EXISTING, FILE_ATTRIBUTE_NORMAL, NULL);
        if (stdin_handle == INVALID_HANDLE_VALUE) {
            CloseHandle(stdout_handle);
            CloseHandle(stderr_handle);
            DeleteFileA(stdout_path);
            DeleteFileA(stderr_path);
            free(stdout_path);
            free(stderr_path);
            return osty_rt_os_exec_error_result("failed to launch process", "could not open NUL for stdin", "runtime.os.exec.error");
        }
    }
    cmdline = osty_rt_os_build_windows_command_line(cmd, args, shell);
    ZeroMemory(&pi, sizeof(pi));
    ZeroMemory(&si, sizeof(si));
    si.cb = sizeof(si);
    si.dwFlags = STARTF_USESTDHANDLES;
    si.hStdInput = stdin_handle;
    si.hStdOutput = stdout_handle;
    si.hStdError = stderr_handle;
    if (!CreateProcessA(NULL, cmdline, NULL, NULL, TRUE, 0, NULL, NULL, &si, &pi)) {
        DWORD code = GetLastError();
        char buf[96];
        int written = snprintf(buf, sizeof(buf), "win32 error %lu", (unsigned long)code);
        CloseHandle(stdin_handle);
        CloseHandle(stdout_handle);
        CloseHandle(stderr_handle);
        DeleteFileA(stdout_path);
        DeleteFileA(stderr_path);
        free(stdout_path);
        free(stderr_path);
        free(cmdline);
        if (written < 0) {
            return osty_rt_os_exec_error_result("failed to launch process", "CreateProcessA failed", "runtime.os.exec.error");
        }
        return osty_rt_os_exec_error_result("failed to launch process", buf, "runtime.os.exec.error");
    }
    CloseHandle(stdin_handle);
    CloseHandle(stdout_handle);
    CloseHandle(stderr_handle);
    free(cmdline);
    WaitForSingleObject(pi.hProcess, INFINITE);
    if (!GetExitCodeProcess(pi.hProcess, &exit_code)) {
        DWORD code = GetLastError();
        char buf[96];
        int written = snprintf(buf, sizeof(buf), "win32 error %lu", (unsigned long)code);
        CloseHandle(pi.hProcess);
        CloseHandle(pi.hThread);
        DeleteFileA(stdout_path);
        DeleteFileA(stderr_path);
        free(stdout_path);
        free(stderr_path);
        if (written < 0) {
            return osty_rt_os_exec_error_result("failed to inspect process exit code", "GetExitCodeProcess failed", "runtime.os.exec.error");
        }
        return osty_rt_os_exec_error_result("failed to inspect process exit code", buf, "runtime.os.exec.error");
    }
    CloseHandle(pi.hProcess);
    CloseHandle(pi.hThread);
    stdout_text = osty_rt_os_read_file_to_string(stdout_path, "runtime.os.exec.stdout");
    stderr_text = osty_rt_os_read_file_to_string(stderr_path, "runtime.os.exec.stderr");
    DeleteFileA(stdout_path);
    DeleteFileA(stderr_path);
    free(stdout_path);
    free(stderr_path);
    if (stdout_text == NULL) {
        return osty_rt_os_exec_error_result("failed to read process stdout", strerror(errno), "runtime.os.exec.error");
    }
    if (stderr_text == NULL) {
        return osty_rt_os_exec_error_result("failed to read process stderr", strerror(errno), "runtime.os.exec.error");
    }
    out = (osty_rt_os_exec_result *)osty_rt_xmalloc(sizeof(*out), "runtime.os.exec.result");
    out->tag = 0;
    out->exit_code = (int64_t)exit_code;
    out->stdout_text = stdout_text;
    out->stderr_text = stderr_text;
    out->error_text = NULL;
    return out;
}
#else
static char **osty_rt_os_build_posix_argv(const char *cmd, void *args, bool shell) {
    int64_t arg_count = args != NULL ? osty_rt_list_len(args) : 0;
    char **argv;
    int64_t i;
    if (shell) {
        argv = (char **)osty_rt_xmalloc(sizeof(char *) * 4, "runtime.os.exec.argv");
        argv[0] = (char *)"/bin/sh";
        argv[1] = (char *)"-c";
        argv[2] = (char *)(cmd == NULL ? "" : cmd);
        argv[3] = NULL;
        return argv;
    }
    argv = (char **)osty_rt_xmalloc(sizeof(char *) * (size_t)(arg_count + 2), "runtime.os.exec.argv");
    argv[0] = (char *)(cmd == NULL ? "" : cmd);
    for (i = 0; i < arg_count; i++) {
        char *arg = (char *)osty_rt_list_get_ptr(args, i);
        argv[i + 1] = arg == NULL ? (char *)"" : arg;
    }
    argv[arg_count + 1] = NULL;
    return argv;
}

static int osty_rt_os_open_tempfile_posix(const char *prefix, char **out_path) {
    const char *tmp = getenv("TMPDIR");
    size_t tmp_len;
    size_t total;
    char *path;
    int fd;
    if (out_path == NULL) {
        errno = EINVAL;
        return -1;
    }
    if (tmp == NULL || tmp[0] == '\0') {
        tmp = "/tmp";
    }
    tmp_len = strlen(tmp);
    total = tmp_len + 1 + strlen(prefix) + 6 + 1;
    path = (char *)osty_rt_xmalloc(total, "runtime.os.exec.tmp.path");
    snprintf(path, total, "%s/%sXXXXXX", tmp, prefix);
    fd = mkstemp(path);
    if (fd < 0) {
        free(path);
        return -1;
    }
    *out_path = path;
    return fd;
}

static osty_rt_os_exec_result *osty_rt_os_exec_posix(const char *cmd, void *args, bool shell) {
    char *stdout_path = NULL;
    char *stderr_path = NULL;
    int stdout_fd = -1;
    int stderr_fd = -1;
    int exec_pipe[2] = {-1, -1};
    char **argv = NULL;
    pid_t pid;
    int status = 0;
    int exec_errno = 0;
    ssize_t exec_read;
    pid_t waited;
    void *stdout_text = NULL;
    void *stderr_text = NULL;
    osty_rt_os_exec_result *out;
    sigset_t sigurg_mask;
    sigset_t old_mask;
    int sigmask_set = 0;
    struct sigaction old_sigchld;
    struct sigaction default_sigchld;
    int sigchld_reset = 0;
    if (cmd == NULL || cmd[0] == '\0') {
        return osty_rt_os_exec_error_result("failed to launch process", "command is empty", "runtime.os.exec.error");
    }
    stdout_fd = osty_rt_os_open_tempfile_posix("osty-out.", &stdout_path);
    if (stdout_fd < 0) {
        return osty_rt_os_exec_error_result("failed to launch process", strerror(errno), "runtime.os.exec.error");
    }
    stderr_fd = osty_rt_os_open_tempfile_posix("osty-err.", &stderr_path);
    if (stderr_fd < 0) {
        close(stdout_fd);
        unlink(stdout_path);
        free(stdout_path);
        return osty_rt_os_exec_error_result("failed to launch process", strerror(errno), "runtime.os.exec.error");
    }
    if (pipe(exec_pipe) != 0) {
        close(stdout_fd);
        close(stderr_fd);
        unlink(stdout_path);
        unlink(stderr_path);
        free(stdout_path);
        free(stderr_path);
        return osty_rt_os_exec_error_result("failed to launch process", strerror(errno), "runtime.os.exec.error");
    }
    (void)fcntl(exec_pipe[1], F_SETFD, FD_CLOEXEC);
    argv = osty_rt_os_build_posix_argv(cmd, args, shell);
    if (sigaction(SIGCHLD, NULL, &old_sigchld) == 0 && old_sigchld.sa_handler == SIG_IGN) {
        memset(&default_sigchld, 0, sizeof(default_sigchld));
        default_sigchld.sa_handler = SIG_DFL;
        sigemptyset(&default_sigchld.sa_mask);
        if (sigaction(SIGCHLD, &default_sigchld, NULL) == 0) {
            sigchld_reset = 1;
        }
    }
    pid = fork();
    if (pid < 0) {
        int err = errno;
        if (sigchld_reset) {
            (void)sigaction(SIGCHLD, &old_sigchld, NULL);
        }
        close(stdout_fd);
        close(stderr_fd);
        close(exec_pipe[0]);
        close(exec_pipe[1]);
        unlink(stdout_path);
        unlink(stderr_path);
        free(stdout_path);
        free(stderr_path);
        free(argv);
        return osty_rt_os_exec_error_result("failed to launch process", strerror(err), "runtime.os.exec.error");
    }
    if (pid == 0) {
        close(exec_pipe[0]);
        if (dup2(stdout_fd, STDOUT_FILENO) < 0 || dup2(stderr_fd, STDERR_FILENO) < 0) {
            int err = errno;
            (void)write(exec_pipe[1], &err, sizeof(err));
            _exit(127);
        }
        close(stdout_fd);
        close(stderr_fd);
        if (shell) {
            execv("/bin/sh", argv);
        } else {
            execvp(cmd, argv);
        }
        {
            int err = errno;
            (void)write(exec_pipe[1], &err, sizeof(err));
        }
        _exit(127);
    }
    close(stdout_fd);
    close(stderr_fd);
    close(exec_pipe[1]);
    sigemptyset(&sigurg_mask);
    sigaddset(&sigurg_mask, SIGURG);
    if (sigprocmask(SIG_BLOCK, &sigurg_mask, &old_mask) == 0) {
        sigmask_set = 1;
    }
    do {
        waited = waitpid(pid, &status, 0);
    } while (waited < 0 && errno == EINTR);
    do {
        exec_read = read(exec_pipe[0], &exec_errno, sizeof(exec_errno));
    } while (exec_read < 0 && errno == EINTR);
    close(exec_pipe[0]);
    if (sigmask_set) {
        (void)sigprocmask(SIG_SETMASK, &old_mask, NULL);
    }
    if (sigchld_reset) {
        (void)sigaction(SIGCHLD, &old_sigchld, NULL);
    }
    free(argv);
    if (waited < 0) {
        unlink(stdout_path);
        unlink(stderr_path);
        free(stdout_path);
        free(stderr_path);
        return osty_rt_os_exec_error_result("failed to wait for process", strerror(errno), "runtime.os.exec.error");
    }
    if (exec_read > 0) {
        unlink(stdout_path);
        unlink(stderr_path);
        free(stdout_path);
        free(stderr_path);
        return osty_rt_os_exec_error_result("failed to launch process", strerror(exec_errno), "runtime.os.exec.error");
    }
    stdout_text = osty_rt_os_read_file_to_string(stdout_path, "runtime.os.exec.stdout");
    stderr_text = osty_rt_os_read_file_to_string(stderr_path, "runtime.os.exec.stderr");
    unlink(stdout_path);
    unlink(stderr_path);
    free(stdout_path);
    free(stderr_path);
    if (stdout_text == NULL) {
        return osty_rt_os_exec_error_result("failed to read process stdout", strerror(errno), "runtime.os.exec.error");
    }
    if (stderr_text == NULL) {
        return osty_rt_os_exec_error_result("failed to read process stderr", strerror(errno), "runtime.os.exec.error");
    }
    out = (osty_rt_os_exec_result *)osty_rt_xmalloc(sizeof(*out), "runtime.os.exec.result");
    out->tag = 0;
    if (WIFEXITED(status)) {
        out->exit_code = (int64_t)WEXITSTATUS(status);
    } else if (WIFSIGNALED(status)) {
        out->exit_code = 128 + (int64_t)WTERMSIG(status);
    } else {
        out->exit_code = 1;
    }
    out->stdout_text = stdout_text;
    out->stderr_text = stderr_text;
    out->error_text = NULL;
    return out;
}
#endif

osty_rt_os_exec_result *osty_rt_os_exec(const char *cmd, void *args, bool shell) {
#if defined(_WIN32)
    return osty_rt_os_exec_windows(cmd, args, shell);
#else
    return osty_rt_os_exec_posix(cmd, args, shell);
#endif
}

osty_rt_os_string_result *osty_rt_os_hostname(void) {
#if defined(_WIN32)
    char buf[MAX_COMPUTERNAME_LENGTH + 1];
    DWORD size = (DWORD)sizeof(buf);
    if (!GetComputerNameA(buf, &size)) {
        DWORD code = GetLastError();
        char errbuf[96];
        int written = snprintf(errbuf, sizeof(errbuf), "win32 error %lu", (unsigned long)code);
        if (written < 0) {
            return osty_rt_os_string_error_result("failed to get hostname", "GetComputerNameA failed", "runtime.os.hostname.error");
        }
        return osty_rt_os_string_error_result("failed to get hostname", errbuf, "runtime.os.hostname.error");
    }
    {
        osty_rt_os_string_result *out =
            (osty_rt_os_string_result *)osty_rt_xmalloc(sizeof(*out), "runtime.os.hostname.result");
        out->tag = 0;
        out->value_text = osty_rt_string_dup_site(buf, (size_t)size, "runtime.os.hostname");
        out->error_text = NULL;
        return out;
    }
#else
    char buf[256];
    if (gethostname(buf, sizeof(buf)) != 0) {
        int err = errno;
        if (err == 0) {
            return osty_rt_os_string_error_result("failed to get hostname", "gethostname failed", "runtime.os.hostname.error");
        }
        return osty_rt_os_string_error_result("failed to get hostname", strerror(err), "runtime.os.hostname.error");
    }
    buf[sizeof(buf) - 1] = '\0';
    {
        osty_rt_os_string_result *out =
            (osty_rt_os_string_result *)osty_rt_xmalloc(sizeof(*out), "runtime.os.hostname.result");
        out->tag = 0;
        out->value_text = osty_rt_string_dup_site(buf, strlen(buf), "runtime.os.hostname");
        out->error_text = NULL;
        return out;
    }
#endif
}

void osty_rt_os_exec_result_free(osty_rt_os_exec_result *result) {
    free(result);
}

void osty_rt_os_string_result_free(osty_rt_os_string_result *result) {
    free(result);
}

int64_t osty_rt_os_pid(void) {
#if defined(_WIN32)
    return (int64_t)GetCurrentProcessId();
#else
    return (int64_t)getpid();
#endif
}

void osty_rt_os_exit(int32_t code) {
    exit((int)code);
}
