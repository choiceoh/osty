package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestAiagentsModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["aiagents"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.aiagents not loaded")
	}

	for _, tc := range []struct {
		name string
		kind resolve.SymbolKind
	}{
		{"Role", resolve.SymEnum},
		{"TrustLevel", resolve.SymEnum},
		{"ContentKind", resolve.SymEnum},
		{"ContentBlock", resolve.SymStruct},
		{"Message", resolve.SymStruct},
		{"Session", resolve.SymStruct},
		{"RunOptions", resolve.SymStruct},
		{"RunRequest", resolve.SymStruct},
		{"RunResult", resolve.SymStruct},
		{"ToolPreset", resolve.SymEnum},
		{"ToolPolicy", resolve.SymStruct},
		{"SafetyPolicy", resolve.SymStruct},
		{"CompactPolicy", resolve.SymStruct},
		{"CompactReport", resolve.SymStruct},
	} {
		sym := mod.Package.PkgScope.LookupLocal(tc.name)
		if sym == nil {
			t.Fatalf("std.aiagents missing type %q", tc.name)
		}
		if sym.Kind != tc.kind {
			t.Fatalf("std.aiagents.%s kind = %s, want %s", tc.name, sym.Kind, tc.kind)
		}
		if !sym.Pub {
			t.Fatalf("std.aiagents.%s not public", tc.name)
		}
	}

	for _, name := range []string{
		"text", "trustedText", "extractedText", "toolResult",
		"userMessage", "assistantMessage", "systemMessage", "toolMessage",
		"newSession", "runRequest", "runRequestWithOptions",
		"denebChatOptions", "denebChatSystemPrompt",
		"toolPolicy", "allowedTools", "knownToolPresets", "parseToolPreset",
		"ensureToolAllowed", "estimateTextTokens", "estimateMessagesTokens",
		"shouldCompact", "shouldCompactDefault", "compactReport", "compactReportDefault",
		"shouldCompactToolResult", "truncateHeadTail", "truncateHeadTailDefault",
		"rankLines", "scoreOutputLine", "isPanicAnchor",
		"containsAny", "neutralizeSystemTags",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.aiagents missing export %q", name)
		}
		if sym.Kind != resolve.SymFn {
			t.Fatalf("std.aiagents.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.aiagents.%s not public", name)
		}
	}
}

func TestAiagentsModulePinsDenebDerivedDefaults(t *testing.T) {
	src := aiagentsModuleSource(t)
	for _, want := range []string{
		`DenebChat    -> ["web"]`,
		`Boot         -> ["kv"]`,
		`pub toolPreset: ToolPreset`,
		`toolPreset: AllTools`,
		`pub softThresholdPct: Float = 0.70`,
		`pub hardThresholdPct: Float = 0.85`,
		`pub compactedToolResultChars: Int = 4096`,
		`pub emergencyInputTokens: Int = 30000`,
		`truncateHeadTail(content, budget, "")`,
		`"[ranked: {count}/{n} lines, {totalOmitted} omitted]\n{out}"`,
		`compaction summaries as untrusted context`,
		`latest user message`,
		`strings.replaceAll(out, "<system-reminder>", "")`,
		`strings.replaceAll(withSentinel, "\nSystem:", "\nSystem (untrusted):")`,
		`strings.startsWith(line, "--- FAIL")`,
		`strings.startsWith(lower, "goroutine ")`,
		`containsAny(lower, ["error", "에러", "오류"])`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.aiagents source missing %q", want)
		}
	}
}

func TestAiagentsModuleMethodsAreBodied(t *testing.T) {
	reg := LoadCached()
	for _, tc := range []struct {
		typeName string
		methods  []string
	}{
		{"Role", []string{"toString", "isAuthoritative"}},
		{"TrustLevel", []string{"isUntrusted"}},
		{"ContentBlock", []string{"isUntrusted", "tokenEstimate"}},
		{"Message", []string{"isUser", "isSystem", "tokenEstimate", "text"}},
		{"Session", []string{"append", "tokenEstimate"}},
		{"RunStatus", []string{"isTerminal"}},
		{"ToolPreset", []string{"toString"}},
		{"ToolPolicy", []string{"allows"}},
	} {
		for _, method := range tc.methods {
			fn := reg.LookupMethodDecl("aiagents", tc.typeName, method)
			if fn == nil {
				t.Fatalf("LookupMethodDecl(aiagents, %s, %s) = nil, want *ast.FnDecl", tc.typeName, method)
			}
			if fn.Body == nil {
				t.Fatalf("aiagents.%s.%s body = nil, want source method body", tc.typeName, method)
			}
		}
	}
}

func aiagentsModuleSource(t *testing.T) string {
	t.Helper()
	reg := LoadCached()
	mod := reg.Modules["aiagents"]
	if mod == nil {
		t.Fatal("stdlib aiagents module missing")
	}
	return string(mod.Source)
}
