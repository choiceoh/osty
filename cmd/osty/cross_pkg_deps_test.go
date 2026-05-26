package main

import (
	"slices"
	"testing"

	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/mir"
	"github.com/osty/osty/internal/resolve"
)

func TestCrossPkgLinkEnabledDefaultsOff(t *testing.T) {
	t.Setenv("OSTY_CROSS_PKG_LINK", "")

	checker := &manifest.Manifest{
		HasPackage: true,
		Package:    manifest.Package{Name: "osty-native-checker"},
	}
	if crossPkgLinkEnabled(checker) {
		t.Fatal("crossPkgLinkEnabled(native checker) = true, want default off")
	}

	ordinary := &manifest.Manifest{
		HasPackage: true,
		Package:    manifest.Package{Name: "demo"},
	}
	if crossPkgLinkEnabled(ordinary) {
		t.Fatal("crossPkgLinkEnabled(ordinary package) = true, want false")
	}
}

func TestCrossPkgLinkEnabledEnvOverride(t *testing.T) {
	ordinary := &manifest.Manifest{
		HasPackage: true,
		Package:    manifest.Package{Name: "demo"},
	}
	t.Setenv("OSTY_CROSS_PKG_LINK", "1")
	if !crossPkgLinkEnabled(ordinary) {
		t.Fatal("crossPkgLinkEnabled(env=1) = false, want true")
	}

	t.Setenv("OSTY_CROSS_PKG_LINK", "0")
	if crossPkgLinkEnabled(ordinary) {
		t.Fatal("crossPkgLinkEnabled(env=0) = true, want false")
	}
}

func TestRequiredCrossPkgSymbolsForDepFindsRefsByDotPathAndPackageName(t *testing.T) {
	module := &mir.Module{
		Functions: []*mir.Function{{
			Name: "main",
			Blocks: []*mir.BasicBlock{{
				Instrs: []mir.Instr{
					&mir.CallInstr{Callee: &mir.FnRef{Symbol: "toolchain.frontB"}},
					&mir.CallInstr{Callee: &mir.FnRef{Symbol: "toolchain.frontA"}},
					&mir.CallInstr{Callee: &mir.FnRef{Symbol: "toolchain.frontB"}},
					&mir.CallInstr{Callee: &mir.FnRef{Symbol: "other.front"}},
					&mir.AssignInstr{
						Src: &mir.UseRV{
							Op: &mir.ConstOp{Const: &mir.FnConst{Symbol: "toolchain.fromFnConst"}},
						},
					},
					&mir.CallInstr{Callee: &mir.FnRef{Symbol: "dep.fromDotPath"}},
				},
			}},
		}},
	}
	pkg := &resolve.Package{Name: "toolchain"}

	got := requiredCrossPkgSymbolsForDep(module, pkg, "dep")
	want := []string{
		"dep.fromDotPath",
		"toolchain.frontA",
		"toolchain.frontB",
		"toolchain.fromFnConst",
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("required symbols = %v, want %v", got, want)
	}
}
