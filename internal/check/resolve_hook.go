package check

import "github.com/osty/osty/internal/resolve"

// init wires resolve's native-check hook to `NativePackageCheck`.
// Production installs the managed subprocess via `UseManagedSubprocessChecker`;
// tests typically install it from TestMain via
// `InstallSubprocessCheckerForPackageTests`. Once installed, resolve's
// fact-artifact computation (NativeResolveFacts → nativeResolveFactArtifacts)
// follows the same checker selection as the rest of the checker boundary.
func init() {
	resolve.NativePackageCheckHook = NativePackageCheck
}
