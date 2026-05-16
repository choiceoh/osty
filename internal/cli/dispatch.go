package cli

import (
	"fmt"
	"strings"
)

type CliFlags struct {
	NoColor         bool
	ForceColor      bool
	MaxErrors       int
	JsonOutput      bool
	Strict          bool
	Fix             bool
	FixDryRun       bool
	ShowScopes      bool
	Trace           bool
	Explain         bool
	Inspect         bool
	AiRepair        bool
	AiMode          string
	DumpNativeDiags bool
}

func DefaultCliFlags() CliFlags {
	return CliFlags{}
}

type ParsedCommand struct {
	Name     string
	Args     []string
	Flags    CliFlags
	SubFlags map[string]string
	Errors   []string
	RawRest  []string
}

func ParsedOk(name string, args []string, flags CliFlags, subFlags map[string]string) ParsedCommand {
	return ParsedCommand{
		Name:     name,
		Args:     args,
		Flags:    flags,
		SubFlags: subFlags,
		Errors:   nil,
	}
}

func ParsedError(msg string) ParsedCommand {
	return ParsedCommand{
		Errors: []string{msg},
	}
}

func (pc ParsedCommand) IsOk() bool {
	return len(pc.Errors) == 0
}

type FlagSpec struct {
	Name         string
	Short        string
	TakesValue   bool
	Required     bool
	DefaultValue *string
	Help         string
}

type ParsedArgs struct {
	Values      map[string]string
	Present     map[string]bool
	Positionals []string
	Errors      []string
}

func (pa ParsedArgs) Has(name string) bool {
	return pa.Present[name]
}

func (pa ParsedArgs) Value(name string) (string, bool) {
	v, ok := pa.Values[name]
	return v, ok
}

func (pa ParsedArgs) ValueOr(name, def string) string {
	if v, ok := pa.Values[name]; ok {
		return v
	}
	return def
}

func Flag(name, short, help string) FlagSpec {
	return FlagSpec{Name: name, Short: short, Help: help}
}

func Option(name, short, help string) FlagSpec {
	return FlagSpec{Name: name, Short: short, TakesValue: true, Help: help}
}

func OptionDefault(name, short, def, help string) FlagSpec {
	return FlagSpec{Name: name, Short: short, TakesValue: true, DefaultValue: &def, Help: help}
}

func RequiredOption(name, short, help string) FlagSpec {
	return FlagSpec{Name: name, Short: short, TakesValue: true, Required: true, Help: help}
}

func ParseFlags(args []string, specs []FlagSpec) ParsedArgs {
	values := make(map[string]string)
	present := make(map[string]bool)
	var positionals []string
	var errors []string

	for _, spec := range specs {
		present[spec.Name] = false
		if spec.DefaultValue != nil {
			values[spec.Name] = *spec.DefaultValue
		}
	}

	positionalOnly := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if positionalOnly {
			positionals = append(positionals, arg)
			continue
		}
		if arg == "--" {
			positionalOnly = true
			continue
		}
		if strings.HasPrefix(arg, "--") && len(arg) > 2 {
			name, inlineValue := splitInlineValue(arg[2:])
			spec, found := findLong(specs, name)
			if !found {
				errors = append(errors, "cli: unknown option --"+name)
				continue
			}
			used := applySpec(spec, inlineValue, args, i, values, present, &errors)
			i += used - 1
			continue
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			name, inlineValue := splitInlineValue(arg[1:])
			spec, found := findShort(specs, name)
			if !found {
				errors = append(errors, "cli: unknown option -"+name)
				continue
			}
			used := applySpec(spec, inlineValue, args, i, values, present, &errors)
			i += used - 1
			continue
		}
		positionals = append(positionals, arg)
	}

	for _, spec := range specs {
		if spec.Required && !present[spec.Name] {
			errors = append(errors, "cli: missing required option --"+spec.Name)
		}
	}

	return ParsedArgs{
		Values:      values,
		Present:     present,
		Positionals: positionals,
		Errors:      errors,
	}
}

func splitInlineValue(text string) (name string, value *string) {
	idx := strings.IndexByte(text, '=')
	if idx < 0 {
		return text, nil
	}
	v := text[idx+1:]
	return text[:idx], &v
}

func findLong(specs []FlagSpec, name string) (FlagSpec, bool) {
	for _, spec := range specs {
		if spec.Name == name {
			return spec, true
		}
	}
	return FlagSpec{}, false
}

func findShort(specs []FlagSpec, short string) (FlagSpec, bool) {
	for _, spec := range specs {
		if spec.Short != "" && spec.Short == short {
			return spec, true
		}
	}
	return FlagSpec{}, false
}

func applySpec(spec FlagSpec, inlineValue *string, args []string, index int, values map[string]string, present map[string]bool, errors *[]string) int {
	present[spec.Name] = true
	if !spec.TakesValue {
		if inlineValue != nil {
			if *inlineValue == "false" {
				values[spec.Name] = "false"
				return 1
			}
		}
		values[spec.Name] = "true"
		return 1
	}

	if inlineValue != nil {
		values[spec.Name] = *inlineValue
		return 1
	}

	if index+1 >= len(args) {
		*errors = append(*errors, "cli: option --"+spec.Name+" requires a value")
		return 1
	}
	v := args[index+1]
	if strings.HasPrefix(v, "-") {
		*errors = append(*errors, "cli: option --"+spec.Name+" requires a value")
		return 1
	}
	values[spec.Name] = v
	return 2
}

type globalParse struct {
	Flags  CliFlags
	Rest   []string
	Errors []string
}

func parseGlobal(args []string) globalParse {
	flags := DefaultCliFlags()
	var rest []string
	var errors []string
	stopped := false

	for i := 0; i < len(args); i++ {
		if stopped {
			rest = append(rest, args[i])
			continue
		}
		arg := args[i]
		if arg == "--" {
			stopped = true
			continue
		}
		if strings.HasPrefix(arg, "--") && len(arg) > 2 {
			raw := arg[2:]
			name, inlineValue := splitInlineValue(raw)
			if inlineValue != nil {
				if applyBoolFlag(&flags, name) {
					continue
				}
				if applyValueFlag(&flags, name, *inlineValue) {
					continue
				}
				stopped = true
				rest = append(rest, arg)
				continue
			}
			if applyBoolFlag(&flags, name) {
				continue
			}
			if isValueFlag(name) {
				if i+1 < len(args) {
					applyValueFlag(&flags, name, args[i+1])
					i++
					continue
				}
				errors = append(errors, "cli: --"+name+" requires a value")
				continue
			}
			stopped = true
			rest = append(rest, arg)
			continue
		}
		stopped = true
		rest = append(rest, arg)
	}

	return globalParse{Flags: flags, Rest: rest, Errors: errors}
}

func applyBoolFlag(flags *CliFlags, name string) bool {
	switch name {
	case "no-color":
		flags.NoColor = true
		return true
	case "color":
		flags.ForceColor = true
		return true
	case "json":
		flags.JsonOutput = true
		return true
	case "strict":
		flags.Strict = true
		return true
	case "fix":
		flags.Fix = true
		return true
	case "fix-dry-run":
		flags.FixDryRun = true
		return true
	case "scopes":
		flags.ShowScopes = true
		return true
	case "trace":
		flags.Trace = true
		return true
	case "explain":
		flags.Explain = true
		return true
	case "inspect":
		flags.Inspect = true
		return true
	case "dump-native-diags":
		flags.DumpNativeDiags = true
		return true
	case "native":
		// Backwards compatibility: self-host pipeline is the only path;
		// accept and ignore so old scripts keep working.
		return true
	case "airepair", "repair":
		flags.AiRepair = true
		return true
	case "no-airepair", "no-repair":
		flags.AiRepair = false
		return true
	}
	return false
}

func isValueFlag(name string) bool {
	return name == "max-errors" || name == "airepair-mode" || name == "repair-mode"
}

func applyValueFlag(flags *CliFlags, name, value string) bool {
	switch name {
	case "max-errors":
		flags.MaxErrors = parseIntOr(value, 0)
		return true
	case "airepair-mode", "repair-mode":
		flags.AiMode = value
		return true
	case "airepair", "repair":
		flags.AiRepair = value != "false"
		return true
	}
	return false
}

func parseIntOr(text string, fallback int) int {
	v := 0
	for _, ch := range text {
		if ch >= '0' && ch <= '9' {
			v = v*10 + int(ch-'0')
		} else {
			return fallback
		}
	}
	if v > 0 {
		return v
	}
	return fallback
}

func KnownCommand(name string) bool {
	switch name {
	case "parse", "tokens", "resolve", "check", "typecheck", "lint",
		"fmt", "airepair", "repair", "gen", "lsp", "query-check",
		"new", "init", "scaffold", "gui", "build", "run",
		"test", "add", "update", "remove", "rm", "fetch",
		"publish", "search", "info", "yank", "unyank", "login",
		"logout", "registry", "doc", "ci", "explain", "pipeline",
		"profiles", "targets", "features", "cache":
		return true
	}
	return false
}

func UsesFrontEndAIRepair(name string) bool {
	switch name {
	case "check", "typecheck", "resolve", "lint":
		return true
	}
	return false
}

func IsTraceableCommand(name string) bool {
	switch name {
	case "tokens", "parse", "resolve", "check", "typecheck", "lint":
		return true
	}
	return false
}

func IsSingleFileCommand(name string) bool {
	switch name {
	case "parse", "tokens", "check", "typecheck", "resolve", "lint":
		return true
	}
	return false
}

func IsOwnParserCommand(name string) bool {
	switch name {
	case "fmt", "airepair", "repair", "gen", "new", "init",
		"build", "run", "test", "add", "update", "lint",
		"ci", "doc", "pipeline", "scaffold", "gui", "publish",
		"search", "yank", "unyank", "login", "logout", "fetch",
		"info", "registry", "profiles", "targets", "features", "cache",
		"remove", "rm":
		return true
	}
	return false
}

func CliUsage() string {
	var b strings.Builder
	fmt.Fprintln(&b, "usage: osty [flags] (parse|tokens|resolve|check|typecheck|lint|fmt|airepair|gen) FILE")
	fmt.Fprintln(&b, "       osty new [--bin|--lib|--workspace|--cli|--service|--gui BACKEND] [--member NAME] NAME")
	fmt.Fprintln(&b, "       osty init [--bin|--lib|--workspace|--cli|--service|--gui BACKEND] [--name NAME]")
	fmt.Fprintln(&b, "       osty build [DIR]")
	fmt.Fprintln(&b, "       osty add NAME[@VER]")
	fmt.Fprintln(&b, "       osty update [NAME...]")
	fmt.Fprintln(&b, "       osty run [-- ARGS...]")
	fmt.Fprintln(&b, "       osty test [PATH|FILTER...]")
	fmt.Fprintln(&b, "       osty publish")
	fmt.Fprintln(&b, "       osty search QUERY")
	fmt.Fprintln(&b, "       osty yank --version V [PKG]")
	fmt.Fprintln(&b, "       osty unyank --version V [PKG]")
	fmt.Fprintln(&b, "       osty login [--registry N]")
	fmt.Fprintln(&b, "       osty logout [--registry N|--all]")
	fmt.Fprintln(&b, "       osty remove NAME [NAME...]")
	fmt.Fprintln(&b, "       osty fetch [--locked|--frozen]")
	fmt.Fprintln(&b, "       osty info NAME [--all-versions]")
	fmt.Fprintln(&b, "       osty registry serve [--addr A] [--root DIR]")
	fmt.Fprintln(&b, "       osty doc [--format FMT] [--out PATH] PATH")
	fmt.Fprintln(&b, "       osty ci [flags] [PATH]")
	fmt.Fprintln(&b, "       osty profiles")
	fmt.Fprintln(&b, "       osty targets")
	fmt.Fprintln(&b, "       osty features")
	fmt.Fprintln(&b, "       osty cache [ls|clean|info]")
	fmt.Fprintln(&b, "       osty gui doctor qtquick")
	fmt.Fprintln(&b, "       osty scaffold <fixture|schema|ffi|polyglot> [flags] NAME")
	fmt.Fprintln(&b, "       osty lsp")
	fmt.Fprintln(&b, "       osty explain [CODE]")
	fmt.Fprintln(&b, "       osty pipeline FILE|DIR")
	return b.String()
}

func ParseArgs(rawArgs []string) ParsedCommand {
	global := parseGlobal(rawArgs)
	if len(global.Errors) > 0 {
		return ParsedError(strings.Join(global.Errors, "\n"))
	}
	if len(global.Rest) == 0 {
		return ParsedError(CliUsage())
	}

	cmd := global.Rest[0]
	subArgs := global.Rest[1:]
	flags := global.Flags

	consumeAIRepairFlags(cmd, subArgs, &flags)

	if IsOwnParserCommand(cmd) {
		parsed := parseSubcommand(cmd, subArgs)
		if len(parsed.Errors) > 0 {
			return ParsedError(strings.Join(parsed.Errors, "\n"))
		}
		sf := parsedToSubFlags(parsed)
		result := ParsedOk(cmd, parsed.Positionals, flags, sf)
		result.RawRest = subArgs
		return result
	}

	result := ParsedOk(cmd, subArgs, flags, nil)
	result.RawRest = subArgs
	return result
}

func consumeAIRepairFlags(cmd string, args []string, flags *CliFlags) {
	if !UsesFrontEndAIRepair(cmd) {
		return
	}
	if flags.AiMode == "" {
		flags.AiMode = "auto"
	}
	flags.AiRepair = true
	for _, arg := range args {
		switch arg {
		case "--airepair", "--repair":
			flags.AiRepair = true
		case "--no-airepair", "--no-repair":
			flags.AiRepair = false
		}
	}
}

func parseSubcommand(cmd string, args []string) ParsedArgs {
	specs := subcommandSpecs(cmd)
	return ParseFlags(args, specs)
}

func subcommandSpecs(cmd string) []FlagSpec {
	switch cmd {
	case "fmt":
		return []FlagSpec{
			Flag("check", "c", "exit 1 if FILE is not already formatted"),
			Flag("write", "w", "overwrite FILE in place"),
			Flag("airepair", "", "enable automatic AI repair before formatting"),
			Flag("no-airepair", "", "disable automatic AI repair before formatting"),
			Flag("repair", "", "alias for --airepair"),
			Flag("no-repair", "", "alias for --no-airepair"),
		}
	case "airepair", "repair":
		return []FlagSpec{
			Flag("check", "c", "exit 1 if FILE would be repaired"),
			Flag("write", "w", "overwrite FILE in place"),
			Flag("json", "", "emit a structured report as JSON"),
			Option("capture-dir", "", "directory for captured artifacts"),
			Option("capture-name", "", "basename for captured artifacts"),
			Option("capture-if", "", "capture mode: residual, changed, or always"),
			Option("stdin-name", "", "filename when FILE is -"),
			Option("mode", "", "acceptance mode: auto, rewrite, parse, or frontend"),
			Option("top", "", "triage: max entries per section"),
			Option("corpus", "", "learn: corpus directory"),
			Option("dest", "", "promote: destination corpus directory"),
			Option("name", "", "promote: basename for promoted files"),
		}
	case "gen":
		return []FlagSpec{
			Option("out", "o", "write generated artifact to this file instead of stdout"),
			OptionDefault("package", "", "main", "backend package/module name"),
			OptionDefault("backend", "", "llvm", "code generation backend (llvm, onb)"),
			Option("emit", "", "artifact mode (llvm-ir, asm)"),
		}
	case "new":
		return []FlagSpec{
			Flag("lib", "", "scaffold a library project"),
			Flag("bin", "", "scaffold a binary project [default]"),
			Flag("workspace", "", "scaffold a virtual workspace with one default member"),
			Flag("cli", "", "scaffold a CLI app starter"),
			Flag("service", "", "scaffold an HTTP service starter"),
			Option("gui", "", "scaffold a GUI app (qtquick or webview2)"),
			OptionDefault("member", "", "core", "workspace member directory name"),
		}
	case "init":
		return []FlagSpec{
			Flag("lib", "", "scaffold a library project"),
			Flag("bin", "", "scaffold a binary project [default]"),
			Flag("workspace", "", "scaffold a virtual workspace"),
			Flag("cli", "", "scaffold a CLI app starter"),
			Flag("service", "", "scaffold an HTTP service starter"),
			Option("gui", "", "scaffold a GUI app (qtquick or webview2)"),
			Option("name", "", "project name (defaults to cwd basename)"),
			OptionDefault("member", "", "core", "workspace member directory name"),
		}
	case "build":
		return []FlagSpec{
			Flag("offline", "", "do not fetch dependencies"),
			Flag("locked", "", "fail if osty.lock would change"),
			Flag("frozen", "", "imply --locked --offline"),
			Flag("force", "", "ignore the build cache and rebuild from source"),
			Option("profile", "", "build profile name"),
			Flag("release", "", "shorthand for --profile release"),
			Option("target", "", "cross-compilation target triple"),
			Option("features", "", "comma-separated feature flags"),
			Flag("no-default-features", "", "drop manifest default features"),
			OptionDefault("backend", "", "llvm", "code generation backend"),
			Option("emit", "", "artifact mode"),
		}
	case "run":
		return []FlagSpec{
			Flag("offline", "", "do not fetch dependencies"),
			Flag("locked", "", "fail if osty.lock would change"),
			Flag("frozen", "", "imply --locked --offline"),
			Option("profile", "", "build profile name"),
			Flag("release", "", "shorthand for --profile release"),
			Option("target", "", "cross-compilation target triple"),
			Option("features", "", "comma-separated feature flags"),
			Flag("no-default-features", "", "drop manifest default features"),
			OptionDefault("backend", "", "llvm", "code generation backend"),
			Option("emit", "", "artifact mode"),
			Flag("airepair", "", "enable AI repair"),
			Flag("no-airepair", "", "disable AI repair"),
			Option("airepair-mode", "", "AI repair mode"),
		}
	case "test":
		return []FlagSpec{
			Flag("offline", "", "do not fetch dependencies"),
			Flag("locked", "", "fail if osty.lock would change"),
			Flag("frozen", "", "imply --locked --offline"),
			Option("profile", "", "build profile name"),
			Flag("release", "", "shorthand for --profile release"),
			Option("target", "", "cross-compilation target triple"),
			Option("features", "", "comma-separated feature flags"),
			Flag("no-default-features", "", "drop manifest default features"),
			OptionDefault("backend", "", "llvm", "code generation backend"),
			Option("emit", "", "artifact mode"),
			Option("seed", "", "RNG seed for test ordering"),
			Flag("serial", "", "run tests sequentially"),
			Option("jobs", "j", "max parallel test workers"),
		}
	case "add":
		return []FlagSpec{
			Option("path", "", "local filesystem dependency path"),
			Option("git", "", "git dependency URL"),
			Option("tag", "", "git tag to pin (with --git)"),
			Option("branch", "", "git branch (with --git)"),
			Option("rev", "", "git commit SHA (with --git)"),
			Option("version", "", "explicit version requirement"),
			Option("rename", "", "local alias for the dep"),
			Flag("dev", "", "add to [dev-dependencies]"),
			Flag("offline", "", "do not fetch dependencies"),
		}
	case "update":
		return []FlagSpec{
			Flag("offline", "", "do not fetch dependencies"),
		}
	case "lint":
		return []FlagSpec{
			Option("explain", "", "describe a lint rule and exit"),
			Flag("list", "", "print every lint rule"),
			Flag("fix", "", "apply machine-applicable suggestions"),
			Flag("fix-dry-run", "", "show fixed source without writing"),
			Flag("strict", "", "exit 1 on warnings (CI mode)"),
		}
	case "ci":
		return []FlagSpec{
			Flag("format", "", "run format check"),
			Flag("lint", "", "run lint check"),
			Flag("policy", "", "run policy check"),
			Flag("lockfile", "", "run lockfile check"),
			Flag("release", "", "run release readiness check"),
			Flag("semver", "", "run semver diff check"),
			Option("baseline", "", "path to baseline API snapshot"),
			Flag("semver-warn-only", "", "emit warnings instead of errors for breaking changes"),
			Flag("strict", "", "exit 1 on warnings"),
		}
	case "doc":
		return []FlagSpec{
			OptionDefault("format", "", "markdown", "output format"),
			Option("out", "", "write docs to PATH instead of stdout"),
			Option("title", "", "document title"),
			Flag("check", "", "exit 1 if doc generation would produce diffs"),
			Flag("verify-examples", "", "compile code examples in doc comments"),
		}
	case "publish":
		return []FlagSpec{
			Option("registry", "", "target registry name"),
			Option("token", "t", "API token"),
			Flag("dry-run", "", "build the tarball but do not upload"),
		}
	case "search":
		return []FlagSpec{
			Option("registry", "", "registry name to query"),
			OptionDefault("limit", "", "20", "max number of hits"),
		}
	case "yank", "unyank":
		return []FlagSpec{
			RequiredOption("version", "v", "version to yank/unyank"),
		}
	case "login":
		return []FlagSpec{
			Option("registry", "", "registry name"),
		}
	case "logout":
		return []FlagSpec{
			Option("registry", "", "registry name"),
			Flag("all", "", "forget all stored tokens"),
		}
	case "registry":
		return []FlagSpec{
			OptionDefault("addr", "", "127.0.0.1:7878", "address to listen on"),
			Option("root", "", "registry data directory"),
			Option("token", "", "bearer token for publish/yank"),
			Flag("allow-anonymous-writes", "", "accept publish/yank without tokens"),
		}
	case "fetch":
		return []FlagSpec{
			Flag("locked", "", "fail if osty.lock would change"),
			Flag("frozen", "", "imply --locked --offline"),
		}
	case "info":
		return []FlagSpec{
			Flag("all-versions", "", "show all published versions"),
		}
	case "profiles":
		return []FlagSpec{
			Flag("verbose", "v", "also print go-flags, env, and inheritance"),
		}
	case "pipeline":
		return []FlagSpec{
			Flag("json", "", "emit per-phase timing as JSON"),
			Flag("trace", "", "stream per-phase timing to stderr"),
			OptionDefault("backend", "", "llvm", "code generation backend"),
		}
	case "scaffold":
		return []FlagSpec{
			Option("name", "", "output basename"),
		}
	case "gui":
		return nil
	case "cache":
		return []FlagSpec{
			Option("profile", "", "profile name for cache info"),
			Option("target", "", "target triple for cache info"),
			Option("backend", "", "backend name for cache info"),
		}
	case "targets", "features", "remove", "rm":
		return nil
	}
	return nil
}

func parsedToSubFlags(parsed ParsedArgs) map[string]string {
	sf := make(map[string]string, len(parsed.Values)+len(parsed.Present))
	for k, v := range parsed.Values {
		sf[k] = v
	}
	for k, v := range parsed.Present {
		if v {
			sf[k] = "true"
		}
	}
	return sf
}
