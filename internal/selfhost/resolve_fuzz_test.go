package selfhost_test

import (
	"testing"

	"github.com/osty/osty/internal/selfhost"
)

func FuzzResolveSourceStructuredNoPanic(f *testing.F) {
	for _, seed := range []string{
		"",
		"fn main() {",
		"fn main() -> Int { let x: Missing = 1\n",
		"pub struct Box<T> { value: T }\nfn main(x: Box<Int>) -> Int { x.value }\n",
		"struct Counter { value: Int }\nimpl Counter { fn next(self) -> Self { self } }\n",
		"use dep::{Widget}\nfn make(x: dep.Widget) -> Widget { x }\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > 4096 {
			t.Skip()
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("ResolveSourceStructured panicked: %v", r)
			}
		}()
		_ = selfhost.ResolveSourceStructured([]byte(src))
	})
}
