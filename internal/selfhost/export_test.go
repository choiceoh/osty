package selfhost

// ImportFnParamNamesForTest re-exports selfhostImportFnParamNames for
// external test packages so the synthesis logic can be unit-tested without
// routing through CheckPackageStructured. Production code keeps the
// unexported spelling.
func ImportFnParamNamesForTest(paramNames []string, paramDefaults []bool) []string {
	return selfhostImportFnParamNames(paramNames, paramDefaults)
}
