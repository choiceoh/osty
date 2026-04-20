package llvmgen

import (
	"strings"
	"testing"
)

// The `recv.downcast::<T>()` AST recognizer is wired into emitCall
// ahead of the generic interface-method-call path. When the
// receiver's static type is `%osty.iface` and the target name
// resolves to an impl with an emitted vtable, the call lowers to a
// four-instruction sequence: extract the vtable, extract the data,
// compare against the target's `@osty.vtable.<impl>__<iface>`
// symbol, and `select` between the data pointer and null. The
// result is ptr-typed, matching the existing optional lowering
// (`T?` == ptr; non-null = Some, null = None).
//
// The self-hosted checker (`toolchain/elab.osty`) does not yet
// type-check `.downcast::<T>()` through to the backend — the
// full-pipeline `osty build` path therefore still rejects the call
// at the checker gate. This test bypasses the checker via
// `generateFromAST` to verify the backend-side lowering shape in
// isolation, so the plumbing is exercised under CI while the
// checker catches up (see CHANGELOG_v0.5.md for the pipeline state).
func TestInterfaceDowncastLowersToVtableCompare(t *testing.T) {
	file := parseLLVMGenFile(t, `interface Printable {
    fn show(self) -> String
}

struct Note {
    pub msg: String,

    pub fn show(self) -> String {
        self.msg
    }
}

fn probe(p: Printable) -> Note? {
    p.downcast::<Note>()
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/iface_downcast.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"%osty.iface = type { ptr, ptr }",
		"@osty.vtable.Note__Printable",
		"extractvalue %osty.iface",
		"icmp eq ptr",
		"select i1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// Reject path: calling `.downcast::<T>()` when T does not implement
// any registered interface should surface a stable-ish error through
// the emitCall dispatcher, not silently fall through to the generic
// "no field downcast" path.
func TestInterfaceDowncastTargetMustImplementInterface(t *testing.T) {
	file := parseLLVMGenFile(t, `interface Printable {
    fn show(self) -> String
}

struct Note {
    pub msg: String,

    pub fn show(self) -> String {
        self.msg
    }
}

struct Unrelated {
    pub k: Int,
}

fn probe(p: Printable) -> Unrelated? {
    p.downcast::<Unrelated>()
}
`)

	_, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/iface_downcast_bad.osty",
	})
	if err == nil {
		t.Fatal("expected error for downcast target that implements no interface")
	}
	// Either the coarse ("any registered interface") or the precise
	// ("does not implement interface %q") form is acceptable — the
	// precise form fires when the receiver's source type carries the
	// interface name, the coarse form when it doesn't.
	msg := err.Error()
	if !strings.Contains(msg, "does not implement any registered interface") &&
		!strings.Contains(msg, "does not implement interface") {
		t.Fatalf("error does not mention the missing-impl reason: %v", err)
	}
}

// When the target type implements an *unrelated* interface — not the
// one the receiver is statically typed at — the backend rejects the
// downcast at compile time rather than emitting a vtable compare that
// would always produce None at runtime.
func TestInterfaceDowncastTargetMustImplementReceiverInterface(t *testing.T) {
	file := parseLLVMGenFile(t, `interface Printable {
    fn show(self) -> String
}

interface Tagged {
    fn tag(self) -> Int
}

struct Note {
    pub msg: String,

    pub fn show(self) -> String {
        self.msg
    }
}

struct Marker {
    pub n: Int,

    pub fn tag(self) -> Int {
        self.n
    }
}

fn probe(p: Printable) -> Marker? {
    p.downcast::<Marker>()
}
`)

	_, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/iface_downcast_wrong_iface.osty",
	})
	if err == nil {
		t.Fatal("expected error when target implements a different interface than the receiver")
	}
	msg := err.Error()
	if !strings.Contains(msg, "does not implement interface") {
		t.Fatalf("error does not mention the specific-interface mismatch: %v", err)
	}
	if !strings.Contains(msg, "Printable") {
		t.Fatalf("error does not name the receiver's interface %q: %v", "Printable", err)
	}
}
