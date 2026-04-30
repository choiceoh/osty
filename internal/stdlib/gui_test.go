package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestGuiModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["gui"]
	if mod == nil || mod.Package == nil || mod.Package.PkgScope == nil {
		t.Fatalf("std.gui not loaded with package scope; registry diagnostics:\n%s", stdlibRebindDiagSummary(reg))
	}

	for _, tc := range []struct {
		name string
		kind resolve.SymbolKind
	}{
		{"Point", resolve.SymStruct},
		{"Size", resolve.SymStruct},
		{"Rect", resolve.SymStruct},
		{"Insets", resolve.SymStruct},
		{"Radius", resolve.SymStruct},
		{"Color", resolve.SymEnum},
		{"Paint", resolve.SymEnum},
		{"Style", resolve.SymStruct},
		{"Theme", resolve.SymStruct},
		{"LayoutMode", resolve.SymEnum},
		{"Layout", resolve.SymStruct},
		{"NodeKind", resolve.SymEnum},
		{"Role", resolve.SymEnum},
		{"Accessibility", resolve.SymStruct},
		{"Node", resolve.SymStruct},
		{"PointerEvent", resolve.SymStruct},
		{"KeyEvent", resolve.SymStruct},
		{"GuiEvent", resolve.SymEnum},
		{"BrowserEventKind", resolve.SymEnum},
		{"BrowserEvent", resolve.SymStruct},
		{"BrowserLauncher", resolve.SymEnum},
		{"BrowserLaunchPlan", resolve.SymStruct},
		{"BrowserLaunchResult", resolve.SymStruct},
		{"RenderCommand", resolve.SymEnum},
		{"RenderFrame", resolve.SymStruct},
		{"Snapshot", resolve.SymStruct},
		{"SnapshotDiff", resolve.SymStruct},
		{"AccessibilityReport", resolve.SymStruct},
	} {
		sym := mod.Package.PkgScope.LookupLocal(tc.name)
		if sym == nil {
			t.Fatalf("std.gui missing type %q", tc.name)
		}
		if sym.Kind != tc.kind {
			t.Fatalf("std.gui.%s kind = %s, want %s", tc.name, sym.Kind, tc.kind)
		}
		if !sym.Pub {
			t.Fatalf("std.gui.%s not public", tc.name)
		}
	}

	for _, name := range []string{
		"point", "size", "rect", "emptyRect", "insets", "insetsXY", "insetsLTRB", "radius",
		"rgb", "rgba", "solid", "border", "defaultBorder", "defaultShadow",
		"defaultTextStyle", "defaultStyle", "defaultTheme", "darkTheme", "styleFromTheme",
		"layout", "rowLayout", "columnLayout", "stackLayout", "grow", "fixed",
		"constraints", "node", "root", "layer", "row", "column", "panel", "label",
		"button", "textInput", "checkbox", "image", "spacer", "computeLayout",
		"render", "routePointer", "focusOrder", "nextFocus", "previousFocus",
		"snapshot", "snapshotTree", "diffSnapshots", "auditAccessibility", "breakpoint", "describeTree",
		"renderHtml", "htmlDocument", "renderSvg", "htmlStyleSheet",
		"browserHtmlDocument", "browserScript", "decodeBrowserEvent", "browserEventTarget", "browserGuiEvent",
		"macBrowserLauncher", "xdgBrowserLauncher", "windowsBrowserLauncher", "commandBrowserLauncher",
		"browserLaunchPlan", "writeBrowserDocument", "openBrowserDocument", "launchBrowserDocument",
		"setNodeValue", "setNodeChecked", "setNodeDisabled", "applyBrowserEvent", "applyDecodedBrowserEvent",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.gui missing export %q", name)
		}
		if sym.Kind != resolve.SymFn {
			t.Fatalf("std.gui.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.gui.%s not public", name)
		}
	}
}

func TestGuiModulePinsRetainedArchitecture(t *testing.T) {
	src := guiModuleSource(t)
	for _, want := range []string{
		"backend-neutral GUI primitives",
		"pub enum RenderCommand",
		"pub struct SnapshotDiff",
		"pub fn computeLayout(rootNode: Node, viewport: Size) -> Node",
		"pub fn routePointer(rootNode: Node, event: PointerEvent) -> EventTarget?",
		"pub fn auditAccessibility(rootNode: Node) -> AccessibilityReport",
		"pub fn describeTree(rootNode: Node) -> String",
		"pub fn renderHtml(rootNode: Node, viewport: Size) -> String",
		"pub fn htmlDocument(title: String, rootNode: Node, viewport: Size) -> String",
		"pub fn browserHtmlDocument(title: String, rootNode: Node, viewport: Size) -> String",
		"pub fn renderSvg(rootNode: Node, viewport: Size) -> String",
		"pub fn writeBrowserDocument(path: String, title: String, rootNode: Node, viewport: Size) -> Result<String, Error>",
		"pub fn openBrowserDocument(path: String, launcher: BrowserLauncher) -> Result<BrowserLaunchResult, Error>",
		"pub fn launchBrowserDocument(path: String, title: String, rootNode: Node, viewport: Size, launcher: BrowserLauncher) -> Result<BrowserLaunchResult, Error>",
		"pub fn browserLaunchPlan(path: String, launcher: BrowserLauncher) -> BrowserLaunchPlan",
		"pub fn decodeBrowserEvent(payload: String) -> Result<BrowserEvent, Error>",
		"pub fn browserEventTarget(payload: String) -> Result<EventTarget, Error>",
		"pub fn setNodeValue(rootNode: Node, id: String, value: String) -> Node",
		"pub fn setNodeChecked(rootNode: Node, id: String, checked: Bool) -> Node",
		"pub fn setNodeDisabled(rootNode: Node, id: String, disabled: Bool) -> Node",
		"pub fn applyBrowserEvent(rootNode: Node, payload: String) -> Result<Node, Error>",
		"pub fn applyDecodedBrowserEvent(rootNode: Node, event: BrowserEvent) -> Node",
		"window.OstyGui&&window.OstyGui.postMessage",
		"osty-gui-event",
		"fs.writeString(path, html)?",
		"os.exec(plan.command, plan.args)?",
		"WindowsStart -> BrowserLaunchPlan { command: \"cmd\", args: [\"/c\", \"start\", \"\", path] }",
		"CommandLauncher(command) -> BrowserLaunchPlan { command: command, args: [path] }",
		"node.value = event.value",
		"node.access.checked = event.checked",
		"children.push(applyBrowserState(child, event))",
		"layoutFlow(children, bounds, true, layout)",
		"data-gui-id",
		"<button{attrs} type=\\\"button\\\"",
		"<input{attrs} type=\\\"text\\\"",
		"<svg xmlns=\\\"http://www.w3.org/2000/svg\\\"",
		"FocusRing(node.frame, rgb(59, 130, 246), node.style.radius)",
		"gui.accessibility: interactive node",
		"gui.accessibility: text input",
		"diff.moved.push(id)",
		"breakpoint(viewport: Size) -> Breakpoint",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.gui source missing %q", want)
		}
	}
}

func TestGuiModuleMethodsAreBodied(t *testing.T) {
	reg := LoadCached()
	for _, tc := range []struct {
		typeName string
		methods  []string
	}{
		{"Point", []string{"add", "sub", "distanceTo"}},
		{"Size", []string{"isEmpty", "area", "clamp"}},
		{"Rect", []string{"contains", "intersects", "intersection", "inset", "translate"}},
		{"Insets", []string{"horizontal", "vertical"}},
		{"Style", []string{"withBackground", "withForeground", "withPadding", "withMargin", "withRadius", "withBorder", "hasBackground", "contentRect"}},
		{"Accessibility", []string{"isInteractive", "hasName"}},
		{"Node", []string{"withId", "withText", "withValue", "withChecked", "withDisabled", "withStyle", "withLayout", "withAccess", "add", "addAll", "isInteractive", "focusable", "measure", "layoutIn", "hitTest", "findById"}},
		{"SnapshotDiff", []string{"isEmpty"}},
		{"AccessibilityReport", []string{"ok"}},
	} {
		for _, method := range tc.methods {
			fn := reg.LookupMethodDecl("gui", tc.typeName, method)
			if fn == nil {
				t.Fatalf("LookupMethodDecl(gui, %s, %s) = nil, want *ast.FnDecl", tc.typeName, method)
			}
			if fn.Body == nil {
				t.Fatalf("gui.%s.%s body = nil, want source method body", tc.typeName, method)
			}
		}
	}
}

func TestGuiModuleImportSurfaceIncludesRenderers(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["gui"]
	if mod == nil || mod.Package == nil {
		t.Fatal("stdlib gui module missing package")
	}
	surface := resolve.PackageExportSurface("std.gui", "gui", mod.Package)
	for _, want := range []string{
		"column",
		"root",
		"size",
		"renderHtml",
		"htmlDocument",
		"browserHtmlDocument",
		"renderSvg",
		"macBrowserLauncher",
		"browserLaunchPlan",
		"writeBrowserDocument",
		"openBrowserDocument",
		"launchBrowserDocument",
		"setNodeValue",
		"setNodeChecked",
		"setNodeDisabled",
		"applyBrowserEvent",
		"applyDecodedBrowserEvent",
		"decodeBrowserEvent",
		"browserEventTarget",
	} {
		found := false
		for _, fn := range surface.Functions {
			if fn.Owner == "gui" && fn.Name == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("std.gui import surface missing alias function %q; functions=%d fields=%d types=%d",
				want, len(surface.Functions), len(surface.Fields), len(surface.TypeDecls))
		}
	}
}

func TestGuiQtQuickModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["gui.qtquick"]
	if mod == nil || mod.Package == nil || mod.Package.PkgScope == nil {
		t.Fatalf("std.gui.qtquick not loaded with package scope; registry diagnostics:\n%s", stdlibRebindDiagSummary(reg))
	}
	for _, name := range []string{"App", "Window", "WindowOptions", "Event", "RuntimeInfo", "RuntimeDiagnostic"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.gui.qtquick missing type %q", name)
		}
		if !sym.Pub {
			t.Fatalf("std.gui.qtquick.%s not public", name)
		}
	}
	for _, name := range []string{"app", "windowOptions", "defaultWindowOptions", "runtimeInfo", "runtimeDiagnostic", "abiVersion", "available", "lastError", "lastErrorMessage"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.gui.qtquick missing export %q", name)
		}
		if sym.Kind != resolve.SymFn {
			t.Fatalf("std.gui.qtquick.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.gui.qtquick.%s not public", name)
		}
	}
	if fn := reg.LookupMethodDecl("gui.qtquick", "App", "events"); fn == nil {
		t.Fatalf("LookupMethodDecl(gui.qtquick, App, events) = nil, want event polling surface")
	}
	if fn := reg.LookupMethodDecl("gui.qtquick", "App", "addImportPath"); fn == nil {
		t.Fatalf("LookupMethodDecl(gui.qtquick, App, addImportPath) = nil, want QML import path surface")
	}
	if fn := reg.LookupMethodDecl("gui.qtquick", "Window", "reload"); fn == nil {
		t.Fatalf("LookupMethodDecl(gui.qtquick, Window, reload) = nil, want QML reload surface")
	}
	src := guiQtQuickModuleSource(t)
	for _, want := range []string{
		`fn osty_qt_app_add_import_path(app: Int, path: String) -> Bool`,
		`fn osty_qt_window_reload(window: Int) -> Bool`,
		`pub fn runtimeDiagnostic() -> RuntimeDiagnostic`,
		`strings.concat(s, "")`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.gui.qtquick source missing %q", want)
		}
	}
}

func guiModuleSource(t *testing.T) string {
	t.Helper()
	reg := LoadCached()
	mod := reg.Modules["gui"]
	if mod == nil {
		t.Fatal("stdlib gui module missing")
	}
	return string(mod.Source)
}

func guiQtQuickModuleSource(t *testing.T) string {
	t.Helper()
	reg := LoadCached()
	mod := reg.Modules["gui.qtquick"]
	if mod == nil {
		t.Fatal("stdlib gui.qtquick module missing")
	}
	return string(mod.Source)
}
