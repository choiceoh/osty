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

/* spawn body: sleeps 2ms before returning, exposes real wall-clock
 * parallelism when run across N spawns. */
static int64_t body_sleep_then_capture(void *env) {
    two_slot_env *e = (two_slot_env *)env;
    osty_rt_thread_sleep(2000000LL); /* 2ms */
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

    /* Test 7: four 2ms-sleeping spawns joined. Wall-clock must be
     * meaningfully less than 4×2ms=8ms if real parallelism exists. We
     * accept up to 6ms as passing; tighter bounds are too flaky under
     * CI load. Each spawn returns its capture; joined sum = 1+2+3+4=10. */
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
    /* Report 1 iff wall-clock shows real parallelism (not strict 4x). */
    printf("%d\n", elapsed_ns < 6000000LL ? 1 : 0);

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

// Select + parallel + race + collectAll are still deliberate aborts —
// their registration surfaces need Osty-side builder work (see
// RUNTIME_SCHEDULER.md roadmap). This test locks the "fail fast"
// behaviour so programs reaching these surfaces don't silently
// misbehave.
func TestBundledRuntimeSchedulerSelectStubAborts(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_select_stub_harness.c")
	binaryPath := filepath.Join(dir, "runtime_select_stub_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdint.h>
#include <stdio.h>

void osty_rt_select(void *s);

int main(void) {
    osty_rt_select(NULL);
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
		t.Fatalf("expected non-zero exit from select stub, got success: %q", runOutput)
	}
	if got := string(runOutput); got == "should not reach\n" {
		t.Fatalf("select stub silently succeeded; not-yet-implemented path must fail loudly")
	}
}
