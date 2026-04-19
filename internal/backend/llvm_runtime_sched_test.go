package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Phase 1A sequential scheduler smoke test. Exercises the public
// `osty_rt_task_*` and `osty_rt_thread_*` surface the MIR backend
// lowers to. The channel/select symbols are not exercised here —
// Phase 1A implements them as abort stubs (see RUNTIME_SCHEDULER.md).
//
// The harness mimics what an Osty program would do after lowering:
// allocate a closure env where env[0] is the fn pointer, then hand
// the env to the scheduler runtime. Test body functions match the
// uniform closure ABI (`int64_t body(void *env [, void *group])`)
// described in §ABI contract of RUNTIME_SCHEDULER.md.
func TestBundledRuntimeSchedulerPhase1A(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}

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

/* taskGroup body: cancels the group and then spawns a child whose
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

    /* Test 5: thread.yield is a safe no-op. */
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

    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}
	cmd := exec.Command("clang", "-std=c11", runtimePath, harnessPath, "-o", binaryPath)
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
	//   ok        (yield no-op didn't crash)
	//   1         (sleep elapsed ≥ 1ms)
	const want = "42\n99\n7\n0\nok\n1\n"
	if got := string(runOutput); got != want {
		t.Fatalf("scheduler harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimeSchedulerChannelStubAborts documents that Phase 1A
// channel send/recv/make surfaces abort with a diagnostic rather than
// link-failing or silently no-op'ing. A program that reaches these
// calls in Phase 1A must fail loudly — Phase 1B replaces them.
func TestBundledRuntimeSchedulerChannelStubAborts(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_chan_stub_harness.c")
	binaryPath := filepath.Join(dir, "runtime_chan_stub_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdint.h>
#include <stdio.h>

void *osty_rt_thread_chan_make(int64_t capacity);

int main(void) {
    (void)osty_rt_thread_chan_make(4);
    printf("should not reach\n");
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}
	cmd := exec.Command("clang", "-std=c11", runtimePath, harnessPath, "-o", binaryPath)
	if buildOutput, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clang failed: %v\n%s", err, buildOutput)
	}
	// The stub calls abort(); the process should exit non-zero and not
	// print "should not reach".
	runOutput, err := exec.Command(binaryPath).CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit from chan stub, got success: %q", runOutput)
	}
	if got := string(runOutput); got == "should not reach\n" {
		t.Fatalf("channel stub silently succeeded; Phase 1A requires loud failure")
	}
}
