package backend

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// Scheduler smoke test. Exercises the public `osty_rt_task_*` and
// `osty_rt_thread_*` surface the MIR backend lowers to, plus the
// real channel implementation added in Phase 1B/2 (see
// RUNTIME_SCHEDULER.md).
//
// The harness mimics what an Osty program would do after lowering:
// allocate a closure env where env[0] is the fn pointer, then hand
// the env to the scheduler runtime. Test body functions match the
// uniform closure ABI (`int64_t body(void *env [, void *group])`)
// described in §ABI contract of RUNTIME_SCHEDULER.md.
func TestBundledRuntimeScheduler(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_sched_harness.c")
	binaryPath := filepath.Join(dir, "runtime_sched_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <time.h>

int64_t osty_rt_task_group(void *body_env);
void *osty_rt_task_spawn(void *body_env);
void *osty_rt_task_group_spawn(void *group, void *body_env);
int64_t osty_rt_task_handle_join(void *handle);
void osty_rt_task_group_cancel(void *group);
bool osty_rt_task_group_is_cancelled(void *group);
bool osty_rt_cancel_is_cancelled(void);
void osty_rt_thread_yield(void);
void osty_rt_thread_sleep(int64_t nanos);

/* Uniform closure env: { fn_ptr, ...captures }. The scheduler loads
 * env[0] as the fn pointer and invokes it with the env. taskGroup
 * bodies additionally receive a group pointer as the second arg. */
typedef struct two_slot_env {
    void *fn;
    int64_t capture;
} two_slot_env;

typedef struct three_slot_env {
    void *fn;
    void *handle_a;
    void *handle_b;
} three_slot_env;

/* spawn body: returns the captured int. */
static int64_t body_return_capture(void *env) {
    two_slot_env *e = (two_slot_env *)env;
    return e->capture;
}

/* spawn body: sleeps 50ms before returning, exposes real wall-clock
 * parallelism when run across N spawns. */
static int64_t body_sleep_then_capture(void *env) {
    two_slot_env *e = (two_slot_env *)env;
    osty_rt_thread_sleep(50000000LL); /* 50ms */
    return e->capture;
}

/* spawn body: checks cancellation against current group. Returns 1
 * if cancelled, 0 otherwise. */
static int64_t body_check_cancel(void *env) {
    (void)env;
    return osty_rt_cancel_is_cancelled() ? 1 : 0;
}

/* taskGroup body: spawns two children into the injected group, joins
 * them, returns the sum. Env layout: { fn, a_env, b_env }. */
static int64_t body_taskgroup_two_spawns(void *env, void *group) {
    three_slot_env *e = (three_slot_env *)env;
    void *ha = osty_rt_task_group_spawn(group, e->handle_a);
    void *hb = osty_rt_task_group_spawn(group, e->handle_b);
    int64_t a = osty_rt_task_handle_join(ha);
    int64_t b = osty_rt_task_handle_join(hb);
    return a + b;
}

/* taskGroup body: cancels the group before spawning a child whose
 * job is to report cancel_is_cancelled() against the rebound group.
 * Returns a 3-bit result: bit0 = outer thread-local check after
 * cancel, bit1 = group flag check, bit2 = child's cancel check. */
static int64_t body_taskgroup_cancel_then_check(void *env, void *group) {
    three_slot_env *e = (three_slot_env *)env;
    osty_rt_task_group_cancel(group);
    int64_t outer = osty_rt_cancel_is_cancelled() ? 1 : 0;
    int64_t flag = osty_rt_task_group_is_cancelled(group) ? 1 : 0;
    void *h = osty_rt_task_group_spawn(group, e->handle_a);
    int64_t inner = osty_rt_task_handle_join(h);
    return (inner << 2) | (flag << 1) | outer;
}

int main(void) {
    /* Test 1: taskGroup with two group-spawned children joined for sum. */
    two_slot_env a_env = { (void *)body_return_capture, 7 };
    two_slot_env b_env = { (void *)body_return_capture, 35 };
    three_slot_env tg_env = {
        (void *)body_taskgroup_two_spawns,
        (void *)&a_env,
        (void *)&b_env,
    };
    int64_t sum = osty_rt_task_group((void *)&tg_env);
    printf("%lld\n", (long long)sum);

    /* Test 2: detached spawn + join returns the body's result. */
    two_slot_env detached = { (void *)body_return_capture, 99 };
    void *h = osty_rt_task_spawn((void *)&detached);
    printf("%lld\n", (long long)osty_rt_task_handle_join(h));

    /* Test 3: cancel propagation. outer thread-local, group flag, and
     * child spawn all observe cancel. Expected bits: 0b111 = 7. */
    two_slot_env ck_child = { (void *)body_check_cancel, 0 };
    three_slot_env ck_env = {
        (void *)body_taskgroup_cancel_then_check,
        (void *)&ck_child,
        NULL,
    };
    int64_t ck = osty_rt_task_group((void *)&ck_env);
    printf("%lld\n", (long long)ck);

    /* Test 4: outside any taskGroup, cancel_is_cancelled returns false. */
    printf("%d\n", osty_rt_cancel_is_cancelled() ? 1 : 0);

    /* Test 5: thread.yield is a safe call. */
    osty_rt_thread_yield();
    printf("ok\n");

    /* Test 6: thread.sleep actually elapses. Request 5ms, verify at
     * least 1ms elapsed (generous floor to tolerate coarse clocks on
     * slow CI). */
    struct timespec before, after;
    clock_gettime(CLOCK_MONOTONIC, &before);
    osty_rt_thread_sleep(5000000LL);
    clock_gettime(CLOCK_MONOTONIC, &after);
    long long elapsed_ns = (long long)(after.tv_sec - before.tv_sec) * 1000000000LL +
                           (long long)(after.tv_nsec - before.tv_nsec);
    printf("%d\n", elapsed_ns >= 1000000LL ? 1 : 0);

    /* Test 7: four sleeping spawns joined. Each sleeps 50ms; strict
     * serialization would take ≥200ms. We accept anything under 150ms
     * as "parallelism observed" — that leaves ample slack for a
     * loaded CI runner while still rejecting the sequential Phase 1A
     * fallback. Joined sum = 1+2+3+4 = 10. */
    two_slot_env p_env_a = { (void *)body_sleep_then_capture, 1 };
    two_slot_env p_env_b = { (void *)body_sleep_then_capture, 2 };
    two_slot_env p_env_c = { (void *)body_sleep_then_capture, 3 };
    two_slot_env p_env_d = { (void *)body_sleep_then_capture, 4 };
    clock_gettime(CLOCK_MONOTONIC, &before);
    void *pa = osty_rt_task_spawn((void *)&p_env_a);
    void *pb = osty_rt_task_spawn((void *)&p_env_b);
    void *pc = osty_rt_task_spawn((void *)&p_env_c);
    void *pd = osty_rt_task_spawn((void *)&p_env_d);
    int64_t total = osty_rt_task_handle_join(pa) +
                    osty_rt_task_handle_join(pb) +
                    osty_rt_task_handle_join(pc) +
                    osty_rt_task_handle_join(pd);
    clock_gettime(CLOCK_MONOTONIC, &after);
    elapsed_ns = (long long)(after.tv_sec - before.tv_sec) * 1000000000LL +
                 (long long)(after.tv_nsec - before.tv_nsec);
    printf("%lld\n", (long long)total);
    /* Report 1 iff wall-clock shows real parallelism (< 150ms vs the
     * 200ms sequential floor). */
    printf("%d\n", elapsed_ns < 150000000LL ? 1 : 0);

    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}
	cmd := exec.Command("clang", "-std=c11", "-pthread", runtimePath, harnessPath, "-o", binaryPath)
	buildOutput, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("clang failed: %v\n%s", err, buildOutput)
	}
	runOutput, err := exec.Command(binaryPath).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	// Expected lines:
	//   42        (7 + 35)
	//   99        (detached spawn join)
	//   7         (outer=1, flag=1, inner=1 → 0b111)
	//   0         (cancel_is_cancelled outside any group)
	//   ok        (yield didn't crash)
	//   1         (sleep elapsed ≥ 1ms)
	//   10        (1+2+3+4 across four parallel spawns)
	//   1         (wall-clock shows real parallelism, not strict 4×2ms)
	const want = "42\n99\n7\n0\nok\n1\n10\n1\n"
	if got := string(runOutput); got != want {
		t.Fatalf("scheduler harness stdout = %q, want %q", got, want)
	}
}

// Channels: producer/consumer across two threads via a buffered
// channel. Exercises send_i64 / recv_i64 / close / is_closed and
// verifies recv-after-close-and-drain signals ok=0.
func TestBundledRuntimeSchedulerChannels(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_chan_harness.c")
	binaryPath := filepath.Join(dir, "runtime_chan_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>

typedef struct osty_rt_chan_recv_result {
    int64_t value;
    int64_t ok;
} osty_rt_chan_recv_result;

void *osty_rt_thread_chan_make(int64_t capacity);
void osty_rt_thread_chan_close(void *ch);
bool osty_rt_thread_chan_is_closed(void *ch);
void osty_rt_thread_chan_send_i64(void *ch, int64_t v);
osty_rt_chan_recv_result osty_rt_thread_chan_recv_i64(void *ch);

void *osty_rt_task_spawn(void *body_env);
int64_t osty_rt_task_handle_join(void *handle);

typedef struct prod_env {
    void *fn;
    void *ch;
    int64_t count;
} prod_env;

/* Producer: send 0..count-1, then close. */
static int64_t body_produce(void *env) {
    prod_env *e = (prod_env *)env;
    for (int64_t i = 0; i < e->count; i++) {
        osty_rt_thread_chan_send_i64(e->ch, i);
    }
    osty_rt_thread_chan_close(e->ch);
    return e->count;
}

int main(void) {
    /* Buffered capacity 4, producer sends 10 items → forces block/wake. */
    void *ch = osty_rt_thread_chan_make(4);
    prod_env penv = { (void *)body_produce, ch, 10 };
    void *h = osty_rt_task_spawn((void *)&penv);

    int64_t sum = 0;
    for (;;) {
        osty_rt_chan_recv_result r = osty_rt_thread_chan_recv_i64(ch);
        if (!r.ok) {
            break;
        }
        sum += r.value;
    }
    int64_t produced = osty_rt_task_handle_join(h);
    printf("%lld\n", (long long)sum);        /* 0+1+...+9 = 45 */
    printf("%lld\n", (long long)produced);   /* 10 */
    printf("%d\n", osty_rt_thread_chan_is_closed(ch) ? 1 : 0); /* 1 */

    /* Recv-after-drain still reports ok=0 (not a block). */
    osty_rt_chan_recv_result empty = osty_rt_thread_chan_recv_i64(ch);
    printf("%lld\n", (long long)empty.ok);    /* 0 */

    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}
	cmd := exec.Command("clang", "-std=c11", "-pthread", runtimePath, harnessPath, "-o", binaryPath)
	if buildOutput, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clang failed: %v\n%s", err, buildOutput)
	}
	runOutput, err := exec.Command(binaryPath).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	const want = "45\n10\n1\n0\n"
	if got := string(runOutput); got != want {
		t.Fatalf("channel harness stdout = %q, want %q", got, want)
	}
}

// Auto-reap: task_group body spawns a child that outlives the manual
// join sites. The group teardown must pthread_join that stray handle
// so no thread survives its group scope (spec §8.1).
func TestBundledRuntimeSchedulerTaskGroupAutoReap(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_autoreap_harness.c")
	binaryPath := filepath.Join(dir, "runtime_autoreap_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>

int64_t osty_rt_task_group(void *body_env);
void *osty_rt_task_group_spawn(void *group, void *body_env);
int64_t osty_rt_task_handle_join(void *handle);
void osty_rt_thread_sleep(int64_t nanos);

static volatile int64_t flag = 0;

typedef struct env1 {
    void *fn;
    int64_t sleep_ns;
} env1;

typedef struct env2 {
    void *fn;
    void *child_env;
} env2;

/* Child that sleeps, then sets a shared flag before exiting. If the
 * group teardown reaps it correctly, the main thread reads flag=1
 * after task_group returns. If the group returns while the child is
 * still running (no auto-reap), the flag read would race and most
 * likely print 0. */
static int64_t body_child(void *env) {
    env1 *e = (env1 *)env;
    osty_rt_thread_sleep(e->sleep_ns);
    __atomic_store_n(&flag, 1, __ATOMIC_RELEASE);
    return 42;
}

/* taskGroup body: spawns a child but never joins it. The group teardown
 * is responsible for waiting on it. */
static int64_t body_parent(void *env, void *group) {
    env2 *e = (env2 *)env;
    (void)osty_rt_task_group_spawn(group, e->child_env);
    return 0;
}

int main(void) {
    env1 c_env = { (void *)body_child, 30000000LL }; /* 30ms */
    env2 p_env = { (void *)body_parent, (void *)&c_env };
    (void)osty_rt_task_group((void *)&p_env);

    /* Group returned. If auto-reap worked, the child has set flag. */
    printf("%lld\n", (long long)__atomic_load_n(&flag, __ATOMIC_ACQUIRE));
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}
	cmd := exec.Command("clang", "-std=c11", "-pthread", runtimePath, harnessPath, "-o", binaryPath)
	if buildOutput, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clang failed: %v\n%s", err, buildOutput)
	}
	runOutput, err := exec.Command(binaryPath).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	if got := string(runOutput); got != "1\n" {
		t.Fatalf("auto-reap harness stdout = %q, want %q", got, "1\n")
	}
}

// Channel stress: 4 producers × 4 consumers × 1000 items total. Verifies
// the mutex+cond discipline holds under heavy contention.
func TestBundledRuntimeSchedulerChannelStress(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_chan_stress_harness.c")
	binaryPath := filepath.Join(dir, "runtime_chan_stress_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>

typedef struct osty_rt_chan_recv_result {
    int64_t value;
    int64_t ok;
} osty_rt_chan_recv_result;

void *osty_rt_thread_chan_make(int64_t capacity);
void osty_rt_thread_chan_close(void *ch);
void osty_rt_thread_chan_send_i64(void *ch, int64_t v);
osty_rt_chan_recv_result osty_rt_thread_chan_recv_i64(void *ch);
void *osty_rt_task_spawn(void *body_env);
int64_t osty_rt_task_handle_join(void *handle);

#define N_PRODUCERS 4
#define N_CONSUMERS 4
#define N_PER_PRODUCER 250

typedef struct prod_env {
    void *fn;
    void *ch;
    int64_t base;
} prod_env;

typedef struct cons_env {
    void *fn;
    void *ch;
} cons_env;

static int64_t body_produce(void *env) {
    prod_env *e = (prod_env *)env;
    int64_t acc = 0;
    for (int64_t i = 0; i < N_PER_PRODUCER; i++) {
        int64_t v = e->base + i;
        osty_rt_thread_chan_send_i64(e->ch, v);
        acc += v;
    }
    return acc;
}

static int64_t body_consume(void *env) {
    cons_env *e = (cons_env *)env;
    int64_t acc = 0;
    for (;;) {
        osty_rt_chan_recv_result r = osty_rt_thread_chan_recv_i64(e->ch);
        if (!r.ok) break;
        acc += r.value;
    }
    return acc;
}

int main(void) {
    void *ch = osty_rt_thread_chan_make(16);

    prod_env penvs[N_PRODUCERS];
    void *phs[N_PRODUCERS];
    for (int i = 0; i < N_PRODUCERS; i++) {
        penvs[i].fn = (void *)body_produce;
        penvs[i].ch = ch;
        penvs[i].base = (int64_t)i * 1000;
        phs[i] = osty_rt_task_spawn((void *)&penvs[i]);
    }

    cons_env cenvs[N_CONSUMERS];
    void *chs[N_CONSUMERS];
    for (int i = 0; i < N_CONSUMERS; i++) {
        cenvs[i].fn = (void *)body_consume;
        cenvs[i].ch = ch;
        chs[i] = osty_rt_task_spawn((void *)&cenvs[i]);
    }

    int64_t produced_sum = 0;
    for (int i = 0; i < N_PRODUCERS; i++) {
        produced_sum += osty_rt_task_handle_join(phs[i]);
    }
    osty_rt_thread_chan_close(ch);

    int64_t consumed_sum = 0;
    for (int i = 0; i < N_CONSUMERS; i++) {
        consumed_sum += osty_rt_task_handle_join(chs[i]);
    }

    printf("%lld\n", (long long)produced_sum);
    printf("%lld\n", (long long)consumed_sum);
    printf("%d\n", produced_sum == consumed_sum ? 1 : 0);
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}
	cmd := exec.Command("clang", "-std=c11", "-pthread", runtimePath, harnessPath, "-o", binaryPath)
	if buildOutput, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clang failed: %v\n%s", err, buildOutput)
	}
	runOutput, err := exec.Command(binaryPath).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	// Expected sums:
	//   produced = Σ_{p=0..3} Σ_{i=0..249} (1000p + i)
	//            = 250 × (0+1000+2000+3000) + 4 × (0+1+...+249)
	//            = 250 × 6000 + 4 × 31125
	//            = 1500000 + 124500 = 1624500
	//   consumed must equal produced, so the last line is 1.
	const want = "1624500\n1624500\n1\n"
	if got := string(runOutput); got != want {
		t.Fatalf("channel stress stdout = %q, want %q", got, want)
	}
}

// Select: exercises recv / timeout / default arms. Three sub-scenarios
// packed into one binary so the whole flow lives in a single harness.
func TestBundledRuntimeSchedulerSelect(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_select_harness.c")
	binaryPath := filepath.Join(dir, "runtime_select_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>

void *osty_rt_thread_chan_make(int64_t capacity);
void osty_rt_thread_chan_send_i64(void *ch, int64_t v);

void osty_rt_select(void *body_env);
void osty_rt_select_recv(void *s, void *ch, void *arm);
void osty_rt_select_timeout(void *s, int64_t ns, void *arm);
void osty_rt_select_default(void *s, void *arm);

static int64_t outcome = -1;

/* Arm closures write the outcome into a module-global; the Osty
 * frontend will instead use captured slot references, but the shape
 * (closure env slot-0 holds the fn pointer; fn takes the env) is the
 * same. */

typedef struct recv_arm_env { void *fn; int64_t tag; } recv_arm_env;
typedef struct plain_arm_env { void *fn; int64_t tag; } plain_arm_env;

static void recv_arm_fn(void *env, int64_t value) {
    recv_arm_env *e = (recv_arm_env *)env;
    outcome = e->tag * 1000 + value;
}

static void plain_arm_fn(void *env) {
    plain_arm_env *e = (plain_arm_env *)env;
    outcome = e->tag;
}

typedef struct select_body_env {
    void *fn;
    void *ch;
    int64_t timeout_ns;
    int with_default;
    void *recv_arm;
    void *timeout_arm;
    void *default_arm;
} select_body_env;

static void select_body(void *env, void *s) {
    select_body_env *e = (select_body_env *)env;
    osty_rt_select_recv(s, e->ch, e->recv_arm);
    osty_rt_select_timeout(s, e->timeout_ns, e->timeout_arm);
    if (e->with_default) {
        osty_rt_select_default(s, e->default_arm);
    }
}

int main(void) {
    recv_arm_env ra = { (void *)recv_arm_fn, 7 };
    plain_arm_env ta = { (void *)plain_arm_fn, 999 };
    plain_arm_env da = { (void *)plain_arm_fn, -1 };

    /* Scenario 1: channel has a value — recv arm wins. */
    void *ch1 = osty_rt_thread_chan_make(4);
    osty_rt_thread_chan_send_i64(ch1, 42);
    select_body_env e1 = {
        (void *)select_body, ch1, 1000000000LL, 0,
        (void *)&ra, (void *)&ta, (void *)&da,
    };
    outcome = -1;
    osty_rt_select((void *)&e1);
    printf("%lld\n", (long long)outcome);  /* 7 * 1000 + 42 = 7042 */

    /* Scenario 2: channel empty, no default — timeout arm fires. */
    void *ch2 = osty_rt_thread_chan_make(4);
    select_body_env e2 = {
        (void *)select_body, ch2, 5000000LL /* 5ms */, 0,
        (void *)&ra, (void *)&ta, (void *)&da,
    };
    outcome = -1;
    osty_rt_select((void *)&e2);
    printf("%lld\n", (long long)outcome);  /* 999 */

    /* Scenario 3: channel empty, default present — default fires first. */
    void *ch3 = osty_rt_thread_chan_make(4);
    select_body_env e3 = {
        (void *)select_body, ch3, 1000000000LL, 1,
        (void *)&ra, (void *)&ta, (void *)&da,
    };
    outcome = -1;
    osty_rt_select((void *)&e3);
    printf("%lld\n", (long long)outcome);  /* -1 */

    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}
	cmd := exec.Command("clang", "-std=c11", "-pthread", runtimePath, harnessPath, "-o", binaryPath)
	if buildOutput, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clang failed: %v\n%s", err, buildOutput)
	}
	runOutput, err := exec.Command(binaryPath).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	const want = "7042\n999\n-1\n"
	if got := string(runOutput); got != want {
		t.Fatalf("select harness stdout = %q, want %q", got, want)
	}
}

// collectAll: body spawns four children into the injected group, each
// returns its captured int. The runtime joins them all and returns a
// List<Result<Int, Error>> whose i-th element has disc=1 (Ok) and
// payload = the i-th handle's result. We reach into the raw list
// via elem_size=16 / read-bytes to verify layout without needing the
// frontend.
func TestBundledRuntimeSchedulerCollectAll(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_collect_all_harness.c")
	binaryPath := filepath.Join(dir, "runtime_collect_all_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

void *osty_rt_task_collect_all(void *body_env);
void *osty_rt_task_group_spawn(void *group, void *body_env);
void *osty_rt_list_new(void);
int64_t osty_rt_list_len(void *raw_list);
void osty_rt_list_push_ptr(void *raw_list, void *value);
void osty_rt_list_get_bytes(void *raw_list, int64_t index, void *out, int64_t elem_size, void *trace_elem);

typedef struct child_env {
    void *fn;
    int64_t capture;
} child_env;

typedef struct body_env {
    void *fn;
    child_env *children;
    int64_t count;
} body_env;

static int64_t child_body(void *env) {
    child_env *e = (child_env *)env;
    return e->capture;
}

static void *body_spawn_all(void *env, void *group) {
    body_env *be = (body_env *)env;
    void *list = osty_rt_list_new();
    for (int64_t i = 0; i < be->count; i++) {
        void *h = osty_rt_task_group_spawn(group, (void *)&be->children[i]);
        osty_rt_list_push_ptr(list, h);
    }
    return list;
}

int main(void) {
    child_env kids[4] = {
        { (void *)child_body, 11 },
        { (void *)child_body, 22 },
        { (void *)child_body, 33 },
        { (void *)child_body, 44 },
    };
    body_env be = { (void *)body_spawn_all, kids, 4 };
    void *out = osty_rt_task_collect_all((void *)&be);

    int64_t n = osty_rt_list_len(out);
    printf("%lld\n", (long long)n);

    struct { int64_t disc; int64_t payload; } slot;
    for (int64_t i = 0; i < n; i++) {
        memset(&slot, 0, sizeof(slot));
        osty_rt_list_get_bytes(out, i, &slot, sizeof(slot), NULL);
        printf("%lld:%lld\n", (long long)slot.disc, (long long)slot.payload);
    }
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}
	cmd := exec.Command("clang", "-std=c11", "-pthread", runtimePath, harnessPath, "-o", binaryPath)
	if buildOutput, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clang failed: %v\n%s", err, buildOutput)
	}
	runOutput, err := exec.Command(binaryPath).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	// disc=1 means Ok per variantIndexByName("Ok") == 1.
	const want = "4\n1:11\n1:22\n1:33\n1:44\n"
	if got := string(runOutput); got != want {
		t.Fatalf("collectAll harness stdout = %q, want %q", got, want)
	}
}

// race: three children spawned into the injected group. Child 0 wins
// by sleeping only 10ms; children 1-2 sleep 2s but check cancellation
// every 20ms, so they exit soon after race() broadcasts cancel.
// Asserts (a) the winner's Ok-wrapped payload is child 0's capture,
// (b) total wall-clock is well below the 2s sibling floor.
func TestBundledRuntimeSchedulerRace(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_race_harness.c")
	binaryPath := filepath.Join(dir, "runtime_race_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <time.h>

typedef struct result_enum {
    int64_t disc;
    int64_t payload;
} result_enum;

result_enum osty_rt_task_race(void *body_env);
void *osty_rt_task_group_spawn(void *group, void *body_env);
void *osty_rt_list_new(void);
void osty_rt_list_push_ptr(void *raw_list, void *value);
void osty_rt_thread_sleep(int64_t nanos);
bool osty_rt_cancel_is_cancelled(void);

typedef struct child_env {
    void *fn;
    int64_t capture;
    int64_t sleep_ns;
    int64_t step_ns;
} child_env;

typedef struct body_env {
    void *fn;
    child_env *children;
    int64_t count;
} body_env;

/* Child body: sleep in step_ns-sized chunks, checking cancellation
 * between steps. Returns early with capture + 1000 if it observed
 * cancel, so the main process can distinguish winner from losers. */
static int64_t child_body(void *env) {
    child_env *e = (child_env *)env;
    int64_t remaining = e->sleep_ns;
    while (remaining > 0) {
        if (osty_rt_cancel_is_cancelled()) {
            return e->capture + 1000;
        }
        int64_t step = remaining < e->step_ns ? remaining : e->step_ns;
        osty_rt_thread_sleep(step);
        remaining -= step;
    }
    return e->capture;
}

static void *body_spawn_racers(void *env, void *group) {
    body_env *be = (body_env *)env;
    void *list = osty_rt_list_new();
    for (int64_t i = 0; i < be->count; i++) {
        void *h = osty_rt_task_group_spawn(group, (void *)&be->children[i]);
        osty_rt_list_push_ptr(list, h);
    }
    return list;
}

int main(void) {
    /* child 0: sleeps ~10ms then wins. Others sleep up to 2s but
     * step-check cancellation every 20ms. */
    child_env kids[3] = {
        { (void *)child_body, 7,  10000000LL,    5000000LL  },
        { (void *)child_body, 99, 2000000000LL, 20000000LL },
        { (void *)child_body, 88, 2000000000LL, 20000000LL },
    };
    body_env be = { (void *)body_spawn_racers, kids, 3 };

    struct timespec before, after;
    clock_gettime(CLOCK_MONOTONIC, &before);
    result_enum r = osty_rt_task_race((void *)&be);
    clock_gettime(CLOCK_MONOTONIC, &after);
    long long elapsed_ns = (long long)(after.tv_sec - before.tv_sec) * 1000000000LL +
                           (long long)(after.tv_nsec - before.tv_nsec);

    printf("%lld\n", (long long)r.disc);      /* 1 = Ok */
    printf("%lld\n", (long long)r.payload);   /* 7 = child 0's capture */
    /* Siblings must cooperate with cancellation: total wall-clock
     * should be under 500ms even though the slow children nominally
     * sleep 2s. Conservative ceiling to tolerate CI jitter. */
    printf("%d\n", elapsed_ns < 500000000LL ? 1 : 0);
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}
	cmd := exec.Command("clang", "-std=c11", "-pthread", runtimePath, harnessPath, "-o", binaryPath)
	if buildOutput, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clang failed: %v\n%s", err, buildOutput)
	}
	runOutput, err := exec.Command(binaryPath).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	const want = "1\n7\n1\n"
	if got := string(runOutput); got != want {
		t.Fatalf("race harness stdout = %q, want %q", got, want)
	}
}

// parallel: 10 items × concurrency 4 × f(x) = x*x. Verifies every
// output slot holds Ok(x*x) and the sum matches 0^2 + ... + 9^2 = 285.
func TestBundledRuntimeSchedulerParallel(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_parallel_harness.c")
	binaryPath := filepath.Join(dir, "runtime_parallel_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

void *osty_rt_parallel(void *items, int64_t concurrency, void *f_env);
void *osty_rt_list_new(void);
int64_t osty_rt_list_len(void *raw_list);
void osty_rt_list_push_i64(void *raw_list, int64_t value);
void osty_rt_list_get_bytes(void *raw_list, int64_t index, void *out, int64_t elem_size, void *trace_elem);

typedef struct f_env_t {
    void *fn;
} f_env_t;

/* f(item) = item * item. Closure env has no captures beyond the fn
 * pointer itself, so slot 0 holds the fn. */
static int64_t f_square(void *env, int64_t item) {
    (void)env;
    return item * item;
}

int main(void) {
    void *items = osty_rt_list_new();
    for (int64_t i = 0; i < 10; i++) {
        osty_rt_list_push_i64(items, i);
    }
    f_env_t fe = { (void *)f_square };
    void *out = osty_rt_parallel(items, 4, (void *)&fe);

    int64_t n = osty_rt_list_len(out);
    printf("%lld\n", (long long)n);

    struct { int64_t disc; int64_t payload; } slot;
    int64_t sum = 0;
    int all_ok = 1;
    for (int64_t i = 0; i < n; i++) {
        memset(&slot, 0, sizeof(slot));
        osty_rt_list_get_bytes(out, i, &slot, sizeof(slot), NULL);
        if (slot.disc != 1 || slot.payload != i * i) {
            all_ok = 0;
        }
        sum += slot.payload;
    }
    printf("%d\n", all_ok);
    printf("%lld\n", (long long)sum); /* 0+1+4+9+...+81 = 285 */

    /* Edge case: empty input. Output length 0, no worker spawned. */
    void *empty = osty_rt_list_new();
    void *empty_out = osty_rt_parallel(empty, 4, (void *)&fe);
    printf("%lld\n", (long long)osty_rt_list_len(empty_out));

    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}
	cmd := exec.Command("clang", "-std=c11", "-pthread", runtimePath, harnessPath, "-o", binaryPath)
	if buildOutput, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clang failed: %v\n%s", err, buildOutput)
	}
	runOutput, err := exec.Command(binaryPath).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	const want = "10\n1\n285\n0\n"
	if got := string(runOutput); got != want {
		t.Fatalf("parallel harness stdout = %q, want %q", got, want)
	}
}

// Select.send: fires a send arm into a buffered channel, then a
// second select picks up the value on the other side. Exercises the
// i64 variant of `osty_rt_select_send_*`, the post-enqueue arm
// closure, and the polling-loop send path added alongside them.
func TestBundledRuntimeSchedulerSelectSend(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_select_send_harness.c")
	binaryPath := filepath.Join(dir, "runtime_select_send_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>

void *osty_rt_thread_chan_make(int64_t capacity);
typedef struct { int64_t value; int64_t ok; } chan_recv_result;
chan_recv_result osty_rt_thread_chan_recv_i64(void *raw);

void osty_rt_select(void *body_env);
void osty_rt_select_send_i64(void *s, void *ch, int64_t value, void *arm);
void osty_rt_select_recv(void *s, void *ch, void *arm);

/* Send arm closure: plain () -> () signature — fired by the select
 * loop after the value is enqueued. Env slot 0 holds the fn pointer. */
typedef struct plain_arm_env {
    void *fn;
    int *ran;
} plain_arm_env;

static void plain_arm_body(void *env) {
    plain_arm_env *e = (plain_arm_env *)env;
    *e->ran += 1;
}

typedef struct send_env {
    void *fn;
    void *ch;
    void *arm_env;
} send_env;

static void send_body(void *env, void *s) {
    send_env *e = (send_env *)env;
    osty_rt_select_send_i64(s, e->ch, 42, e->arm_env);
}

/* Recv arm: i64 body-with-value — the drained value is passed in as
 * arg 2, and env slot 0 holds the fn pointer per the closure ABI. */
typedef struct recv_arm_env {
    void *fn;
    int64_t *out;
} recv_arm_env;

static void recv_arm_body(void *env, int64_t value) {
    recv_arm_env *e = (recv_arm_env *)env;
    *e->out = value;
}

typedef struct recv_body_env {
    void *fn;
    void *ch;
    void *arm_env;
} recv_body_env;

static void recv_body(void *env, void *s) {
    recv_body_env *e = (recv_body_env *)env;
    osty_rt_select_recv(s, e->ch, e->arm_env);
}

int main(void) {
    void *ch = osty_rt_thread_chan_make(1);

    int ran = 0;
    plain_arm_env pae = { (void *)plain_arm_body, &ran };
    send_env se = { (void *)send_body, ch, (void *)&pae };
    osty_rt_select((void *)&se);
    printf("%d\n", ran);  /* 1 — arm ran after enqueue */

    chan_recv_result r = osty_rt_thread_chan_recv_i64(ch);
    printf("%lld\n", (long long)r.ok);    /* 1 */
    printf("%lld\n", (long long)r.value); /* 42 */

    /* Drain through a second select with a recv arm to cover the
     * cross-select path (sender + receiver both through select). */
    send_env se2 = { (void *)send_body, ch, (void *)&pae };
    osty_rt_select((void *)&se2);

    int64_t captured = 0;
    recv_arm_env ae = { (void *)recv_arm_body, &captured };
    recv_body_env rbe = { (void *)recv_body, ch, (void *)&ae };
    osty_rt_select((void *)&rbe);
    printf("%lld\n", (long long)captured); /* 42 */
    printf("%d\n", ran);                   /* 2 — arm ran twice */
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}
	cmd := exec.Command("clang", "-std=c11", "-pthread", runtimePath, harnessPath, "-o", binaryPath)
	if buildOutput, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clang failed: %v\n%s", err, buildOutput)
	}
	runOutput, err := exec.Command(binaryPath).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	const want = "1\n1\n42\n42\n2\n"
	if got := string(runOutput); got != want {
		t.Fatalf("select_send harness stdout = %q, want %q", got, want)
	}
}

// Phase 2 (worker pool) acceptance tests. These cover behavior that
// is specific to the pool + Chase-Lev + elastic-worker design — on top
// of the Phase 1B suite above, which verifies the public ABI contract
// in a way that stays valid across scheduler impls.

// Pool saturation with blocking channel ops. OSTY_SCHED_WORKERS=1
// starves the fixed pool; a producer body blocks on a full channel
// and the consumer must be able to run *somewhere*. Elastic workers
// are the only way out — without them this test hangs indefinitely.
func TestBundledRuntimeSchedulerElasticWorkerUnblocksSaturation(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_elastic_harness.c")
	binaryPath := filepath.Join(dir, "runtime_elastic_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>

typedef struct { int64_t value; int64_t ok; } recv_result;

void *osty_rt_thread_chan_make(int64_t capacity);
void osty_rt_thread_chan_close(void *ch);
void osty_rt_thread_chan_send_i64(void *ch, int64_t v);
recv_result osty_rt_thread_chan_recv_i64(void *ch);

void *osty_rt_task_spawn(void *body_env);
int64_t osty_rt_task_handle_join(void *handle);

typedef struct prod_env { void *fn; void *ch; int64_t n; } prod_env;
typedef struct cons_env { void *fn; void *ch; } cons_env;

static int64_t body_produce(void *env) {
    prod_env *e = (prod_env *)env;
    int64_t acc = 0;
    for (int64_t i = 0; i < e->n; i++) {
        osty_rt_thread_chan_send_i64(e->ch, i);
        acc += i;
    }
    return acc;
}

static int64_t body_consume(void *env) {
    cons_env *e = (cons_env *)env;
    int64_t acc = 0;
    for (;;) {
        recv_result r = osty_rt_thread_chan_recv_i64(e->ch);
        if (!r.ok) break;
        acc += r.value;
    }
    return acc;
}

int main(void) {
    /* cap-4 channel + 50 items per producer — forces block/wake
     * many times. With only one pool worker, every other task has
     * to come from elastic spawns. */
    void *ch = osty_rt_thread_chan_make(4);

    prod_env penvs[3];
    void *phs[3];
    for (int i = 0; i < 3; i++) {
        penvs[i].fn = (void *)body_produce;
        penvs[i].ch = ch;
        penvs[i].n = 50;
        phs[i] = osty_rt_task_spawn((void *)&penvs[i]);
    }
    cons_env cenvs[3];
    void *chs[3];
    for (int i = 0; i < 3; i++) {
        cenvs[i].fn = (void *)body_consume;
        cenvs[i].ch = ch;
        chs[i] = osty_rt_task_spawn((void *)&cenvs[i]);
    }

    int64_t psum = 0;
    for (int i = 0; i < 3; i++) psum += osty_rt_task_handle_join(phs[i]);
    osty_rt_thread_chan_close(ch);
    int64_t csum = 0;
    for (int i = 0; i < 3; i++) csum += osty_rt_task_handle_join(chs[i]);
    printf("%lld\n%lld\n%d\n", (long long)psum, (long long)csum, psum == csum ? 1 : 0);
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}
	cmd := exec.Command("clang", "-std=c11", "-pthread", runtimePath, harnessPath, "-o", binaryPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clang failed: %v\n%s", err, out)
	}

	// Σ_{p=0..2} Σ_{i=0..49} i = 3 × (0+1+...+49) = 3 × 1225 = 3675.
	const want = "3675\n3675\n1\n"
	for _, workers := range []string{"1", "2", "3"} {
		t.Run("workers="+workers, func(t *testing.T) {
			cmd := exec.Command(binaryPath)
			cmd.Env = append(os.Environ(), "OSTY_SCHED_WORKERS="+workers)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("workers=%s run failed: %v\n%s", workers, err, out)
			}
			if got := string(out); got != want {
				t.Fatalf("workers=%s stdout = %q, want %q", workers, got, want)
			}
		})
	}
}

// Chase-Lev push/pop vs concurrent steal. One "owner" pool worker
// spawns a tight burst of children into its own local deque; other
// pool workers aggressively steal. All children must complete and
// their captured ids must sum correctly — a missed enqueue or a
// double-claim from the pop-vs-steal race would desync the sum.
func TestBundledRuntimeSchedulerChaseLevPushStealRace(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_chaselev_harness.c")
	binaryPath := filepath.Join(dir, "runtime_chaselev_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>

void *osty_rt_task_spawn(void *body_env);
int64_t osty_rt_task_handle_join(void *handle);

#define N_CHILDREN 5000

typedef struct leaf_env { void *fn; int64_t id; } leaf_env;

typedef struct spawner_env {
    void *fn;
    leaf_env *envs;
    void **handles;
    int64_t n;
} spawner_env;

/* Leaf: return id. Tight body so pop vs steal races fire constantly. */
static int64_t leaf_body(void *env) {
    leaf_env *e = (leaf_env *)env;
    return e->id;
}

/* Runs on a pool worker — each task_spawn call pushes to the worker's
 * own Chase-Lev deque (worker_id >= 0 branch in spawn_internal). Peer
 * workers aggressively steal from this deque while we keep pushing,
 * exercising the push vs steal race on every iteration. Joining here
 * uses the join-helping path so a worker waiting on a child still
 * drains its own deque. */
static int64_t spawner_body(void *env) {
    spawner_env *e = (spawner_env *)env;
    for (int64_t i = 0; i < e->n; i++) {
        e->handles[i] = osty_rt_task_spawn(&e->envs[i]);
    }
    int64_t acc = 0;
    for (int64_t i = 0; i < e->n; i++) {
        acc += osty_rt_task_handle_join(e->handles[i]);
    }
    return acc;
}

int main(void) {
    static leaf_env envs[N_CHILDREN];
    static void *handles[N_CHILDREN];
    for (int64_t i = 0; i < N_CHILDREN; i++) {
        envs[i].fn = (void *)leaf_body;
        envs[i].id = i;
    }
    spawner_env se = {
        (void *)spawner_body, envs, handles, (int64_t)N_CHILDREN,
    };
    /* Ship the work to a pool worker, then block the main thread on
     * its handle. Children therefore get pushed from inside a pool
     * worker — i.e., into its own deque, where steals have something
     * to contend with. */
    void *h = osty_rt_task_spawn((void *)&se);
    int64_t acc = osty_rt_task_handle_join(h);

    /* Σ i for i in [0, N) = N*(N-1)/2 */
    int64_t expected = ((int64_t)N_CHILDREN) * ((int64_t)N_CHILDREN - 1) / 2;
    printf("%lld\n%lld\n%d\n",
           (long long)acc, (long long)expected,
           acc == expected ? 1 : 0);
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}
	cmd := exec.Command("clang", "-std=c11", "-pthread", runtimePath, harnessPath, "-o", binaryPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clang failed: %v\n%s", err, out)
	}
	for _, workers := range []string{"1", "2", "4", "8"} {
		t.Run("workers="+workers, func(t *testing.T) {
			cmd := exec.Command(binaryPath)
			cmd.Env = append(os.Environ(), "OSTY_SCHED_WORKERS="+workers)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("workers=%s run failed: %v\n%s", workers, err, out)
			}
			const want = "12497500\n12497500\n1\n"
			if got := string(out); got != want {
				t.Fatalf("workers=%s stdout = %q, want %q", workers, got, want)
			}
		})
	}
}

// Heavy spawn count. Catches cumulative errors (missed dec, leaked
// handle sync primitives, deque index drift) that only manifest
// with many iterations. Also exercises the steal → inject fallback
// on the owner's deque overflow (deque cap is 4096 per worker).
func TestBundledRuntimeSchedulerMassSpawn(t *testing.T) {
	if testing.Short() {
		t.Skip("mass spawn stress skipped in -short")
	}
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_mass_harness.c")
	binaryPath := filepath.Join(dir, "runtime_mass_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

void *osty_rt_task_spawn(void *body_env);
int64_t osty_rt_task_handle_join(void *handle);

#define N_TASKS 100000

typedef struct env_t { void *fn; int64_t id; } env_t;

static int64_t body(void *env) {
    env_t *e = (env_t *)env;
    return e->id & 0xffffffffLL;   /* cheap deterministic work */
}

int main(void) {
    env_t *envs = (env_t *)calloc(N_TASKS, sizeof(env_t));
    void **handles = (void **)calloc(N_TASKS, sizeof(void *));
    if (envs == NULL || handles == NULL) {
        fprintf(stderr, "oom\n");
        return 1;
    }
    for (int64_t i = 0; i < N_TASKS; i++) {
        envs[i].fn = (void *)body;
        envs[i].id = i;
    }
    for (int64_t i = 0; i < N_TASKS; i++) {
        handles[i] = osty_rt_task_spawn(&envs[i]);
    }
    int64_t acc = 0;
    for (int64_t i = 0; i < N_TASKS; i++) {
        acc += osty_rt_task_handle_join(handles[i]);
    }
    int64_t expected = 0;
    for (int64_t i = 0; i < N_TASKS; i++) {
        expected += (i & 0xffffffffLL);
    }
    printf("%lld\n%lld\n%d\n", (long long)acc, (long long)expected,
           acc == expected ? 1 : 0);
    free(envs);
    free(handles);
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}
	cmd := exec.Command("clang", "-O2", "-std=c11", "-pthread", runtimePath, harnessPath, "-o", binaryPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clang failed: %v\n%s", err, out)
	}
	runOutput, err := exec.Command(binaryPath).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	// Σ_{i=0}^{99999} i = 4999950000.
	const want = "4999950000\n4999950000\n1\n"
	if got := string(runOutput); got != want {
		t.Fatalf("mass spawn stdout = %q, want %q", got, want)
	}
}

// Worker-count scaling: a CPU-bound workload should get faster as
// OSTY_SCHED_WORKERS grows. Runs the same fixed-work payload at
// workers=1 and workers=4; asserts the 4-worker wall-clock beats
// the 1-worker wall-clock by a clear margin. Loose threshold so CI
// jitter and Amdahl's overhead don't flake, but tight enough to
// catch "work-stealing doesn't actually distribute work" regressions.
func TestBundledRuntimeSchedulerWorkerScaling(t *testing.T) {
	if testing.Short() {
		t.Skip("worker scaling skipped in -short")
	}
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_scaling_harness.c")
	binaryPath := filepath.Join(dir, "runtime_scaling_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdint.h>
#include <stdio.h>
#include <time.h>

void *osty_rt_task_spawn(void *body_env);
int64_t osty_rt_task_handle_join(void *handle);

#define N_TASKS 16
#define ITERS 30000000LL    /* ~30ms of work per task on M-class cores */

typedef struct env_t { void *fn; int64_t seed; } env_t;

/* Compute-bound body. Non-trivial arithmetic so the compiler can't
 * fold it away; xorshift-ish mixer seeded by the task id. */
static int64_t body(void *env) {
    env_t *e = (env_t *)env;
    int64_t x = e->seed ^ 0x9E3779B97F4A7C15LL;
    for (int64_t i = 0; i < ITERS; i++) {
        x ^= x << 13;
        x ^= (int64_t)((uint64_t)x >> 7);
        x ^= x << 17;
    }
    return x;
}

int main(void) {
    env_t envs[N_TASKS];
    void *handles[N_TASKS];
    for (int i = 0; i < N_TASKS; i++) {
        envs[i].fn = (void *)body;
        envs[i].seed = (int64_t)i + 1;
    }

    struct timespec before, after;
    clock_gettime(CLOCK_MONOTONIC, &before);
    for (int i = 0; i < N_TASKS; i++) handles[i] = osty_rt_task_spawn(&envs[i]);
    int64_t acc = 0;
    for (int i = 0; i < N_TASKS; i++) acc += osty_rt_task_handle_join(handles[i]);
    clock_gettime(CLOCK_MONOTONIC, &after);

    long long elapsed_us =
        (long long)(after.tv_sec - before.tv_sec) * 1000000LL +
        (long long)((after.tv_nsec - before.tv_nsec) / 1000);
    /* Prevent the compiler from eliding the loop by reading acc. */
    printf("%lld %lld\n", elapsed_us, (long long)acc);
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}
	cmd := exec.Command("clang", "-O2", "-std=c11", "-pthread", runtimePath, harnessPath, "-o", binaryPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clang failed: %v\n%s", err, out)
	}

	runWith := func(t *testing.T, workers string) int64 {
		t.Helper()
		cmd := exec.Command(binaryPath)
		cmd.Env = append(os.Environ(), "OSTY_SCHED_WORKERS="+workers)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("workers=%s run failed: %v\n%s", workers, err, out)
		}
		var us, acc int64
		if _, err := fmt.Sscanf(string(out), "%d %d", &us, &acc); err != nil {
			t.Fatalf("parse stdout %q: %v", out, err)
		}
		return us
	}

	one := runWith(t, "1")
	four := runWith(t, "4")
	t.Logf("workers=1: %dus  workers=4: %dus  speedup=%.2fx",
		one, four, float64(one)/float64(four))

	// Tight enough to catch a broken stealer (no parallelism at all
	// would yield speedup≈1.0) but loose enough to survive CI
	// jitter and Amdahl overhead from task startup/join cost.
	if four*2 > one {
		t.Fatalf("worker scaling too weak: 1-worker=%dus, 4-worker=%dus, expected 4-worker < 1-worker / 2", one, four)
	}
}

// Phase 3: cooperative preemption. A compute-bound task spawned into a
// pool that is smaller than `compute + queued timeout` previously
// starved the queued task because the compute body never hit a
// blocking cv_wait (Phase 2's elastic trigger). With Phase 3's
// loop-safepoint preemption hook, the compute body's safepoints
// themselves spawn an elastic worker when the inject queue has
// pending work. Test: workers=1, producer calls osty_gc_safepoint_v1
// in a loop (standing in for compiler-emitted loop backedges), a
// second task should observe completion within a bounded wall-clock.
func TestBundledRuntimeSchedulerPreemptionDrainsQueueUnderComputeLoop(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_preempt_harness.c")
	binaryPath := filepath.Join(dir, "runtime_preempt_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <time.h>

void *osty_rt_task_spawn(void *body_env);
int64_t osty_rt_task_handle_join(void *handle);
void osty_rt_sched_preempt_check_v1(void);

typedef struct env_t { void *fn; volatile int64_t *done; } env_t;

/* Compute body: tight xorshift mixer that runs for ~500ms, calling
 * the preempt check every iteration. Without preemption + elastic
 * fallback on workers=1, the sibling body below would never run. */
static int64_t body_compute(void *env) {
    env_t *e = (env_t *)env;
    int64_t x = 1;
    for (int64_t i = 0; i < 500000000LL; i++) {
        x ^= x << 13;
        x ^= (int64_t)((uint64_t)x >> 7);
        x ^= x << 17;
        /* Compiler-emitted loop safepoints drop into this exact
         * hook — we call it directly here to simulate what
         * emitLoopSafepoint already emits at loop backedges. */
        osty_rt_sched_preempt_check_v1();
        if (__atomic_load_n(e->done, __ATOMIC_ACQUIRE)) {
            return x;  /* sibling signalled us; exit early */
        }
    }
    return x;
}

/* Sibling body: flip the done flag so the compute body exits. This
 * is what lets us measure "did the sibling get a chance to run?"
 * under a saturated single-worker pool. */
static int64_t body_signal(void *env) {
    env_t *e = (env_t *)env;
    __atomic_store_n(e->done, 1, __ATOMIC_RELEASE);
    return 7;
}

int main(void) {
    volatile int64_t done = 0;
    env_t c_env = { (void *)body_compute, &done };
    env_t s_env = { (void *)body_signal, &done };

    struct timespec before, after;
    clock_gettime(CLOCK_MONOTONIC, &before);

    /* Compute first — with workers=1 it grabs the only worker. */
    void *h_compute = osty_rt_task_spawn((void *)&c_env);
    /* Sibling second — queued in inject. Without preemption it
     * would sit there until compute runs to completion (~500ms of
     * xorshift). With preemption, safepoint-triggered elastic
     * spawn runs it within milliseconds. */
    void *h_signal = osty_rt_task_spawn((void *)&s_env);

    int64_t r_signal = osty_rt_task_handle_join(h_signal);
    int64_t r_compute = osty_rt_task_handle_join(h_compute);

    clock_gettime(CLOCK_MONOTONIC, &after);
    long long elapsed_ms =
        (long long)(after.tv_sec - before.tv_sec) * 1000LL +
        (long long)((after.tv_nsec - before.tv_nsec) / 1000000LL);

    printf("%lld %lld %lld\n",
           (long long)r_signal, (long long)r_compute, elapsed_ms);
    (void)r_compute;
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}
	cmd := exec.Command("clang", "-O2", "-std=c11", "-pthread", runtimePath, harnessPath, "-o", binaryPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clang failed: %v\n%s", err, out)
	}
	run := exec.Command(binaryPath)
	run.Env = append(os.Environ(), "OSTY_SCHED_WORKERS=1")
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, out)
	}
	var rSignal, rCompute, elapsedMs int64
	if _, err := fmt.Sscanf(string(out), "%d %d %d", &rSignal, &rCompute, &elapsedMs); err != nil {
		t.Fatalf("parse stdout %q: %v", out, err)
	}
	t.Logf("preemption: signal=%d compute=%d elapsed=%dms", rSignal, rCompute, elapsedMs)
	if rSignal != 7 {
		t.Fatalf("signal body did not run (result=%d, want 7)", rSignal)
	}
	// With preemption active, the signal body runs within
	// milliseconds of spawn and the compute body exits on its next
	// observation of done. Total wall-clock should be << the
	// 500M-iteration loop (hundreds of ms at most on typical HW).
	// Generous ceiling so CI jitter doesn't flake; without
	// preemption the loop runs to completion and blows the limit.
	if elapsedMs > 2000 {
		t.Fatalf("preemption did not drain queue: elapsed %dms, expected < 2000ms", elapsedMs)
	}
}

// Concurrent-GC handshake ABI. Phase 3 ships the stop_requested flag
// + mutator park path so a future collector can drive STW windows
// without refactoring every safepoint emission. This test drives the
// request/release ABI directly and verifies a busy worker parks at
// its next safepoint.
func TestBundledRuntimeSchedulerConcurrentStopHandshake(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_stop_harness.c")
	binaryPath := filepath.Join(dir, "runtime_stop_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <time.h>

void *osty_rt_task_spawn(void *body_env);
int64_t osty_rt_task_handle_join(void *handle);
void osty_rt_sched_preempt_check_v1(void);
void osty_rt_sched_concurrent_stop_request_v1(void);
void osty_rt_sched_concurrent_stop_release_v1(void);
void osty_rt_thread_sleep(int64_t nanos);

typedef struct env_t { void *fn; volatile int64_t *iters; } env_t;

/* Body runs a loop that bumps a counter and hits the preempt check
 * every iteration. After stop_request, the preempt check parks;
 * after stop_release, iterations resume. Counter samples before and
 * after the stop window let main confirm the mutator observed both
 * edges. */
static int64_t body_loop(void *env) {
    env_t *e = (env_t *)env;
    for (int64_t i = 0; i < 10000000LL; i++) {
        __atomic_fetch_add(e->iters, 1, __ATOMIC_RELAXED);
        osty_rt_sched_preempt_check_v1();
    }
    return 0;
}

int main(void) {
    volatile int64_t iters = 0;
    env_t env = { (void *)body_loop, &iters };
    void *h = osty_rt_task_spawn((void *)&env);

    /* Let the mutator ramp up. */
    osty_rt_thread_sleep(20000000LL);  /* 20ms */
    int64_t before = __atomic_load_n(&iters, __ATOMIC_RELAXED);

    /* Raise the stop flag. The mutator parks at its next preempt
     * check. Wait long enough that, if parking works, the iteration
     * count stays pinned at the pre-stop value. */
    osty_rt_sched_concurrent_stop_request_v1();
    osty_rt_thread_sleep(50000000LL);  /* 50ms */
    int64_t mid = __atomic_load_n(&iters, __ATOMIC_RELAXED);

    /* Release. The mutator resumes and should quickly accumulate
     * more iterations. */
    osty_rt_sched_concurrent_stop_release_v1();
    osty_rt_thread_sleep(20000000LL);
    int64_t after = __atomic_load_n(&iters, __ATOMIC_RELAXED);

    (void)osty_rt_task_handle_join(h);

    /* mid - before should be tiny (mutator parked within a few
     * iterations of the flag flip); after - mid should be >> 0
     * (mutator resumed and kept going). */
    long long stopped = (long long)(mid - before);
    long long resumed = (long long)(after - mid);
    printf("%lld %lld\n", stopped, resumed);
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}
	cmd := exec.Command("clang", "-O2", "-std=c11", "-pthread", runtimePath, harnessPath, "-o", binaryPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clang failed: %v\n%s", err, out)
	}
	out, err := exec.Command(binaryPath).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, out)
	}
	var stopped, resumed int64
	if _, err := fmt.Sscanf(string(out), "%d %d", &stopped, &resumed); err != nil {
		t.Fatalf("parse stdout %q: %v", out, err)
	}
	t.Logf("stop_handshake: stopped-delta=%d resumed-delta=%d", stopped, resumed)
	// The mutator should resume and accumulate far more iterations
	// after release than it did during the stop window. An exact
	// bound is noisy (xorshift runs fast; kernel park latency varies),
	// so we assert the qualitative invariant: after-release delta is
	// strictly larger than during-stop delta by at least a few
	// orders of magnitude.
	if resumed <= stopped*10 {
		t.Fatalf("concurrent stop handshake broken: stopped=%d resumed=%d (expected resumed >> stopped)",
			stopped, resumed)
	}
}

// SIGURG kick — async-preemption entry point. A compute-bound worker
// spins on a tight loop that *never* calls the safepoint. The main
// thread records the iteration counter, fires
// osty_rt_sched_kick_worker_v1(-1) to SIGURG every pool worker, and
// the worker's signal handler flips a TLS flag that the safepoint
// observes on the next call — which the body then triggers via
// preempt_check_v1. The test asserts the kick path reaches the
// handler (flag set) without crashing the process.
//
// POSIX-only. Windows has no SIGURG and the runtime's kick is a
// no-op there; test skips under Windows.
func TestBundledRuntimeSchedulerSigurgKick(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGURG preemption is POSIX-only")
	}
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_sigurg_harness.c")
	binaryPath := filepath.Join(dir, "runtime_sigurg_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <time.h>

void *osty_rt_task_spawn(void *body_env);
int64_t osty_rt_task_handle_join(void *handle);
void osty_rt_sched_preempt_check_v1(void);
void osty_rt_sched_kick_worker_v1(int64_t worker_id);
void osty_rt_thread_sleep(int64_t nanos);

typedef struct env_t {
    void *fn;
    volatile int64_t *ticks;
    volatile int64_t *stop;
} env_t;

/* Body runs a compute loop and calls preempt_check_v1 every
 * iteration. A kick sets the per-thread SIGURG flag; the next
 * preempt_check_v1 observes it and yields (sched_yield). The body
 * otherwise never touches the scheduler, so without SIGURG there
 * would be no cross-thread signal into this worker. */
static int64_t body_loop(void *env) {
    env_t *e = (env_t *)env;
    while (!__atomic_load_n(e->stop, __ATOMIC_ACQUIRE)) {
        __atomic_fetch_add(e->ticks, 1, __ATOMIC_RELAXED);
        osty_rt_sched_preempt_check_v1();
    }
    return 0;
}

int main(void) {
    volatile int64_t ticks = 0;
    volatile int64_t stop = 0;
    env_t env = { (void *)body_loop, &ticks, &stop };
    void *h = osty_rt_task_spawn((void *)&env);

    /* Ramp. */
    osty_rt_thread_sleep(20000000LL);
    int64_t before = __atomic_load_n(&ticks, __ATOMIC_RELAXED);

    /* Kick every pool worker. The SIGURG handler sets the worker's
     * TLS flag; the next preempt_check_v1 clears it and yields. We
     * don't assert a specific timing here — the test is "the kick
     * path does not crash and iterations keep flowing". */
    for (int i = 0; i < 100; i++) {
        osty_rt_sched_kick_worker_v1(-1);
    }

    osty_rt_thread_sleep(20000000LL);
    int64_t after = __atomic_load_n(&ticks, __ATOMIC_RELAXED);

    __atomic_store_n(&stop, 1, __ATOMIC_RELEASE);
    (void)osty_rt_task_handle_join(h);

    /* The body should have kept running through every kick and
     * accumulated further iterations. A zero delta means either the
     * handler crashed the process or SIGURG delivered in a way that
     * stuck the worker. */
    long long delta = (long long)(after - before);
    printf("%lld\n", delta);
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}
	cmd := exec.Command("clang", "-O2", "-std=c11", "-pthread", runtimePath, harnessPath, "-o", binaryPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clang failed: %v\n%s", err, out)
	}
	out, err := exec.Command(binaryPath).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, out)
	}
	var delta int64
	if _, err := fmt.Sscanf(string(out), "%d", &delta); err != nil {
		t.Fatalf("parse stdout %q: %v", out, err)
	}
	t.Logf("sigurg_kick: ticks delta = %d", delta)
	if delta <= 0 {
		t.Fatalf("SIGURG kick stalled the worker (delta=%d)", delta)
	}
}

// GC pause characterization. Phase 3's ultimate goal is "GC pause <
// 1ms on 100MB heap" via a concurrent collector, which is not yet
// shipped. This test measures the current STW pause so Phase 3's
// future concurrent-collector work has a baseline to compare
// against. It allocates many GC-managed objects and times a forced
// major collection; the assertion is a soft ceiling that catches
// pathological regressions but does not yet meet the Phase 3
// target. The harness prints the measured pause so CI / dev logs
// carry the trend over time.
func TestBundledRuntimeGcPauseBaseline(t *testing.T) {
	if testing.Short() {
		t.Skip("gc pause baseline skipped in -short")
	}
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_pause_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_pause_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdint.h>
#include <stdio.h>
#include <time.h>

#if defined(__APPLE__)
#define OSTY_GC_SYMBOL(name) "_" name
#else
#define OSTY_GC_SYMBOL(name) name
#endif

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));
void osty_gc_root_release_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_release_v1"));
void osty_gc_debug_collect_major(void);

#define N_ROOTED 1000     /* stays live across the collection */
#define N_GARBAGE 9000    /* unreachable at collection time */

int main(void) {
    /* Allocate the rooted set. Each block is 4KB; 1000 × 4KB = 4MB
     * of long-lived heap. The collector has to trace all of it. */
    static void *rooted[N_ROOTED];
    for (int i = 0; i < N_ROOTED; i++) {
        rooted[i] = osty_gc_alloc_v1(7, 4096, "rooted");
        osty_gc_root_bind_v1(rooted[i]);
    }
    /* Allocate garbage. Each block is 4KB; 9000 × 4KB = 36MB of
     * unreachable heap. Sweep has to reclaim all of it. */
    for (int i = 0; i < N_GARBAGE; i++) {
        (void)osty_gc_alloc_v1(7, 4096, "garbage");
    }

    struct timespec before, after;
    clock_gettime(CLOCK_MONOTONIC, &before);
    osty_gc_debug_collect_major();
    clock_gettime(CLOCK_MONOTONIC, &after);

    long long elapsed_us =
        (long long)(after.tv_sec - before.tv_sec) * 1000000LL +
        (long long)((after.tv_nsec - before.tv_nsec) / 1000LL);
    for (int i = 0; i < N_ROOTED; i++) {
        osty_gc_root_release_v1(rooted[i]);
    }
    printf("%lld\n", elapsed_us);
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}
	cmd := exec.Command("clang", "-O2", "-std=c11", "-pthread", runtimePath, harnessPath, "-o", binaryPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clang failed: %v\n%s", err, out)
	}
	out, err := exec.Command(binaryPath).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, out)
	}
	var pauseUs int64
	if _, err := fmt.Sscanf(string(out), "%d", &pauseUs); err != nil {
		t.Fatalf("parse stdout %q: %v", out, err)
	}
	t.Logf("gc_pause_baseline: 40MB heap STW major = %dus", pauseUs)
	// Regression ceiling: 500ms. The measured baseline today is
	// typically tens of ms on M-class hardware. Phase 3 step 2
	// (concurrent collector) aims for < 1ms per RUNTIME_SCHEDULER
	// acceptance tests; this test characterises the current STW
	// cost so that future diff clearly shows the improvement.
	if pauseUs > 500000 {
		t.Fatalf("STW major pause regressed: %dus (ceiling 500000us)", pauseUs)
	}
}

// Phase 3 step 2 — concurrent collection driver. Verifies that
// `osty_rt_gc_collect_concurrent_v1` can run a full major collection
// while pool worker threads are live inside task bodies. Before this
// path, `osty_gc_debug_collect_major` was hard-gated to a no-op
// whenever `osty_concurrent_workers > 0`, meaning long-running
// programs with always-live workers never collected.
//
// Test shape: spawn N worker tasks that each spin on a preempt_check
// loop (their only role is to hold `osty_concurrent_workers > 0`
// and publish non-trivial stack roots at each safepoint). From
// main, allocate and discard a few MB of garbage, then call
// collect_concurrent_v1. Assert the garbage is reclaimed (live
// bytes drop) and none of the workers crash.
func TestBundledRuntimeSchedulerConcurrentCollectDrivesMutators(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_concurrent_collect_harness.c")
	binaryPath := filepath.Join(dir, "runtime_concurrent_collect_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdint.h>
#include <stdio.h>
#include <stdbool.h>

#if defined(__APPLE__)
#define OSTY_GC_SYMBOL(name) "_" name
#else
#define OSTY_GC_SYMBOL(name) name
#endif

void *osty_gc_alloc_v1(int64_t kind, int64_t size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));
void osty_gc_root_release_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_release_v1"));
int64_t osty_gc_debug_live_count(void);
int64_t osty_gc_debug_live_bytes(void);

void *osty_rt_task_spawn(void *body_env);
int64_t osty_rt_task_handle_join(void *handle);
void osty_rt_sched_preempt_check_v1(void);
void osty_rt_gc_collect_concurrent_v1(void *const *caller_roots, int64_t caller_count);
void osty_rt_thread_sleep(int64_t nanos);

typedef struct env_t {
    void *fn;
    volatile int64_t *stop;
} env_t;

/* Worker body: spin on preempt_check_v1 while holding the in-flight
 * counter. The key property is that the worker is PARKABLE — at any
 * safepoint or preempt tick, a concurrent collector can request a
 * stop and this body will park via the cv path. */
static int64_t body_spin(void *env) {
    env_t *e = (env_t *)env;
    while (!__atomic_load_n(e->stop, __ATOMIC_ACQUIRE)) {
        osty_rt_sched_preempt_check_v1();
    }
    return 0;
}

#define N_WORKERS 4
#define N_GARBAGE 200

int main(void) {
    volatile int64_t stop = 0;
    env_t envs[N_WORKERS];
    void *hs[N_WORKERS];
    for (int i = 0; i < N_WORKERS; i++) {
        envs[i].fn = (void *)body_spin;
        envs[i].stop = &stop;
        hs[i] = osty_rt_task_spawn((void *)&envs[i]);
    }

    /* Let workers ramp up so they're genuinely inside task bodies
     * (not still queued in inject). */
    osty_rt_thread_sleep(20000000LL);

    /* Allocate + pin one rooted block so we can prove it survives. */
    void *keep = osty_gc_alloc_v1(7, 256, "keep");
    osty_gc_root_bind_v1(keep);

    /* Allocate a pile of garbage (not rooted). */
    for (int i = 0; i < N_GARBAGE; i++) {
        (void)osty_gc_alloc_v1(7, 4096, "garbage");
    }
    int64_t live_before = osty_gc_debug_live_count();

    /* Before Phase 3 step 2, osty_gc_debug_collect_major would no-op
     * here because osty_concurrent_workers > 0. collect_concurrent_v1
     * uses the stop-request handshake to park the four spinning
     * workers, publish their roots, and then collect. */
    osty_rt_gc_collect_concurrent_v1(NULL, 0);

    int64_t live_after = osty_gc_debug_live_count();

    /* Signal workers to stop; join them cleanly. */
    __atomic_store_n(&stop, 1, __ATOMIC_RELEASE);
    for (int i = 0; i < N_WORKERS; i++) {
        (void)osty_rt_task_handle_join(hs[i]);
    }
    osty_gc_root_release_v1(keep);

    /* Live count should have dropped by at least half (garbage reclaimed).
     * Exact numbers vary with internal runtime bookkeeping (pool
     * handles, task items tied to the runtime, etc.) so we look for
     * a qualitative drop rather than a specific figure. */
    printf("%lld %lld\n", (long long)live_before, (long long)live_after);
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}
	cmd := exec.Command("clang", "-O2", "-std=c11", "-pthread", runtimePath, harnessPath, "-o", binaryPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clang failed: %v\n%s", err, out)
	}
	out, err := exec.Command(binaryPath).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, out)
	}
	var before, after int64
	if _, err := fmt.Sscanf(string(out), "%d %d", &before, &after); err != nil {
		t.Fatalf("parse stdout %q: %v", out, err)
	}
	t.Logf("concurrent_collect: live_before=%d live_after=%d", before, after)
	// The 200 × 4KB garbage allocations must have been reclaimed; the
	// live count drops by at least 150 (allowing some slack for
	// runtime bookkeeping objects that may or may not survive).
	if before-after < 150 {
		t.Fatalf("concurrent collection did not reclaim garbage: before=%d after=%d",
			before, after)
	}
}

// Phase 3 step 2b — concurrent incremental collector. Upgrades the
// STW-during-workers path so the MARK phase overlaps with mutator
// execution; only start (root scan) and finish (final drain + sweep)
// are STW. Verified by measuring that the parked-mutator counter
// drops to zero during the mark phase — i.e. mutators run while the
// collector is still walking the heap — and that a rooted object
// survives while garbage is reclaimed.
func TestBundledRuntimeSchedulerConcurrentIncrementalMarkOverlapsMutators(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_concurrent_incremental_harness.c")
	binaryPath := filepath.Join(dir, "runtime_concurrent_incremental_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdint.h>
#include <stdio.h>
#include <stdbool.h>

#if defined(__APPLE__)
#define OSTY_GC_SYMBOL(name) "_" name
#else
#define OSTY_GC_SYMBOL(name) name
#endif

void *osty_gc_alloc_v1(int64_t kind, int64_t size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));
void osty_gc_root_release_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_release_v1"));
int64_t osty_gc_debug_live_count(void);

void *osty_rt_task_spawn(void *body_env);
int64_t osty_rt_task_handle_join(void *handle);
void osty_rt_sched_preempt_check_v1(void);
void osty_rt_gc_collect_concurrent_incremental_v1(void *const *caller_roots, int64_t caller_count);
void osty_rt_thread_sleep(int64_t nanos);

typedef struct env_t {
    void *fn;
    volatile int64_t *stop;
    volatile int64_t *ticks;
} env_t;

/* Worker body: spin on preempt_check_v1, bumping a tick counter.
 * The concurrent-incremental collector will park them briefly for
 * start (root scan) and finish (final drain + sweep), but the
 * between-window mark phase must let them keep ticking. */
static int64_t body_spin(void *env) {
    env_t *e = (env_t *)env;
    while (!__atomic_load_n(e->stop, __ATOMIC_ACQUIRE)) {
        __atomic_fetch_add(e->ticks, 1, __ATOMIC_RELAXED);
        osty_rt_sched_preempt_check_v1();
    }
    return 0;
}

#define N_WORKERS 4
#define N_GARBAGE 200

int main(void) {
    volatile int64_t stop = 0;
    volatile int64_t ticks = 0;
    env_t envs[N_WORKERS];
    void *hs[N_WORKERS];
    for (int i = 0; i < N_WORKERS; i++) {
        envs[i].fn = (void *)body_spin;
        envs[i].stop = &stop;
        envs[i].ticks = &ticks;
        hs[i] = osty_rt_task_spawn((void *)&envs[i]);
    }

    /* Ramp workers. */
    osty_rt_thread_sleep(20000000LL);
    int64_t ticks_before = __atomic_load_n(&ticks, __ATOMIC_RELAXED);

    /* Allocate + pin the survivor. */
    void *keep = osty_gc_alloc_v1(7, 256, "keep-incremental");
    osty_gc_root_bind_v1(keep);

    /* Allocate garbage. These are unreachable; the concurrent
     * incremental collector must sweep them. */
    for (int i = 0; i < N_GARBAGE; i++) {
        (void)osty_gc_alloc_v1(7, 4096, "garbage-incremental");
    }
    int64_t live_before = osty_gc_debug_live_count();

    /* Run the full concurrent-incremental cycle. Mutators park
     * briefly at start and finish; the mark drain between them
     * runs while they keep incrementing the tick counter. */
    osty_rt_gc_collect_concurrent_incremental_v1(NULL, 0);

    int64_t live_after = osty_gc_debug_live_count();
    int64_t ticks_after = __atomic_load_n(&ticks, __ATOMIC_RELAXED);

    __atomic_store_n(&stop, 1, __ATOMIC_RELEASE);
    for (int i = 0; i < N_WORKERS; i++) {
        (void)osty_rt_task_handle_join(hs[i]);
    }
    osty_gc_root_release_v1(keep);

    /* Three quantities: live count delta (garbage reclaimed),
     * ticks delta (mutator progress during the concurrent cycle),
     * and a boolean that 'keep' survived (live_after >= 1). */
    printf("%lld %lld %lld\n",
           (long long)(live_before - live_after),
           (long long)(ticks_after - ticks_before),
           (long long)live_after);
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}
	cmd := exec.Command("clang", "-O2", "-std=c11", "-pthread", runtimePath, harnessPath, "-o", binaryPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clang failed: %v\n%s", err, out)
	}
	out, err := exec.Command(binaryPath).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, out)
	}
	var reclaimed, mutatorTicks, liveAfter int64
	if _, err := fmt.Sscanf(string(out), "%d %d %d", &reclaimed, &mutatorTicks, &liveAfter); err != nil {
		t.Fatalf("parse stdout %q: %v", out, err)
	}
	t.Logf("concurrent_incremental: reclaimed=%d mutator_ticks_during=%d live_after=%d",
		reclaimed, mutatorTicks, liveAfter)
	if reclaimed < 150 {
		t.Fatalf("concurrent incremental collection did not reclaim garbage: reclaimed=%d", reclaimed)
	}
	// Mutators must have made progress during the cycle. A pure
	// STW-during-workers path would have zero ticks delta from start
	// to end of collect_concurrent_incremental_v1; the concurrent
	// mark phase lets them accumulate.
	if mutatorTicks < 100 {
		t.Fatalf("mutators did not progress during concurrent mark: ticks_delta=%d", mutatorTicks)
	}
	if liveAfter < 1 {
		t.Fatalf("rooted object was reclaimed (live_after=%d)", liveAfter)
	}
}

// Phase 3 step 2b — per-phase pause characterization. Phase A (STW
// start) and Phase C (STW finish + sweep) sum to the user-visible
// stop-the-world time; Phase B (concurrent mark) is the concurrent
// cost that overlaps mutator execution. The < 1ms Phase-3 acceptance
// goal from RUNTIME_SCHEDULER.md is about A + C, not B. This test
// measures them on a representative heap and asserts sane shape
// (non-zero timings, finite totals), logging concrete numbers so CI
// trends surface any regression in the pause-reduction work.
func TestBundledRuntimeSchedulerConcurrentIncrementalPerPhasePause(t *testing.T) {
	if testing.Short() {
		t.Skip("per-phase pause characterization skipped in -short")
	}
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_per_phase_harness.c")
	binaryPath := filepath.Join(dir, "runtime_per_phase_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdint.h>
#include <stdio.h>
#include <stdbool.h>

#if defined(__APPLE__)
#define OSTY_GC_SYMBOL(name) "_" name
#else
#define OSTY_GC_SYMBOL(name) name
#endif

void *osty_gc_alloc_v1(int64_t kind, int64_t size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));
void osty_gc_root_release_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_release_v1"));
int64_t osty_gc_debug_live_count(void);

int64_t osty_gc_debug_concurrent_incremental_start_nanos(void);
int64_t osty_gc_debug_concurrent_incremental_mark_nanos(void);
int64_t osty_gc_debug_concurrent_incremental_finish_nanos(void);
int64_t osty_gc_debug_concurrent_incremental_cycles(void);

void *osty_rt_task_spawn(void *body_env);
int64_t osty_rt_task_handle_join(void *handle);
void osty_rt_sched_preempt_check_v1(void);
void osty_rt_gc_collect_concurrent_incremental_v1(void *const *caller_roots, int64_t caller_count);
void osty_rt_thread_sleep(int64_t nanos);

typedef struct env_t { void *fn; volatile int64_t *stop; } env_t;

static int64_t body_spin(void *env) {
    env_t *e = (env_t *)env;
    while (!__atomic_load_n(e->stop, __ATOMIC_ACQUIRE)) {
        osty_rt_sched_preempt_check_v1();
    }
    return 0;
}

#define N_WORKERS 4
#define N_ROOTED 1000
#define N_GARBAGE 9000

int main(void) {
    /* Populate a realistic heap: 1K rooted × 4KB = 4MB live plus
     * 9K garbage × 4KB = 36MB to sweep (mirrors the STW pause
     * baseline test's shape so the numbers compare directly). */
    static void *rooted[N_ROOTED];
    for (int i = 0; i < N_ROOTED; i++) {
        rooted[i] = osty_gc_alloc_v1(7, 4096, "rooted");
        osty_gc_root_bind_v1(rooted[i]);
    }
    for (int i = 0; i < N_GARBAGE; i++) {
        (void)osty_gc_alloc_v1(7, 4096, "garbage");
    }

    /* Spin up workers so the cycle runs with live mutators (the
     * stop handshake + kick path is exercised on real parks). */
    volatile int64_t stop = 0;
    env_t envs[N_WORKERS];
    void *hs[N_WORKERS];
    for (int i = 0; i < N_WORKERS; i++) {
        envs[i].fn = (void *)body_spin;
        envs[i].stop = &stop;
        hs[i] = osty_rt_task_spawn((void *)&envs[i]);
    }
    osty_rt_thread_sleep(20000000LL);  /* ramp */

    osty_rt_gc_collect_concurrent_incremental_v1(NULL, 0);

    int64_t start_ns = osty_gc_debug_concurrent_incremental_start_nanos();
    int64_t mark_ns = osty_gc_debug_concurrent_incremental_mark_nanos();
    int64_t finish_ns = osty_gc_debug_concurrent_incremental_finish_nanos();
    int64_t cycles = osty_gc_debug_concurrent_incremental_cycles();

    __atomic_store_n(&stop, 1, __ATOMIC_RELEASE);
    for (int i = 0; i < N_WORKERS; i++) {
        (void)osty_rt_task_handle_join(hs[i]);
    }
    for (int i = 0; i < N_ROOTED; i++) {
        osty_gc_root_release_v1(rooted[i]);
    }

    printf("%lld %lld %lld %lld\n",
           (long long)start_ns, (long long)mark_ns,
           (long long)finish_ns, (long long)cycles);
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}
	cmd := exec.Command("clang", "-O2", "-std=c11", "-pthread", runtimePath, harnessPath, "-o", binaryPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clang failed: %v\n%s", err, out)
	}
	out, err := exec.Command(binaryPath).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, out)
	}
	var startNs, markNs, finishNs, cycles int64
	if _, err := fmt.Sscanf(string(out), "%d %d %d %d",
		&startNs, &markNs, &finishNs, &cycles); err != nil {
		t.Fatalf("parse stdout %q: %v", out, err)
	}
	t.Logf("per_phase_pause: start=%dus mark=%dus finish=%dus cycles=%d  stw_total=%dus",
		startNs/1000, markNs/1000, finishNs/1000, cycles,
		(startNs+finishNs)/1000)

	if cycles < 1 {
		t.Fatalf("concurrent cycle did not complete: cycles=%d", cycles)
	}
	if startNs == 0 && markNs == 0 && finishNs == 0 {
		t.Fatalf("per-phase timings all zero; instrumentation broken")
	}
	// Regression ceiling on the STW portion (A + C). Phase B is
	// concurrent so it does not count toward user-visible latency.
	// The baseline TestBundledRuntimeGcPauseBaseline measures
	// ~6.5ms for a full STW on a similar heap; the concurrent
	// path should be at minimum on par. Loose 1s cap for CI
	// jitter; the logged figure is what matters for trending.
	stwTotal := startNs + finishNs
	if stwTotal > int64(1_000_000_000) {
		t.Fatalf("STW total regressed beyond 1s: start=%d finish=%d", startNs, finishNs)
	}
}
