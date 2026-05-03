package stdlib

import "testing"

func TestProcessModuleExposesExecutionSurface(t *testing.T) {
	reg := LoadCached()

	for _, name := range []string{
		"command", "commandArgs", "shell", "pipeline", "pipe", "run", "runShell", "shellEscape", "joinEscaped",
	} {
		fn := reg.LookupFnDecl("process", name)
		if fn == nil {
			t.Fatalf("LookupFnDecl(process, %s) = nil, want *ast.FnDecl", name)
		}
		if fn.Body == nil {
			t.Fatalf("process.%s body = nil, want source wrapper body", name)
		}
	}

	for _, name := range []string{"abort", "unreachable", "todo"} {
		fn := reg.LookupFnDecl("process", name)
		if fn == nil {
			t.Fatalf("LookupFnDecl(process, %s) = nil, want *ast.FnDecl", name)
		}
		if fn.Body != nil {
			t.Fatalf("process.%s body != nil, want backend-owned primitive", name)
		}
	}

	for _, name := range []string{
		"arg", "withArgs", "withCwd", "withEnv", "withEnvMap", "withTimeoutMillis", "withStdin", "pipe", "run", "shellLine",
	} {
		fn := reg.LookupMethodDecl("process", "ProcessCommand", name)
		if fn == nil {
			t.Fatalf("LookupMethodDecl(process, ProcessCommand, %s) = nil, want *ast.FnDecl", name)
		}
		if fn.Body == nil {
			t.Fatalf("process.ProcessCommand.%s body = nil, want source wrapper body", name)
		}
	}

	for _, name := range []string{"ok", "check"} {
		fn := reg.LookupMethodDecl("process", "ProcessOutput", name)
		if fn == nil {
			t.Fatalf("LookupMethodDecl(process, ProcessOutput, %s) = nil, want *ast.FnDecl", name)
		}
		if fn.Body == nil {
			t.Fatalf("process.ProcessOutput.%s body = nil, want source wrapper body", name)
		}
	}

	for _, name := range []string{
		"stage", "withCwd", "withEnv", "withEnvMap", "withTimeoutMillis", "withStdin", "run", "shellLine",
	} {
		fn := reg.LookupMethodDecl("process", "ProcessPipeline", name)
		if fn == nil {
			t.Fatalf("LookupMethodDecl(process, ProcessPipeline, %s) = nil, want *ast.FnDecl", name)
		}
		if fn.Body == nil {
			t.Fatalf("process.ProcessPipeline.%s body = nil, want source wrapper body", name)
		}
	}
}
