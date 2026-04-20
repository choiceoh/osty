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

// Select.send is still a deliberate abort — its signature needs a
// value-register the MIR path doesn't emit yet. Locks the "fail loud"
// contract for now.
func TestBundledRuntimeSchedulerSelectSendStubAborts(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_select_send_stub_harness.c")
	binaryPath := filepath.Join(dir, "runtime_select_send_stub_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdint.h>
#include <stdio.h>

void osty_rt_select_send(void *s, void *ch, void *arm);

int main(void) {
    osty_rt_select_send(NULL, NULL, NULL);
    printf("should not reach\n");
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
	if err == nil {
		t.Fatalf("expected non-zero exit from select.send stub, got success: %q", runOutput)
	}
	if got := string(runOutput); got == "should not reach\n" {
		t.Fatalf("select.send stub silently succeeded; not-yet-implemented path must fail loudly")
	}
}
