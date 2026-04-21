/* Feature test macros enable the POSIX 2008 surface (nanosleep,
 * pthread_mutexattr_settype, sched_yield, PTHREAD_MUTEX_RECURSIVE)
 * across glibc/musl/Darwin regardless of compiler default. 2008
 * supersedes the earlier 199309L level previously used for nanosleep
 * alone. Windows skips the POSIX surface entirely and uses the Win32
 * synchronization primitives below. */
#if !defined(_WIN32)
#ifndef _POSIX_C_SOURCE
#define _POSIX_C_SOURCE 200809L
#endif
#ifndef _XOPEN_SOURCE
#define _XOPEN_SOURCE 700
#endif
#endif

#include <stdbool.h>
#include <stdint.h>
#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

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
#  include <sched.h>
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

typedef struct osty_gc_header {
    struct osty_gc_header *next;
    struct osty_gc_header *prev;
    int64_t object_kind;
    int64_t byte_size;
    int64_t root_count;
    bool marked;
    osty_gc_trace_fn trace;
    osty_gc_destroy_fn destroy;
    const char *site;
    void *payload;
} osty_gc_header;

typedef struct osty_rt_list {
    int64_t len;
    int64_t cap;
    size_t elem_size;
    osty_rt_trace_slot_fn trace_elem;
    bool pointer_elems;
    int64_t gc_offset_count;
    int64_t *gc_offsets;
    unsigned char *data;
} osty_rt_list;

typedef struct osty_rt_map {
    int64_t len;
    int64_t cap;
    int64_t key_kind;
    int64_t value_kind;
    size_t value_size;
    osty_rt_trace_slot_fn value_trace;
    unsigned char *keys;
    unsigned char *values;
    // Per-map recursive mutex. Guarantees that all public map ops are
    // mutually exclusive on a single instance so concurrent mutation
    // can't realloc the slot arrays mid-read, drop the len mid-walk,
    // or interleave an insert's memmove. Recursive so that `update`
    // callbacks that touch the same map from within the lock don't
    // self-deadlock.
    pthread_mutex_t mu;
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
     * Allocation is dedicated: `osty.rt.closure_env_alloc_v1` rather
     * than the generic `osty.gc.alloc_v1`, so the trace is installed
     * at construction and not as a post-alloc mutation. Phase 1 still
     * emits envs with `capture_count = 0`, exercising the allocation
     * ABI while captures themselves are lowered in Phase 4. */
    OSTY_GC_KIND_CLOSURE_ENV = 1029,
};

/* Closure environment payload. The thunk ABI is preserved — the LLVM
 * call site still does `load ptr, ptr %env` — because `fn_ptr` stays
 * at offset 0. `capture_count` and `captures[]` follow so the trace
 * callback can iterate without any side metadata.
 *
 * Capture slots hold `void *`, always through managed-pointer
 * semantics. Non-pointer captures (scalar values boxed by the Osty
 * frontend) would still go here as tagged managed values; the
 * frontend is responsible for boxing. */
typedef struct osty_rt_closure_env {
    void *fn_ptr;
    int64_t capture_count;
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
static int64_t osty_gc_pressure_limit_bytes = 32768;
static bool osty_gc_collection_requested = false;
static int64_t osty_gc_pre_write_count = 0;
static int64_t osty_gc_pre_write_managed_count = 0;
static int64_t osty_gc_post_write_count = 0;
static int64_t osty_gc_post_write_managed_count = 0;
static int64_t osty_gc_load_count = 0;
static int64_t osty_gc_load_managed_count = 0;

/* Phase A2 cumulative counters (RUNTIME_GC_DELTA §9.3).
 *
 * `allocated_since_collect` only tracks pressure between collections; once
 * the collector runs it resets to zero. These cumulative totals preserve
 * the full lifetime signal so tests and `osty_gc_debug_stats` can observe
 * allocation throughput and sweep pressure across cycles. */
static int64_t osty_gc_allocated_bytes_total = 0;
static int64_t osty_gc_swept_count_total = 0;
static int64_t osty_gc_swept_bytes_total = 0;

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
typedef struct osty_gc_index_slot {
    void *payload;
    osty_gc_header *header;
} osty_gc_index_slot;

#define OSTY_GC_INDEX_TOMBSTONE ((void *)(uintptr_t)1)

static osty_gc_index_slot *osty_gc_index_slots = NULL;
static int64_t osty_gc_index_capacity = 0;
static int64_t osty_gc_index_count = 0;
static int64_t osty_gc_index_tombstones = 0;
static int64_t osty_gc_index_find_ops_total = 0;

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

typedef struct osty_gc_remembered_edge {
    void *owner;
    void *value;
} osty_gc_remembered_edge;

static osty_gc_remembered_edge *osty_gc_remembered_edges = NULL;
static int64_t osty_gc_remembered_edge_count = 0;
static int64_t osty_gc_remembered_edge_cap = 0;

static void osty_gc_barrier_logs_clear(void);

void osty_gc_post_write_v1(void *owner, void *value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.post_write_v1"));
void *osty_gc_load_v1(void *value) __asm__(OSTY_GC_SYMBOL("osty.gc.load_v1"));
void osty_gc_mark_slot_v1(void *slot_addr) __asm__(OSTY_GC_SYMBOL("osty.gc.mark_slot_v1"));

static void osty_rt_abort(const char *message) {
    fprintf(stderr, "osty llvm runtime: %s\n", message);
    abort();
}

/* GC + runtime-wide lock. Recursive so that collection (invoked from
 * within allocate_managed) can re-enter write-barriers and root scans
 * without deadlocking. Initialized on first use so no constructor is
 * required. */
static osty_rt_once_t osty_gc_lock_once = OSTY_RT_ONCE_INIT;
static osty_rt_rmu_t osty_gc_lock;
static int64_t osty_concurrent_workers = 0;

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
    osty_concurrent_workers += 1;
    osty_gc_release();
}

static void osty_sched_workers_dec(void) {
    osty_gc_acquire();
    if (osty_concurrent_workers <= 0) {
        osty_gc_release();
        osty_rt_abort("scheduler: worker counter underflow");
    }
    osty_concurrent_workers -= 1;
    osty_gc_release();
}

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

static void osty_gc_index_insert(void *payload, osty_gc_header *header);

static void osty_gc_index_grow(int64_t new_capacity) {
    osty_gc_index_slot *old_slots = osty_gc_index_slots;
    int64_t old_cap = osty_gc_index_capacity;
    int64_t i;
    osty_gc_index_slots = (osty_gc_index_slot *)calloc((size_t)new_capacity,
                                                       sizeof(osty_gc_index_slot));
    if (osty_gc_index_slots == NULL) {
        osty_rt_abort("out of memory (gc index)");
    }
    osty_gc_index_capacity = new_capacity;
    osty_gc_index_count = 0;
    osty_gc_index_tombstones = 0;
    if (old_slots != NULL) {
        for (i = 0; i < old_cap; i++) {
            void *p = old_slots[i].payload;
            if (p != NULL && p != OSTY_GC_INDEX_TOMBSTONE) {
                osty_gc_index_insert(p, old_slots[i].header);
            }
        }
        free(old_slots);
    }
}

static void osty_gc_index_insert(void *payload, osty_gc_header *header) {
    size_t mask;
    size_t idx;
    /* Keep load factor ≤ 0.75 including tombstones so probes stay
     * short. Capacity is always a power of two — start at 128. */
    if (osty_gc_index_slots == NULL ||
        (osty_gc_index_count + osty_gc_index_tombstones + 1) * 4 >=
            osty_gc_index_capacity * 3) {
        int64_t new_cap = osty_gc_index_capacity == 0 ? 128 : osty_gc_index_capacity * 2;
        osty_gc_index_grow(new_cap);
    }
    mask = (size_t)(osty_gc_index_capacity - 1);
    idx = osty_gc_index_hash(payload) & mask;
    while (osty_gc_index_slots[idx].payload != NULL &&
           osty_gc_index_slots[idx].payload != OSTY_GC_INDEX_TOMBSTONE) {
        idx = (idx + 1) & mask;
    }
    if (osty_gc_index_slots[idx].payload == OSTY_GC_INDEX_TOMBSTONE) {
        osty_gc_index_tombstones -= 1;
    }
    osty_gc_index_slots[idx].payload = payload;
    osty_gc_index_slots[idx].header = header;
    osty_gc_index_count += 1;
}

static osty_gc_header *osty_gc_index_lookup(void *payload) {
    size_t mask;
    size_t idx;
    if (osty_gc_index_slots == NULL || payload == NULL ||
        payload == OSTY_GC_INDEX_TOMBSTONE) {
        return NULL;
    }
    mask = (size_t)(osty_gc_index_capacity - 1);
    idx = osty_gc_index_hash(payload) & mask;
    while (osty_gc_index_slots[idx].payload != NULL) {
        if (osty_gc_index_slots[idx].payload == payload) {
            return osty_gc_index_slots[idx].header;
        }
        idx = (idx + 1) & mask;
    }
    return NULL;
}

static void osty_gc_index_remove(void *payload) {
    size_t mask;
    size_t idx;
    if (osty_gc_index_slots == NULL || payload == NULL ||
        payload == OSTY_GC_INDEX_TOMBSTONE) {
        return;
    }
    mask = (size_t)(osty_gc_index_capacity - 1);
    idx = osty_gc_index_hash(payload) & mask;
    while (osty_gc_index_slots[idx].payload != NULL) {
        if (osty_gc_index_slots[idx].payload == payload) {
            osty_gc_index_slots[idx].payload = OSTY_GC_INDEX_TOMBSTONE;
            osty_gc_index_slots[idx].header = NULL;
            osty_gc_index_count -= 1;
            osty_gc_index_tombstones += 1;
            return;
        }
        idx = (idx + 1) & mask;
    }
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
    osty_gc_index_insert(header->payload, header);
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
    osty_gc_index_remove(header->payload);
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

static void osty_gc_note_allocation(size_t payload_size) {
    int64_t pressure_limit = osty_gc_pressure_limit_now();

    if (payload_size > (size_t)INT64_MAX) {
        osty_rt_abort("GC payload size overflow");
    }
    osty_gc_allocated_since_collect += (int64_t)payload_size;
    osty_gc_allocated_bytes_total += (int64_t)payload_size;
    if (pressure_limit <= 0) {
        return;
    }
    if (osty_gc_live_bytes >= pressure_limit || osty_gc_allocated_since_collect >= pressure_limit) {
        osty_gc_collection_requested = true;
    }
}

static osty_gc_header *osty_gc_find_header(void *payload) {
    /* Phase A3 depth: O(1) expected via hash index. The linked list
     * is now purely for iteration order (mark seeding, sweep, heap
     * validation) — all "is this a managed pointer" queries go
     * through the hash. */
    osty_gc_index_find_ops_total += 1;
    return osty_gc_index_lookup(payload);
}

static void *osty_gc_allocate_managed(size_t byte_size, int64_t object_kind, const char *site, osty_gc_trace_fn trace, osty_gc_destroy_fn destroy) {
    osty_gc_header *header;
    size_t payload_size = byte_size;
    size_t total_size;

    if (payload_size == 0) {
        payload_size = 1;
    }
    if (payload_size > SIZE_MAX - sizeof(osty_gc_header)) {
        osty_rt_abort("GC allocation overflow");
    }
    total_size = sizeof(osty_gc_header) + payload_size;
    header = (osty_gc_header *)calloc(1, total_size);
    if (header == NULL) {
        osty_rt_abort("out of memory");
    }
    header->object_kind = object_kind;
    header->byte_size = (int64_t)payload_size;
    header->trace = trace;
    header->destroy = destroy;
    header->site = site;
    header->payload = (void *)(header + 1);
    osty_gc_acquire();
    osty_gc_link(header);
    osty_gc_note_allocation(payload_size);
    osty_gc_release();
    return header->payload;
}

static void osty_gc_mark_payload(void *payload);
bool osty_rt_strings_Equal(const char *left, const char *right);
int64_t osty_rt_strings_Compare(const char *left, const char *right);
int64_t osty_rt_strings_Count(const char *value, const char *substr);
bool osty_rt_strings_Contains(const char *value, const char *substr);
bool osty_rt_strings_HasSuffix(const char *value, const char *suffix);
const char *osty_rt_strings_Join(void *raw_parts, const char *sep);
const char *osty_rt_strings_Repeat(const char *value, int64_t n);
const char *osty_rt_strings_ReplaceAll(const char *value, const char *old, const char *new_value);
const char *osty_rt_strings_Slice(const char *value, int64_t start, int64_t end);
const char *osty_rt_strings_TrimPrefix(const char *value, const char *prefix);
const char *osty_rt_strings_TrimSuffix(const char *value, const char *suffix);
const char *osty_rt_strings_TrimSpace(const char *value);
void *osty_rt_strings_Chars(const char *value);
void *osty_rt_strings_Bytes(const char *value);
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
    if (list->pointer_elems) {
        for (i = 0; i < list->len; i++) {
            void *child = NULL;
            memcpy(&child, list->data + ((size_t)i * list->elem_size), sizeof(child));
            if (child != NULL) {
                osty_gc_mark_payload(child);
            }
        }
        return;
    }
    if (list->gc_offset_count <= 0 || list->gc_offsets == NULL) {
        return;
    }
    for (i = 0; i < list->len; i++) {
        unsigned char *elem = list->data + ((size_t)i * list->elem_size);
        for (j = 0; j < list->gc_offset_count; j++) {
            void *child = NULL;
            memcpy(&child, elem + (size_t)list->gc_offsets[j], sizeof(child));
            if (child != NULL) {
                osty_gc_mark_payload(child);
            }
        }
    }
}

static void osty_rt_list_destroy(void *payload) {
    osty_rt_list *list = (osty_rt_list *)payload;
    if (list != NULL) {
        free(list->gc_offsets);
        free(list->data);
    }
}

static bool osty_rt_value_equals(const void *left, const void *right, size_t size, int64_t kind) {
    if (kind == OSTY_RT_ABI_STRING) {
        const char *left_value = NULL;
        const char *right_value = NULL;
        memcpy(&left_value, left, sizeof(left_value));
        memcpy(&right_value, right, sizeof(right_value));
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
        free(map->keys);
        free(map->values);
        if (map->mu_init) {
            pthread_mutex_destroy(&map->mu);
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

static void osty_gc_mark_stack_push(osty_gc_header *header) {
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
    if (osty_gc_mark_stack_count > osty_gc_mark_stack_max_depth) {
        osty_gc_mark_stack_max_depth = osty_gc_mark_stack_count;
    }
}

static void osty_gc_mark_header(osty_gc_header *header) {
    /* Enqueue only — the actual trace happens in `osty_gc_mark_drain`.
     * Setting `marked` before pushing prevents duplicate entries when the
     * same header is reached via multiple edges (the second call short
     * circuits on the `marked` check). */
    if (header == NULL || header->marked) {
        return;
    }
    header->marked = true;
    osty_gc_mark_stack_push(header);
}

static void osty_gc_mark_payload(void *payload) {
    osty_gc_mark_header(osty_gc_find_header(payload));
}

static void osty_gc_mark_drain(void) {
    while (osty_gc_mark_stack_count > 0) {
        osty_gc_header *header = osty_gc_mark_stack[--osty_gc_mark_stack_count];
        if (header->trace != NULL) {
            /* Trace callbacks re-enter `osty_gc_mark_*` for children, which
             * just push more work onto this stack — the C call stack stays
             * bounded regardless of object graph depth. */
            header->trace(header->payload);
        }
    }
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
    osty_gc_mark_payload(payload);
}

/* Phase A2 depth: monotonic timing helper. Falls back to 0 on systems
 * where `clock_gettime` is unavailable — callers must treat zero as
 * "no measurement" (still strictly monotonic inside a single run). */
static int64_t osty_gc_now_nanos(void) {
    struct timespec ts;
    if (clock_gettime(CLOCK_MONOTONIC, &ts) != 0) {
        return 0;
    }
    return (int64_t)ts.tv_sec * 1000000000LL + (int64_t)ts.tv_nsec;
}

static void osty_gc_collect_now_with_stack_roots(void *const *root_slots, int64_t root_slot_count) {
    osty_gc_header *header;
    osty_gc_header *next;
    int64_t i;
    int64_t t_start;
    int64_t t_end;

    /* Entry invariant: every header has `marked == false` (sweep clears
     * the colour on its way out, so the next cycle does not need a
     * dedicated pre-pass). */
    t_start = osty_gc_now_nanos();

    /* 1. Seed the work queue from pinned roots (`root_bind_v1` holders). */
    header = osty_gc_objects;
    while (header != NULL) {
        if (header->root_count > 0) {
            osty_gc_mark_header(header);
        }
        header = header->next;
    }
    /* 2. Seed from the current safepoint's stack-slot descriptors. */
    for (i = 0; i < root_slot_count; i++) {
        osty_gc_mark_root_slot((void *)root_slots[i]);
    }
    /* 3. Seed from registered global slots (`global_root_register_v1`). */
    for (i = 0; i < osty_gc_global_root_count; i++) {
        osty_gc_mark_root_slot(osty_gc_global_root_slots[i]);
    }
    /* 4. Drain the work queue iteratively — bounded C stack regardless of
     *    object graph depth. Replaces prior recursive trace. */
    osty_gc_mark_drain();
    /* 5. Sweep unreachable objects and clear the colour on survivors so
     *    the post-collection heap satisfies the "no stale marks" invariant
     *    that Phase A1 `osty_gc_debug_validate_heap` relies on. */
    header = osty_gc_objects;
    while (header != NULL) {
        next = header->next;
        if (!header->marked) {
            osty_gc_swept_count_total += 1;
            osty_gc_swept_bytes_total += header->byte_size;
            if (header->destroy != NULL) {
                header->destroy(header->payload);
            }
            osty_gc_unlink(header);
            free(header);
        } else {
            header->marked = false;
        }
        header = next;
    }
    osty_gc_collection_count += 1;
    osty_gc_allocated_since_collect = 0;
    osty_gc_collection_requested = false;
    osty_gc_barrier_logs_clear();

    t_end = osty_gc_now_nanos();
    if (t_start != 0 && t_end >= t_start) {
        int64_t elapsed = t_end - t_start;
        osty_gc_collection_nanos_last = elapsed;
        osty_gc_collection_nanos_total += elapsed;
        if (elapsed > osty_gc_collection_nanos_max) {
            osty_gc_collection_nanos_max = elapsed;
        }
    }
}

static void osty_gc_collect_now(void) {
    osty_gc_collect_now_with_stack_roots(NULL, 0);
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
    return (osty_rt_list *)raw_list;
}

static void osty_rt_list_ensure_layout(osty_rt_list *list, size_t elem_size, osty_rt_trace_slot_fn trace_elem) {
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

static void osty_rt_list_ensure_gc_offsets(osty_rt_list *list, const int64_t *gc_offsets, int64_t gc_offset_count) {
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
    if (list->pointer_elems) {
        osty_rt_abort("list pointer elements cannot also use GC offsets");
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

static void osty_rt_list_reserve(osty_rt_list *list, int64_t min_cap) {
    int64_t next_cap = list->cap;
    void *next_data;
    size_t want_bytes;

    if (min_cap <= list->cap) {
        return;
    }
    if (list->elem_size == 0) {
        osty_rt_abort("list element size is zero");
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
    next_data = realloc(list->data, want_bytes);
    if (next_data == NULL) {
        osty_rt_abort("out of memory");
    }
    list->data = (unsigned char *)next_data;
    list->cap = next_cap;
}

static void osty_rt_list_push_raw(void *raw_list, const void *value, size_t elem_size, osty_rt_trace_slot_fn trace_elem) {
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
    if (len != 0) {
        memcpy(out, start, len);
    }
    out[len] = '\0';
    return out;
}

static char *osty_rt_string_dup_range(const char *start, size_t len) {
    return osty_rt_string_dup_site(start, len, "runtime.strings.split.part");
}

static bool osty_rt_f64_same_bits(double left, double right) {
    uint64_t left_bits = 0;
    uint64_t right_bits = 0;
    memcpy(&left_bits, &left, sizeof(left_bits));
    memcpy(&right_bits, &right, sizeof(right_bits));
    return left_bits == right_bits;
}

void *osty_rt_list_new(void) {
    return osty_gc_allocate_managed(sizeof(osty_rt_list), OSTY_GC_KIND_LIST, "runtime.list", osty_rt_list_trace, osty_rt_list_destroy);
}

int64_t osty_rt_list_len(void *raw_list) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    return list->len;
}

void osty_rt_list_pop_discard(void *raw_list) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    if (list == NULL || list->len <= 0) {
        osty_rt_abort("list.pop on empty list");
    }
    list->len -= 1;
}

void osty_rt_list_push_i64(void *raw_list, int64_t value) {
    osty_rt_list_push_raw(raw_list, &value, sizeof(value), NULL);
}

void osty_rt_list_push_i1(void *raw_list, bool value) {
    osty_rt_list_push_raw(raw_list, &value, sizeof(value), NULL);
}

void osty_rt_list_push_f64(void *raw_list, double value) {
    osty_rt_list_push_raw(raw_list, &value, sizeof(value), NULL);
}

void osty_rt_list_push_ptr(void *raw_list, void *value) {
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

int64_t osty_rt_list_get_i64(void *raw_list, int64_t index) {
    int64_t value;
    memcpy(&value, osty_rt_list_get_raw(raw_list, index, sizeof(value), NULL), sizeof(value));
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

void *osty_rt_list_get_ptr(void *raw_list, int64_t index) {
    void *value;
    memcpy(&value, osty_rt_list_get_raw(raw_list, index, sizeof(value), osty_gc_mark_slot_v1), sizeof(value));
    return osty_gc_load_v1(value);
}

void osty_rt_list_get_bytes(void *raw_list, int64_t index, void *out_value, int64_t elem_size, osty_rt_trace_slot_fn trace_elem) {
    if (out_value == NULL || elem_size < 0) {
        osty_rt_abort("invalid list get_bytes call");
    }
    memcpy(out_value, osty_rt_list_get_raw(raw_list, index, (size_t)elem_size, trace_elem), (size_t)elem_size);
}

void osty_rt_list_get_bytes_v1(void *raw_list, int64_t index, void *out, int64_t elem_size) {
    if (out == NULL) {
        osty_rt_abort("list output buffer is null");
    }
    if (elem_size < 0) {
        osty_rt_abort("negative list element size");
    }
    osty_rt_list_get_bytes(raw_list, index, out, elem_size, NULL);
}

void osty_rt_list_set_i64(void *raw_list, int64_t index, int64_t value) {
    osty_rt_list_set_raw(raw_list, index, &value, sizeof(value), NULL);
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
    if (left_value == NULL || right_value == NULL) {
        if (left_value == right_value) {
            return 0;
        }
        if (left_value == NULL) {
            return -1;
        }
        return 1;
    }
    return strcmp(left_value, right_value);
}

void *osty_rt_list_sorted_i64(void *raw_list) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    void *out = osty_rt_list_new();
    int64_t i;

    osty_rt_list_ensure_layout(list, sizeof(int64_t), NULL);
    for (i = 0; i < list->len; i++) {
        int64_t value = osty_rt_list_get_i64(raw_list, i);
        osty_rt_list_push_i64(out, value);
    }
    qsort(osty_rt_list_cast(out)->data, (size_t)osty_rt_list_cast(out)->len, sizeof(int64_t), osty_rt_compare_i64_ascending);
    return out;
}

void *osty_rt_list_sorted_i1(void *raw_list) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    void *out = osty_rt_list_new();
    int64_t i;

    osty_rt_list_ensure_layout(list, sizeof(bool), NULL);
    for (i = 0; i < list->len; i++) {
        bool value = osty_rt_list_get_i1(raw_list, i);
        osty_rt_list_push_i1(out, value);
    }
    qsort(osty_rt_list_cast(out)->data, (size_t)osty_rt_list_cast(out)->len, sizeof(bool), osty_rt_compare_i1_ascending);
    return out;
}

void *osty_rt_list_sorted_f64(void *raw_list) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    void *out = osty_rt_list_new();
    int64_t i;

    osty_rt_list_ensure_layout(list, sizeof(double), NULL);
    for (i = 0; i < list->len; i++) {
        double value = osty_rt_list_get_f64(raw_list, i);
        osty_rt_list_push_f64(out, value);
    }
    qsort(osty_rt_list_cast(out)->data, (size_t)osty_rt_list_cast(out)->len, sizeof(double), osty_rt_compare_f64_ascending);
    return out;
}

void *osty_rt_list_sorted_string(void *raw_list) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    void *out = osty_rt_list_new();
    int64_t i;

    osty_rt_list_ensure_layout(list, sizeof(void *), osty_gc_mark_slot_v1);
    for (i = 0; i < list->len; i++) {
        void *value = osty_rt_list_get_ptr(raw_list, i);
        osty_rt_list_push_ptr(out, value);
    }
    qsort(osty_rt_list_cast(out)->data, (size_t)osty_rt_list_cast(out)->len, sizeof(void *), osty_rt_compare_string_ascending);
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

    out_raw = osty_rt_list_new();
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
        return out_raw;
    }

    osty_rt_list_reserve(out, count);
    memcpy(out->data,
           src->data + (size_t)start * src->elem_size,
           (size_t)count * src->elem_size);
    out->len = count;

    // Emit write barriers for each embedded pointer we copied in, so
    // incremental / generational GC tracks the new list correctly.
    if (out->elem_size == sizeof(void *) && src->trace_elem != NULL) {
        int64_t i;
        for (i = 0; i < count; i++) {
            void *child = NULL;
            memcpy(&child,
                   out->data + (size_t)i * out->elem_size,
                   sizeof(child));
            if (child != NULL) {
                osty_gc_post_write_v1(out_raw, child, OSTY_GC_KIND_LIST);
            }
        }
    } else if (out->gc_offset_count > 0) {
        int64_t i;
        int64_t j;
        for (i = 0; i < count; i++) {
            unsigned char *elem = out->data + (size_t)i * out->elem_size;
            for (j = 0; j < out->gc_offset_count; j++) {
                void *child = NULL;
                memcpy(&child,
                       elem + (size_t)out->gc_offsets[j],
                       sizeof(child));
                if (child != NULL) {
                    osty_gc_post_write_v1(out_raw, child, OSTY_GC_KIND_LIST);
                }
            }
        }
    }

    return out_raw;
}

bool osty_rt_strings_Equal(const char *left, const char *right) {
    if (left == NULL || right == NULL) {
        return left == right;
    }
    return strcmp(left, right) == 0;
}

const char *osty_rt_int_to_string(int64_t value) {
    char buffer[32];
    int written = snprintf(buffer, sizeof(buffer), "%lld", (long long)value);
    if (written < 0) {
        osty_rt_abort("failed to format Int as String");
    }
    return osty_rt_string_dup_site(buffer, (size_t)written, "runtime.int.to_string");
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
    int precision;

    if (isnan(value)) {
        return osty_rt_string_dup_site("NaN", 3, "runtime.float.to_string");
    }
    if (isinf(value)) {
        if (value < 0) {
            return osty_rt_string_dup_site("-Inf", 4, "runtime.float.to_string");
        }
        return osty_rt_string_dup_site("+Inf", 4, "runtime.float.to_string");
    }

    buffer[0] = '\0';
    for (precision = 1; precision <= 17; precision++) {
        char *end = NULL;
        double parsed;

        if (snprintf(buffer, sizeof(buffer), "%.*g", precision, value) < 0) {
            osty_rt_abort("failed to format Float as String");
        }
        parsed = strtod(buffer, &end);
        if (end != NULL && *end == '\0' && osty_rt_f64_same_bits(parsed, value)) {
            break;
        }
    }
    return osty_rt_string_dup_site(buffer, strlen(buffer), "runtime.float.to_string");
}

int64_t osty_rt_strings_Compare(const char *left, const char *right) {
    int result;
    if (left == NULL) {
        if (right == NULL || right[0] == '\0') {
            return 0;
        }
        return -1;
    }
    if (right == NULL) {
        return left[0] == '\0' ? 0 : 1;
    }
    result = strcmp(left, right);
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

    value = (value == NULL) ? "" : value;
    substr = (substr == NULL) ? "" : substr;
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

int64_t osty_rt_strings_ByteLen(const char *value) {
    if (value == NULL) {
        return 0;
    }
    return (int64_t)strlen(value);
}

const char *osty_rt_strings_Concat(const char *left, const char *right) {
    size_t left_len = (left == NULL) ? 0 : strlen(left);
    size_t right_len = (right == NULL) ? 0 : strlen(right);
    char *out = (char *)osty_gc_allocate_managed(left_len + right_len + 1, OSTY_GC_KIND_STRING, "runtime.strings.concat", NULL, NULL);
    if (left_len != 0) {
        memcpy(out, left, left_len);
    }
    if (right_len != 0) {
        memcpy(out + left_len, right, right_len);
    }
    out[left_len + right_len] = '\0';
    return out;
}

bool osty_rt_strings_Contains(const char *value, const char *substr) {
    if (value == NULL || substr == NULL) {
        return false;
    }
    if (substr[0] == '\0') {
        return true;
    }
    return strstr(value, substr) != NULL;
}

bool osty_rt_strings_HasPrefix(const char *value, const char *prefix) {
    size_t prefix_len;
    if (value == NULL || prefix == NULL) {
        return false;
    }
    prefix_len = strlen(prefix);
    return strncmp(value, prefix, prefix_len) == 0;
}

bool osty_rt_strings_HasSuffix(const char *value, const char *suffix) {
    size_t value_len;
    size_t suffix_len;
    if (value == NULL || suffix == NULL) {
        return false;
    }
    value_len = strlen(value);
    suffix_len = strlen(suffix);
    if (suffix_len > value_len) {
        return false;
    }
    return strncmp(value + (value_len - suffix_len), suffix, suffix_len) == 0;
}

void *osty_rt_strings_Split(const char *value, const char *sep) {
    osty_rt_list *out = (osty_rt_list *)osty_rt_list_new();
    const char *cursor;
    const char *next;
    size_t sep_len;

    if (value == NULL) {
        return out;
    }
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
    while ((next = strstr(cursor, sep)) != NULL) {
        char *piece = osty_rt_string_dup_range(cursor, (size_t)(next - cursor));
        osty_rt_list_push_ptr(out, piece);
        cursor = next + sep_len;
    }
    osty_rt_list_push_ptr(out, osty_rt_string_dup_range(cursor, strlen(cursor)));
    return out;
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
    if (value == NULL) {
        return out;
    }

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

    out = (osty_rt_list *)osty_rt_list_new();
    if (value == NULL) {
        return out;
    }
    cursor = (const unsigned char *)value;
    n = strlen(value);
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
    sep_len = (sep == NULL) ? 0 : strlen(sep);
    total = 0;
    for (i = 0; i < count; i++) {
        piece = ((const char **)parts->data)[i];
        if (piece != NULL) {
            total += strlen(piece);
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
            piece_len = strlen(piece);
            if (piece_len != 0) {
                memcpy(cursor, piece, piece_len);
                cursor += piece_len;
            }
        }
        if (i + 1 < count && sep_len != 0) {
            memcpy(cursor, sep, sep_len);
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

    value = (value == NULL) ? "" : value;
    if (n <= 0) {
        return osty_rt_string_dup_site("", 0, "runtime.strings.repeat.empty");
    }
    value_len = strlen(value);
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

    value = (value == NULL) ? "" : value;
    old = (old == NULL) ? "" : old;
    new_value = (new_value == NULL) ? "" : new_value;
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

    value = (value == NULL) ? "" : value;
    value_len = strlen(value);
    if (start < 0 || end < start) {
        osty_rt_abort("runtime.strings.slice: invalid bounds");
    }
    if ((uint64_t)end > (uint64_t)value_len) {
        osty_rt_abort("runtime.strings.slice: end out of range");
    }
    return osty_rt_string_dup_site(value + start, (size_t)(end - start), "runtime.strings.slice");
}

const char *osty_rt_strings_TrimPrefix(const char *value, const char *prefix) {
    const char *start;

    if (value == NULL) {
        return osty_rt_string_dup_site("", 0, "runtime.strings.trim_prefix.empty");
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

    if (value == NULL) {
        return osty_rt_string_dup_site("", 0, "runtime.strings.trim_suffix.empty");
    }
    value_len = strlen(value);
    suffix_len = (suffix == NULL) ? 0 : strlen(suffix);
    if (suffix_len != 0 && suffix_len <= value_len &&
        strncmp(value + (value_len - suffix_len), suffix, suffix_len) == 0) {
        return osty_rt_string_dup_site(value, value_len - suffix_len, "runtime.strings.trim_suffix");
    }
    return osty_rt_string_dup_site(value, value_len, "runtime.strings.trim_suffix");
}

const char *osty_rt_strings_TrimSpace(const char *value) {
    const char *start;
    const char *end;
    size_t len;
    char *out;

    if (value == NULL) {
        out = (char *)osty_gc_allocate_managed(1, OSTY_GC_KIND_STRING, "runtime.strings.trim_space.empty", NULL, NULL);
        out[0] = '\0';
        return out;
    }
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

static void *osty_rt_map_value_slot(osty_rt_map *map, int64_t index) {
    return (void *)(map->values + ((size_t)index * map->value_size));
}

static void *osty_rt_map_key_slot(osty_rt_map *map, int64_t index) {
    return (void *)(map->keys + ((size_t)index * osty_rt_kind_size(map->key_kind)));
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

static int64_t osty_rt_map_find_index(osty_rt_map *map, const void *key) {
    int64_t i;
    size_t key_size = osty_rt_kind_size(map->key_kind);
    for (i = 0; i < map->len; i++) {
        if (osty_rt_value_equals(osty_rt_map_key_slot(map, i), key, key_size, map->key_kind)) {
            return i;
        }
    }
    return -1;
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
    {
        pthread_mutexattr_t attr;
        if (pthread_mutexattr_init(&attr) != 0) {
            osty_rt_abort("map lock: mutexattr_init failed");
        }
        if (pthread_mutexattr_settype(&attr, PTHREAD_MUTEX_RECURSIVE) != 0) {
            osty_rt_abort("map lock: mutexattr_settype failed");
        }
        if (pthread_mutex_init(&map->mu, &attr) != 0) {
            osty_rt_abort("map lock: mutex_init failed");
        }
        pthread_mutexattr_destroy(&attr);
        map->mu_init = 1;
    }
    return map;
}

// Public lock/unlock for composite ops (update) that must hold the
// lock across get + callback + insert. Recursive mutex so calls from
// a user callback into the same map (e.g. counts.len()) re-acquire
// instead of self-deadlocking.
void osty_rt_map_lock(void *raw_map) {
    osty_rt_map *map = (osty_rt_map *)raw_map;
    if (map == NULL || !map->mu_init) return;
    pthread_mutex_lock(&map->mu);
}

void osty_rt_map_unlock(void *raw_map) {
    osty_rt_map *map = (osty_rt_map *)raw_map;
    if (map == NULL || !map->mu_init) return;
    pthread_mutex_unlock(&map->mu);
}

static bool osty_rt_map_contains_raw(void *raw_map, const void *key) {
    osty_rt_map *map = (osty_rt_map *)raw_map;
    return map != NULL && osty_rt_map_find_index(map, key) >= 0;
}

static void osty_rt_map_insert_raw(void *raw_map, const void *key, const void *value) {
    osty_rt_map *map = (osty_rt_map *)raw_map;
    int64_t index;
    size_t key_size;
    if (map == NULL || key == NULL || value == NULL) {
        osty_rt_abort("invalid map insert");
    }
    key_size = osty_rt_kind_size(map->key_kind);
    index = osty_rt_map_find_index(map, key);
    if (index < 0) {
        osty_rt_map_reserve(map, map->len + 1);
        index = map->len;
        map->len += 1;
    }
    memcpy(osty_rt_map_key_slot(map, index), key, key_size);
    memcpy(osty_rt_map_value_slot(map, index), value, map->value_size);
}

static bool osty_rt_map_remove_raw(void *raw_map, const void *key) {
    osty_rt_map *map = (osty_rt_map *)raw_map;
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
    return true;
}

static void osty_rt_map_get_or_abort_raw(void *raw_map, const void *key, void *out_value) {
    osty_rt_map *map = (osty_rt_map *)raw_map;
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
static bool osty_rt_map_get_raw(void *raw_map, const void *key, void *out_value) {
    osty_rt_map *map = (osty_rt_map *)raw_map;
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
// into *out_value. Backs the for-(k, v)-in-m iteration path — key
// accessors are macro-generated per K suffix below.
void osty_rt_map_value_at(void *raw_map, int64_t index, void *out_value) {
    osty_rt_map *map = (osty_rt_map *)raw_map;
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
    osty_rt_map *map = (osty_rt_map *)raw_map;
    int64_t n;
    if (map == NULL) {
        osty_rt_abort("map is null");
    }
    osty_rt_map_lock(raw_map);
    n = map->len;
    osty_rt_map_unlock(raw_map);
    return n;
}

void *osty_rt_map_keys(void *raw_map) {
    osty_rt_map *map = (osty_rt_map *)raw_map;
    void *out = osty_rt_list_new();
    int64_t i;
    if (map == NULL) {
        osty_rt_abort("map is null");
    }
    osty_rt_map_lock(raw_map);
    for (i = 0; i < map->len; i++) {
        switch (map->key_kind) {
        case OSTY_RT_ABI_I64: {
            int64_t value = 0;
            memcpy(&value, osty_rt_map_key_slot(map, i), sizeof(value));
            osty_rt_list_push_i64(out, value);
            break;
        }
        case OSTY_RT_ABI_I1: {
            bool value = false;
            memcpy(&value, osty_rt_map_key_slot(map, i), sizeof(value));
            osty_rt_list_push_i1(out, value);
            break;
        }
        case OSTY_RT_ABI_F64: {
            double value = 0.0;
            memcpy(&value, osty_rt_map_key_slot(map, i), sizeof(value));
            osty_rt_list_push_f64(out, value);
            break;
        }
        case OSTY_RT_ABI_PTR:
        case OSTY_RT_ABI_STRING: {
            void *value = NULL;
            memcpy(&value, osty_rt_map_key_slot(map, i), sizeof(value));
            osty_rt_list_push_ptr(out, value);
            break;
        }
        default:
            osty_rt_abort("unsupported map key list kind");
        }
    }
    osty_rt_map_unlock(raw_map);
    return out;
}

// Every public keyed op takes the per-map lock, runs the raw op, and
// releases. Recursive so that `update`'s outer lock + a re-entrant op
// from a user callback (e.g. counts.len() inside f) don't deadlock.
// key_at snapshots the key under the lock — the return is by-value so
// the caller doesn't hold any reference past unlock.
#define OSTY_RT_DEFINE_MAP_KEY_OPS(suffix, ctype) \
bool osty_rt_map_contains_##suffix(void *raw_map, ctype key) { \
    bool r; \
    osty_rt_map_lock(raw_map); \
    r = osty_rt_map_contains_raw(raw_map, &key); \
    osty_rt_map_unlock(raw_map); \
    return r; \
} \
void osty_rt_map_insert_##suffix(void *raw_map, ctype key, const void *value) { \
    osty_rt_map_lock(raw_map); \
    osty_rt_map_insert_raw(raw_map, &key, value); \
    osty_rt_map_unlock(raw_map); \
} \
bool osty_rt_map_remove_##suffix(void *raw_map, ctype key) { \
    bool r; \
    osty_rt_map_lock(raw_map); \
    r = osty_rt_map_remove_raw(raw_map, &key); \
    osty_rt_map_unlock(raw_map); \
    return r; \
} \
void osty_rt_map_get_or_abort_##suffix(void *raw_map, ctype key, void *out_value) { \
    osty_rt_map_lock(raw_map); \
    osty_rt_map_get_or_abort_raw(raw_map, &key, out_value); \
    osty_rt_map_unlock(raw_map); \
} \
bool osty_rt_map_get_##suffix(void *raw_map, ctype key, void *out_value) { \
    bool r; \
    osty_rt_map_lock(raw_map); \
    r = osty_rt_map_get_raw(raw_map, &key, out_value); \
    osty_rt_map_unlock(raw_map); \
    return r; \
} \
ctype osty_rt_map_key_at_##suffix(void *raw_map, int64_t index) { \
    osty_rt_map *map = (osty_rt_map *)raw_map; \
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
}

OSTY_RT_DEFINE_MAP_KEY_OPS(i64, int64_t)
OSTY_RT_DEFINE_MAP_KEY_OPS(i1, bool)
OSTY_RT_DEFINE_MAP_KEY_OPS(f64, double)
OSTY_RT_DEFINE_MAP_KEY_OPS(ptr, void *)
OSTY_RT_DEFINE_MAP_KEY_OPS(string, const char *)

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

int64_t osty_rt_set_len(void *raw_set) {
    osty_rt_set *set = (osty_rt_set *)raw_set;
    if (set == NULL) {
        osty_rt_abort("set is null");
    }
    return set->len;
}

void *osty_rt_set_to_list(void *raw_set) {
    osty_rt_set *set = (osty_rt_set *)raw_set;
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
    osty_rt_set *set = (osty_rt_set *)raw_set; \
    return set != NULL && osty_rt_set_find_index(set, &item) >= 0; \
} \
bool osty_rt_set_insert_##suffix(void *raw_set, ctype item) { \
    osty_rt_set *set = (osty_rt_set *)raw_set; \
    size_t elem_size; \
    if (set == NULL) { osty_rt_abort("set is null"); } \
    if (osty_rt_set_find_index(set, &item) >= 0) { return false; } \
    elem_size = osty_rt_kind_size(set->elem_kind); \
    osty_rt_set_reserve(set, set->len + 1); \
    memcpy(set->items + ((size_t)set->len * elem_size), &item, elem_size); \
    set->len += 1; \
    return true; \
} \
bool osty_rt_set_remove_##suffix(void *raw_set, ctype item) { \
    osty_rt_set *set = (osty_rt_set *)raw_set; \
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
 * pointer to a `{unsigned char *data; int64_t len}` struct. Only the
 * length / emptiness queries are wired up at the LLVM layer right now;
 * construction (literals, string.bytes(), etc.) is not yet surfaced,
 * but the ABI is fixed so future call sites can link against it.
 */
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

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void osty_gc_pre_write_v1(void *owner, void *old_value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.pre_write_v1"));
void osty_gc_post_write_v1(void *owner, void *value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.post_write_v1"));
void *osty_gc_load_v1(void *value) __asm__(OSTY_GC_SYMBOL("osty.gc.load_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));
void osty_gc_root_release_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_release_v1"));
void osty_gc_global_root_register_v1(void *slot) __asm__(OSTY_GC_SYMBOL("osty.gc.global_root_register_v1"));
void osty_gc_global_root_unregister_v1(void *slot) __asm__(OSTY_GC_SYMBOL("osty.gc.global_root_unregister_v1"));
void osty_gc_safepoint_v1(int64_t safepoint_id, void *const *root_slots, int64_t root_slot_count) __asm__(OSTY_GC_SYMBOL("osty.gc.safepoint_v1"));
void *osty_rt_enum_alloc_ptr_v1(const char *site) __asm__(OSTY_GC_SYMBOL("osty.rt.enum_alloc_ptr_v1"));
void *osty_rt_enum_alloc_scalar_v1(const char *site) __asm__(OSTY_GC_SYMBOL("osty.rt.enum_alloc_scalar_v1"));

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) {
    if (byte_size < 0) {
        osty_rt_abort("negative GC allocation size");
    }
    return osty_gc_allocate_managed((size_t)byte_size, object_kind == 0 ? OSTY_GC_KIND_GENERIC : object_kind, site, NULL, NULL);
}

static void osty_rt_enum_ptr_payload_trace(void *payload) {
    osty_gc_mark_root_slot(payload);
}

/* Phase A4 depth (RUNTIME_GC_DELTA §2.4). Trace callback for closure
 * envs: walks the capture array so any managed pointer stored in a
 * closure capture survives GC while the env itself is reachable.
 *
 * The env layout is self-describing (see `osty_rt_closure_env`) so the
 * trace needs no external descriptor — it reads `capture_count`, then
 * passes each slot address to `osty_gc_mark_slot_v1`. Non-managed
 * capture values (scalars that the Osty frontend stores as raw bits
 * inside the slot) are handled transparently because `mark_slot_v1`
 * filters through `find_header`.
 */
static void osty_rt_closure_env_trace(void *payload) {
    osty_rt_closure_env *env = (osty_rt_closure_env *)payload;
    int64_t i;
    if (env == NULL || env->capture_count <= 0) {
        return;
    }
    for (i = 0; i < env->capture_count; i++) {
        osty_gc_mark_slot_v1((void *)&env->captures[i]);
    }
}

/* Phase A4 depth: dedicated allocator for closure envs. Takes the
 * capture count up front so the layout and trace callback are set
 * together — there is no post-alloc mutation window where a
 * collection could see an env without its trace installed.
 *
 * Exported as `osty.rt.closure_env_alloc_v1` so the LLVM emitter can
 * call it with a single `call ptr @osty.rt.closure_env_alloc_v1(i64 N,
 * ptr %site)` at the fn-value materialisation site.
 */
void *osty_rt_closure_env_alloc_v1(int64_t capture_count, const char *site)
    __asm__(OSTY_GC_SYMBOL("osty.rt.closure_env_alloc_v1"));

void *osty_rt_closure_env_alloc_v1(int64_t capture_count, const char *site) {
    size_t byte_size;
    osty_rt_closure_env *env;

    if (capture_count < 0) {
        osty_rt_abort("negative closure env capture count");
    }
    if ((size_t)capture_count > (SIZE_MAX - sizeof(osty_rt_closure_env)) / sizeof(void *)) {
        osty_rt_abort("closure env capture count overflow");
    }
    byte_size = sizeof(osty_rt_closure_env) + (size_t)capture_count * sizeof(void *);
    env = (osty_rt_closure_env *)osty_gc_allocate_managed(
        byte_size, OSTY_GC_KIND_CLOSURE_ENV, site,
        osty_rt_closure_env_trace, NULL);
    env->capture_count = capture_count;
    return env;
}

void *osty_rt_enum_alloc_ptr_v1(const char *site) {
    return osty_gc_allocate_managed(8, OSTY_GC_KIND_GENERIC, site, osty_rt_enum_ptr_payload_trace, NULL);
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

    (void)slot_kind;
    osty_gc_acquire();
    osty_gc_pre_write_count += 1;
    if (old_value == NULL) {
        osty_gc_release();
        return;
    }
    if (osty_gc_find_header(old_value) == NULL) {
        osty_gc_release();
        return;
    }
    osty_gc_pre_write_managed_count += 1;
    osty_gc_satb_log_append(old_value);
    owner_header = osty_gc_find_header(owner);
    if (owner_header != NULL && owner_header->root_count > 0) {
        osty_gc_collection_requested = true;
    }
    osty_gc_release();
}

void osty_gc_post_write_v1(void *owner, void *value, int64_t slot_kind) {
    osty_gc_header *owner_header;

    (void)slot_kind;
    osty_gc_acquire();
    osty_gc_post_write_count += 1;
    if (owner == NULL || value == NULL) {
        osty_gc_release();
        return;
    }
    owner_header = osty_gc_find_header(owner);
    if (owner_header == NULL) {
        osty_gc_release();
        return;
    }
    if (osty_gc_find_header(value) == NULL) {
        osty_gc_release();
        return;
    }
    osty_gc_post_write_managed_count += 1;
    osty_gc_remembered_edges_append(owner, value);
    if (owner_header->root_count > 0) {
        osty_gc_collection_requested = true;
    }
    osty_gc_release();
}

void *osty_gc_load_v1(void *value) {
    osty_gc_acquire();
    osty_gc_load_count += 1;
    if (osty_gc_find_header(value) != NULL) {
        osty_gc_load_managed_count += 1;
    }
    osty_gc_release();
    return value;
}

void osty_gc_mark_slot_v1(void *slot_addr) {
    /* Only reachable inside `osty_gc_collect_now_with_stack_roots`,
     * which runs under `osty_gc_lock` (see safepoint / debug collect).
     * No additional lock required. */
    osty_gc_mark_root_slot(slot_addr);
}

void osty_gc_root_bind_v1(void *root) {
    osty_gc_header *header;
    osty_gc_acquire();
    header = osty_gc_find_header(root);
    if (header == NULL) {
        osty_gc_release();
        return;
    }
    if (header->root_count == INT64_MAX) {
        osty_gc_release();
        osty_rt_abort("GC root count overflow");
    }
    header->root_count += 1;
    osty_gc_release();
}

void osty_gc_root_release_v1(void *root) {
    osty_gc_header *header;
    osty_gc_acquire();
    header = osty_gc_find_header(root);
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

void osty_gc_safepoint_v1(int64_t safepoint_id, void *const *root_slots, int64_t root_slot_count) {
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
    if (osty_concurrent_workers > 0) {
        osty_gc_release();
        return;
    }
    osty_gc_collect_now_with_stack_roots(root_slots, root_slot_count);
    osty_gc_release();
}

void osty_gc_debug_collect(void) {
    osty_gc_acquire();
    if (osty_concurrent_workers > 0) {
        /* Same cross-thread root safety gate as safepoint_v1. */
        osty_gc_release();
        return;
    }
    osty_gc_collect_now();
    osty_gc_release();
}

int64_t osty_gc_debug_live_count(void) {
    return osty_gc_live_count;
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

int64_t osty_gc_debug_global_root_count(void) {
    return osty_gc_global_root_count;
}

int64_t osty_gc_debug_satb_log_count(void) {
    return osty_gc_satb_log_count;
}

int64_t osty_gc_debug_remembered_edge_count(void) {
    return osty_gc_remembered_edge_count;
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
            (long long)s.index_capacity, (long long)s.index_find_ops_total);
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
};

int64_t osty_gc_debug_validate_heap(void) {
    osty_gc_header *header;
    int64_t walked_count = 0;
    int64_t walked_bytes = 0;
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
        if (header->marked) {
            status = OSTY_GC_VALIDATE_STALE_MARK;
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
    if (osty_gc_mark_stack_count != 0) {
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
    if (osty_gc_allocated_since_collect < 0 ||
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
 * Scheduler: pthread-backed (RUNTIME_SCHEDULER.md Phase 1B/2 hybrid)
 *
 * The public ABI is unchanged from Phase 1A; this is the first
 * implementation that runs tasks on real OS threads and gives channels
 * proper block/wake semantics. Key points:
 *
 *   - task_spawn / task_group_spawn → pthread_create; handle_join →
 *     pthread_join. Bodies run in parallel when CPU allows.
 *   - task_group(body) runs body on the calling thread and activates
 *     a group. Children created via task_group_spawn inherit the group
 *     in their TLS (osty_sched_current_group). Callers are responsible
 *     for joining handles; the group teardown does not auto-reap
 *     stragglers yet (tracked in RUNTIME_SCHEDULER.md roadmap).
 *   - Channels are bounded ring buffers with pthread_mutex + two
 *     condition variables. send blocks while full, recv while empty.
 *     close wakes both waits; recv after close-and-drain returns ok=0.
 *     Capacity 0 is clamped to 1 (documented limitation; true
 *     rendezvous lands with Phase 1B fibers).
 *   - select / race / collectAll / parallel remain as loud aborts
 *     because their registration surface needs Osty-side builder
 *     allocation we have not wired yet; the message points at the
 *     scheduler roadmap so programs that reach them fail fast.
 *
 * ABI abuse notice: MIR declares task_group / handle_join with the
 * caller's Osty-side return type (usually i64 or ptr). pthread_join
 * yields a `void *` that the linker treats as compatible 64-bit on
 * x86_64 SysV and AArch64 AAPCS. Scalar/ptr returns up to 8 bytes are
 * supported. Float returns travel as raw bits through int64_t; struct
 * returns still require Phase 2 ABI work.
 * ====================================================================== */

typedef int64_t (*osty_task_group_body_fn)(void *env, void *group);
typedef int64_t (*osty_task_spawn_body_fn)(void *env);

struct osty_rt_task_handle_impl_;

typedef struct osty_rt_task_handle_node {
    struct osty_rt_task_handle_impl_ *handle;
    struct osty_rt_task_handle_node *next;
} osty_rt_task_handle_node;

typedef struct osty_rt_task_group_impl {
    volatile int64_t cancelled;          /* 0 = live, 1 = cancelled */
    void *cause;                         /* reserved for error propagation */
    osty_rt_mu_t children_mu;
    osty_rt_task_handle_node *children;  /* all handles spawned into group */
} osty_rt_task_group_impl;

typedef struct osty_rt_task_handle_impl_ {
    osty_rt_thread_t thread;
    int has_thread;                      /* 1 while thread is joinable */
    void *body_env;
    osty_rt_task_group_impl *group;      /* inherited group, may be NULL */
    volatile int64_t result;
    volatile int32_t done;
    int32_t errored;                     /* reserved */
} osty_rt_task_handle_impl;

static OSTY_RT_TLS osty_rt_task_group_impl *osty_sched_current_group = NULL;

/* Handles live in the GC heap so they are reclaimed once the Osty
 * `Handle<T>` reference drops. While the spawned thread runs,
 * `osty_concurrent_workers > 0` keeps collection paused, so the
 * handle pointer the trampoline carries cannot be freed under it.
 * Once the thread exits and Osty drops its reference, the next
 * collection sweeps the handle. No explicit free on the error path
 * either — the handle becomes unreachable and gets reaped. */
static osty_rt_task_handle_impl *osty_sched_alloc_handle(void) {
    return (osty_rt_task_handle_impl *)osty_gc_allocate_managed(
        sizeof(osty_rt_task_handle_impl),
        OSTY_GC_KIND_GENERIC,
        "runtime.task.handle",
        NULL, NULL);
}

static void osty_sched_unimplemented(const char *what) {
    fprintf(stderr,
            "osty llvm runtime: %s is not implemented yet "
            "(see RUNTIME_SCHEDULER.md for roadmap).\n",
            what);
    abort();
}

static void osty_rt_task_thread_cleanup(osty_rt_task_handle_impl *h) {
    /* Invoked on the worker's normal return path. The runtime never
     * calls pthread_cancel / pthread_exit, so bodies always reach the
     * closing return; unwind-safe cleanup is unnecessary. */
    __atomic_store_n(&h->done, 1, __ATOMIC_RELEASE);
    osty_sched_workers_dec();
}

static void *osty_rt_task_thread_trampoline(void *arg) {
    osty_rt_task_handle_impl *h = (osty_rt_task_handle_impl *)arg;
    if (h->group != NULL) {
        osty_sched_current_group = h->group;
    }
    osty_task_spawn_body_fn fn = (osty_task_spawn_body_fn)(*(void **)h->body_env);
    h->result = fn(h->body_env);
    osty_rt_task_thread_cleanup(h);
    return NULL;
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

static void osty_rt_task_group_reap(osty_rt_task_group_impl *g) {
    /* Called from task_group() after the body returns. Any child still
     * marked joinable is joined so no thread outlives its group scope
     * (spec §8.1). Handles themselves are not freed — the Osty caller
     * may still read their result fields. Nodes ARE freed because the
     * group stack-frame is about to disappear. */
    osty_rt_mu_lock(&g->children_mu);
    osty_rt_task_handle_node *node = g->children;
    g->children = NULL;
    osty_rt_mu_unlock(&g->children_mu);

    while (node != NULL) {
        osty_rt_task_handle_node *next = node->next;
        osty_rt_task_handle_impl *h = node->handle;
        if (h != NULL && h->has_thread) {
            if (osty_rt_thread_join(h->thread, NULL) != 0) {
                osty_rt_abort("task_group: thread join failed during reap");
            }
            h->has_thread = 0;
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

    /* Reap any children the body forgot to join (spec §8.1 structured
     * lifetime). Runs before we restore the previous TLS group so that
     * a nested taskGroup's teardown sees its own scope. */
    osty_rt_task_group_reap(&group);
    osty_rt_mu_destroy(&group.children_mu);

    osty_sched_current_group = prev;
    return result;
}

static void *osty_rt_task_spawn_internal(void *group, void *body_env) {
    if (body_env == NULL) {
        osty_rt_abort("task_spawn: null body env");
    }
    osty_rt_task_handle_impl *h = osty_sched_alloc_handle();
    h->body_env = body_env;
    h->group = (osty_rt_task_group_impl *)group;
    osty_sched_workers_inc();
    if (osty_rt_thread_start(&h->thread,
                             osty_rt_task_thread_trampoline, h) != 0) {
        osty_sched_workers_dec();
        /* h is GC-managed — letting it go unreachable is enough. */
        osty_rt_abort("task_spawn: thread start failed");
    }
    h->has_thread = 1;
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
    osty_rt_task_handle_impl *h = (osty_rt_task_handle_impl *)handle;
    if (h->has_thread) {
        if (osty_rt_thread_join(h->thread, NULL) != 0) {
            osty_rt_abort("task_handle_join: thread join failed");
        }
        h->has_thread = 0;
    }
    /* After pthread_join, h->done is acquire-visible via the implicit
     * release on worker_dec; read via atomic for clarity. */
    (void)__atomic_load_n(&h->done, __ATOMIC_ACQUIRE);
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

typedef struct osty_rt_chan_impl {
    osty_rt_mu_t mu;
    osty_rt_cond_t not_full;
    osty_rt_cond_t not_empty;
    int64_t cap;
    int64_t head;
    int64_t tail;
    int64_t count;
    int closed;
    int64_t *slots;
} osty_rt_chan_impl;

typedef struct osty_rt_chan_recv_result {
    int64_t value;
    int64_t ok;
} osty_rt_chan_recv_result;

static osty_rt_chan_impl *osty_rt_chan_cast(void *ch, const char *op) {
    if (ch == NULL) {
        char buf[64];
        snprintf(buf, sizeof(buf), "%s: null channel", op);
        osty_rt_abort(buf);
    }
    return (osty_rt_chan_impl *)ch;
}

static void osty_rt_chan_destroy(void *payload) {
    /* Invoked by the GC sweep when no Osty reference remains. Closing
     * is idempotent but we don't require the channel to be closed
     * first — unreachable implies no live sender or receiver. */
    osty_rt_chan_impl *ch = (osty_rt_chan_impl *)payload;
    if (ch == NULL) {
        return;
    }
    osty_rt_mu_destroy(&ch->mu);
    osty_rt_cond_destroy(&ch->not_full);
    osty_rt_cond_destroy(&ch->not_empty);
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
        sizeof(osty_rt_chan_impl), OSTY_GC_KIND_GENERIC,
        "runtime.thread.chan", NULL, osty_rt_chan_destroy);
    ch->slots = (int64_t *)calloc((size_t)capacity, sizeof(int64_t));
    if (ch->slots == NULL) {
        osty_rt_abort("thread.chan.make: out of memory");
    }
    ch->cap = capacity;
    if (osty_rt_mu_init(&ch->mu) != 0 ||
        osty_rt_cond_init(&ch->not_full) != 0 ||
        osty_rt_cond_init(&ch->not_empty) != 0) {
        free(ch->slots);
        ch->slots = NULL;
        osty_rt_abort("thread.chan.make: sync init failed");
    }
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
    osty_rt_chan_send_raw(osty_rt_chan_cast(raw, "thread.chan.send"), value);
}

void osty_rt_thread_chan_send_i1(void *raw, bool value) {
    osty_rt_chan_send_raw(osty_rt_chan_cast(raw, "thread.chan.send"),
                          (int64_t)(value ? 1 : 0));
}

void osty_rt_thread_chan_send_f64(void *raw, double value) {
    int64_t bits = 0;
    memcpy(&bits, &value, sizeof(bits));
    osty_rt_chan_send_raw(osty_rt_chan_cast(raw, "thread.chan.send"), bits);
}

void osty_rt_thread_chan_send_ptr(void *raw, void *value) {
    osty_rt_chan_send_raw(osty_rt_chan_cast(raw, "thread.chan.send"),
                          (int64_t)(uintptr_t)value);
}

void osty_rt_thread_chan_send_bytes_v1(void *raw, const void *src, int64_t sz) {
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
    osty_rt_chan_send_raw(osty_rt_chan_cast(raw, "thread.chan.send"),
                          (int64_t)(uintptr_t)copy);
}

osty_rt_chan_recv_result osty_rt_thread_chan_recv_i64(void *raw) {
    return osty_rt_chan_recv_raw(osty_rt_chan_cast(raw, "thread.chan.recv"));
}

osty_rt_chan_recv_result osty_rt_thread_chan_recv_i1(void *raw) {
    osty_rt_chan_recv_result r = osty_rt_chan_recv_raw(
        osty_rt_chan_cast(raw, "thread.chan.recv"));
    if (r.ok) {
        r.value = r.value != 0 ? 1 : 0;
    }
    return r;
}

osty_rt_chan_recv_result osty_rt_thread_chan_recv_f64(void *raw) {
    return osty_rt_chan_recv_raw(osty_rt_chan_cast(raw, "thread.chan.recv"));
}

osty_rt_chan_recv_result osty_rt_thread_chan_recv_ptr(void *raw) {
    return osty_rt_chan_recv_raw(osty_rt_chan_cast(raw, "thread.chan.recv"));
}

osty_rt_chan_recv_result osty_rt_thread_chan_recv_bytes_v1(void *raw) {
    return osty_rt_chan_recv_raw(osty_rt_chan_cast(raw, "thread.chan.recv"));
}

/* ---- Select: polling builder with recv / timeout / default arms.
 *
 * Surface contract (stable with Phase 1A abort stubs):
 *   osty_rt_select(body_env)                 // 1-arg
 *   osty_rt_select_recv(s, ch, arm)          // 3-arg
 *   osty_rt_select_send(s, ch, arm)          // 3-arg — NOT YET WIRED
 *   osty_rt_select_timeout(s, ns, arm)       // 3-arg
 *   osty_rt_select_default(s, arm)           // 2-arg
 *
 * `body_env` is a closure env whose slot-0 is the body function. The
 * body is invoked as `body(env, s)` where `s` is a stack-allocated
 * builder this function owns. Arms the body registers are evaluated
 * non-blockingly in their registration order; if none is ready we
 * sleep 1ms and retry, firing the first timeout arm whose deadline
 * elapses. A default arm fires immediately if present and no other
 * arm is ready on the first pass.
 *
 * Arm closures are void-returning; the Osty frontend is expected to
 * capture the result slot inside the closure (same pattern as
 * taskGroup body's result propagation). Recv-arm closures receive
 * the drained value as their second argument.
 *
 * Send arms require a value register alongside the channel — that
 * shape is not yet emitted by MIR, so `osty_rt_select_send` aborts
 * with a diagnostic if reached. A 4-arg surface (value register)
 * will replace this when the emitter catches up.
 */

typedef enum {
    OSTY_RT_SELECT_ARM_RECV = 0,
    OSTY_RT_SELECT_ARM_TIMEOUT = 1,
    OSTY_RT_SELECT_ARM_DEFAULT = 2,
} osty_rt_select_arm_kind;

typedef struct osty_rt_select_arm {
    osty_rt_select_arm_kind kind;
    void *ch;              /* recv: channel ptr; otherwise NULL */
    int64_t timeout_ns;    /* timeout arm only */
    void *arm_env;         /* closure env */
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
                                    void *arm_env) {
    osty_rt_mu_lock(&sel->mu);
    if (sel->count >= OSTY_RT_SELECT_ARM_CAPACITY) {
        osty_rt_mu_unlock(&sel->mu);
        osty_rt_abort("thread.select: arm capacity exceeded");
    }
    sel->arms[sel->count].kind = kind;
    sel->arms[sel->count].ch = ch;
    sel->arms[sel->count].timeout_ns = timeout_ns;
    sel->arms[sel->count].arm_env = arm_env;
    sel->count += 1;
    osty_rt_mu_unlock(&sel->mu);
}

void osty_rt_select_recv(void *s, void *ch, void *arm) {
    osty_rt_select_impl *sel = osty_rt_select_cast(s, "thread.select.recv");
    if (ch == NULL) {
        osty_rt_abort("thread.select.recv: null channel");
    }
    osty_rt_select_arm_push(sel, OSTY_RT_SELECT_ARM_RECV, ch, 0, arm);
}

void osty_rt_select_send(void *s, void *ch, void *arm) {
    (void)s; (void)ch; (void)arm;
    osty_sched_unimplemented(
        "thread.select.send (awaiting value-register surface from MIR)");
}

void osty_rt_select_timeout(void *s, int64_t ns, void *arm) {
    osty_rt_select_impl *sel = osty_rt_select_cast(s, "thread.select.timeout");
    if (ns < 0) {
        osty_rt_abort("thread.select.timeout: negative duration");
    }
    osty_rt_select_arm_push(sel, OSTY_RT_SELECT_ARM_TIMEOUT, NULL, ns, arm);
}

void osty_rt_select_default(void *s, void *arm) {
    osty_rt_select_impl *sel = osty_rt_select_cast(s, "thread.select.default");
    osty_rt_select_arm_push(sel, OSTY_RT_SELECT_ARM_DEFAULT, NULL, 0, arm);
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
        /* Poll recv arms in registration order. */
        for (int i = 0; i < sel.count; i++) {
            if (sel.arms[i].kind != OSTY_RT_SELECT_ARM_RECV) {
                continue;
            }
            osty_rt_chan_impl *ch = (osty_rt_chan_impl *)sel.arms[i].ch;
            int64_t value = 0;
            if (osty_rt_select_try_recv(ch, &value)) {
                osty_rt_mu_destroy(&sel.mu);
                osty_rt_select_invoke_recv(&sel.arms[i], value);
                return;
            }
        }

        /* Default arm fires if no recv is ready on the first pass. */
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

void *osty_rt_task_race(void *body) {
    (void)body;
    osty_sched_unimplemented("race");
    return NULL;
}

void *osty_rt_task_collect_all(void *body) {
    (void)body;
    osty_sched_unimplemented("collectAll");
    return NULL;
}

void *osty_rt_parallel(void *items, int64_t concurrency, void *f) {
    (void)items; (void)concurrency; (void)f;
    osty_sched_unimplemented("parallel");
    return NULL;
}
