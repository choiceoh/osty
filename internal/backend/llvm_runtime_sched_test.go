package backend

import (
	"os"
	"os/exec"
	"path/filepath"
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
