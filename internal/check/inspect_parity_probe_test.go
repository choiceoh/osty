package check

import (
	"fmt"
	"strings"
	"testing"

	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/stdlib"
)

// TestInspectParityProbe dumps Go check.Inspect and self-host
// selfhost.InspectFromSource side-by-side so divergences can be
// eyeballed. Runs only under -v; no assertion.
func TestInspectParityProbe(t *testing.T) {
	if !testing.Verbose() {
		t.Skip("verbose only")
	}
	src := []byte(`fn sum(xs: List<Int>) -> Int {
    let mut acc = 0
    for x in xs {
        acc = acc + x
    }
    acc
}

fn first<T>(xs: List<T>) -> T? {
    if xs.isEmpty() { None } else { Some(xs[0]) }
}

struct Address { pub city: String }
struct User { pub address: Address }

fn run(user: User) {
    let n = sum([1, 2, 3])
    let head: Int? = first::<Int>([1, 2])
    let city = user.address.city
}
`)
	file, res := parseResolvedFile(t, src)
	reg := stdlib.LoadCached()
	chk := SelfhostFile(file, res, Opts{Source: src, Stdlib: reg, Primitives: reg.Primitives, ResultMethods: reg.ResultMethods})
	gorecs := Inspect(file, chk)
	sorecs := selfhost.InspectFromSource(src)

	var b strings.Builder
	b.WriteString("=== Go inspect ===\n")
	for _, r := range gorecs {
		ty := "-"
		if r.Type != nil {
			ty = r.Type.String()
		}
		hint := "-"
		if r.Hint != nil {
			hint = r.Hint.String()
		}
		fmt.Fprintf(&b, "%4d-%-4d %-16s rule=%-12s ty=%-24s hint=%-14s notes=%v\n",
			r.Pos.Offset, r.End.Offset, r.NodeKind, r.Rule, ty, hint, r.Notes)
	}
	b.WriteString("=== Self-host inspect ===\n")
	for _, r := range sorecs {
		ty := "-"
		if r.Type != nil {
			ty = r.Type.String()
		}
		fmt.Fprintf(&b, "%4d-%-4d %-16s rule=%-12s ty=%-24s hint=%-14s notes=%v\n",
			r.Start, r.End, r.NodeKind, r.Rule, ty, r.HintName, r.Notes)
	}
	t.Log("\n" + b.String())
}
