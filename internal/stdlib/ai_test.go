package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestAiModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["ai"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.ai not loaded")
	}

	for _, tc := range []struct {
		name string
		kind resolve.SymbolKind
	}{
		{"Provider", resolve.SymEnum},
		{"Role", resolve.SymEnum},
		{"ToolChoiceMode", resolve.SymEnum},
		{"ProviderConfig", resolve.SymStruct},
		{"ToolDefinition", resolve.SymStruct},
		{"ToolChoice", resolve.SymStruct},
		{"ToolCall", resolve.SymStruct},
		{"ToolResult", resolve.SymStruct},
		{"Message", resolve.SymStruct},
		{"ChatOptions", resolve.SymStruct},
		{"ChatRequest", resolve.SymStruct},
		{"Usage", resolve.SymStruct},
		{"Choice", resolve.SymStruct},
		{"ChatResponse", resolve.SymStruct},
		{"EmbeddingRequest", resolve.SymStruct},
		{"EmbeddingVector", resolve.SymStruct},
		{"EmbeddingResponse", resolve.SymStruct},
	} {
		sym := mod.Package.PkgScope.LookupLocal(tc.name)
		if sym == nil {
			t.Fatalf("std.ai missing type %q", tc.name)
		}
		if sym.Kind != tc.kind {
			t.Fatalf("std.ai.%s kind = %s, want %s", tc.name, sym.Kind, tc.kind)
		}
		if !sym.Pub {
			t.Fatalf("std.ai.%s not public", tc.name)
		}
	}

	for _, name := range []string{
		"provider", "providerNoKey", "openAI", "openAICompatible",
		"openAICompatibleLocal", "openRouter", "anthropic",
		"gemini", "ollama", "lmStudio", "custom", "customNoAuth",
		"message", "system", "developer", "user", "assistant", "tool",
		"functionTool", "functionToolNoSchema", "functionToolValue", "toolChoiceAuto",
		"toolChoiceNone", "toolChoiceRequired", "toolChoiceAny",
		"toolChoiceNamed", "toolCall", "toolCallNoId", "assistantToolCalls",
		"toolCallValue", "toolResult", "toolResultNamed", "toolResultJson",
		"toolResultValue", "toolResultMessage",
		"options", "chat", "chatText", "withOptions", "withTemperature",
		"withTopP", "withMaxOutputTokens", "withStop", "withStream",
		"withJsonResponse", "withResponseFormat", "withUser", "withSeed",
		"withReasoningEffort", "withTools", "withTool", "withToolChoice",
		"withToolsJson", "withToolChoiceJson", "withExtraJson",
		"embedding", "embeddingText", "withEmbeddingDimensions", "withEmbeddingEncoding",
		"chatUrl", "modelsUrl", "embeddingsUrl", "supportsEmbeddings",
		"headers", "validateConfig", "validateChat", "validateEmbedding",
		"encodeChat", "chatHttpRequest", "sendChat", "sendChatText",
		"modelsHttpRequest", "sendModels", "encodeEmbedding", "embeddingHttpRequest",
		"sendEmbedding", "parseChatResponse", "parseModelsResponse",
		"parseEmbeddingResponse", "estimateChatTokens", "streamDataPayload",
		"isDonePayload", "responseErrorMessage",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.ai missing export %q", name)
		}
		if sym.Kind != resolve.SymFn {
			t.Fatalf("std.ai.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.ai.%s not public", name)
		}
	}
}

func TestAiModulePinsProviderPayloads(t *testing.T) {
	src := aiModuleSource(t)
	for _, want := range []string{
		`OpenAI    -> Ok(encodeOpenAIChat(req, true))`,
		`let key = if currentOpenAI { "max_completion_tokens" } else { "max_tokens" }`,
		`prop("max_tokens", maxOutputOrDefault(req.options, 1024).toString())`,
		`prop("systemInstruction", object([prop("parts", array([object([prop("text", quote(systemPrompt))])]))]))`,
		`out.insert("x-goog-api-key", strings.trimSpace(config.apiKey))`,
		`out.insert("anthropic-version", version)`,
		`out.insert("HTTP-Referer", strings.trimSpace(config.referer))`,
		`out.insert("X-OpenRouter-Title", strings.trimSpace(config.title))`,
		`Ok(joinUrl(base, "/v1beta/models/{geminiModelPath(model)}:generateContent"))`,
		`Ok(joinUrl(base, "/chat/completions"))`,
		`parseOpenAIUsage(root)`,
		`parseAnthropicUsage(root)`,
		`parseGeminiUsage(root)`,
		`pub fn streamDataPayload(line: String) -> String?`,
		`strings.trimSpace(payload) == "[DONE]"`,
		`pub tools: List<ToolDefinition> = []`,
		`pub toolChoice: ToolChoice? = None`,
		`pub toolCalls: List<ToolCall> = []`,
		`pub toolResults: List<ToolResult> = []`,
		`props.push(prop("tools", renderOpenAITools(opts.tools)))`,
		`props.push(prop("tool_choice", renderOpenAIToolChoice(choice)))`,
		`props.push(prop("tools", renderAnthropicTools(req.options.tools)))`,
		`props.push(prop("tool_choice", renderAnthropicToolChoice(choice)))`,
		`props.push(prop("tools", renderGeminiTools(req.options.tools)))`,
		`props.push(prop("toolConfig", renderGeminiToolConfig(choice)))`,
		`prop("tool_calls", renderOpenAIToolCalls(msg.toolCalls))`,
		`prop("type", quote("tool_use"))`,
		`prop("type", quote("tool_result"))`,
		`prop("functionCall", object([`,
		`prop("functionResponse", object([`,
		`toolCalls: parseOpenAIToolCalls(arrayField(msg, "tool_calls"))`,
		`toolCalls: parseAnthropicToolCalls(blocks)`,
		`toolCalls: parseGeminiToolCalls(parts)`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.ai source missing %q", want)
		}
	}
}

func TestAiModuleMethodsAreBodied(t *testing.T) {
	reg := LoadCached()
	for _, tc := range []struct {
		typeName string
		methods  []string
	}{
		{"Provider", []string{"toString", "isOpenAICompatible", "defaultBaseUrl"}},
		{"ToolChoiceMode", []string{"isConfigured"}},
		{"ToolDefinition", []string{"withDescription", "withParametersJson", "withParameters", "withStrict"}},
		{"ToolChoice", []string{"isConfigured"}},
		{"ToolCall", []string{"arguments", "withId"}},
		{"ToolResult", []string{"bodyJson"}},
		{"Role", []string{"toString", "isInstruction", "toAnthropicRole", "toGeminiRole"}},
		{"ProviderConfig", []string{"resolvedBaseUrl", "withBaseUrl", "withApiKey", "withOrganization", "withProject", "withApp", "withApiVersion", "withBeta", "withHeader"}},
		{"Message", []string{"tokenEstimate", "isInstruction", "withToolCall", "withToolCalls", "withToolResult"}},
		{"ChatRequest", []string{"append", "tokenEstimate"}},
		{"ChatResponse", []string{"text", "toolCalls", "hasToolCalls"}},
	} {
		for _, method := range tc.methods {
			fn := reg.LookupMethodDecl("ai", tc.typeName, method)
			if fn == nil {
				t.Fatalf("LookupMethodDecl(ai, %s, %s) = nil, want *ast.FnDecl", tc.typeName, method)
			}
			if fn.Body == nil {
				t.Fatalf("ai.%s.%s body = nil, want source method body", tc.typeName, method)
			}
		}
	}
}

func aiModuleSource(t *testing.T) string {
	t.Helper()
	reg := LoadCached()
	mod := reg.Modules["ai"]
	if mod == nil {
		t.Fatal("stdlib ai module missing")
	}
	return string(mod.Source)
}
