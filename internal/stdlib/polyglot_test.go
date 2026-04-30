package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestPolyglotModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["polyglot"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.polyglot not loaded")
	}

	for _, tc := range []struct {
		name string
		kind resolve.SymbolKind
	}{
		{"Language", resolve.SymEnum},
		{"BoundaryProtocol", resolve.SymEnum},
		{"LinkMode", resolve.SymEnum},
		{"ComponentKind", resolve.SymEnum},
		{"ContractStability", resolve.SymEnum},
		{"CheckKind", resolve.SymEnum},
		{"CheckStatus", resolve.SymEnum},
		{"RetryPolicy", resolve.SymStruct},
		{"ExecutionPolicy", resolve.SymStruct},
		{"BoundaryContract", resolve.SymStruct},
		{"ExecPlan", resolve.SymStruct},
		{"Boundary", resolve.SymStruct},
		{"PolyglotCheck", resolve.SymStruct},
		{"CheckRun", resolve.SymStruct},
		{"DoctorRunReport", resolve.SymStruct},
		{"DoctorReport", resolve.SymStruct},
		{"Component", resolve.SymStruct},
		{"Workspace", resolve.SymStruct},
	} {
		sym := mod.Package.PkgScope.LookupLocal(tc.name)
		if sym == nil {
			t.Fatalf("std.polyglot missing type %q", tc.name)
		}
		if sym.Kind != tc.kind {
			t.Fatalf("std.polyglot.%s kind = %s, want %s", tc.name, sym.Kind, tc.kind)
		}
		if !sym.Pub {
			t.Fatalf("std.polyglot.%s not public", tc.name)
		}
	}

	for _, name := range []string{
		"languageName", "parseLanguage", "sourceExtension", "runtimeCommand", "compilerCommand",
		"languageFromExtension", "languageFromPath", "sameLanguage", "isTwoLanguage",
		"protocolName", "linkModeName", "componentKindName",
		"stabilityName", "checkKindName", "checkStatusName", "retryPolicy",
		"strictPolicy", "relaxedPolicy",
		"command", "command0", "scriptPlan", "scriptPlan0", "compilePlan", "compilePlan0",
		"testPlan", "testPlan0", "buildPlan", "buildPlan0",
		"contract", "stableContract", "frozenContract", "contractWithStability",
		"contractSchemas", "contractSchemaHash", "contractInvariant", "contractRequiredEnv",
		"contractSecretEnv", "contractArtifact", "contractLimits", "contractRetry",
		"majorVersion", "compatibleMajor", "contractSummary", "contractFingerprint", "validateContract",
		"checked", "unchecked", "withArg", "withArgs", "withEnv", "withCwd",
		"boundary", "commandBoundary", "linkedBoundary", "withContract",
		"boundaryWithContractSpec", "boundaryId",
		"component", "goGateway", "rustCore", "pythonWorker", "jsWorker", "nodeTooling",
		"withBuild", "withTest", "withArtifact", "withResponsibility",
		"workspace", "goRustWorkspace", "goRustCAbi", "ostyWith", "ostyGoService",
		"ostyRustCore", "ostyPythonWorker", "ostyNodeTooling", "withSharedProtocol",
		"workspaceWithPolicy", "workspaceWithContract", "workspaceWithCheck",
		"buildPlans", "testPlans", "checkPlan", "checkLimits", "checkRetry",
		"componentBuildCheck", "componentTestCheck",
		"boundaryContractCheck", "toolchainCheck", "defaultChecks", "runCheck",
		"runCheckWithPolicy", "runChecks", "runChecksWithPolicy", "checkRunOk",
		"checkRunFailed", "renderCheckRun",
		"doctor", "doctorRun", "renderDoctor", "renderDoctorRun", "validateWorkspace", "workspaceSummary",
		"run", "runBoundary", "commandLine", "describe",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.polyglot missing export %q", name)
		}
		if sym.Kind != resolve.SymFn {
			t.Fatalf("std.polyglot.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.polyglot.%s not public", name)
		}
	}
}

func TestPolyglotModuleSourcePinsTwoLanguageBoundaryBehavior(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["polyglot"]
	if mod == nil {
		t.Fatal("std.polyglot module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		`pub enum LinkMode`,
		`pub enum ComponentKind`,
		`pub struct BoundaryContract`,
		`pub struct DoctorRunReport`,
		`pub struct DoctorReport`,
		`pub struct Workspace`,
		`pub fn goRustWorkspace(goRoot: String, rustRoot: String) -> Result<Workspace, Error>`,
		`pub fn ostyRustCore(ostyRoot: String, rustRoot: String) -> Result<Workspace, Error>`,
		`pub fn ostyPythonWorker(ostyRoot: String, pythonRoot: String) -> Result<Workspace, Error>`,
		`linkedBoundary(Go, Rust, Json, CAbi, boundaryPlan)?`,
		`boundaryWithContractSpec(b, c)?`,
		`Osty keeps application policy; the guest language supplies mature ecosystem or runtime depth.`,
		`pub fn runCheckWithPolicy(check: PolyglotCheck, policy: ExecutionPolicy) -> CheckRun`,
		`pub fn doctorRun(ws: Workspace) -> DoctorRunReport`,
		`contractAuditErrors(c, policy)`,
		`required env {name} is not set`,
		`pub fn doctor(ws: Workspace) -> DoctorReport`,
		`policy.requireContracts`,
		`defaultChecks(ws)`,
		`validateWorkspace(ws)?`,
		`restoreEnv(backups)`,
		`if !plan.allowNonZero && output.exitCode != 0`,
		`"polyglot: workspace needs at least one explicit boundary"`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.polyglot source missing %q", want)
		}
	}
}
