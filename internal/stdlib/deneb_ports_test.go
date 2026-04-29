package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestDenebPortedModulesSurface(t *testing.T) {
	reg := LoadCached()
	tests := []struct {
		module string
		types  map[string]resolve.SymbolKind
		fns    []string
	}{
		{
			module: "redact",
			types: map[string]resolve.SymbolKind{
				"SecretKind":   resolve.SymEnum,
				"RedactPolicy": resolve.SymStruct,
			},
			fns: []string{
				"defaultPolicy", "redact", "redactWithPolicy", "maskToken", "maskTokenWithPolicy",
				"isAlreadyMasked", "mightContainSecret", "looksLikeSecretToken",
				"knownSecretPrefixes", "sensitiveKeyNames", "containsAny",
			},
		},
		{
			module: "security",
			types: map[string]resolve.SymbolKind{
				"SafeUrlPolicy":  resolve.SymStruct,
				"UrlSafety":      resolve.SymStruct,
				"LinkExtraction": resolve.SymStruct,
			},
			fns: []string{
				"defaultSafeUrlPolicy", "isSafeUrl", "checkUrl", "validateSessionKey",
				"sanitizeHtml", "normalizeHost", "isLocalHost", "isCloudMetadataHost",
				"isPrivateHost", "isNumericPrivateIpv4", "extractSafeLinksDefault", "extractSafeLinks",
			},
		},
		{
			module: "search",
			types: map[string]resolve.SymbolKind{
				"QueryMode": resolve.SymEnum,
				"Document":  resolve.SymStruct,
				"Hit":       resolve.SymStruct,
				"Index":     resolve.SymStruct,
			},
			fns: []string{
				"document", "index", "upsert", "upsertFields", "remove", "clear",
				"indexLen", "searchIndex", "searchIndexOR", "tokenize", "searchDefault",
				"search", "searchWithMode", "scoreDocument", "snippetDefault", "snippet", "containsToken",
			},
		},
		{
			module: "markdown",
			types: map[string]resolve.SymbolKind{
				"BlockKind":   resolve.SymEnum,
				"Block":       resolve.SymStruct,
				"CodeFence":   resolve.SymStruct,
				"HtmlOptions": resolve.SymStruct,
				"HtmlResult":  resolve.SymStruct,
			},
			fns: []string{
				"parseBlocks", "extractFences", "stripMarkdown", "htmlToMarkdown",
				"htmlToMarkdownResult", "htmlToMarkdownWithOptions",
				"stripTags", "decodeEntities",
			},
		},
		{
			module: "media",
			types: map[string]resolve.SymbolKind{
				"MediaKind":        resolve.SymEnum,
				"Detected":         resolve.SymStruct,
				"MediaTokenResult": resolve.SymStruct,
			},
			fns: []string{
				"detectBytes", "detect", "detectByName", "detectByMagic", "mimeFromExtension",
				"kindFromMime", "isTextMime", "extensionOf", "isBinarySample",
				"looksLikeText", "cleanArchivePath", "humanSize", "isYoutubeUrl", "youtubeId",
				"parseMediaTokens",
			},
		},
		{
			module: "httpretry",
			types: map[string]resolve.SymbolKind{
				"FailureReason":      resolve.SymEnum,
				"RetryAction":        resolve.SymEnum,
				"RetryPolicy":        resolve.SymStruct,
				"ClassificationHint": resolve.SymStruct,
				"Decision":           resolve.SymStruct,
			},
			fns: []string{
				"defaultPolicy", "classify", "classifyWithCode", "classifyWithHint",
				"classifyMessage", "classifyProviderCode", "decideDefault", "decide",
				"decisionForReason", "backoffMsDefault", "backoffMs", "isRetriableStatus",
			},
		},
		{
			module: "jsonl",
			types: map[string]resolve.SymbolKind{
				"Record": resolve.SymStruct,
			},
			fns: []string{
				"encodeValue", "encodeRecord", "parseLine", "parseLines",
				"appendLine", "appendValue", "compactHeadTail", "isJsonl",
			},
		},
		{
			module: "tokenest",
			types: map[string]resolve.SymbolKind{
				"Family":       resolve.SymEnum,
				"ScriptClass":  resolve.SymEnum,
				"ScriptCounts": resolve.SymStruct,
				"Estimate":     resolve.SymStruct,
				"Calibration":  resolve.SymStruct,
			},
			fns: []string{
				"estimate", "estimateForModel", "estimateForFamily", "estimateDetailed",
				"estimateWithCalibration", "estimateBytes", "calibration", "recordFeedback",
				"correctionFactor", "applyCalibration", "resolveFamily", "countScripts", "classifyChar", "ratio",
			},
		},
		{
			module: "shortid",
			types: map[string]resolve.SymbolKind{
				"Generator": resolve.SymStruct,
				"Next":      resolve.SymStruct,
			},
			fns: []string{"generator", "next", "format", "pad4", "sanitizePrefix"},
		},
		{
			module: "metrics",
			types: map[string]resolve.SymbolKind{
				"Counter": resolve.SymStruct,
				"Sample":  resolve.SymStruct,
			},
			fns: []string{"counter", "inc", "add", "get", "snapshot", "key"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.module, func(t *testing.T) {
			mod := reg.Modules[tc.module]
			if mod == nil || mod.Package == nil {
				t.Fatalf("std.%s not loaded", tc.module)
			}
			for name, kind := range tc.types {
				sym := mod.Package.PkgScope.LookupLocal(name)
				if sym == nil {
					t.Fatalf("std.%s missing type %q", tc.module, name)
				}
				if sym.Kind != kind {
					t.Fatalf("std.%s.%s kind = %s, want %s", tc.module, name, sym.Kind, kind)
				}
				if !sym.Pub {
					t.Fatalf("std.%s.%s not public", tc.module, name)
				}
			}
			for _, name := range tc.fns {
				sym := mod.Package.PkgScope.LookupLocal(name)
				if sym == nil {
					t.Fatalf("std.%s missing export %q", tc.module, name)
				}
				if sym.Kind != resolve.SymFn {
					t.Fatalf("std.%s.%s kind = %s, want fn", tc.module, name, sym.Kind)
				}
				if !sym.Pub {
					t.Fatalf("std.%s.%s not public", tc.module, name)
				}
			}
		})
	}
}

func TestDenebPortedModulesPinDependencyFreeBehaviors(t *testing.T) {
	for _, tc := range []struct {
		module string
		wants  []string
	}{
		{"redact", []string{
			`"sk-", "sk_", "ghp_"`,
			`redactDbConnStrings(out, policy)`,
			`redactJsonFields(out, policy)`,
			`redactTelegramTokens(out, policy)`,
			`redactDiscordMentions(out)`,
			`redactPhoneNumbers(out)`,
			`[REDACTED PRIVATE KEY]`,
			`redactPrivateKeyBlocks(out, policy)`,
			`redactSensitiveKeyValues(out, policy)`,
			`redactUrlUserinfo(out, policy)`,
			`redactUrlQueryParams(out, policy)`,
			`redactFormBody(out, policy)`,
			`isFormBodyLike(trimmed)`,
			`shouldRedactKeyValue`,
			`strings.startsWith(token, "eyJ")`,
		}},
		{"security", []string{
			`allowCloudMetadata: Bool = false`,
			`strings.startsWith(input, "\\\\")`,
			`metadata.google.internal`,
			`isNumericPrivateIpv4(host)`,
			`stripMarkdownLinks`,
			`findBareUrls`,
			`isBalancedTrailingBracket`,
			`countChar(text, '(')`,
			`key.charCount() > 512`,
		}},
		{"search", []string{
			`pub struct Index`,
			`pub fn upsert`,
			`pub fn remove`,
			`searchIndexOR`,
			`tokenize(text: String)`,
			`if hits.isEmpty() && mode == Auto`,
			`docFrequency(corpus, q)`,
			`tfScore = (tf.toFloat() * 2.2)`,
			`isHangul(c)`,
			`scoreInCorpus(doc, docs, queryTokens`,
		}},
		{"markdown", []string{
			"strings.startsWith(line, \"```\") || strings.startsWith(line, \"~~~\")",
			`htmlToMarkdown`,
			`removePairedTagBody(out, "script")`,
			`removePairedTagBody(out, "head")`,
			`if opts.stripNoise`,
			`convertTables(out)`,
			`convertPreCode(out)`,
			`convertAnchors(out)`,
			`convertImages(out)`,
			`convertOrderedLists(out)`,
			`extractCodeLanguage`,
			`markdownTableRow`,
			`escapeTableCell`,
			`filenameFromUrl`,
			`&hellip;`,
			`decodeEntities(stripTags(out))`,
			`Block { kind: FencedCode`,
		}},
		{"media", []string{
			`hasPrefix(data, [0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A])`,
			`cleanArchivePath`,
			`isYoutubeUrl`,
			`detectOOXML(data)`,
			`parseMediaTokens`,
			`image/x-icon`,
			`normalizeMediaSource`,
			`stripBracketDirectives`,
			`[[audio_as_voice]]`,
			`application/octet-stream`,
		}},
		{"httpretry", []string{
			`AuthPermanent`,
			`ClassificationHint`,
			`ThinkingSignature`,
			`LongContextTier`,
			`classifyProviderCode(providerCode)`,
			`usageLimitTransientSignals`,
			`contextOverflowPatterns`,
			`isLargeSession(hint) && containsAny(msg, serverDisconnectPatterns())`,
			`stripThinking: true`,
			`retryOnce: true`,
			`attempt <= 1`,
			`status == 408 || status == 429 || status == 500`,
			`delay = delay * 2`,
		}},
		{"jsonl", []string{
			`json.parseValue(trimmed)`,
			`appendLine`,
			`compactHeadTail`,
			`jsonl lines omitted`,
		}},
		{"tokenest", []string{
			`Family identifies tokenizer families`,
			`Hangul -> 1.5`,
			`resolveFamily(modelID: String)`,
			`recordFeedback(mut cal: Calibration`,
			`correctionFactor(family: Family, cal: Calibration)`,
			`4.0 + highRatio * 0.5`,
		}},
		{"shortid", []string{
			`prefix: gen.prefix`,
			`n % 10000`,
			`sanitizePrefix(prefix)`,
		}},
		{"metrics", []string{
			`strings.join(labels, "\u{0}")`,
			`counter.values.insert`,
			`snapshot(counter: Counter)`,
		}},
	} {
		t.Run(tc.module, func(t *testing.T) {
			src := moduleSource(t, tc.module)
			for _, want := range tc.wants {
				if !strings.Contains(src, want) {
					t.Fatalf("std.%s source missing %q", tc.module, want)
				}
			}
		})
	}
}

func moduleSource(t *testing.T, module string) string {
	t.Helper()
	reg := LoadCached()
	mod := reg.Modules[module]
	if mod == nil {
		t.Fatalf("stdlib %s module missing", module)
	}
	return string(mod.Source)
}
