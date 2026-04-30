package stdlib

import (
	"strings"
	"testing"
)

func TestRpaModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["rpa"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.rpa not loaded")
	}
	for _, name := range []string{
		"point", "bounds", "windowQuery", "titleContains", "appContains",
		"processContains", "windowID", "exactTitle", "macBackend",
		"linuxX11Backend", "linuxWaylandBackend", "windowsBackend",
		"customBackend", "moveTo", "clickAt", "doubleClickAt", "rightClickAt",
		"click", "drag", "scroll", "typeText", "keyTap", "hotkey",
		"pasteText", "findWindowsAction", "focus", "waitWindow", "pause",
		"script", "repeatAction", "plan", "plans", "run", "runScript",
		"findWindows", "firstWindow", "focusFirstWindow", "parseWindowList",
		"filterWindows", "matchesWindow", "summarize",
	} {
		requirePublicFn(t, mod, "rpa", name)
	}
	for _, name := range []string{
		"Platform", "MouseButton", "ScrollDirection", "Modifier", "MatchMode",
		"WindowState", "Point", "Bounds", "WindowQuery", "WindowInfo",
		"Backend", "CommandPlan", "Action", "ActionResult", "Script",
	} {
		requirePublicType(t, mod, "rpa", name)
	}
}

func TestRpaModulePinsDesktopAutomationBehavior(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["rpa"]
	if mod == nil {
		t.Fatal("std.rpa module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		`std.rpa - desktop automation plans for repetitive work`,
		`pub enum Action`,
		`MoveMouse(Point)`,
		`ClickMouse(Point, MouseButton, Int)`,
		`KeyTap(List<Modifier>, String)`,
		`FindWindows(WindowQuery)`,
		`pub fn macBackend() -> Backend`,
		`mouseCommand: "cliclick"`,
		`windowCommand: "osascript"`,
		`pub fn linuxX11Backend() -> Backend`,
		`mouseCommand: "xdotool"`,
		`pub fn windowsBackend() -> Backend`,
		`windowCommand: "powershell"`,
		`pub fn runScript(scriptValue: Script, backend: Backend) -> Result<List<ActionResult>, Error>`,
		`pub fn findWindows(query: WindowQuery, backend: Backend) -> Result<List<WindowInfo>, Error>`,
		`pub fn parseWindowList(text: String, platform: Platform) -> List<WindowInfo>`,
		`filterWindows(parseWindowList(output.stdout, backend.platform), query)`,
		`xdotool search --onlyvisible --name`,
		`System Events`,
		`Get-Process | Where-Object`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.rpa source missing %q", want)
		}
	}
}

func TestRpaModuleMethodsAreBodied(t *testing.T) {
	reg := LoadCached()
	for _, tc := range []struct {
		typeName string
		methods  []string
	}{
		{"Platform", []string{"toString"}},
		{"MouseButton", []string{"number", "cliclickName"}},
		{"Point", []string{"translate", "toPair"}},
		{"Bounds", []string{"isEmpty", "contains", "center"}},
		{"WindowQuery", []string{"matches", "withLimit", "visible"}},
		{"WindowInfo", []string{"label"}},
		{"Backend", []string{"dry", "withMouseCommand", "withKeyboardCommand", "withWindowCommand"}},
		{"CommandPlan", []string{"commandLine", "sensitive"}},
		{"Action", []string{"description"}},
		{"ActionResult", []string{"ok"}},
		{"Script", []string{"append", "delay", "repeat"}},
	} {
		for _, method := range tc.methods {
			fn := reg.LookupMethodDecl("rpa", tc.typeName, method)
			if fn == nil {
				t.Fatalf("LookupMethodDecl(rpa, %s, %s) = nil, want *ast.FnDecl", tc.typeName, method)
			}
			if fn.Body == nil {
				t.Fatalf("rpa.%s.%s body = nil, want source method body", tc.typeName, method)
			}
		}
	}
}
