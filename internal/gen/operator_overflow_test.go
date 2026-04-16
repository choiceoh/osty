package gen_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIntegerOperatorsCheckedRuntime(t *testing.T) {
	goSrc, err := transpileWithStdlib(t, `fn main() {
    let hi: Int8 = 120
    let lo: Int8 = -120
    let eleven: Int8 = 11
    let two: Int8 = 2
    let one: Int8 = 1
    let mut x: Int8 = 126
    x += 1
    x -= 2
    x *= 1
    x >>= 1
    x <<= 1
    println("{(hi + 7).toInt()} {(lo - 8).toInt()} {(eleven * eleven).toInt()} {(eleven / two).toInt()} {(eleven % two).toInt()} {(one << 6).toInt()} {(hi >> 3).toInt()} {x.toInt()}")
}
`)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := strings.TrimSpace(runGo(t, goSrc))
	want := "127 -128 121 5 1 64 15 124"
	if out != want {
		t.Fatalf("stdout = %q, want %q\n--- source ---\n%s", out, want, goSrc)
	}
}

func TestIntegerOperatorsAbortOnOverflow(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "add",
			src: `fn main() {
    let x: Int8 = 127
    println((x + 1).toInt())
}
`,
			want: "integer overflow",
		},
		{
			name: "multiply",
			src: `fn main() {
    let x: Int8 = 64
    println((x * 2).toInt())
}
`,
			want: "integer overflow",
		},
		{
			name: "divide_min",
			src: `fn main() {
    let x: Int8 = -128
    let y: Int8 = -1
    println((x / y).toInt())
}
`,
			want: "integer overflow",
		},
		{
			name: "shift",
			src: `fn main() {
    let x: Int8 = 1
    let y: Int8 = 8
    println((x << y).toInt())
}
`,
			want: "invalid shift count",
		},
		{
			name: "compound",
			src: `fn main() {
    let mut x: Int8 = 126
    x += 2
    println(x.toInt())
}
`,
			want: "integer overflow",
		},
		{
			name: "unary",
			src: `fn main() {
    let x: Int8 = -128
    println((-x).toInt())
}
`,
			want: "integer overflow",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			goSrc, err := transpileWithStdlib(t, tc.src)
			if err != nil {
				t.Fatalf("transpile: %v\n%s", err, goSrc)
			}
			out := runGoExpectFail(t, goSrc)
			if !strings.Contains(out, tc.want) {
				t.Fatalf("failure output missing %q:\n%s\n--- source ---\n%s", tc.want, out, goSrc)
			}
		})
	}
}

func runGoExpectFail(t *testing.T, src []byte) string {
	t.Helper()
	if testing.Short() {
		t.Skip("generated Go execution test (slow)")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go binary not on PATH; skipping generated Go execution")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("go", "run", path)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("go run unexpectedly succeeded\n--- source ---\n%s\n--- output ---\n%s", src, out)
	}
	return string(out)
}
