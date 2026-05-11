package check

import "github.com/osty/osty/internal/resolve"

// init wires resolve's native-check hook to the production factory.
// Importing internal/check anywhere — production CLI or test binary — is
// enough to swap the default embedded path for the factory-routed one;
// after UseManagedSubprocessChecker / InstallSubprocessCheckerForPackageTests
// installs a subprocess factory, resolve's fact-artifact computation
// (NativeResolveFacts → nativeResolveFactArtifacts) follows the same
// selection as the rest of the checker boundary.
func init() {
	resolve.NativePackageCheckHook = NativePackageCheck
}
