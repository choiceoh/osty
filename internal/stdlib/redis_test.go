package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestRedisModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["redis"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.redis not loaded")
	}
	for _, name := range []string{
		"defaultPort", "config", "localhost", "withAuth", "withUserAuth", "withDatabase",
		"withTls", "withConnectTimeout", "withResponseMaxBytes", "addr", "url", "redactedUrl",
		"command", "rawCommand", "withArg", "withArgs", "parts",
		"pipeline", "emptyPipeline", "append", "transaction",
		"renderCommand", "renderCommandBytes", "renderPipeline", "renderPipelineBytes",
		"simpleString", "errorReply", "integer", "bulkString", "nullBulkString", "array", "nullArray",
		"render", "parse", "parsePrefix", "parseComplete", "parseMany", "parseBytes", "parseBytesPrefix", "parseManyBytes", "parseCommand", "commandFromResp",
		"stringValue", "intValue", "boolValue", "expectOk",
		"ping", "pingMessage", "auth", "authUser", "hello", "selectDb",
		"get", "set", "setOptions", "setWithOptions", "withExpireSeconds", "withExpireMs",
		"setKeepTtl", "setIfExists", "setIfMissing", "setGetOldValue",
		"del", "exists", "expire", "pexpire", "ttl", "incr", "incrBy", "decr",
		"hget", "hset", "hdel", "lpush", "rpush", "lpop", "rpop",
		"sadd", "srem", "smembers", "publish", "subscribe",
		"scanOptions", "scanWithPattern", "scanWithCount", "scanWithType", "scan",
		"xadd", "xread", "bootstrapCommands", "connect", "request", "requestPipeline", "close",
		"getString", "setString", "setStringWithOptions", "deleteKeys", "existsKeys", "expireKey",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.redis missing export %q", name)
		}
		if sym.Kind != resolve.SymFn {
			t.Fatalf("std.redis.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.redis.%s not public", name)
		}
	}
	for _, name := range []string{"Resp", "SetMode", "Config", "Client", "Command", "Pipeline", "Parsed", "SetOptions", "ScanOptions"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.redis missing type %q", name)
		}
		if !sym.Pub {
			t.Fatalf("std.redis.%s not public", name)
		}
	}
}

func TestRedisModuleSourcePinsIntegrationHelpers(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["redis"]
	if mod == nil {
		t.Fatal("std.redis module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		`pub fn renderCommand(cmd: Command) -> String`,
		`renderBulkArray(parts(cmd))`,
		`value.bytes().len()`,
		`pub fn parseComplete(text: String) -> Result<Resp, Error>`,
		`pub fn parseBytesPrefix(data: Bytes) -> Result<Parsed, Error>`,
		`pub fn setWithOptions(key: String, value: String, opts: SetOptions) -> Result<Command, Error>`,
		`pub fn scan(cursor: Int, opts: ScanOptions) -> Result<Command, Error>`,
		`pub fn xadd(stream: String, id: String, fields: Map<String, String>) -> Result<Command, Error>`,
		`pub fn bootstrapCommands(cfg: Config) -> Result<Pipeline, Error>`,
		`expectOk(request(client, cmd)?)?`,
		`pub fn getString(client: Client, key: String) -> Result<String?, Error>`,
		`fn parseBytesAt(data: Bytes, start: Int) -> Result<ByteParsed, Error>`,
		`long-lived pooling, TLS, Pub/Sub loops, and cluster routing`,
		`Err(error.new("redis: TLS requires a TLS-capable transport"))`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.redis source missing %q", want)
		}
	}
}
