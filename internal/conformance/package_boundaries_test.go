package conformance

import (
	"go/parser"
	"go/token"
	"io/fs"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const internalRouteImport = "github.com/2218342221/RouteMorphSDK/internal/route/"

func TestRoutePackagesDoNotDependOnOtherRoutePackages(t *testing.T) {
	sdkRoot := sdkRootDir(t)
	routeRoot := filepath.Join(sdkRoot, "internal", "route")
	err := filepath.WalkDir(routeRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		owner := filepath.Base(filepath.Dir(path))
		for _, imported := range file.Imports {
			importPath, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return err
			}
			if strings.HasPrefix(importPath, internalRouteImport) && pathpkg.Base(importPath) != owner {
				t.Errorf("%s imports sibling route package %s", path, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFacadeDoesNotOwnProtocolPairImplementations(t *testing.T) {
	sdkRoot := sdkRootDir(t)
	entries, err := filepath.Glob(filepath.Join(sdkRoot, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range entries {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imported := range file.Imports {
			importPath, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(importPath, internalRouteImport) {
				t.Errorf("public facade file %s imports route implementation %s", path, importPath)
			}
		}
	}
}

func sdkRootDir(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate conformance test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
}
