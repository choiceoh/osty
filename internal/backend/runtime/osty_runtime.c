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

/* Snapshot / file-IO primitives — mkdir is spelled differently on Win32
 * vs. POSIX, so we funnel the one-directory-level call through
 * osty_rt_mkdir_one. Everything else (fopen/fread/fwrite/getenv) is
 * already cross-platform in the C standard library. */
#if defined(_WIN32)
#  include <direct.h>
#  define OSTY_RT_MKDIR_ONE(path) _mkdir(path)
#else
#  include <sys/stat.h>
#  include <sys/types.h>
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
#  include <sched.h>
#  include <signal.h>
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
#define OSTY_GC_PROMOTE_AGE_DEFAULT 3
#define OSTY_GC_PROMOTE_AGE_ENV "OSTY_GC_PROMOTE_AGE"
#define OSTY_GC_NURSERY_LIMIT_ENV "OSTY_GC_NURSERY_BYTES"
#define OSTY_GC_NURSERY_LIMIT_DEFAULT 8192

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
    int64_t root_count;
    int64_t pin_count;
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
    /* Phase D groundwork: stable logical identity. Payload pointers are
     * still the operational handles today, but this id stays attached to
     * the object even once future compaction starts moving addresses. */
    uint64_t stable_id;
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
    int64_t index_inline[OSTY_RT_MAP_INLINE_INDEX_CAP];
    // Per-map recursive mutex. Guarantees that all public map ops are
    // mutually exclusive on a single instance so concurrent mutation
    // can't realloc the slot arrays mid-read, drop the len mid-walk,
    // or interleave an insert's memmove. Recursive so that `update`
    // callbacks that touch the same map from within the lock don't
    // self-deadlock.
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
    unsigned char *data;
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
    * Allocation is dedicated: `osty.rt.closure_env_alloc_v1` rather
     * than the generic `osty.gc.alloc_v1`, so the trace is installed
     * at construction and not as a post-alloc mutation. Phase 1 still
     * emits envs with `capture_count = 0`, exercising the allocation
     * ABI while captures themselves are lowered in Phase 4. */
    OSTY_GC_KIND_CLOSURE_ENV = 1029,
    OSTY_GC_KIND_CHANNEL = 1030,
};

enum {
    OSTY_GC_STORAGE_DIRECT = 0,
    OSTY_GC_STORAGE_BUMP_YOUNG = 1,
    OSTY_GC_STORAGE_BUMP_SURVIVOR = 2,
    OSTY_GC_STORAGE_BUMP_OLD = 3,
    OSTY_GC_STORAGE_BUMP_PINNED = 4,
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
    size_t first_tombstone = SIZE_MAX;
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
    while (osty_gc_index_slots[idx].payload != NULL) {
        if (osty_gc_index_slots[idx].payload == OSTY_GC_INDEX_TOMBSTONE) {
            if (first_tombstone == SIZE_MAX) {
                first_tombstone = idx;
            }
        } else if (osty_gc_index_slots[idx].payload == payload) {
            osty_gc_index_slots[idx].header = header;
            return;
        }
        idx = (idx + 1) & mask;
    }
    if (first_tombstone != SIZE_MAX) {
        idx = first_tombstone;
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

    if (osty_gc_identity_slots == NULL || stable_id == 0 ||
        stable_id == OSTY_GC_IDENTITY_TOMBSTONE) {
        return NULL;
    }
    mask = (size_t)(osty_gc_identity_capacity - 1);
    idx = osty_gc_identity_hash(stable_id) & mask;
    while (osty_gc_identity_slots[idx].stable_id != 0) {
        if (osty_gc_identity_slots[idx].stable_id == stable_id) {
            return osty_gc_identity_slots[idx].header;
        }
        idx = (idx + 1) & mask;
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
    osty_gc_index_insert(header->payload, header);
    osty_gc_identity_insert(header->stable_id, header);
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
    return header->destroy == NULL;
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
    size_t total_bytes;
    uintptr_t raw;
    uintptr_t aligned;

    block_size = min_size > (size_t)OSTY_GC_BUMP_BLOCK_BYTES
                     ? min_size
                     : (size_t)OSTY_GC_BUMP_BLOCK_BYTES;
    if (block_size > SIZE_MAX - sizeof(*block) - OSTY_GC_SIZE_CLASS_ALIGN) {
        osty_rt_abort("GC bump block size overflow");
    }
    total_bytes = sizeof(*block) + block_size + OSTY_GC_SIZE_CLASS_ALIGN;
    block = (osty_gc_bump_block *)calloc(1, total_bytes);
    if (block == NULL) {
        osty_rt_abort("out of memory (gc bump block)");
    }
    raw = (uintptr_t)block->storage;
    aligned = (raw + (uintptr_t)OSTY_GC_SIZE_CLASS_ALIGN - 1u) &
              ~((uintptr_t)OSTY_GC_SIZE_CLASS_ALIGN - 1u);
    block->data = (unsigned char *)aligned;
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
    size_t chunk_size = 0;
    bool humongous;
    bool reused = false;
    uint8_t storage_kind = OSTY_GC_STORAGE_DIRECT;

    total_size = osty_gc_total_size_for_payload_size(payload_size);
    payload_size = total_size - sizeof(osty_gc_header);
    humongous = osty_gc_total_size_is_humongous(total_size);
    header = NULL;
    if (!humongous) {
        osty_gc_acquire();
        header = osty_gc_free_list_take(total_size, &chunk_size, &storage_kind);
        if (header != NULL) {
            reused = true;
        } else {
            header = osty_gc_tlab_take(total_size);
            if (header != NULL) {
                storage_kind = OSTY_GC_STORAGE_BUMP_YOUNG;
            }
        }
        osty_gc_release();
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
    header->object_kind = object_kind;
    header->byte_size = (int64_t)payload_size;
    header->trace = trace;
    header->destroy = destroy;
    header->site = site;
    header->payload = (void *)(header + 1);
    header->storage_kind = storage_kind;
    /* Phase B: every new allocation enters the nursery. Promotion to
     * OLD happens inside a minor collection after
     * `osty_gc_promote_age` survivals. */
    header->generation = OSTY_GC_GEN_YOUNG;
    header->age = 0;
    /* Phase C: header starts WHITE. An incremental major in
     * progress will NOT retroactively colour this allocation — it was
     * born after the mark snapshot and stays white until the cycle
     * ends, at which point sweep reclaims it if still unrooted. SATB
     * plus mutator assist together keep the grey queue draining so
     * this is a bounded pressure, not an allocation floodgate. */
    header->color = OSTY_GC_COLOR_WHITE;
    header->marked = false;
    osty_gc_acquire();
    if (humongous) {
        osty_gc_humongous_alloc_count_total += 1;
        osty_gc_humongous_alloc_bytes_total += (int64_t)payload_size;
    }
    header->stable_id = osty_gc_allocate_stable_id();
    osty_gc_link(header);
    osty_gc_note_allocation(payload_size);
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
    osty_gc_release();
    return header->payload;
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
    header->object_kind = object_kind;
    header->byte_size = (int64_t)payload_size;
    header->trace = trace;
    header->destroy = destroy;
    header->site = site;
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
static size_t osty_rt_kind_size(int64_t kind);
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
bool osty_rt_bytes_is_valid_utf8(void *raw_bytes);
const char *osty_rt_bytes_to_string(void *raw_bytes);
uint8_t osty_rt_bytes_get(void *raw_bytes, int64_t index);
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

static void osty_rt_map_mutex_init_or_abort(osty_rt_map *map) {
    if (map == NULL) {
        osty_rt_abort("map lock: null map");
    }
    if (osty_rt_rmu_init(&map->mu) != 0) {
        osty_rt_abort("map lock: mutex_init failed");
    }
    map->mu_init = 1;
}

static bool osty_rt_value_equals(const void *left, const void *right, size_t size, int64_t kind) {
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

static size_t osty_rt_hash_bytes(const unsigned char *bytes, size_t len) {
    uint64_t h = 1469598103934665603ULL;
    size_t i;
    for (i = 0; i < len; i++) {
        h ^= (uint64_t)bytes[i];
        h *= 1099511628211ULL;
    }
    return (size_t)osty_rt_hash_mix64(h);
}

static size_t osty_rt_map_key_hash(int64_t kind, const void *key) {
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
        if (value == NULL) {
            return (size_t)osty_rt_hash_mix64(0ULL);
        }
        return osty_rt_hash_bytes((const unsigned char *)value, strlen(value));
    }
    default:
        osty_rt_abort("unsupported map key hash kind");
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
        if (map->index_slots != NULL && map->index_slots != map->index_inline) {
            free(map->index_slots);
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
     * Phase C: the tri-colour check `color != WHITE` short-circuits
     * both already-enqueued (GREY) and already-traced (BLACK) cases
     * so repeated reach via different edges pushes at most once. */
    if (header == NULL || header->color != OSTY_GC_COLOR_WHITE) {
        return;
    }
    /* Phase B: during a minor collection, OLD headers are treated as
     * permanent roots — we neither mark nor follow them. Any OLD→YOUNG
     * edge is captured by the remembered set and seeded separately in
     * `osty_gc_collect_minor_with_stack_roots`. Without this filter the
     * minor pass would sweep through tenured objects unnecessarily,
     * collapsing back to major semantics. */
    if (osty_gc_minor_in_progress && header->generation == OSTY_GC_GEN_OLD) {
        return;
    }
    header->color = OSTY_GC_COLOR_GREY;
    header->marked = true;
    osty_gc_mark_stack_push(header);
}

static void osty_gc_mark_payload(void *payload) {
    osty_gc_mark_header(osty_gc_find_header(payload));
}

/* Phase C: drain up to `budget` grey headers. Budget of 0 or negative
 * means "drain everything" (the pre-Phase-C semantics, still used by
 * STW major/minor). Returns the number of headers actually traced so
 * the incremental scheduler can pace future steps. */
static int64_t osty_gc_mark_drain_budget(int64_t budget) {
    int64_t done = 0;
    bool unlimited = budget <= 0;
    while (osty_gc_mark_stack_count > 0 && (unlimited || done < budget)) {
        osty_gc_header *header = osty_gc_mark_stack[--osty_gc_mark_stack_count];
        header->color = OSTY_GC_COLOR_BLACK;
        if (header->trace != NULL) {
            /* Trace callbacks re-enter `osty_gc_mark_*` for children,
             * which push more GREY work onto this stack — the C call
             * stack stays bounded regardless of object graph depth. */
            header->trace(header->payload);
        }
        done += 1;
    }
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

static void osty_gc_remap_list_payload(osty_rt_list *list) {
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
    if (list->pointer_elems) {
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

static void osty_gc_remap_map_payload(osty_rt_map *map) {
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

static void osty_gc_remap_set_payload(osty_rt_set *set) {
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

static void osty_gc_remap_closure_env_payload(osty_rt_closure_env *env) {
    int64_t i;

    if (env == NULL) {
        return;
    }
    for (i = 0; i < env->capture_count; i++) {
        osty_gc_remap_slot((void *)&env->captures[i]);
    }
}

static void osty_gc_remap_chan_payload(osty_rt_chan_impl *ch) {
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
    switch (header->object_kind) {
    case OSTY_GC_KIND_LIST:
        osty_gc_remap_list_payload((osty_rt_list *)header->payload);
        return;
    case OSTY_GC_KIND_MAP:
        osty_gc_remap_map_payload((osty_rt_map *)header->payload);
        return;
    case OSTY_GC_KIND_SET:
        osty_gc_remap_set_payload((osty_rt_set *)header->payload);
        return;
    case OSTY_GC_KIND_CLOSURE_ENV:
        osty_gc_remap_closure_env_payload(
            (osty_rt_closure_env *)header->payload);
        return;
    case OSTY_GC_KIND_CHANNEL:
        osty_gc_remap_chan_payload((osty_rt_chan_impl *)header->payload);
        return;
    default:
        break;
    }
    if (header->trace != NULL && header->byte_size == (int64_t)sizeof(void *)) {
        osty_gc_remap_slot(header->payload);
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

    osty_gc_index_remove(old_header->payload);
    osty_gc_index_insert(new_header->payload, new_header);
    osty_gc_identity_insert(new_header->stable_id, new_header);
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
    header = osty_gc_objects;
    while (header != NULL) {
        if (osty_gc_header_is_movable(header)) {
            osty_gc_header *clone = osty_gc_clone_header(header);
            osty_gc_forwarding_insert(header->payload, clone);
            moved += 1;
            moved_bytes += header->byte_size;
        }
        header = header->next;
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
    for (i = 0; i < osty_gc_global_root_count; i++) {
        osty_gc_remap_slot(osty_gc_global_root_slots[i]);
    }

    header = osty_gc_objects;
    while (header != NULL) {
        osty_gc_header *current = osty_gc_forwarding_lookup(header->payload);
        if (current == NULL) {
            current = header;
        }
        osty_gc_remap_header_payload(current);
        header = header->next;
    }

    header = osty_gc_objects;
    while (header != NULL) {
        next = header->next;
        {
            osty_gc_header *replacement =
                osty_gc_forwarding_lookup(header->payload);
            if (replacement != NULL) {
                osty_gc_replace_header(header, replacement);
                osty_gc_release_replaced_header(header);
            }
        }
        header = next;
    }
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
    header = osty_gc_young_head;
    while (header != NULL) {
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
        header = header->next_gen;
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
    for (i = 0; i < osty_gc_global_root_count; i++) {
        osty_gc_remap_slot(osty_gc_global_root_slots[i]);
    }

    header = osty_gc_objects;
    while (header != NULL) {
        osty_gc_header *current = osty_gc_forwarding_lookup(header->payload);
        if (current == NULL) {
            current = header;
        }
        osty_gc_remap_header_payload(current);
        header = header->next;
    }

    header = osty_gc_objects;
    while (header != NULL) {
        next = header->next;
        {
            osty_gc_header *replacement =
                osty_gc_forwarding_lookup(header->payload);
            if (replacement != NULL) {
                osty_gc_replace_header(header, replacement);
                osty_gc_release_replaced_header(header);
            }
        }
        header = next;
    }
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
    osty_gc_header *next;
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
    /* 3. Seed from registered global slots. */
    for (i = 0; i < osty_gc_global_root_count; i++) {
        osty_gc_mark_root_slot(osty_gc_global_root_slots[i]);
    }
    /* 4. Drain the work queue iteratively. */
    osty_gc_mark_drain();
    /* 5. Sweep unreachable across both generations. Survivors get
     *    their colour reset to WHITE so the next cycle starts from a
     *    clean slate. */
    header = osty_gc_objects;
    while (header != NULL) {
        next = header->next;
        if (header->color == OSTY_GC_COLOR_WHITE) {
            osty_gc_swept_count_total += 1;
            osty_gc_swept_bytes_total += header->byte_size;
            if (header->destroy != NULL) {
                header->destroy(header->payload);
            }
            osty_gc_unlink(header);
            osty_gc_reclaim_swept_header(header);
        } else {
            header->color = OSTY_GC_COLOR_WHITE;
            header->marked = false;
        }
        header = next;
    }
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
    /* 3. Global roots. */
    for (i = 0; i < osty_gc_global_root_count; i++) {
        osty_gc_mark_root_slot(osty_gc_global_root_slots[i]);
    }
    /* 4. Remembered set: for every edge whose owner is currently OLD,
     *    mark the value. This is where Phase A's edge log pays off —
     *    without it we would have to re-scan the entire OLD generation
     *    for references into YOUNG. */
    for (i = 0; i < osty_gc_remembered_edge_count; i++) {
        osty_gc_header *owner = osty_gc_find_header(osty_gc_remembered_edges[i].owner);
        if (owner == NULL || owner->generation != OSTY_GC_GEN_OLD) {
            continue;
        }
        osty_gc_mark_payload(osty_gc_remembered_edges[i].value);
    }
    /* 5. Drain. */
    osty_gc_mark_drain();
    osty_gc_minor_in_progress = false;
    /* 6. Sweep YOUNG unreachable; promote YOUNG survivors whose age
     *    reaches the threshold. We walk the young list exclusively —
     *    OLD is never touched by a minor. */
    header = osty_gc_young_head;
    while (header != NULL) {
        next = header->next_gen;
        if (header->color == OSTY_GC_COLOR_WHITE) {
            osty_gc_swept_count_total += 1;
            osty_gc_swept_bytes_total += header->byte_size;
            if (header->destroy != NULL) {
                header->destroy(header->payload);
            }
            osty_gc_unlink(header);
            osty_gc_reclaim_swept_header(header);
        } else {
            header->color = OSTY_GC_COLOR_WHITE;
            header->marked = false;
        }
        header = next;
    }
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
        /* Start a fresh cycle. */
        osty_gc_state = OSTY_GC_STATE_MARK_INCREMENTAL;
        osty_gc_incremental_seed_roots(root_slots, root_slot_count);
    }
    if (osty_gc_state == OSTY_GC_STATE_MARK_INCREMENTAL) {
        int64_t budget = osty_gc_incremental_budget_now();
        int64_t done = osty_gc_mark_drain_budget(budget);
        osty_gc_incremental_steps_total += 1;
        osty_gc_incremental_work_total += done;
        if (osty_gc_mark_stack_count == 0) {
            /* Cycle done — finish it inline. */
            osty_gc_state = OSTY_GC_STATE_SWEEPING;
            osty_gc_incremental_sweep();
            osty_gc_collection_count += 1;
            osty_gc_major_count += 1;
            osty_gc_allocated_since_collect = 0;
            osty_gc_allocated_since_minor = 0;
            osty_gc_collection_requested = false;
            osty_gc_collection_requested_major = false;
            osty_gc_barrier_logs_clear();
            osty_gc_state = OSTY_GC_STATE_IDLE;
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
        osty_gc_auto_drive_incremental(root_slots, root_slot_count);
        return;
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
    for (i = 0; i < osty_gc_global_root_count; i++) {
        osty_gc_mark_root_slot(osty_gc_global_root_slots[i]);
    }
}

static void osty_gc_incremental_sweep(void) {
    osty_gc_header *header;
    osty_gc_header *next;
    header = osty_gc_objects;
    while (header != NULL) {
        next = header->next;
        if (header->color == OSTY_GC_COLOR_WHITE) {
            osty_gc_swept_count_total += 1;
            osty_gc_swept_bytes_total += header->byte_size;
            if (header->destroy != NULL) {
                header->destroy(header->payload);
            }
            osty_gc_unlink(header);
            osty_gc_reclaim_swept_header(header);
        } else {
            header->color = OSTY_GC_COLOR_WHITE;
            header->marked = false;
        }
        header = next;
    }
}

/* Public incremental entry points. The `_with_stack_roots` variant is
 * the real worker; the no-arg `osty_gc_collect_incremental_start`
 * calls it with an empty stack-slot array so tests / callers without
 * visible frame descriptors can still drive the machinery. */
void osty_gc_collect_incremental_start_with_stack_roots(void *const *root_slots, int64_t root_slot_count) {
    osty_gc_acquire();
    if (osty_gc_state != OSTY_GC_STATE_IDLE) {
        osty_gc_release();
        osty_rt_abort("incremental start called while collection already in progress");
    }
    if (osty_concurrent_workers > 0) {
        osty_gc_release();
        return;
    }
    osty_gc_state = OSTY_GC_STATE_MARK_INCREMENTAL;
    osty_gc_incremental_seed_roots(root_slots, root_slot_count);
    osty_gc_release();
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

void osty_gc_collect_incremental_finish(void) {
    int64_t t_start = osty_gc_now_nanos();
    osty_gc_acquire();
    if (osty_gc_state == OSTY_GC_STATE_IDLE) {
        osty_gc_release();
        return;
    }
    /* Drain any remaining greys so the sweep sees a consistent
     * colouring. The incremental barrier (SATB pre_write) could have
     * dropped new greys on us between the last step and this call. */
    (void)osty_gc_mark_drain_budget(0);
    osty_gc_state = OSTY_GC_STATE_SWEEPING;
    osty_gc_incremental_sweep();
    osty_gc_collection_count += 1;
    osty_gc_major_count += 1;
    osty_gc_allocated_since_collect = 0;
    osty_gc_allocated_since_minor = 0;
    osty_gc_collection_requested = false;
    osty_gc_collection_requested_major = false;
    osty_gc_barrier_logs_clear();
    osty_gc_state = OSTY_GC_STATE_IDLE;
    osty_gc_release();

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
    osty_rt_list *sorted = osty_rt_list_cast(out);

    osty_gc_root_bind_v1(raw_list);
    osty_gc_root_bind_v1(out);
    osty_rt_list_ensure_layout(list, sizeof(int64_t), NULL);
    osty_rt_list_ensure_layout(sorted, sizeof(int64_t), NULL);
    osty_rt_list_copy_initialized(out, sorted, list->data, list->len);
    qsort(sorted->data, (size_t)sorted->len, sizeof(int64_t), osty_rt_compare_i64_ascending);
    osty_gc_root_release_v1(out);
    osty_gc_root_release_v1(raw_list);
    return out;
}

void *osty_rt_list_sorted_i1(void *raw_list) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    void *out = osty_rt_list_new();
    osty_rt_list *sorted = osty_rt_list_cast(out);

    osty_gc_root_bind_v1(raw_list);
    osty_gc_root_bind_v1(out);
    osty_rt_list_ensure_layout(list, sizeof(bool), NULL);
    osty_rt_list_ensure_layout(sorted, sizeof(bool), NULL);
    osty_rt_list_copy_initialized(out, sorted, list->data, list->len);
    qsort(sorted->data, (size_t)sorted->len, sizeof(bool), osty_rt_compare_i1_ascending);
    osty_gc_root_release_v1(out);
    osty_gc_root_release_v1(raw_list);
    return out;
}

void *osty_rt_list_sorted_f64(void *raw_list) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    void *out = osty_rt_list_new();
    osty_rt_list *sorted = osty_rt_list_cast(out);

    osty_gc_root_bind_v1(raw_list);
    osty_gc_root_bind_v1(out);
    osty_rt_list_ensure_layout(list, sizeof(double), NULL);
    osty_rt_list_ensure_layout(sorted, sizeof(double), NULL);
    osty_rt_list_copy_initialized(out, sorted, list->data, list->len);
    qsort(sorted->data, (size_t)sorted->len, sizeof(double), osty_rt_compare_f64_ascending);
    osty_gc_root_release_v1(out);
    osty_gc_root_release_v1(raw_list);
    return out;
}

void *osty_rt_list_sorted_string(void *raw_list) {
    osty_rt_list *list = osty_rt_list_cast(raw_list);
    void *out = osty_rt_list_new();
    osty_rt_list *sorted = osty_rt_list_cast(out);

    osty_gc_root_bind_v1(raw_list);
    osty_gc_root_bind_v1(out);
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

int64_t osty_rt_strings_IndexOf(const char *value, const char *substr) {
    const char *next;

    value = (value == NULL) ? "" : value;
    substr = (substr == NULL) ? "" : substr;
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

const char *osty_rt_strings_Replace(const char *value, const char *old, const char *new_value) {
    const char *next;
    size_t value_len;
    size_t old_len;
    size_t new_len;
    size_t prefix_len;
    size_t suffix_len;
    size_t total;
    char *out;

    value = (value == NULL) ? "" : value;
    old = (old == NULL) ? "" : old;
    new_value = (new_value == NULL) ? "" : new_value;
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

const char *osty_rt_strings_TrimStart(const char *value) {
    const char *start;

    if (value == NULL) {
        return osty_rt_string_dup_site("", 0, "runtime.strings.trim_start.empty");
    }
    start = value;
    while (*start == ' ' || *start == '\t' || *start == '\n' || *start == '\r' || *start == '\v' || *start == '\f') {
        start++;
    }
    return osty_rt_string_dup_site(start, strlen(start), "runtime.strings.trim_start");
}

const char *osty_rt_strings_TrimEnd(const char *value) {
    const char *end;
    size_t len;

    if (value == NULL) {
        return osty_rt_string_dup_site("", 0, "runtime.strings.trim_end.empty");
    }
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

void *osty_rt_strings_ToBytes(const char *value) {
    size_t len;
    size_t total;
    osty_rt_bytes *out;
    unsigned char *data;

    value = (value == NULL) ? "" : value;
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
    map->index_slots = NULL;
    map->index_cap = 0;
    map->index_len = 0;
}

static void osty_rt_map_index_insert_slot(osty_rt_map *map, int64_t slot) {
    uint64_t mask = (uint64_t)(map->index_cap - 1);
    uint64_t idx = (uint64_t)osty_rt_map_key_hash(map->key_kind, osty_rt_map_key_slot(map, slot)) & mask;
    while (map->index_slots[idx] != 0) {
        idx = (idx + 1) & mask;
    }
    map->index_slots[idx] = slot + 1;
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
        map->index_slots = map->index_inline;
        memset(map->index_slots, 0, (size_t)cap * sizeof(int64_t));
        map->index_cap = cap;
    } else {
        if (map->index_cap != cap || map->index_slots == NULL || map->index_slots == map->index_inline) {
            if (map->index_slots != NULL && map->index_slots != map->index_inline) {
                free(map->index_slots);
            }
            map->index_slots = (int64_t *)calloc((size_t)cap, sizeof(int64_t));
            if (map->index_slots == NULL) {
                osty_rt_abort("out of memory");
            }
            map->index_cap = cap;
        } else {
            memset(map->index_slots, 0, (size_t)map->index_cap * sizeof(int64_t));
        }
    }
    map->index_len = 0;
    for (i = 0; i < map->len; i++) {
        osty_rt_map_index_insert_slot(map, i);
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

static int64_t osty_rt_map_find_index_linear(osty_rt_map *map, const void *key) {
    int64_t i;
    size_t key_size = osty_rt_kind_size(map->key_kind);
    for (i = 0; i < map->len; i++) {
        if (osty_rt_value_equals(osty_rt_map_key_slot(map, i), key, key_size, map->key_kind)) {
            return i;
        }
    }
    return -1;
}

static int64_t osty_rt_map_find_index_indexed(osty_rt_map *map, const void *key) {
    size_t key_size = osty_rt_kind_size(map->key_kind);
    uint64_t mask = (uint64_t)(map->index_cap - 1);
    uint64_t idx = (uint64_t)osty_rt_map_key_hash(map->key_kind, key) & mask;

    for (;;) {
        int64_t entry = map->index_slots[idx];
        int64_t slot;
        if (entry == 0) {
            return -1;
        }
        slot = entry - 1;
        if (slot >= 0 && slot < map->len &&
            osty_rt_value_equals(osty_rt_map_key_slot(map, slot), key, key_size, map->key_kind)) {
            return slot;
        }
        idx = (idx + 1) & mask;
    }
}

static int64_t osty_rt_map_find_index(osty_rt_map *map, const void *key) {
    if (map->index_cap > 0 && map->index_slots != NULL) {
        return osty_rt_map_find_index_indexed(map, key);
    }
    return osty_rt_map_find_index_linear(map, key);
}

static osty_rt_map *osty_rt_map_cast(void *raw_map) {
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
    osty_rt_map_mutex_init_or_abort(map);
    return map;
}

// Public lock/unlock for composite ops (update) that must hold the
// lock across get + callback + insert. Recursive mutex so calls from
// a user callback into the same map (e.g. counts.len()) re-acquire
// instead of self-deadlocking.
void osty_rt_map_lock(void *raw_map) {
    osty_rt_map *map = osty_rt_map_cast(raw_map);
    if (map == NULL || !map->mu_init) return;
    osty_rt_rmu_lock(&map->mu);
}

void osty_rt_map_unlock(void *raw_map) {
    osty_rt_map *map = osty_rt_map_cast(raw_map);
    if (map == NULL || !map->mu_init) return;
    osty_rt_rmu_unlock(&map->mu);
}

static bool osty_rt_map_contains_raw(void *raw_map, const void *key) {
    osty_rt_map *map = osty_rt_map_cast(raw_map);
    return map != NULL && osty_rt_map_find_index(map, key) >= 0;
}

static void osty_rt_map_insert_raw(void *raw_map, const void *key, const void *value) {
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
    if (inserted) {
        if (map->len >= 8) {
            if (map->index_cap == 0 || map->index_slots == NULL ||
                (map->index_len + 1) * 10 >= map->index_cap * 7) {
                osty_rt_map_index_rebuild(map, map->len);
            } else {
                osty_rt_map_index_insert_slot(map, index);
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

static void osty_rt_map_get_or_abort_raw(void *raw_map, const void *key, void *out_value) {
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
static bool osty_rt_map_get_raw(void *raw_map, const void *key, void *out_value) {
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
    osty_rt_map *map = osty_rt_map_cast(raw_map);
    void *out = osty_rt_list_new();
    osty_rt_list *keys = osty_rt_list_cast(out);
    int64_t count = 0;
    if (map == NULL) {
        osty_rt_abort("map is null");
    }
    osty_gc_root_bind_v1(raw_map);
    osty_gc_root_bind_v1(out);
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
    if (osty_gc_state == OSTY_GC_STATE_MARK_INCREMENTAL &&
        old_header->color == OSTY_GC_COLOR_WHITE) {
        old_header->color = OSTY_GC_COLOR_GREY;
        old_header->marked = true;
        osty_gc_mark_stack_push(old_header);
        osty_gc_satb_barrier_greyed_total += 1;
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

void osty_gc_mark_slot_v1(void *slot_addr) {
    /* Only reachable inside `osty_gc_collect_now_with_stack_roots`,
     * which runs under `osty_gc_lock` (see safepoint / debug collect).
     * No additional lock required. */
    if (osty_gc_trace_slot_mode_current == OSTY_GC_TRACE_SLOT_MODE_REMAP) {
        osty_gc_remap_slot(slot_addr);
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

void osty_gc_root_bind_v1(void *root) {
    osty_gc_header *header;
    osty_gc_acquire();
    header = osty_gc_root_binding_header(root);
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
    osty_gc_acquire();
    header = osty_gc_find_header_or_forwarded(root);
    if (header == NULL) {
        osty_gc_release();
        return;
    }
    if (header->pin_count == INT64_MAX) {
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

/* Phase B test-access entry points. `debug_collect` above stays a
 * forced-major call so existing Phase A tests (which assume "one
 * collection frees every unrooted object") keep their semantics.
 * `debug_collect_minor` / `debug_collect_major` let Phase B tests pin
 * the tier explicitly regardless of pressure state. */
void osty_gc_debug_collect_minor(void) {
    osty_gc_acquire();
    if (osty_concurrent_workers > 0) {
        osty_gc_release();
        return;
    }
    osty_gc_collect_minor_with_stack_roots(NULL, 0);
    osty_gc_release();
}

void osty_gc_debug_collect_major(void) {
    osty_gc_acquire();
    if (osty_concurrent_workers > 0) {
        osty_gc_release();
        return;
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
            (long long)s.nursery_limit_bytes, (long long)s.promote_age);
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
        if (osty_gc_identity_lookup(header->stable_id) != header) {
            status = OSTY_GC_VALIDATE_IDENTITY_INDEX_MISMATCH;
            goto done;
        }
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
    if (osty_gc_identity_count != osty_gc_live_count) {
        status = OSTY_GC_VALIDATE_IDENTITY_INDEX_MISMATCH;
        goto done;
    }
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

#if !defined(OSTY_RT_PLATFORM_WIN32)
#  include <unistd.h>    /* sysconf(_SC_NPROCESSORS_ONLN) */
#endif
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

struct osty_rt_task_handle_impl_;
typedef struct osty_rt_task_handle_impl_ osty_rt_task_handle_impl;

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
    osty_rt_task_handle_impl *h =
        (osty_rt_task_handle_impl *)osty_gc_allocate_managed(
            sizeof(osty_rt_task_handle_impl),
            OSTY_GC_KIND_GENERIC,
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

    /* ---- Phase C: STW finish + sweep ---- */
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
    for (i = 0; i < ch->count; i++) {
        int64_t idx = (ch->head + i) % ch->cap;
        void *child = (void *)(uintptr_t)ch->slots[idx];
        if (child != NULL) {
            osty_gc_mark_payload(child);
        }
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

/* std.env command-line argument surface.
 *
 * The emitter (see internal/llvmgen/stdlib_env_shim.go) routes the Osty
 * call `env.args()` to `osty_rt_env_args`, and when a script/binary's
 * package imports `std.env` the generated `main` is widened to
 * (i32 argc, ptr argv) and begins with a call to `osty_rt_env_args_init`.
 * The init call hands us the raw C argv; each `env.args()` invocation
 * materializes a fresh GC-managed List<String> by duplicating every
 * argv entry through the string allocator. Freshness matters because
 * the returned list is mutable from Osty and must not alias process
 * argv storage. */
static int64_t osty_rt_env_stored_argc = 0;
static char *const *osty_rt_env_stored_argv = NULL;

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
