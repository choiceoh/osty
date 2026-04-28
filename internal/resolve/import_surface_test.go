package resolve

import (
	"testing"

	"github.com/osty/osty/internal/selfhost"
)

func TestPackageImportSurfacesCoverUseShapes(t *testing.T) {
	foo := packageWithRun("foo", []byte(`pub fn bar() -> Int { 1 }
pub struct Baz {}
`))
	fooBar := packageWithRun("bar", []byte(`pub fn leaf() -> Int { 2 }
`))
	fooUpperBar := packageWithRun("Bar", []byte(`pub fn make() -> Int { 3 }
`))
	client := packageWithRun("client", []byte(`use foo
use foo.bar
use foo::{bar as callBar, Baz}
use foo.Bar as BazPkg
pub use foo.Bar as PublicBar
`))
	ws := &Workspace{
		Packages: map[string]*Package{
			"foo":     foo,
			"foo.bar": fooBar,
			"foo.Bar": fooUpperBar,
		},
		loading: map[string]bool{},
	}

	surfaces := PackageImportSurfaces(client, ws, nil)
	for _, alias := range []string{"foo", "bar", "BazPkg", "PublicBar"} {
		if findImportSurface(surfaces, alias) == nil {
			t.Fatalf("missing import surface alias %q in %#v", alias, surfaces)
		}
	}
	if findImportSurface(surfaces, "callBar") != nil {
		t.Fatalf("scoped member alias should not create a separate package surface: %#v", surfaces)
	}
	fooSurface := findImportSurface(surfaces, "foo")
	if !surfaceHasFn(*fooSurface, "bar") {
		t.Fatalf("foo surface missing exported fn bar: %#v", fooSurface)
	}
	if !surfaceHasType(*fooSurface, "Baz") {
		t.Fatalf("foo surface missing exported type Baz: %#v", fooSurface)
	}
	if !surfaceHasFn(*findImportSurface(surfaces, "bar"), "leaf") {
		t.Fatalf("foo.bar surface missing exported fn leaf: %#v", surfaces)
	}
	if !surfaceHasFn(*findImportSurface(surfaces, "BazPkg"), "make") {
		t.Fatalf("foo.Bar as BazPkg surface missing exported fn make: %#v", surfaces)
	}
}

func packageWithRun(name string, src []byte) *Package {
	return &Package{
		Name: name,
		Files: []*PackageFile{{
			Path:   name + ".osty",
			Source: src,
			Run:    selfhost.Run(src),
		}},
	}
}

func findImportSurface(surfaces []selfhost.PackageCheckImport, alias string) *selfhost.PackageCheckImport {
	for i := range surfaces {
		if surfaces[i].Alias == alias {
			return &surfaces[i]
		}
	}
	return nil
}

func surfaceHasFn(surface selfhost.PackageCheckImport, name string) bool {
	for _, fn := range surface.Functions {
		if fn.Owner == "" && fn.Name == name {
			return true
		}
	}
	return false
}

func surfaceHasType(surface selfhost.PackageCheckImport, name string) bool {
	for _, typ := range surface.TypeDecls {
		if typ.Name == name || typ.Name == surface.Alias+"."+name {
			return true
		}
	}
	return false
}
