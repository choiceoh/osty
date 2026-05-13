package runner

import "testing"

func TestIsValidScaffoldName(t *testing.T) {
	valid := []string{
		"myproj", "my-tool", "my_tool", "_private",
		"A", "abc123", "a_b-c_0-9",
	}
	for _, n := range valid {
		if !IsValidScaffoldName(n) {
			t.Errorf("IsValidScaffoldName(%q) = false, want true", n)
		}
	}
	invalid := []string{
		"", "1abc", "9", "-abc",
		"my.tool", "my tool", "my/tool", "my@tool",
		"한글",
	}
	for _, n := range invalid {
		if IsValidScaffoldName(n) {
			t.Errorf("IsValidScaffoldName(%q) = true, want false", n)
		}
	}
}

func TestResolveFixtureCases(t *testing.T) {
	cases := []struct {
		requested int
		want      FixtureCaseCount
	}{
		{0, FixtureCaseCount{Count: 3, OverCap: false}},
		{-5, FixtureCaseCount{Count: 3, OverCap: false}},
		{1, FixtureCaseCount{Count: 1, OverCap: false}},
		{10, FixtureCaseCount{Count: 10, OverCap: false}},
		{64, FixtureCaseCount{Count: 64, OverCap: false}},
		{65, FixtureCaseCount{Count: 64, OverCap: true}},
		{1000, FixtureCaseCount{Count: 64, OverCap: true}},
	}
	for _, c := range cases {
		got := ResolveFixtureCases(c.requested)
		if got != c.want {
			t.Errorf("ResolveFixtureCases(%d) = %+v, want %+v", c.requested, got, c.want)
		}
	}
}

func TestScaffoldFixtureCapsMatchOstyConstants(t *testing.T) {
	if ScaffoldFixtureCasesDefault != 3 {
		t.Errorf("ScaffoldFixtureCasesDefault = %d, want 3", ScaffoldFixtureCasesDefault)
	}
	if ScaffoldFixtureCasesMax != 64 {
		t.Errorf("ScaffoldFixtureCasesMax = %d, want 64", ScaffoldFixtureCasesMax)
	}
}

func TestScaffoldDefaultWorkspaceMember(t *testing.T) {
	if got := ScaffoldDefaultWorkspaceMember(); got != "core" {
		t.Errorf("ScaffoldDefaultWorkspaceMember() = %q, want %q", got, "core")
	}
}

func TestScaffoldRelativePaths(t *testing.T) {
	cases := []struct {
		name    string
		kind    string
		member  string
		want    []string
	}{
		{
			"bin", "bin", "",
			[]string{"osty.toml", "main.osty", "main_test.osty", ".gitignore"},
		},
		{
			"lib", "lib", "",
			[]string{"osty.toml", "lib.osty", "lib_test.osty", ".gitignore"},
		},
		{
			"workspace-default-member", "workspace", "",
			[]string{
				"osty.toml", ".gitignore",
				"core/osty.toml", "core/main.osty", "core/main_test.osty", "core/.gitignore",
			},
		},
		{
			"workspace-custom-member", "workspace", "alpha",
			[]string{
				"osty.toml", ".gitignore",
				"alpha/osty.toml", "alpha/main.osty", "alpha/main_test.osty", "alpha/.gitignore",
			},
		},
		{
			"cli", "cli", "",
			[]string{"osty.toml", "main.osty", "args.osty", "app.osty", "app_test.osty", ".gitignore"},
		},
		{
			"service", "service", "",
			[]string{"osty.toml", "main.osty", "routes.osty", "routes_test.osty", ".gitignore"},
		},
		{
			"gui-qtquick", "gui-qtquick", "",
			[]string{"osty.toml", "main.osty", "ui/main.qml", "README.md", ".gitignore"},
		},
		{
			"gui-webview2", "gui-webview2", "",
			[]string{
				"osty.toml",
				"src/main.osty", "src/main_test.osty",
				"ui/index.html", "ui/app.css", "ui/app.js",
				"assets/.gitkeep", ".gitignore",
			},
		},
		{
			"unknown-falls-back-to-bin", "nonsense", "",
			[]string{"osty.toml", "main.osty", "main_test.osty", ".gitignore"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ScaffoldRelativePaths(c.kind, c.member)
			if len(got) != len(c.want) {
				t.Fatalf("ScaffoldRelativePaths(%q, %q) length = %d, want %d\n got: %v\nwant: %v",
					c.kind, c.member, len(got), len(c.want), got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("path[%d] = %q, want %q", i, got[i], c.want[i])
				}
			}
		})
	}
}
