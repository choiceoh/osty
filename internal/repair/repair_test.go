package repair_test

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/repair"
)

func TestSourceRepairsCommonAIMistakes(t *testing.T) {
	src := []byte(`function main() {
    let n = 0X1F;
    let x = foo::bar();
    match n {
        1 => "one"
        _ => "other"
    }
    if ok {
        println("yes")
    }
    else {
        println("no")
    }
    xs.
        map(|x| x)
    opt?.
        value()
}
`)
	got := string(repair.Source(src).Source)
	want := `fn main() {
    let n = 0x1F
    let x = foo.bar()
    match n {
        1 -> "one"
        _ -> "other"
    }
    if ok {
        println("yes")
    } else {
        println("no")
    }
    xs
        .map(|x| x)
    opt
        ?.value()
}
`
	if got != want {
		t.Fatalf("repair mismatch:\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestSourceLeavesStringsCommentsAndTurbofishAlone(t *testing.T) {
	src := []byte(`fn main() {
    let s = "=> 0X1F foo::bar();"
    // => 0X1F foo::bar();
    let n = parse::<Int>("42")
}
`)
	res := repair.Source(src)
	got := string(res.Source)
	if got != string(src) {
		t.Fatalf("unexpected repair:\n%s", got)
	}
	if len(res.Changes) != 0 {
		t.Fatalf("expected no changes, got %d", len(res.Changes))
	}
}

func TestSourceRepairsFuncKeywordInDeclarationShape(t *testing.T) {
	src := []byte("pub func greet(name: String) { println(name) }\n")
	got := string(repair.Source(src).Source)
	if want := "pub fn greet(name: String) { println(name) }\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSourceIsIdempotent(t *testing.T) {
	src := []byte(`func main() {
    let n = 0B1010;
    if ok {
        println("ok")
    }
    else {
        println("no")
    }
}
`)
	once := repair.Source(src).Source
	twice := repair.Source(once).Source
	if string(once) != string(twice) {
		t.Fatalf("not idempotent:\nonce:\n%s\ntwice:\n%s", once, twice)
	}
	if strings.Contains(string(once), "0B") || strings.Contains(string(once), ";") {
		t.Fatalf("repair missed obvious syntax: %s", once)
	}
}

func TestSourceRepairsForeignControlFlowAndOperators(t *testing.T) {
	src := []byte(`public def main() {
    var keepGoing = True; const fallback = null
    while keepGoing and not done {
        if current is not nil {
            console.log(current);
        } elif fallback is None {
            console.log("fallback")
        }
    }
}
`)
	got := string(repair.Source(src).Source)
	want := `pub fn main() {
    let mut keepGoing = true
    let fallback = None
    for keepGoing && ! done {
        if current != None {
            println(current)
        } else if fallback == None {
            println("fallback")
        }
    }
}
`
	if got != want {
		t.Fatalf("repair mismatch:\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestSourceRepairsSwitchCaseAndShortVar(t *testing.T) {
	src := []byte(`fn main() {
    value := read()
    switch value {
        case null:
            println("one")
        default:
            println("other")
    }
}
`)
	got := string(repair.Source(src).Source)
	want := `fn main() {
    let value = read()
    match value {
        None ->
            println("one")
        _ ->
            println("other")
    }
}
`
	if got != want {
		t.Fatalf("repair mismatch:\nwant:\n%s\ngot:\n%s", want, got)
	}
}
