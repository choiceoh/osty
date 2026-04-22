package llvmgen

import (
	"strings"
	"testing"
)

// TestTryGenerateNativeOwnedModuleCoversInterfaceVtableSurface locks
// the Phase 6a scaffold: native path emits the fat-pointer type,
// a per-impl vtable constant, and a per-method shim that adapts
// the ptr-receiver interface ABI to the concrete struct's by-value
// method ABI. The test source never actually *dispatches* through
// the interface — `v.size()` is a direct method call. This phase
// only verifies the vtable surface exists so later phases (boxing,
// indirect dispatch) can plug into the same symbols.
func TestTryGenerateNativeOwnedModuleCoversInterfaceVtableSurface(t *testing.T) {
	src := `interface Sized {
    fn size(self) -> Int
}

struct Vec {
    count: Int,

    fn size(self) -> Int {
        self.count
    }
}

fn main() {
    let v = Vec { count: 3 }
    println(v.size())
}
`
	mod := lowerNativeEntryModule(t, src)
	out, ok, err := TryGenerateNativeOwnedModule(mod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/native_iface_vtable.osty",
	})
	if err != nil {
		t.Fatalf("TryGenerateNativeOwnedModule errored: %v", err)
	}
	if !ok {
		t.Fatal("TryGenerateNativeOwnedModule reported uncovered for Sized/Vec vtable surface")
	}
	got := string(out)
	for _, want := range []string{
		"%osty.iface = type { ptr, ptr }",
		"@osty.vtable.Vec__Sized = internal constant",
		"ptr @osty.shim.Vec__Sized__size",
		"define i64 @osty.shim.Vec__Sized__size(ptr %self_data)",
		"load %Vec, ptr %self_data",
		"call i64 @Vec__size(%Vec %self)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("native-owned IR missing %q:\n%s", want, got)
		}
	}
}

// TestTryGenerateNativeOwnedModuleCoversInterfaceVtableWithArgs
// locks the non-self parameter case: the shim threads the
// interface method's non-receiver args through to the concrete
// call. Uses the Phase 6c test source (without the boxing /
// dispatch step, which is a later batch).
func TestTryGenerateNativeOwnedModuleCoversInterfaceVtableWithArgs(t *testing.T) {
	src := `interface Combine {
    fn combine(self, other: Int) -> Int
}

struct Thing {
    x: Int,

    fn combine(self, other: Int) -> Int {
        self.x + other
    }
}

fn main() {
    let t = Thing { x: 3 }
    println(t.combine(4))
}
`
	mod := lowerNativeEntryModule(t, src)
	out, ok, err := TryGenerateNativeOwnedModule(mod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/native_iface_vtable_args.osty",
	})
	if err != nil {
		t.Fatalf("TryGenerateNativeOwnedModule errored: %v", err)
	}
	if !ok {
		t.Fatal("TryGenerateNativeOwnedModule reported uncovered for Combine/Thing vtable surface")
	}
	got := string(out)
	for _, want := range []string{
		"@osty.vtable.Thing__Combine",
		"define i64 @osty.shim.Thing__Combine__combine(ptr %self_data, i64 %arg0)",
		"call i64 @Thing__combine(%Thing %self, i64 %arg0)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("native-owned IR missing %q:\n%s", want, got)
		}
	}
}

// TestTryGenerateNativeOwnedModuleCoversInterfaceBoxingDispatch locks
// Phase 6b: `let s: Sized = v` boxes `v` into `%osty.iface` and
// `s.size()` dispatches indirectly through the vtable.
func TestTryGenerateNativeOwnedModuleCoversInterfaceBoxingDispatch(t *testing.T) {
	src := `interface Sized {
    fn size(self) -> Int
}

struct Vec {
    count: Int,

    fn size(self) -> Int {
        self.count
    }
}

fn main() {
    let v = Vec { count: 3 }
    let s: Sized = v
    println(s.size())
}
`
	mod := lowerNativeEntryModule(t, src)
	out, ok, err := TryGenerateNativeOwnedModule(mod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/native_iface_box_dispatch.osty",
	})
	if err != nil {
		t.Fatalf("TryGenerateNativeOwnedModule errored: %v", err)
	}
	if !ok {
		t.Fatal("TryGenerateNativeOwnedModule reported uncovered for Sized/Vec boxing + dispatch")
	}
	got := string(out)
	for _, want := range []string{
		"%osty.iface = type { ptr, ptr }",
		"@osty.vtable.Vec__Sized",
		"alloca %Vec",
		"store %Vec",
		"insertvalue %osty.iface undef, ptr",
		"insertvalue %osty.iface",
		"ptr @osty.vtable.Vec__Sized, 1",
		"extractvalue %osty.iface",
		"getelementptr ptr",
		"load ptr, ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("native-owned IR missing %q:\n%s", want, got)
		}
	}
}

// TestTryGenerateNativeOwnedModuleCoversInterfaceDispatchWithArgs
// locks Phase 6c: non-self arguments flow through the indirect
// call with their native LLVM types.
func TestTryGenerateNativeOwnedModuleCoversInterfaceDispatchWithArgs(t *testing.T) {
	src := `interface Combine {
    fn combine(self, other: Int) -> Int
}

struct Thing {
    x: Int,

    fn combine(self, other: Int) -> Int {
        self.x + other
    }
}

fn main() {
    let t = Thing { x: 3 }
    let c: Combine = t
    println(c.combine(4))
}
`
	mod := lowerNativeEntryModule(t, src)
	out, ok, err := TryGenerateNativeOwnedModule(mod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/native_iface_dispatch_args.osty",
	})
	if err != nil {
		t.Fatalf("TryGenerateNativeOwnedModule errored: %v", err)
	}
	if !ok {
		t.Fatal("TryGenerateNativeOwnedModule reported uncovered for Combine/Thing dispatch-with-args")
	}
	got := string(out)
	if !strings.Contains(got, ", i64 4)") {
		t.Fatalf("non-self arg `i64 4` missing from indirect call:\n%s", got)
	}
	if !strings.Contains(got, "@osty.vtable.Thing__Combine") {
		t.Fatalf("vtable symbol missing:\n%s", got)
	}
}

// TestTryGenerateNativeOwnedModuleSkipsInterfaceWithoutImpl locks
// the guard: an interface with no structural impl in the module
// must not produce any vtable / shim symbols. Otherwise we'd leak
// dangling `@osty.vtable.<X>__<I>` references for unused surfaces.
func TestTryGenerateNativeOwnedModuleSkipsInterfaceWithoutImpl(t *testing.T) {
	src := `interface Unused {
    fn nothing(self) -> Int
}

struct Plain {
    value: Int,
}

fn main() {
    let p = Plain { value: 1 }
    println(p.value)
}
`
	mod := lowerNativeEntryModule(t, src)
	out, ok, err := TryGenerateNativeOwnedModule(mod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/native_iface_no_impl.osty",
	})
	if err != nil {
		t.Fatalf("TryGenerateNativeOwnedModule errored: %v", err)
	}
	if !ok {
		t.Fatal("TryGenerateNativeOwnedModule reported uncovered for orphan interface")
	}
	got := string(out)
	if strings.Contains(got, "osty.vtable") {
		t.Fatalf("unexpected vtable symbol when no struct implements Unused:\n%s", got)
	}
	if strings.Contains(got, "osty.shim") {
		t.Fatalf("unexpected shim symbol when no struct implements Unused:\n%s", got)
	}
	// `%osty.iface = type { ptr, ptr }` should only be emitted when an
	// impl exists. Orphan interfaces yield no surface at all.
	if strings.Contains(got, "%osty.iface") {
		t.Fatalf("unexpected osty.iface type def when no struct implements Unused:\n%s", got)
	}
}
