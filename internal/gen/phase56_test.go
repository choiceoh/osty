package gen_test

import "testing"

// TestFFIFmt — `use go "fmt"` emits a real import and call sites
// resolve to the imported package.
func TestFFIFmt(t *testing.T) {
	src := `use go "fmt" {
    fn Println(v: String)
}

fn main() {
    fmt.Println("via go fmt")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if out != "via go fmt\n" {
		t.Errorf("got %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestFFIAliased — `use go "strings" as s` emits an aliased import
// and calls through `s.Name` resolve to the package.
func TestFFIAliased(t *testing.T) {
	src := `use go "strings" as s {
    fn ToUpper(x: String) -> String
    fn Repeat(x: String, n: Int) -> String
}

fn main() {
    println(s.ToUpper("hello"))
    println(s.Repeat("ab", 3))
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "HELLO\nababab\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}

// TestChannelBuffered — a buffered channel round-trips a value via
// send + recv.
func TestChannelBuffered(t *testing.T) {
	src := `fn main() {
    let ch = thread.chan::<Int>(1)
    ch <- 42
    match ch.recv() {
        Some(v) -> println("got {v}"),
        None -> println("closed"),
    }
    ch.close()
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if out != "got 42\n" {
		t.Errorf("got %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestChannelClosed — recv on a closed channel returns None.
func TestChannelClosed(t *testing.T) {
	src := `fn main() {
    let ch = thread.chan::<Int>(1)
    ch <- 7
    ch.close()
    match ch.recv() {
        Some(v) -> println("first: {v}"),
        None -> println("empty"),
    }
    match ch.recv() {
        Some(v) -> println("second: {v}"),
        None -> println("closed"),
    }
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "first: 7\nclosed\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}

// TestSpawnReturn — spawn + join returns the closure's value.
func TestSpawnReturn(t *testing.T) {
	src := `fn compute(x: Int) -> Int { x * x }

fn main() {
    let h = spawn(|| compute(5))
    println("result = {h.join()}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if out != "result = 25\n" {
		t.Errorf("got %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestSpawnChannel — producer goroutine feeds a consumer via channel,
// for-range over a closed channel drains it cleanly.
func TestSpawnChannel(t *testing.T) {
	src := `fn producer(ch: Channel<Int>) {
    for i in 1..=5 {
        ch <- i
    }
    ch.close()
}

fn main() {
    let ch = thread.chan::<Int>(0)
    let h = spawn(|| producer(ch))
    let mut total = 0
    for x in ch {
        total = total + x
    }
    h.join()
    println("total = {total}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if out != "total = 15\n" {
		t.Errorf("got %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestCLIResultViaCheck — Ok/Err prelude variants make it past the
// checker without the old E0728. Sanity test that the full pipeline
// accepts a canonical Result usage.
func TestCLIResultViaCheck(t *testing.T) {
	src := `fn parse(s: String) -> Result<Int, String> {
    if s == "ok" { Ok(1) } else { Err("no") }
}

fn main() {
    match parse("ok") {
        Ok(n) -> println("n = {n}"),
        Err(e) -> println("e = {e}"),
    }
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if out != "n = 1\n" {
		t.Errorf("got %q\n--- src ---\n%s", out, goSrc)
	}
}
