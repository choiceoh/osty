package check

import (
	"testing"

	"github.com/osty/osty/internal/stdlib"
)

func TestSelfhostFileAcceptsStdOptionResultModuleHelpers(t *testing.T) {
	src := []byte(`use std.option
use std.result

enum FsError {
    NotFound(String)

    pub fn message(self) -> String {
        match self {
            NotFound(path) -> "missing {path}",
        }
    }
}

fn main() {
    let flattenedMaybe: Int? = option.flatten::<Int>(Some(Some(3)))
    let transposedMaybe: Result<Int?, FsError> = option.transpose::<Int, FsError>(Some(Ok(4)))
    let unzippedMaybe: (Int?, String?) = option.unzip(Some((5, "zip")))
    let valuesMaybe: List<Int> = option.values::<Int>([Some(1), None, Some(2)])
    let anyMaybe: Int? = option.any::<Int>([None, Some(9), None])
    let allMaybe: List<Int>? = option.all::<Int>([Some(1), Some(2), Some(3)])
    let traversedMaybe: List<Int>? = option.traverse::<Int, Int>([1, 2, 3], |n: Int| if n > 0 { Some(n * 2) } else { None })
    let filterMappedMaybe: List<Int> = option.filterMap::<Int, Int>([1, 0, 2], |n: Int| if n > 0 { Some(n * 10) } else { None })
    let foundMaybe: String? = option.findMap::<Int, String>([0, 1, 2], |n: Int| if n == 2 { Some("two") } else { None })
    let mapped2Maybe: Int? = option.map2::<Int, Int, Int>(Some(2), Some(3), |a: Int, b: Int| a + b)
    let mapped3Maybe: Int? = option.map3::<Int, Int, Int, Int>(Some(2), Some(3), Some(4), |a: Int, b: Int, c: Int| a + b + c)

    let flattenedResult: Result<Int, FsError> = result.flatten::<Int, FsError>(Ok(Ok(6)))
    let transposedResult: Result<Int, FsError>? = result.transpose::<Int, FsError>(Ok(Some(7)))
    let valuesResult: List<Int> = result.values::<Int, FsError>([Ok(1), Err(FsError.NotFound("a")), Ok(2)])
    let errorsResult: List<FsError> = result.errors::<Int, FsError>([Ok(1), Err(FsError.NotFound("b"))])
    let partitionedResult: (List<Int>, List<FsError>) = result.partition::<Int, FsError>([Ok(3), Err(FsError.NotFound("c"))])
    let allResult: Result<List<Int>, FsError> = result.all::<Int, FsError>([Ok(4), Ok(5)])
    let traversedResult: Result<List<Int>, FsError> = result.traverse::<Int, Int, FsError>([1, 2], |n: Int| Ok(n * 3))
    let mapped2Result: Result<Int, FsError> = result.map2::<Int, Int, FsError, Int>(Ok(2), Ok(3), |a: Int, b: Int| a * b)
    let mapped3Result: Result<Int, FsError> = result.map3::<Int, Int, Int, FsError, Int>(Ok(2), Ok(3), Ok(4), |a: Int, b: Int, c: Int| a + b + c)
    let allErrorsResult: Result<List<Int>, List<FsError>> = result.allErrors::<Int, FsError>([Ok(1), Err(FsError.NotFound("d")), Err(FsError.NotFound("e"))])
    let traversedErrorsResult: Result<List<Int>, List<FsError>> = result.traverseErrors::<Int, Int, FsError>([1, -1, 2], |n: Int| if n > 0 { Ok(n) } else { Err(FsError.NotFound("neg")) })

    let _ = (flattenedMaybe, transposedMaybe, unzippedMaybe, valuesMaybe, anyMaybe, allMaybe, mapped2Maybe, mapped3Maybe)
    let _ = (traversedMaybe, filterMappedMaybe, foundMaybe, flattenedResult, transposedResult, mapped2Result, mapped3Result)
    let _ = (valuesResult, errorsResult, partitionedResult, allResult, traversedResult, allErrorsResult, traversedErrorsResult)
}
`)

	file, res := parseResolvedFile(t, src)
	chk := SelfhostFile(file, res, Opts{Source: src, Stdlib: stdlib.LoadCached()})
	if len(chk.Diags) != 0 {
		t.Fatalf("SelfhostFile diagnostics = %v, want none", chk.Diags)
	}
}
