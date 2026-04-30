package stdlib

import "testing"

func TestCmdModuleExposesAutomationBuilderSurface(t *testing.T) {
	reg := LoadCached()

	for _, name := range []string{
		"command", "commandArgs", "shell", "pipeline", "pipe", "shellEscape", "joinEscaped",
	} {
		fn := reg.LookupFnDecl("cmd", name)
		if fn == nil {
			t.Fatalf("LookupFnDecl(cmd, %s) = nil, want *ast.FnDecl", name)
		}
		if fn.Body == nil {
			t.Fatalf("cmd.%s body = nil, want source wrapper body", name)
		}
	}

	for _, name := range []string{
		"arg", "withArgs", "withCwd", "withEnv", "withEnvMap", "withTimeoutMillis", "run", "shellLine",
	} {
		fn := reg.LookupMethodDecl("cmd", "Command", name)
		if fn == nil {
			t.Fatalf("LookupMethodDecl(cmd, Command, %s) = nil, want *ast.FnDecl", name)
		}
		if fn.Body == nil {
			t.Fatalf("cmd.Command.%s body = nil, want source wrapper body", name)
		}
	}

	for _, name := range []string{"ok", "check"} {
		fn := reg.LookupMethodDecl("cmd", "RunOutput", name)
		if fn == nil {
			t.Fatalf("LookupMethodDecl(cmd, RunOutput, %s) = nil, want *ast.FnDecl", name)
		}
		if fn.Body == nil {
			t.Fatalf("cmd.RunOutput.%s body = nil, want source wrapper body", name)
		}
	}

	for _, name := range []string{
		"stage", "withCwd", "withEnv", "withEnvMap", "withTimeoutMillis", "run", "shellLine",
	} {
		fn := reg.LookupMethodDecl("cmd", "Pipeline", name)
		if fn == nil {
			t.Fatalf("LookupMethodDecl(cmd, Pipeline, %s) = nil, want *ast.FnDecl", name)
		}
		if fn.Body == nil {
			t.Fatalf("cmd.Pipeline.%s body = nil, want source wrapper body", name)
		}
	}
}
