// core_snapshot.go snapshots the Osty-authored CI support surface into the
// native package while toolchain sources remain the long-term owner.

package ci

import (
	host "github.com/osty/osty/internal/cihost"
	"math"
	ciStrings "strings"
)

// Osty: toolchain/ci.osty:113:5
type CheckName = string

// Osty: toolchain/ci.osty:115:5
var CheckFormat CheckName = "format"

// Osty: toolchain/ci.osty:116:5
var CheckLint CheckName = "lint"

// Osty: toolchain/ci.osty:117:5
var CheckPolicy CheckName = "policy"

// Osty: toolchain/ci.osty:118:5
var CheckLockfile CheckName = "lockfile"

// Osty: toolchain/ci.osty:119:5
var CheckRelease CheckName = "release"

// Osty: toolchain/ci.osty:120:5
var CheckSemver CheckName = "semver"

// Osty: toolchain/ci.osty:123:5
type Options struct {
	Format         bool
	Lint           bool
	Policy         bool
	Lockfile       bool
	Release        bool
	Semver         bool
	SemverWarnOnly bool
	Strict         bool
	Baseline       string
	MaxFileBytes   int
}

// Osty: toolchain/ci.osty:137:5
type Check struct {
	Name    CheckName
	Passed  bool
	Skipped bool
	Note    string
	Diags   []*host.Diagnostic
}

// Osty: toolchain/ci.osty:146:5
type Report struct {
	ProjectRoot string
	StartedAt   host.Time
	FinishedAt  host.Time
	Checks      []*Check
}

// Osty: toolchain/ci.osty:152:9
func (self *Report) AllPassed() bool {
	// Osty: toolchain/ci.osty:153:9
	for _, c := range self.Checks {
		// Osty: toolchain/ci.osty:154:13
		if c.Skipped {
			// Osty: toolchain/ci.osty:155:17
			_ = c
		} else if !(c.Passed) {
			// Osty: toolchain/ci.osty:157:17
			return false
		}
	}
	return true
}

// Osty: toolchain/ci.osty:165:5
type Runner struct {
	Root      string
	Opts      *Options
	Manifest  *host.Manifest
	Workspace *host.ResolveWorkspace
	Packages  []*host.ResolvePackage
	Results   []*host.PackageResult
}

// Osty: toolchain/ci.osty:173:9
func (self *Runner) Load() string {
	// Osty: toolchain/ci.osty:174:9
	loaded := host.LoadRunnerState(self.Root, self.Manifest)
	_ = loaded
	// Osty: toolchain/ci.osty:175:9
	if loaded.Error != "" {
		// Osty: toolchain/ci.osty:176:13
		return loaded.Error
	}
	// Osty: toolchain/ci.osty:178:13
	self.Root = loaded.Root
	// Osty: toolchain/ci.osty:179:13
	self.Manifest = loaded.Manifest
	// Osty: toolchain/ci.osty:180:13
	self.Workspace = loaded.Workspace
	// Osty: toolchain/ci.osty:181:13
	self.Packages = loaded.Packages
	// Osty: toolchain/ci.osty:182:13
	self.Results = loaded.Results
	return ""
}

// Osty: toolchain/ci.osty:186:9
func (self *Runner) Run() *Report {
	// Osty: toolchain/ci.osty:187:9
	rep := &Report{ProjectRoot: self.Root, StartedAt: host.NowUTC(), FinishedAt: host.NowUTC(), Checks: make([]*Check, 0, 1)}
	_ = rep
	// Osty: toolchain/ci.osty:194:9
	if self.Opts.Format {
		// Osty: toolchain/ci.osty:195:13
		func() struct{} { rep.Checks = append(rep.Checks, self.checkFormat()); return struct{}{} }()
	} else {
		// Osty: toolchain/ci.osty:197:13
		func() struct{} { rep.Checks = append(rep.Checks, skipped(CheckFormat)); return struct{}{} }()
	}
	// Osty: toolchain/ci.osty:199:9
	if self.Opts.Policy {
		// Osty: toolchain/ci.osty:200:13
		func() struct{} { rep.Checks = append(rep.Checks, self.checkPolicy()); return struct{}{} }()
	} else {
		// Osty: toolchain/ci.osty:202:13
		func() struct{} { rep.Checks = append(rep.Checks, skipped(CheckPolicy)); return struct{}{} }()
	}
	// Osty: toolchain/ci.osty:204:9
	if self.Opts.Lockfile {
		// Osty: toolchain/ci.osty:205:13
		func() struct{} { rep.Checks = append(rep.Checks, self.checkLockfile()); return struct{}{} }()
	} else {
		// Osty: toolchain/ci.osty:207:13
		func() struct{} { rep.Checks = append(rep.Checks, skipped(CheckLockfile)); return struct{}{} }()
	}
	// Osty: toolchain/ci.osty:209:9
	if self.Opts.Lint {
		// Osty: toolchain/ci.osty:210:13
		func() struct{} { rep.Checks = append(rep.Checks, self.checkLint()); return struct{}{} }()
	} else {
		// Osty: toolchain/ci.osty:212:13
		func() struct{} { rep.Checks = append(rep.Checks, skipped(CheckLint)); return struct{}{} }()
	}
	// Osty: toolchain/ci.osty:214:9
	if self.Opts.Release {
		// Osty: toolchain/ci.osty:215:13
		func() struct{} { rep.Checks = append(rep.Checks, self.checkRelease()); return struct{}{} }()
	} else {
		// Osty: toolchain/ci.osty:217:13
		func() struct{} { rep.Checks = append(rep.Checks, skipped(CheckRelease)); return struct{}{} }()
	}
	// Osty: toolchain/ci.osty:219:9
	if self.Opts.Semver {
		// Osty: toolchain/ci.osty:220:13
		func() struct{} { rep.Checks = append(rep.Checks, self.checkSemver()); return struct{}{} }()
	} else {
		// Osty: toolchain/ci.osty:222:13
		func() struct{} { rep.Checks = append(rep.Checks, skipped(CheckSemver)); return struct{}{} }()
	}
	// Osty: toolchain/ci.osty:225:9
	for _, c := range rep.Checks {
		// Osty: toolchain/ci.osty:226:13
		if c.Skipped {
			// Osty: toolchain/ci.osty:227:17
			_ = c
		} else {
			// Osty: toolchain/ci.osty:229:18
			c.Passed = ciCheckHostPassed(c.Diags, self.Opts.Strict)
		}
	}
	// Osty: toolchain/ci.osty:232:12
	rep.FinishedAt = host.NowUTC()
	return rep
}

// Osty: toolchain/ci.osty:236:5
func (self *Runner) checkFormat() *Check {
	return checkFromHostResult(CheckFormat, host.CheckFormat(self.Root, self.ostyFiles()))
}

// Osty: toolchain/ci.osty:240:5
func (self *Runner) checkPolicy() *Check {
	// Osty: toolchain/ci.osty:241:9
	c := &Check{Name: CheckPolicy, Passed: false, Skipped: false, Note: "", Diags: host.EmptyDiagnostics()}
	_ = c
	// Osty: toolchain/ci.osty:248:9
	if !(host.HasManifest(self.Manifest)) {
		// Osty: toolchain/ci.osty:249:14
		c.Note = "no osty.toml found; policy checks skipped"
		// Osty: toolchain/ci.osty:250:13
		return c
	}
	// Osty: toolchain/ci.osty:253:9
	core := host.ManifestCoreOf(self.Manifest)
	_ = core
	// Osty: toolchain/ci.osty:254:9
	pushCoreDiagnostics(c, ciPolicyManifestFieldsCore(coreToCiManifest(core)))
	// Osty: toolchain/ci.osty:255:9
	if host.HasWorkspace(self.Manifest) {
		// Osty: toolchain/ci.osty:256:13
		members := host.WorkspaceMembers(self.Manifest)
		_ = members
		// Osty: toolchain/ci.osty:257:13
		pushCoreDiagnostics(c, ciPolicyWorkspaceMembersCore(members))
		// Osty: toolchain/ci.osty:258:13
		pushHostDiagnostics(c, host.CheckWorkspaceMemberPaths(self.Root, members))
	}
	// Osty: toolchain/ci.osty:260:9
	pushHostDiagnostics(c, host.CheckPolicyFileSizes(self.Root, self.ostyFiles(), self.Opts.MaxFileBytes))
	return c
}

// Osty: toolchain/ci.osty:267:5
func (self *Runner) checkLockfile() *Check {
	return checkFromHostResult(CheckLockfile, host.CheckLockfile(self.Root, self.Manifest))
}

// Osty: toolchain/ci.osty:271:5
func (self *Runner) checkLint() *Check {
	return checkFromHostResult(CheckLint, host.CheckLint(self.Manifest, self.Packages, self.Results))
}

// Osty: toolchain/ci.osty:275:5
func (self *Runner) checkRelease() *Check {
	// Osty: toolchain/ci.osty:276:9
	c := &Check{Name: CheckRelease, Passed: false, Skipped: false, Note: "", Diags: host.EmptyDiagnostics()}
	_ = c
	// Osty: toolchain/ci.osty:283:9
	pushCoreDiagnostics(c, ciReleaseManifestCore(releaseCore(self.Manifest)))
	// Osty: toolchain/ci.osty:284:9
	if host.HasManifest(self.Manifest) && host.ManifestCoreOf(self.Manifest).HasPackage {
		// Osty: toolchain/ci.osty:285:13
		pushHostDiagnostics(c, host.CheckReleaseLockfile(self.Root, self.Manifest))
	}
	return c
}

// Osty: toolchain/ci.osty:290:5
func (self *Runner) checkSemver() *Check {
	// Osty: toolchain/ci.osty:291:9
	c := &Check{Name: CheckSemver, Passed: false, Skipped: false, Note: "", Diags: host.EmptyDiagnostics()}
	_ = c
	// Osty: toolchain/ci.osty:298:9
	if self.Opts.Baseline == "" {
		// Osty: toolchain/ci.osty:299:13
		func() struct{} {
			c.Diags = append(c.Diags, host.Synthetic("error", "CI401", "semver check enabled but --baseline was not set"))
			return struct{}{}
		}()
		// Osty: toolchain/ci.osty:302:13
		return c
	}
	// Osty: toolchain/ci.osty:305:9
	baseline := host.ReadSnapshotHost(self.Opts.Baseline)
	_ = baseline
	// Osty: toolchain/ci.osty:306:9
	if baseline.Error != "" {
		// Osty: toolchain/ci.osty:307:13
		func() struct{} {
			c.Diags = append(c.Diags, host.Synthetic("error", "CI402", ciJoin2("cannot read baseline snapshot: ", baseline.Error)))
			return struct{}{}
		}()
		// Osty: toolchain/ci.osty:314:13
		return c
	}
	// Osty: toolchain/ci.osty:316:9
	if packageCount(self.Packages) == 0 {
		// Osty: toolchain/ci.osty:317:13
		func() struct{} {
			c.Diags = append(c.Diags, host.Synthetic("error", "CI403", "no package loaded; cannot capture current API"))
			return struct{}{}
		}()
		// Osty: toolchain/ci.osty:320:13
		return c
	}
	// Osty: toolchain/ci.osty:323:9
	manifestCore := host.ManifestCoreOf(self.Manifest)
	_ = manifestCore
	// Osty: toolchain/ci.osty:324:9
	current := host.NewWorkspaceSnapshotHost(self.Packages, manifestCore.Version, manifestCore.Edition)
	_ = current
	// Osty: toolchain/ci.osty:329:9
	breakingIsError := !(ciMajorBumped(baseline.Snapshot.Version, current.Version))
	_ = breakingIsError
	// Osty: toolchain/ci.osty:330:9
	if self.Opts.SemverWarnOnly {
		// Osty: toolchain/ci.osty:331:13
		breakingIsError = false
	}
	// Osty: toolchain/ci.osty:334:9
	diff := host.CompareSnapshots(baseline.Snapshot, current)
	_ = diff
	// Osty: toolchain/ci.osty:335:9
	for _, s := range diff.Removed {
		// Osty: toolchain/ci.osty:336:13
		sev := ciBreakingSeverity(breakingIsError)
		_ = sev
		// Osty: toolchain/ci.osty:337:13
		func() struct{} {
			c.Diags = append(c.Diags, host.Synthetic(sev, "CI410", ciJoin5("breaking: exported ", s.Symbol.Kind, " \"", qualifiedSymbol(s), "\" was removed")))
			return struct{}{}
		}()
	}
	// Osty: toolchain/ci.osty:351:9
	for _, s := range diff.Changed {
		// Osty: toolchain/ci.osty:352:13
		sev := ciBreakingSeverity(breakingIsError)
		_ = sev
		// Osty: toolchain/ci.osty:353:13
		func() struct{} {
			c.Diags = append(c.Diags, host.Synthetic(sev, "CI411", ciJoin7("breaking: exported ", s.Symbol.Kind, " \"", qualifiedSymbol(s), "\" signature changed (now: ", s.Symbol.Sig, ")")))
			return struct{}{}
		}()
	}
	// Osty: toolchain/ci.osty:369:9
	for _, s := range diff.Added {
		// Osty: toolchain/ci.osty:370:13
		func() struct{} {
			c.Diags = append(c.Diags, host.Synthetic("warning", "CI420", ciJoin5("additive: exported ", s.Symbol.Kind, " \"", qualifiedSymbol(s), "\" is new")))
			return struct{}{}
		}()
	}
	return c
}

// Osty: toolchain/ci.osty:387:5
func (self *Runner) ostyFiles() []string {
	return host.OstyFiles(self.Root, self.Packages)
}

// Osty: toolchain/ci.osty:393:5
func DefaultOptions() *Options {
	return &Options{Format: true, Lint: true, Policy: true, Lockfile: true, Release: false, Semver: false, SemverWarnOnly: false, Strict: false, Baseline: "", MaxFileBytes: 0}
}

// Osty: toolchain/ci.osty:408:5
func NewRunner(root string, opts *Options) *Runner {
	// Osty: toolchain/ci.osty:409:5
	next := &Options{Format: opts.Format, Lint: opts.Lint, Policy: opts.Policy, Lockfile: opts.Lockfile, Release: opts.Release, Semver: opts.Semver, SemverWarnOnly: opts.SemverWarnOnly, Strict: opts.Strict, Baseline: opts.Baseline, MaxFileBytes: ciPolicyMaxFileBytes(opts.Policy, opts.MaxFileBytes)}
	_ = next
	return &Runner{Root: root, Opts: next, Manifest: nil, Workspace: nil, Packages: host.EmptyPackages(), Results: host.EmptyPackageResults()}
}

// Osty: toolchain/ci.osty:431:5
func CapturePackage(pkg *host.ResolvePackage) []host.Symbol {
	return host.CapturePackageHost(pkg)
}

// Osty: toolchain/ci.osty:435:5
func NewSingleSnapshot(pkg *host.ResolvePackage, version string, edition string) host.Snapshot {
	return host.NewSingleSnapshotHost(pkg, version, edition)
}

// Osty: toolchain/ci.osty:439:5
func NewWorkspaceSnapshot(packages []*host.ResolvePackage, version string, edition string) host.Snapshot {
	return host.NewWorkspaceSnapshotHost(packages, version, edition)
}

// Osty: toolchain/ci.osty:447:5
func WriteSnapshot(path string, snap host.Snapshot) string {
	return host.WriteSnapshotHost(path, snap)
}

// Osty: toolchain/ci.osty:451:5
func ReadSnapshot(path string) host.SnapshotReadResult {
	return host.ReadSnapshotHost(path)
}

// Osty: toolchain/ci.osty:455:5
func Compare(baseline host.Snapshot, current host.Snapshot) host.Diff {
	return host.CompareSnapshots(baseline, current)
}

// Osty: toolchain/ci.osty:460:5
type CiDiagCore struct {
	severity string
	code     string
	message  string
}

// Osty: toolchain/ci.osty:466:5
type CiManifestCore struct {
	hasPackage  bool
	name        string
	version     string
	edition     string
	license     string
	description string
}

// Osty: toolchain/ci.osty:475:5
type CiDependencyCore struct {
	name   string
	path   string
	hasGit bool
	gitTag string
	gitRev string
}

// Osty: toolchain/ci.osty:483:5
type CiReleaseCore struct {
	hasManifest  bool
	hasPackage   bool
	version      string
	license      string
	dependencies []*CiDependencyCore
}

// Osty: toolchain/ci.osty:491:5
type CiSplit struct {
	count  int
	first  string
	second string
	third  string
}

// Osty: toolchain/ci.osty:498:5
type CiSplitFive struct {
	count  int
	first  string
	second string
	third  string
	fourth string
	fifth  string
}

// Osty: toolchain/ci.osty:507:5
func ciPolicyMaxFileBytes(policy bool, maxFileBytes int) int {
	return func() int {
		if policy && maxFileBytes == 0 {
			return 1048576
		} else {
			return maxFileBytes
		}
	}()
}

// Osty: toolchain/ci.osty:515:5
func ciCheckPassed(diags []*CiDiagCore, strict bool) bool {
	// Osty: toolchain/ci.osty:516:5
	for _, d := range diags {
		// Osty: toolchain/ci.osty:517:9
		if d.severity == "error" {
			// Osty: toolchain/ci.osty:518:13
			return false
		}
		// Osty: toolchain/ci.osty:520:9
		if strict && d.severity == "warning" {
			// Osty: toolchain/ci.osty:521:13
			return false
		}
	}
	return true
}

// Osty: toolchain/ci.osty:527:5
func ciDiagCount(diags []*CiDiagCore) int {
	// Osty: toolchain/ci.osty:528:5
	count := 0
	_ = count
	// Osty: toolchain/ci.osty:529:5
	for _, d := range diags {
		// Osty: toolchain/ci.osty:530:9
		_ = d
		// Osty: toolchain/ci.osty:531:9
		func() {
			var _cur1 int = count
			var _rhs2 int = 1
			if _rhs2 > 0 && _cur1 > math.MaxInt-_rhs2 {
				panic("integer overflow")
			}
			if _rhs2 < 0 && _cur1 < math.MinInt-_rhs2 {
				panic("integer overflow")
			}
			count = _cur1 + _rhs2
		}()
	}
	return count
}

// Osty: toolchain/ci.osty:536:5
func ciSkippedNote() string {
	return "not enabled"
}

// Osty: toolchain/ci.osty:540:5
func ciPolicyManifestFieldsCore(m *CiManifestCore) []*CiDiagCore {
	// Osty: toolchain/ci.osty:541:5
	var out []*CiDiagCore = make([]*CiDiagCore, 0, 1)
	_ = out
	// Osty: toolchain/ci.osty:542:5
	if !(m.hasPackage) {
		// Osty: toolchain/ci.osty:543:9
		return out
	}
	// Osty: toolchain/ci.osty:546:5
	if m.name == "" {
		// Osty: toolchain/ci.osty:547:9
		func() struct{} {
			out = append(out, ciError("CI101", "package.name is missing in osty.toml"))
			return struct{}{}
		}()
	} else if !(ciPackageNameValid(m.name)) {
		// Osty: toolchain/ci.osty:549:9
		func() struct{} {
			out = append(out, ciError("CI102", ciJoin3("package.name \"", m.name, "\": expected [a-z][a-z0-9_-]*")))
			return struct{}{}
		}()
	}
	// Osty: toolchain/ci.osty:557:5
	if m.version == "" {
		// Osty: toolchain/ci.osty:558:9
		func() struct{} {
			out = append(out, ciError("CI103", "package.version is missing in osty.toml"))
			return struct{}{}
		}()
	}
	// Osty: toolchain/ci.osty:560:5
	if m.edition == "" {
		// Osty: toolchain/ci.osty:561:9
		func() struct{} {
			out = append(out, ciWarning("CI104", "package.edition is not set - pin an edition (e.g. \"0.4\") to avoid drift"))
			return struct{}{}
		}()
	}
	// Osty: toolchain/ci.osty:568:5
	if m.license == "" {
		// Osty: toolchain/ci.osty:569:9
		func() struct{} {
			out = append(out, ciWarning("CI105", "package.license is not set - set an SPDX identifier or leave the key empty if truly unlicensed"))
			return struct{}{}
		}()
	}
	// Osty: toolchain/ci.osty:576:5
	if m.description == "" {
		// Osty: toolchain/ci.osty:577:9
		func() struct{} {
			out = append(out, ciWarning("CI106", "package.description is empty - registry listings will be unhelpful"))
			return struct{}{}
		}()
	}
	return out
}

// Osty: toolchain/ci.osty:585:5
func ciPolicyWorkspaceMembersCore(members []string) []*CiDiagCore {
	// Osty: toolchain/ci.osty:586:5
	var out []*CiDiagCore = make([]*CiDiagCore, 0, 1)
	_ = out
	// Osty: toolchain/ci.osty:587:5
	if ciStringListEmpty(members) {
		// Osty: toolchain/ci.osty:588:9
		func() struct{} {
			out = append(out, ciError("CI110", "[workspace] declared but members is empty"))
			return struct{}{}
		}()
		// Osty: toolchain/ci.osty:589:9
		return out
	}
	// Osty: toolchain/ci.osty:592:5
	for _, mem := range members {
		// Osty: toolchain/ci.osty:593:9
		if ciWorkspaceMemberEscapes(mem) {
			// Osty: toolchain/ci.osty:594:13
			func() struct{} {
				out = append(out, ciError("CI111", ciJoin3("workspace member \"", mem, "\" escapes the project root")))
				return struct{}{}
			}()
		}
	}
	return out
}

// Osty: toolchain/ci.osty:605:5
func ciWorkspaceMemberEscapes(mem string) bool {
	return mem == "" || ciStrings.HasPrefix(mem, "..") || ciStrings.HasPrefix(mem, "/")
}

// Osty: toolchain/ci.osty:609:5
func ciReleaseManifestCore(input *CiReleaseCore) []*CiDiagCore {
	// Osty: toolchain/ci.osty:610:5
	var out []*CiDiagCore = make([]*CiDiagCore, 0, 1)
	_ = out
	// Osty: toolchain/ci.osty:611:5
	if !(input.hasManifest) {
		// Osty: toolchain/ci.osty:612:9
		func() struct{} {
			out = append(out, ciError("CI301", "no osty.toml found; nothing to release"))
			return struct{}{}
		}()
		// Osty: toolchain/ci.osty:613:9
		return out
	}
	// Osty: toolchain/ci.osty:615:5
	if !(input.hasPackage) {
		// Osty: toolchain/ci.osty:616:9
		func() struct{} {
			out = append(out, ciError("CI302", "only package projects can be released (virtual workspaces have nothing to publish)"))
			return struct{}{}
		}()
		// Osty: toolchain/ci.osty:622:9
		return out
	}
	// Osty: toolchain/ci.osty:625:5
	if input.version == "" {
		// Osty: toolchain/ci.osty:626:9
		func() struct{} { out = append(out, ciError("CI303", "package.version is missing")); return struct{}{} }()
	} else if !(ciIsStrictSemver(input.version)) {
		// Osty: toolchain/ci.osty:628:9
		func() struct{} {
			out = append(out, ciError("CI304", ciJoin3("package.version \"", input.version, "\" is not strict SemVer")))
			return struct{}{}
		}()
	}
	// Osty: toolchain/ci.osty:635:5
	if input.license == "" {
		// Osty: toolchain/ci.osty:636:9
		func() struct{} {
			out = append(out, ciError("CI305", "package.license is required for release"))
			return struct{}{}
		}()
	}
	// Osty: toolchain/ci.osty:639:5
	for _, dep := range input.dependencies {
		// Osty: toolchain/ci.osty:640:9
		if dep.path != "" {
			// Osty: toolchain/ci.osty:641:13
			func() struct{} {
				out = append(out, ciError("CI306", ciJoin5("dependency \"", dep.name, "\" uses path=\"", dep.path, "\"; path deps cannot be published")))
				return struct{}{}
			}()
		}
		// Osty: toolchain/ci.osty:654:9
		if dep.hasGit && dep.gitTag == "" && dep.gitRev == "" {
			// Osty: toolchain/ci.osty:655:13
			func() struct{} {
				out = append(out, ciWarning("CI307", ciJoin3("dependency \"", dep.name, "\" is tracked by branch; pin to a tag or rev before release")))
				return struct{}{}
			}()
		}
	}
	return out
}

// Osty: toolchain/ci.osty:671:5
func ciLockHasCore(packages []string, name string) bool {
	// Osty: toolchain/ci.osty:672:5
	for _, pkg := range packages {
		// Osty: toolchain/ci.osty:673:9
		if pkg == name {
			// Osty: toolchain/ci.osty:674:13
			return true
		}
	}
	return false
}

// Osty: toolchain/ci.osty:680:5
func ciMajorBumped(base string, cur string) bool {
	// Osty: toolchain/ci.osty:681:5
	baseMajor := ciSemverMajor(base)
	_ = baseMajor
	// Osty: toolchain/ci.osty:682:5
	curMajor := ciSemverMajor(cur)
	_ = curMajor
	return baseMajor >= 0 && curMajor > baseMajor
}

// Osty: toolchain/ci.osty:686:5
func ciIsStrictSemver(raw string) bool {
	// Osty: toolchain/ci.osty:687:5
	if raw == "" {
		// Osty: toolchain/ci.osty:688:9
		return false
	}
	// Osty: toolchain/ci.osty:690:5
	text := ciStrings.TrimPrefix(raw, "v")
	_ = text
	// Osty: toolchain/ci.osty:691:5
	if text == "" {
		// Osty: toolchain/ci.osty:692:9
		return false
	}
	// Osty: toolchain/ci.osty:695:5
	plus := ciSplitSummary(ciStrings.SplitN(text, "+", 2))
	_ = plus
	// Osty: toolchain/ci.osty:696:5
	if plus.count == 2 && !(ciBuildIdentifiersValid(plus.second)) {
		// Osty: toolchain/ci.osty:697:9
		return false
	}
	// Osty: toolchain/ci.osty:700:5
	dash := ciSplitSummary(ciStrings.SplitN(plus.first, "-", 2))
	_ = dash
	// Osty: toolchain/ci.osty:701:5
	if !(ciSemCoreValid(dash.first)) {
		// Osty: toolchain/ci.osty:702:9
		return false
	}
	// Osty: toolchain/ci.osty:704:5
	if dash.count == 2 && !(ciPreIdentifiersValid(dash.second)) {
		// Osty: toolchain/ci.osty:705:9
		return false
	}
	return true
}

// Osty: toolchain/ci.osty:710:1
func skipped(name CheckName) *Check {
	return &Check{Name: name, Passed: false, Skipped: true, Note: ciSkippedNote(), Diags: host.EmptyDiagnostics()}
}

// Osty: toolchain/ci.osty:720:1
func checkFromHostResult(name CheckName, result host.CheckResult) *Check {
	return &Check{Name: name, Passed: false, Skipped: false, Note: result.Note, Diags: result.Diagnostics}
}

// Osty: toolchain/ci.osty:730:1
func pushHostDiagnostics(c *Check, diags []*host.Diagnostic) {
	// Osty: toolchain/ci.osty:731:5
	for _, d := range diags {
		// Osty: toolchain/ci.osty:732:9
		func() struct{} { c.Diags = append(c.Diags, d); return struct{}{} }()
	}
}

// Osty: toolchain/ci.osty:736:1
func pushCoreDiagnostics(c *Check, diags []*CiDiagCore) {
	// Osty: toolchain/ci.osty:737:5
	for _, d := range diags {
		// Osty: toolchain/ci.osty:738:9
		func() struct{} {
			c.Diags = append(c.Diags, host.Synthetic(d.severity, d.code, d.message))
			return struct{}{}
		}()
	}
}

// Osty: toolchain/ci.osty:742:1
func ciCheckHostPassed(diags []*host.Diagnostic, strict bool) bool {
	// Osty: toolchain/ci.osty:743:5
	for _, d := range diags {
		// Osty: toolchain/ci.osty:744:9
		severity := host.DiagnosticSeverity(d)
		_ = severity
		// Osty: toolchain/ci.osty:745:9
		if severity == "error" {
			// Osty: toolchain/ci.osty:746:13
			return false
		}
		// Osty: toolchain/ci.osty:748:9
		if strict && severity == "warning" {
			// Osty: toolchain/ci.osty:749:13
			return false
		}
	}
	return true
}

// Osty: toolchain/ci.osty:755:1
func coreToCiManifest(m host.ManifestCore) *CiManifestCore {
	return &CiManifestCore{hasPackage: m.HasPackage, name: m.Name, version: m.Version, edition: m.Edition, license: m.License, description: m.Description}
}

// Osty: toolchain/ci.osty:766:1
func releaseCore(manifest *host.Manifest) *CiReleaseCore {
	// Osty: toolchain/ci.osty:767:5
	m := host.ManifestCoreOf(manifest)
	_ = m
	// Osty: toolchain/ci.osty:768:5
	var deps []*CiDependencyCore = make([]*CiDependencyCore, 0, 1)
	_ = deps
	// Osty: toolchain/ci.osty:769:5
	for _, row := range host.DependencyCoreRows(manifest) {
		// Osty: toolchain/ci.osty:770:9
		d := dependencyCoreFromRow(row)
		_ = d
		// Osty: toolchain/ci.osty:771:9
		func() struct{} {
			deps = append(deps, &CiDependencyCore{name: d.name, path: d.path, hasGit: d.hasGit, gitTag: d.gitTag, gitRev: d.gitRev})
			return struct{}{}
		}()
	}
	return &CiReleaseCore{hasManifest: host.HasManifest(manifest), hasPackage: m.HasPackage, version: m.Version, license: m.License, dependencies: deps}
}

// Osty: toolchain/ci.osty:790:1
func dependencyCoreFromRow(row string) *CiDependencyCore {
	// Osty: toolchain/ci.osty:791:5
	parts := ciSplitFive(ciStrings.Split(row, "\t"))
	_ = parts
	return &CiDependencyCore{name: parts.first, path: parts.second, hasGit: parts.third == "true", gitTag: parts.fourth, gitRev: parts.fifth}
}

// Osty: toolchain/ci.osty:801:1
func packageCount(packages []*host.ResolvePackage) int {
	// Osty: toolchain/ci.osty:802:5
	count := 0
	_ = count
	// Osty: toolchain/ci.osty:803:5
	for _, pkg := range packages {
		// Osty: toolchain/ci.osty:804:9
		_ = pkg
		// Osty: toolchain/ci.osty:805:9
		func() {
			var _cur3 int = count
			var _rhs4 int = 1
			if _rhs4 > 0 && _cur3 > math.MaxInt-_rhs4 {
				panic("integer overflow")
			}
			if _rhs4 < 0 && _cur3 < math.MinInt-_rhs4 {
				panic("integer overflow")
			}
			count = _cur3 + _rhs4
		}()
	}
	return count
}

// Osty: toolchain/ci.osty:810:1
func ciBreakingSeverity(breakingIsError bool) string {
	return func() string {
		if breakingIsError {
			return "error"
		} else {
			return "warning"
		}
	}()
}

// Osty: toolchain/ci.osty:818:1
func qualifiedSymbol(ref host.SymbolRef) string {
	return func() string {
		if ref.Pkg == "" {
			return ref.Symbol.Name
		} else {
			return ciJoin3(ref.Pkg, ".", ref.Symbol.Name)
		}
	}()
}

// Osty: toolchain/ci.osty:826:1
func ciError(code string, message string) *CiDiagCore {
	return &CiDiagCore{severity: "error", code: code, message: message}
}

// Osty: toolchain/ci.osty:830:1
func ciWarning(code string, message string) *CiDiagCore {
	return &CiDiagCore{severity: "warning", code: code, message: message}
}

// Osty: toolchain/ci.osty:834:1
func ciJoin2(a string, b string) string {
	// Osty: toolchain/ci.osty:835:5
	var parts []string = make([]string, 0, 1)
	_ = parts
	// Osty: toolchain/ci.osty:836:5
	func() struct{} { parts = append(parts, a); return struct{}{} }()
	// Osty: toolchain/ci.osty:837:5
	func() struct{} { parts = append(parts, b); return struct{}{} }()
	return ciStrings.Join(parts, "")
}

// Osty: toolchain/ci.osty:841:1
func ciJoin3(a string, b string, c string) string {
	// Osty: toolchain/ci.osty:842:5
	var parts []string = make([]string, 0, 1)
	_ = parts
	// Osty: toolchain/ci.osty:843:5
	func() struct{} { parts = append(parts, a); return struct{}{} }()
	// Osty: toolchain/ci.osty:844:5
	func() struct{} { parts = append(parts, b); return struct{}{} }()
	// Osty: toolchain/ci.osty:845:5
	func() struct{} { parts = append(parts, c); return struct{}{} }()
	return ciStrings.Join(parts, "")
}

// Osty: toolchain/ci.osty:849:1
func ciJoin5(a string, b string, c string, d string, e string) string {
	// Osty: toolchain/ci.osty:850:5
	var parts []string = make([]string, 0, 1)
	_ = parts
	// Osty: toolchain/ci.osty:851:5
	func() struct{} { parts = append(parts, a); return struct{}{} }()
	// Osty: toolchain/ci.osty:852:5
	func() struct{} { parts = append(parts, b); return struct{}{} }()
	// Osty: toolchain/ci.osty:853:5
	func() struct{} { parts = append(parts, c); return struct{}{} }()
	// Osty: toolchain/ci.osty:854:5
	func() struct{} { parts = append(parts, d); return struct{}{} }()
	// Osty: toolchain/ci.osty:855:5
	func() struct{} { parts = append(parts, e); return struct{}{} }()
	return ciStrings.Join(parts, "")
}

// Osty: toolchain/ci.osty:859:1
func ciJoin7(a string, b string, c string, d string, e string, f string, g string) string {
	// Osty: toolchain/ci.osty:860:5
	var parts []string = make([]string, 0, 1)
	_ = parts
	// Osty: toolchain/ci.osty:861:5
	func() struct{} { parts = append(parts, a); return struct{}{} }()
	// Osty: toolchain/ci.osty:862:5
	func() struct{} { parts = append(parts, b); return struct{}{} }()
	// Osty: toolchain/ci.osty:863:5
	func() struct{} { parts = append(parts, c); return struct{}{} }()
	// Osty: toolchain/ci.osty:864:5
	func() struct{} { parts = append(parts, d); return struct{}{} }()
	// Osty: toolchain/ci.osty:865:5
	func() struct{} { parts = append(parts, e); return struct{}{} }()
	// Osty: toolchain/ci.osty:866:5
	func() struct{} { parts = append(parts, f); return struct{}{} }()
	// Osty: toolchain/ci.osty:867:5
	func() struct{} { parts = append(parts, g); return struct{}{} }()
	return ciStrings.Join(parts, "")
}

// Osty: toolchain/ci.osty:871:1
func ciStringListEmpty(xs []string) bool {
	// Osty: toolchain/ci.osty:872:5
	for _, x := range xs {
		// Osty: toolchain/ci.osty:873:9
		_ = x
		// Osty: toolchain/ci.osty:874:9
		return false
	}
	return true
}

// Osty: toolchain/ci.osty:879:1
func ciPackageNameValid(name string) bool {
	// Osty: toolchain/ci.osty:880:5
	if name == "" {
		// Osty: toolchain/ci.osty:881:9
		return false
	}
	// Osty: toolchain/ci.osty:883:5
	first := true
	_ = first
	// Osty: toolchain/ci.osty:884:5
	for _, unit := range ciStrings.Split(name, "") {
		// Osty: toolchain/ci.osty:885:9
		if first {
			// Osty: toolchain/ci.osty:886:13
			if !(unit >= "a" && unit <= "z") {
				// Osty: toolchain/ci.osty:887:17
				return false
			}
			// Osty: toolchain/ci.osty:889:13
			first = false
		} else if !((unit >= "a" && unit <= "z") || (unit >= "0" && unit <= "9") || unit == "_" || unit == "-") {
			// Osty: toolchain/ci.osty:891:13
			return false
		}
	}
	return true
}

// Osty: toolchain/ci.osty:897:1
func ciSplitSummary(parts []string) *CiSplit {
	// Osty: toolchain/ci.osty:898:5
	count := 0
	_ = count
	// Osty: toolchain/ci.osty:899:5
	first := ""
	_ = first
	// Osty: toolchain/ci.osty:900:5
	second := ""
	_ = second
	// Osty: toolchain/ci.osty:901:5
	third := ""
	_ = third
	// Osty: toolchain/ci.osty:902:5
	for _, part := range parts {
		// Osty: toolchain/ci.osty:903:9
		if count == 0 {
			// Osty: toolchain/ci.osty:904:13
			first = part
		} else if count == 1 {
			// Osty: toolchain/ci.osty:906:13
			second = part
		} else if count == 2 {
			// Osty: toolchain/ci.osty:908:13
			third = part
		}
		// Osty: toolchain/ci.osty:910:9
		func() {
			var _cur5 int = count
			var _rhs6 int = 1
			if _rhs6 > 0 && _cur5 > math.MaxInt-_rhs6 {
				panic("integer overflow")
			}
			if _rhs6 < 0 && _cur5 < math.MinInt-_rhs6 {
				panic("integer overflow")
			}
			count = _cur5 + _rhs6
		}()
	}
	return &CiSplit{count: count, first: first, second: second, third: third}
}

// Osty: toolchain/ci.osty:915:1
func ciSplitFive(parts []string) *CiSplitFive {
	// Osty: toolchain/ci.osty:916:5
	count := 0
	_ = count
	// Osty: toolchain/ci.osty:917:5
	first := ""
	_ = first
	// Osty: toolchain/ci.osty:918:5
	second := ""
	_ = second
	// Osty: toolchain/ci.osty:919:5
	third := ""
	_ = third
	// Osty: toolchain/ci.osty:920:5
	fourth := ""
	_ = fourth
	// Osty: toolchain/ci.osty:921:5
	fifth := ""
	_ = fifth
	// Osty: toolchain/ci.osty:922:5
	for _, part := range parts {
		// Osty: toolchain/ci.osty:923:9
		if count == 0 {
			// Osty: toolchain/ci.osty:924:13
			first = part
		} else if count == 1 {
			// Osty: toolchain/ci.osty:926:13
			second = part
		} else if count == 2 {
			// Osty: toolchain/ci.osty:928:13
			third = part
		} else if count == 3 {
			// Osty: toolchain/ci.osty:930:13
			fourth = part
		} else if count == 4 {
			// Osty: toolchain/ci.osty:932:13
			fifth = part
		}
		// Osty: toolchain/ci.osty:934:9
		func() {
			var _cur7 int = count
			var _rhs8 int = 1
			if _rhs8 > 0 && _cur7 > math.MaxInt-_rhs8 {
				panic("integer overflow")
			}
			if _rhs8 < 0 && _cur7 < math.MinInt-_rhs8 {
				panic("integer overflow")
			}
			count = _cur7 + _rhs8
		}()
	}
	return &CiSplitFive{count: count, first: first, second: second, third: third, fourth: fourth, fifth: fifth}
}

// Osty: toolchain/ci.osty:939:1
func ciSemverMajor(raw string) int {
	// Osty: toolchain/ci.osty:940:5
	if !(ciIsStrictSemver(raw)) {
		// Osty: toolchain/ci.osty:941:9
		return -1
	}
	// Osty: toolchain/ci.osty:943:5
	text := ciStrings.TrimPrefix(raw, "v")
	_ = text
	// Osty: toolchain/ci.osty:944:5
	plus := ciSplitSummary(ciStrings.SplitN(text, "+", 2))
	_ = plus
	// Osty: toolchain/ci.osty:945:5
	dash := ciSplitSummary(ciStrings.SplitN(plus.first, "-", 2))
	_ = dash
	// Osty: toolchain/ci.osty:946:5
	core := ciSplitSummary(ciStrings.Split(dash.first, "."))
	_ = core
	return ciParseSemNumber(core.first)
}

// Osty: toolchain/ci.osty:950:1
func ciSemCoreValid(text string) bool {
	// Osty: toolchain/ci.osty:951:5
	parts := ciSplitSummary(ciStrings.Split(text, "."))
	_ = parts
	// Osty: toolchain/ci.osty:952:5
	if parts.count != 3 {
		// Osty: toolchain/ci.osty:953:9
		return false
	}
	return ciParseSemNumber(parts.first) >= 0 && ciParseSemNumber(parts.second) >= 0 && ciParseSemNumber(parts.third) >= 0
}

// Osty: toolchain/ci.osty:960:1
func ciPreIdentifiersValid(text string) bool {
	// Osty: toolchain/ci.osty:961:5
	if text == "" {
		// Osty: toolchain/ci.osty:962:9
		return false
	}
	// Osty: toolchain/ci.osty:964:5
	for _, part := range ciStrings.Split(text, ".") {
		// Osty: toolchain/ci.osty:965:9
		if !(ciPreIdentValid(part)) {
			// Osty: toolchain/ci.osty:966:13
			return false
		}
	}
	return true
}

// Osty: toolchain/ci.osty:972:1
func ciBuildIdentifiersValid(text string) bool {
	// Osty: toolchain/ci.osty:973:5
	if text == "" {
		// Osty: toolchain/ci.osty:974:9
		return false
	}
	// Osty: toolchain/ci.osty:976:5
	for _, part := range ciStrings.Split(text, ".") {
		// Osty: toolchain/ci.osty:977:9
		if !(ciIdentifierTextValid(part)) {
			// Osty: toolchain/ci.osty:978:13
			return false
		}
	}
	return true
}

// Osty: toolchain/ci.osty:984:1
func ciPreIdentValid(text string) bool {
	// Osty: toolchain/ci.osty:985:5
	if text == "" {
		// Osty: toolchain/ci.osty:986:9
		return false
	}
	// Osty: toolchain/ci.osty:988:5
	if ciAllAsciiDigits(text) {
		// Osty: toolchain/ci.osty:989:9
		return ciStringUnitCount(text) == 1 || !(ciStrings.HasPrefix(text, "0"))
	}
	return ciIdentifierTextValid(text)
}

// Osty: toolchain/ci.osty:994:1
func ciIdentifierTextValid(text string) bool {
	// Osty: toolchain/ci.osty:995:5
	if text == "" {
		// Osty: toolchain/ci.osty:996:9
		return false
	}
	// Osty: toolchain/ci.osty:998:5
	for _, unit := range ciStrings.Split(text, "") {
		// Osty: toolchain/ci.osty:999:9
		if !(ciIsSemIdentUnit(unit)) {
			// Osty: toolchain/ci.osty:1000:13
			return false
		}
	}
	return true
}

// Osty: toolchain/ci.osty:1006:1
func ciAllAsciiDigits(text string) bool {
	// Osty: toolchain/ci.osty:1007:5
	if text == "" {
		// Osty: toolchain/ci.osty:1008:9
		return false
	}
	// Osty: toolchain/ci.osty:1010:5
	for _, unit := range ciStrings.Split(text, "") {
		// Osty: toolchain/ci.osty:1011:9
		if ciDigitStringValue(unit) < 0 {
			// Osty: toolchain/ci.osty:1012:13
			return false
		}
	}
	return true
}

// Osty: toolchain/ci.osty:1018:1
func ciStringUnitCount(text string) int {
	// Osty: toolchain/ci.osty:1019:5
	count := 0
	_ = count
	// Osty: toolchain/ci.osty:1020:5
	for _, unit := range ciStrings.Split(text, "") {
		// Osty: toolchain/ci.osty:1021:9
		_ = unit
		// Osty: toolchain/ci.osty:1022:9
		func() {
			var _cur9 int = count
			var _rhs10 int = 1
			if _rhs10 > 0 && _cur9 > math.MaxInt-_rhs10 {
				panic("integer overflow")
			}
			if _rhs10 < 0 && _cur9 < math.MinInt-_rhs10 {
				panic("integer overflow")
			}
			count = _cur9 + _rhs10
		}()
	}
	return count
}

// Osty: toolchain/ci.osty:1027:1
func ciParseSemNumber(text string) int {
	// Osty: toolchain/ci.osty:1028:5
	if text == "" {
		// Osty: toolchain/ci.osty:1029:9
		return -1
	}
	// Osty: toolchain/ci.osty:1031:5
	if ciStringUnitCount(text) > 1 && ciStrings.HasPrefix(text, "0") {
		// Osty: toolchain/ci.osty:1032:9
		return -1
	}
	// Osty: toolchain/ci.osty:1034:5
	out := 0
	_ = out
	// Osty: toolchain/ci.osty:1035:5
	for _, unit := range ciStrings.Split(text, "") {
		// Osty: toolchain/ci.osty:1036:9
		digit := ciDigitStringValue(unit)
		_ = digit
		// Osty: toolchain/ci.osty:1037:9
		if digit < 0 {
			// Osty: toolchain/ci.osty:1038:13
			return -1
		}
		// Osty: toolchain/ci.osty:1040:9
		func() {
			var _cur11 int = func() int {
				var _p13 int = out
				var _rhs14 int = 10
				if _p13 != 0 && _rhs14 != 0 {
					if _p13 == int(-1) && _rhs14 == math.MinInt {
						panic("integer overflow")
					}
					if _rhs14 == int(-1) && _p13 == math.MinInt {
						panic("integer overflow")
					}
					if _p13 > 0 {
						if _rhs14 > 0 && _p13 > math.MaxInt/_rhs14 {
							panic("integer overflow")
						}
						if _rhs14 < 0 && _rhs14 < math.MinInt/_p13 {
							panic("integer overflow")
						}
					} else {
						if _rhs14 > 0 && _p13 < math.MinInt/_rhs14 {
							panic("integer overflow")
						}
						if _rhs14 < 0 && _p13 < math.MaxInt/_rhs14 {
							panic("integer overflow")
						}
					}
				}
				return _p13 * _rhs14
			}()
			var _rhs12 int = digit
			if _rhs12 > 0 && _cur11 > math.MaxInt-_rhs12 {
				panic("integer overflow")
			}
			if _rhs12 < 0 && _cur11 < math.MinInt-_rhs12 {
				panic("integer overflow")
			}
			out = _cur11 + _rhs12
		}()
	}
	return out
}

// Osty: toolchain/ci.osty:1045:1
func ciIsSemIdentUnit(unit string) bool {
	return (unit >= "0" && unit <= "9") || (unit >= "A" && unit <= "Z") || (unit >= "a" && unit <= "z") || unit == "-"
}

// Osty: toolchain/ci.osty:1049:1
func ciDigitStringValue(unit string) int {
	return func() int {
		if unit == "0" {
			return 0
		} else if unit == "1" {
			return 1
		} else if unit == "2" {
			return 2
		} else if unit == "3" {
			return 3
		} else if unit == "4" {
			return 4
		} else if unit == "5" {
			return 5
		} else if unit == "6" {
			return 6
		} else if unit == "7" {
			return 7
		} else if unit == "8" {
			return 8
		} else if unit == "9" {
			return 9
		} else {
			return -1
		}
	}()
}
