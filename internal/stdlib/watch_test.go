package stdlib

import (
	"strings"
	"testing"
)

func TestWatchModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["watch"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.watch not loaded")
	}
	for _, name := range []string{
		"options", "snapshot", "snapshotWithOptions", "parseRows", "parseEntry", "parseEntryForRoot",
		"diff", "open", "openWithOptions", "poll", "wait", "watch",
		"task", "transformTask", "shellTask", "syncTask", "runTask", "runTasks", "runLoop",
		"changedPaths", "renderEvents", "summary", "kindName",
	} {
		requirePublicFn(t, mod, "watch", name)
	}
	for _, name := range []string{"EntryKind", "EventKind", "Entry", "Event", "Snapshot", "Options", "Watcher", "Poll", "Task"} {
		requirePublicType(t, mod, "watch", name)
	}
}

func TestWatchModuleSourcePinsPollingDiffAndTaskBehavior(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["watch"]
	if mod == nil {
		t.Fatal("std.watch module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		`pub fn snapshotWithOptions(opts: Options) -> Result<Snapshot, Error>`,
		`parseRows(opts.root, fs.watch(opts.root)?)?`,
		`let parts = strings.splitN(row, "\t", 4)`,
		`pub fn diff(before: Snapshot, after: Snapshot) -> List<Event>`,
		`time.sleep(millisDuration(current.options.pollMillis))?`,
		`pub fn watch(options: Options, handler: fn(List<Event>) -> Result<(), Error>) -> Result<(), Error>`,
		`transformTask(name, cmd.shell(line), suffixes)`,
		`pub fn runLoop(options: Options, tasks: List<Task>) -> Result<(), Error>`,
		`Created -> "created"`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.watch source missing %q", want)
		}
	}
}
