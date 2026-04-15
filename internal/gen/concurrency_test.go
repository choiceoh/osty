package gen_test

import (
	"strings"
	"testing"
)

// TestTaskGroupBasic — taskGroup waits for all spawned tasks and
// returns the closure's final value.
func TestTaskGroupBasic(t *testing.T) {
	src := `fn fetch(n: Int) -> Int { n * 100 }

fn main() {
    let total = taskGroup(|g| {
        let h1 = g.spawn(|| fetch(1))
        let h2 = g.spawn(|| fetch(2))
        let h3 = g.spawn(|| fetch(3))
        h1.join() + h2.join() + h3.join()
    })
    println("total = {total}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "total = 600" {
		t.Errorf("got %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestTaskGroupWaitsOnExit — a spawned task that outlives the
// closure body still runs to completion before taskGroup returns.
func TestTaskGroupWaitsOnExit(t *testing.T) {
	src := `fn main() {
    let ch = thread.chan::<Int>(1)
    taskGroup(|g| {
        g.spawn(|| {
            thread.sleep(20.ms)
            ch <- 42
        })
    })
    match ch.recv() {
        Some(v) -> println("got {v}"),
        None -> println("closed"),
    }
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "got 42" {
		t.Errorf("got %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestTaskGroupCancellation — g.cancel() signals g.isCancelled()
// across concurrent workers; cooperating workers exit early.
func TestTaskGroupCancellation(t *testing.T) {
	src := `fn worker(id: Int, g: TaskGroup) -> String {
    for i in 0..50 {
        if g.isCancelled() {
            return "w{id}@{i}"
        }
        thread.sleep(5.ms)
    }
    "w{id}done"
}

fn main() {
    let r = taskGroup(|g| {
        let h1 = g.spawn(|| worker(1, g))
        let h2 = g.spawn(|| worker(2, g))
        thread.sleep(20.ms)
        g.cancel()
        let s1 = h1.join()
        let s2 = h2.join()
        "{s1}|{s2}"
    })
    println(r)
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := strings.TrimSpace(runGo(t, goSrc))
	// Both workers should have exited via cancel, not completion.
	if strings.Contains(out, "done") {
		t.Errorf("workers shouldn't have finished: %q\n--- src ---\n%s", out, goSrc)
	}
	if !strings.Contains(out, "w1@") || !strings.Contains(out, "w2@") {
		t.Errorf("expected both workers to report cancel: %q", out)
	}
}

// TestParallelBasic — parallel runs each closure concurrently and
// returns them in source order.
func TestParallelBasic(t *testing.T) {
	src := `fn square(n: Int) -> Int { n * n }

fn main() {
    let rs = parallel(|| square(2), || square(3), || square(4))
    for r in rs {
        println(r)
    }
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "4\n9\n16\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}

// TestSelectRecvPath — thread.select routes to the ready channel.
func TestSelectRecvPath(t *testing.T) {
	src := `fn main() {
    let ch = thread.chan::<Int>(1)
    ch <- 7
    thread.select(|s| {
        s.recv(ch, |v| println("got {v}"))
        s.timeout(100.ms, || println("slow"))
    })
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "got 7" {
		t.Errorf("got %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestSelectDefaultPath — empty channel + default branch runs
// immediately without blocking.
func TestSelectDefaultPath(t *testing.T) {
	src := `fn main() {
    let ch = thread.chan::<Int>(1)
    thread.select(|s| {
        s.recv(ch, |v| println("got {v}"))
        s.default(|| println("empty"))
    })
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "empty" {
		t.Errorf("got %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestSelectTimeoutFires — empty channel + short timeout takes the
// timeout branch.
func TestSelectTimeoutFires(t *testing.T) {
	src := `fn main() {
    let ch = thread.chan::<Int>(1)
    thread.select(|s| {
        s.recv(ch, |v| println("got {v}"))
        s.timeout(20.ms, || println("timeout"))
    })
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "timeout" {
		t.Errorf("got %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestTaskGroupResultPropagation — the closure's Result<T, E> flows
// out of taskGroup. `?` inside the closure propagates as normal.
func TestTaskGroupResultPropagation(t *testing.T) {
	src := `fn work(s: String) -> Result<Int, String> {
    if s == "ok" { Ok(10) } else { Err("bad {s}") }
}

fn collect(a: String, b: String) -> Result<Int, String> {
    taskGroup(|g| {
        let h1 = g.spawn(|| work(a))
        let h2 = g.spawn(|| work(b))
        let x = h1.join()?
        let y = h2.join()?
        Ok(x + y)
    })
}

fn main() {
    match collect("ok", "ok") {
        Ok(n) -> println("sum = {n}"),
        Err(e) -> println("err: {e}"),
    }
    match collect("ok", "nope") {
        Ok(n) -> println("sum = {n}"),
        Err(e) -> println("err: {e}"),
    }
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "sum = 20\nerr: bad nope\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}
