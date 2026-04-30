package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
)

func TestScheduleModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["schedule"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.schedule not loaded")
	}
	for _, name := range []string{
		"seconds", "minutes", "hours", "days",
		"once", "intervalMs", "intervalAnchored", "daily", "dailyAt", "cron", "cronInZone",
		"withMissedPolicy", "defaultRetryPolicy", "retryPolicy",
		"task", "namedTask", "withRetry", "scheduleTask", "pause", "resume", "cancel",
		"isTerminal", "isRepeating", "isDue", "due", "tick", "run", "runDue",
		"markStarted", "markSuccess", "markFailure", "contextFor", "nextWake",
		"nextDelayMs", "nextRunAfter", "nextInterval", "nextDaily",
		"parseCron", "parseCronInZone", "matchesCronAt", "matchesCron", "nextCron",
		"momentAt", "retryDelayMs", "shouldRetry", "retryAfter",
	} {
		requirePublicFn(t, mod, "schedule", name)
	}
	for _, name := range []string{
		"ScheduleKind", "MissedRunPolicy", "TaskState", "RunReason", "Handler",
		"Schedule", "CronField", "CronSpec", "Moment", "RetryPolicy",
		"RetryDecision", "Task", "TaskContext", "DueTask", "Tick",
	} {
		requirePublicType(t, mod, "schedule", name)
	}
}

func TestScheduleModuleSourcePinsSchedulerBehavior(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["schedule"]
	if mod == nil {
		t.Fatal("std.schedule module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		`pub type Handler = fn(TaskContext) -> Result<(), Error>`,
		`pub fn cron(expression: String) -> Result<Schedule, Error>`,
		`pub fn runDue(tasks: List<Task>, nowMs: Int, body: Handler) -> List<Task>`,
		`parseCronField(parts[0], 0, 59, "minute")?`,
		`let dom = fieldMatches(spec.dayOfMonth, moment.dayOfMonth)`,
		`field.values.contains(value) || (value == 0 && field.values.contains(7))`,
		`let delay = if retryable {`,
		`fn civilFromDays(daysSinceEpoch: Int) -> Moment`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.schedule source missing %q", want)
		}
	}
}

func TestScheduleImportResolvesCronAndRetryWorkflow(t *testing.T) {
	src := `
use std.schedule

pub fn body(ctx: schedule.TaskContext) -> Result<(), Error> {
    let _ = ctx.taskId
    Ok(())
}

pub fn demo() -> Result<schedule.Task, Error> {
    let daily = schedule.cron("@daily")?
    let policy = schedule.retryPolicy(4, 100, 1000, 2)?
    let initial = schedule.withRetry(schedule.namedTask("daily_cleanup", "Daily cleanup", daily), policy)
    let planned = schedule.scheduleTask(initial, 0)?
    let due = schedule.due([planned], planned.nextRunMs)
    let _ = schedule.tick([planned], planned.nextRunMs)
    let _ = schedule.momentAt(planned.nextRunMs, 0)
    let failed = schedule.markFailure(planned, planned.nextRunMs, "network timeout")
    let _ = schedule.retryAfter(failed.attempts, policy, failed.lastRunMs)
    let _ = schedule.runDue([failed], failed.nextRunMs, body)
    if due.len() > 0 {
        return Ok(due[0].task)
    }
    Ok(failed)
}
`
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	if len(parseDiags) != 0 {
		t.Fatalf("parse diagnostics: %v", parseDiags)
	}
	res := resolve.ResolveFileSourceDefault([]byte(src), file, Load())
	for _, d := range res.Diags {
		if d == nil || d.Severity != diag.Error {
			continue
		}
		t.Errorf("resolver rejected std.schedule fixture: %s: %s", d.Code, d.Message)
	}
}
