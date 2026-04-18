package backend

import (
	"testing"

	"github.com/osty/osty/internal/stdlib"
)

func TestStdlibCheckResultNilInputs(t *testing.T) {
	if got := stdlibCheckResult(nil, "strings"); got != nil {
		t.Fatalf("nil registry = %v, want nil", got)
	}
	reg := stdlib.LoadCached()
	if got := stdlibCheckResult(reg, "nonexistent_module"); got != nil {
		t.Fatalf("unknown module = %v, want nil", got)
	}
}

func TestStdlibCheckResultStringsModule(t *testing.T) {
	reg := stdlib.LoadCached()
	chk := stdlibCheckResult(reg, "strings")
	if chk == nil {
		t.Fatalf("stdlibCheckResult(strings) = nil, want non-nil *check.Result")
	}
	// Sanity: at least some expression types should be recorded for a
	// non-empty stdlib module. Exact count varies with the checker
	// coverage, so assert "more than a handful" rather than a precise
	// number.
	if len(chk.Types) < 10 {
		t.Errorf("chk.Types has %d entries, expected the checker to cover more of the strings module", len(chk.Types))
	}
}

func TestStdlibCheckResultCached(t *testing.T) {
	reg := stdlib.LoadCached()
	first := stdlibCheckResult(reg, "strings")
	second := stdlibCheckResult(reg, "strings")
	// Same *check.Result pointer means the sync.Once path fired once.
	if first != second {
		t.Fatalf("cache miss: first=%p second=%p, want same pointer", first, second)
	}
}
