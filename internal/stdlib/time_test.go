package stdlib

import (
	"strings"
	"testing"
)

func TestTimeModuleDerivedHelpersAreBodied(t *testing.T) {
	reg := LoadCached()
	for _, tc := range []struct {
		typeName string
		method   string
	}{
		{"Duration", "abs"},
		{"Duration", "micros"},
		{"Duration", "millis"},
		{"Duration", "seconds"},
		{"Instant", "add"},
		{"Instant", "sub"},
		{"Instant", "since"},
		{"Instant", "inZone"},
		{"ZonedTime", "format"},
	} {
		fn := reg.LookupMethodDecl("time", tc.typeName, tc.method)
		if fn == nil {
			t.Fatalf("LookupMethodDecl(time, %s, %s) = nil", tc.typeName, tc.method)
		}
		if fn.Body == nil {
			t.Fatalf("time.%s.%s body = nil, want Osty helper body", tc.typeName, tc.method)
		}
	}
}

func TestTimeModuleSourcePinsDurationHelpers(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["time"]
	if mod == nil {
		t.Fatal("stdlib time module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		"Duration { nanoseconds: self.nanoseconds.abs() }",
		"let nanosPerMicro: Int64 = 1000",
		"let nanosPerMilli: Int64 = 1000000",
		"let nanosPerSecond: Int64 = 1000000000",
		"self.nanoseconds / nanosPerMilli",
		"pub fn since(self, earlier: Instant) -> Duration",
		"self.sub(earlier)",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("time helper source missing %q", want)
		}
	}
}
