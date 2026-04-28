package selfhost

import "testing"

func FuzzCheckSourceStructuredNoPanic(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte("fn main() { let x = 1 }\n"),
		[]byte("fn main( {\n"),
		[]byte("use std.strings as strings\nfn main() { strings.join([\"a\"], \",\") }\n"),
		[]byte("fn id<T>(x: T) -> T { x }\nfn main() { id::<Int>(1) }\n"),
		[]byte{0xff, 0x00, '\n', '{', '}'},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		_ = CheckSourceStructured(src)
	})
}
