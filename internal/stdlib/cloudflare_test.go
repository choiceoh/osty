package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestCloudflareModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := requireCloudflareModule(t, reg)

	for _, tc := range []struct {
		name string
		kind resolve.SymbolKind
	}{
		{"R2Jurisdiction", resolve.SymEnum},
		{"R2StorageClass", resolve.SymEnum},
		{"R2Permission", resolve.SymEnum},
		{"DnsRecordKind", resolve.SymEnum},
		{"Client", resolve.SymStruct},
		{"TurnstileChallenge", resolve.SymStruct},
		{"TurnstileResult", resolve.SymStruct},
		{"ApiError", resolve.SymStruct},
		{"KvPutOptions", resolve.SymStruct},
		{"QueueMessage", resolve.SymStruct},
		{"QueuePullOptions", resolve.SymStruct},
		{"R2BucketOptions", resolve.SymStruct},
		{"R2TemporaryCredentials", resolve.SymStruct},
		{"R2Signer", resolve.SymStruct},
		{"R2SigningDates", resolve.SymStruct},
		{"DnsRecord", resolve.SymStruct},
		{"CachePurge", resolve.SymStruct},
	} {
		sym := mod.Package.PkgScope.LookupLocal(tc.name)
		if sym == nil {
			t.Fatalf("std.cloudflare missing type %q", tc.name)
		}
		if sym.Kind != tc.kind {
			t.Fatalf("std.cloudflare.%s kind = %s, want %s", tc.name, sym.Kind, tc.kind)
		}
		if !sym.Pub {
			t.Fatalf("std.cloudflare.%s not public", tc.name)
		}
	}

	for _, name := range []string{
		"apiBaseUrl", "client", "account", "zone", "accountZone", "headers", "jsonHeaders",
		"turnstile", "turnstileSiteverifyUrl", "turnstileJsonBody", "turnstileFormValues",
		"turnstileSiteverifyJsonRequest", "turnstileSiteverifyFormRequest", "sendTurnstile", "parseTurnstileResult",
		"accountUrl", "zoneUrl",
		"kvNamespacesUrl", "kvNamespaceUrl", "kvValueUrl",
		"listKvNamespacesHttpRequest", "createKvNamespaceHttpRequest", "renameKvNamespaceHttpRequest",
		"deleteKvNamespaceHttpRequest", "getKvValueHttpRequest", "putKvValueHttpRequest",
		"deleteKvValueHttpRequest", "putKvValueWithOptionsHttpRequest", "kvBulkEntryText", "kvBulkEntryTextWithOptions", "kvBulkPutHttpRequest", "kvBulkDeleteHttpRequest",
		"r2BucketsUrl", "r2BucketUrl", "listR2BucketsHttpRequest", "getR2BucketHttpRequest",
		"r2BucketOptions", "createR2BucketHttpRequest", "createR2BucketWithOptionsHttpRequest", "deleteR2BucketHttpRequest", "r2TempCredentialsHttpRequest",
		"r2S3Endpoint", "r2ObjectUrl", "r2Signer", "r2SigningDates",
		"r2PutObjectHttpRequest", "r2GetObjectHttpRequest", "r2DeleteObjectHttpRequest",
		"queueBaseUrl", "queueUrl", "listQueuesHttpRequest", "createQueueHttpRequest",
		"queueTextMessage", "queueJsonMessage", "queueMessageJson",
		"pushQueueMessageHttpRequest", "pushQueueBatchHttpRequest", "pushQueueBatchWithDelayHttpRequest",
		"queuePullOptions", "pullQueueMessagesHttpRequest", "pullQueueMessagesWithOptionsHttpRequest",
		"ackQueueMessagesHttpRequest", "ackRetryQueueMessagesHttpRequest", "purgeQueueHttpRequest",
		"dnsRecordsUrl", "dnsRecordUrl", "dnsRecord", "listDnsRecordsHttpRequest",
		"listDnsRecordsFilteredHttpRequest", "createDnsRecordHttpRequest", "updateDnsRecordHttpRequest", "deleteDnsRecordHttpRequest",
		"purgeEverything", "purgeFiles", "purgeTags", "purgeHosts", "purgePrefixes", "cachePurgeHttpRequest",
		"workerUrl", "workerInvokeEmptyHttpRequest", "workerInvokeEmptyWithTokenHttpRequest",
		"workerInvokeHttpRequest", "workerInvokeWithTokenHttpRequest", "workerInvokeJsonHttpRequest", "workerInvokeJsonWithTokenHttpRequest",
		"workerScriptUrl", "getWorkerScriptHttpRequest", "deleteWorkerScriptHttpRequest",
		"sendJson", "requireSuccess", "parseApiError",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.cloudflare missing export %q", name)
		}
		if sym.Kind != resolve.SymFn {
			t.Fatalf("std.cloudflare.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.cloudflare.%s not public", name)
		}
	}
}

func TestCloudflareModuleMethodsAreBodied(t *testing.T) {
	reg := LoadCached()
	for _, tc := range []struct {
		typeName string
		methods  []string
	}{
		{"R2Jurisdiction", []string{"toString"}},
		{"R2StorageClass", []string{"toString"}},
		{"R2Permission", []string{"toString"}},
		{"DnsRecordKind", []string{"toString"}},
		{"Client", []string{"withAccountId", "withZoneId", "withBaseUrl", "withClientInfo", "withHeader"}},
		{"TurnstileChallenge", []string{"withRemoteIp", "withIdempotencyKey"}},
		{"TurnstileResult", []string{"summary"}},
		{"ApiError", []string{"summary"}},
		{"KvPutOptions", []string{"withExpiration", "withExpirationTtl"}},
		{"QueueMessage", []string{"withDelay"}},
		{"QueuePullOptions", []string{"withBatchSize", "withVisibilityTimeout"}},
		{"R2Signer", []string{"withSessionToken"}},
		{"CachePurge", []string{"withFile", "withTag", "withHost", "withPrefix"}},
	} {
		for _, method := range tc.methods {
			fn := reg.LookupMethodDecl("cloudflare", tc.typeName, method)
			if fn == nil {
				t.Fatalf("LookupMethodDecl(cloudflare, %s, %s) = nil, want *ast.FnDecl", tc.typeName, method)
			}
			if fn.Body == nil {
				t.Fatalf("cloudflare.%s.%s body = nil, want source method body", tc.typeName, method)
			}
		}
	}
}

func TestCloudflareModulePinsEndpointAndSigningBehavior(t *testing.T) {
	reg := LoadCached()
	mod := requireCloudflareModule(t, reg)
	src := string(mod.Source)
	for _, want := range []string{
		`https://api.cloudflare.com/client/v4`,
		`https://challenges.cloudflare.com/turnstile/v0/siteverify`,
		`storage/kv/namespaces`,
		`/values/{encodeKeyName(key)?}`,
		`r2/buckets`,
		`r2/temp-access-credentials`,
		`https://{account}.r2.cloudflarestorage.com`,
		`AWS4-HMAC-SHA256`,
		`{dates.shortDate}/auto/s3/aws4_request`,
		`crypto.hmac.sha256(kRegion, bytes.fromString("s3"))`,
		`queues/{queue}`,
		`messages/batch`,
		`messages/ack`,
		`dns_records/{cleanRecordId}`,
		`zoneUrl(client, "purge_cache")`,
		`purge_everything`,
		`.workers.dev`,
		`workers/scripts/{encodePathSegment(cleanWorkerScriptName(scriptName)?)}`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.cloudflare source missing %q", want)
		}
	}
}

func requireCloudflareModule(t *testing.T, reg *Registry) *Module {
	t.Helper()
	mod := reg.Modules["cloudflare"]
	if mod == nil || mod.Package == nil || mod.Package.PkgScope == nil {
		var details []string
		for _, d := range reg.Diags {
			if d != nil && (strings.Contains(d.File, "cloudflare") || strings.Contains(d.Message, "cloudflare")) {
				details = append(details, d.Error())
			}
		}
		t.Fatalf("std.cloudflare not fully loaded; diagnostics=%v", details)
	}
	return mod
}
