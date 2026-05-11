package check

import (
	"strings"
	"testing"
)

func TestDefaultNativeCheckerInvalidOverrideMessage(t *testing.T) {
	t.Setenv(nativeCheckerEnv, "/definitely/missing/osty-native-checker")

	checker, note := defaultNativeChecker()
	if checker != nil {
		t.Fatalf("defaultNativeChecker returned %#v, want nil", checker)
	}
	if !strings.Contains(note, "override was not found") {
		t.Fatalf("note = %q, want override wording", note)
	}
	if !strings.Contains(note, "unset it to use the default checker path") {
		t.Fatalf("note = %q, want fallback guidance", note)
	}
}
