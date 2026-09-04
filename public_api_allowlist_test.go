package routemorph_test

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestPublicAPIAllowlist(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate SDK source")
	}
	directory := filepath.Dir(source)
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	fileSet := token.NewFileSet()
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		matched, err := build.Default.MatchFile(directory, name)
		if err != nil {
			t.Fatalf("match %s: %v", name, err)
		}
		if !matched {
			continue
		}
		file, err := parser.ParseFile(fileSet, filepath.Join(directory, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		if file.Name.Name == "routemorph" {
			files = append(files, file)
		}
	}
	if len(files) == 0 {
		t.Fatal("routemorph package not found")
	}
	var symbols []string
	for _, file := range files {
		for _, declaration := range file.Decls {
			switch declaration := declaration.(type) {
			case *ast.FuncDecl:
				if !declaration.Name.IsExported() {
					continue
				}
				if declaration.Recv == nil {
					symbols = append(symbols, "func "+declaration.Name.Name)
					continue
				}
				if receiver, ok := exportedReceiver(declaration.Recv.List[0].Type); ok {
					symbols = append(symbols, "method "+receiver+"."+declaration.Name.Name)
				}
			case *ast.GenDecl:
				for _, spec := range declaration.Specs {
					switch spec := spec.(type) {
					case *ast.TypeSpec:
						if spec.Name.IsExported() {
							symbols = append(symbols, "type "+spec.Name.Name)
						}
					case *ast.ValueSpec:
						for _, name := range spec.Names {
							if name.IsExported() {
								symbols = append(symbols, declaration.Tok.String()+" "+name.Name)
							}
						}
					}
				}
			}
		}
	}
	sort.Strings(symbols)
	want := []string{
		"const ProtocolChat", "const ProtocolGenerateContent", "const ProtocolMessages", "const ProtocolResponses",
		"const RouteModeBuffered", "const RouteModeIncremental", "const RouteModeNative",
		"func EncodeError", "func InspectRequest", "func NewAnthropicMessagesAdapter", "func NewGeminiGenerateContentAdapter",
		"func NewOpenAIChatCompletionsAdapter", "func NewOpenAIResponsesAdapter", "func ParseProtocol", "func WithModel",
		"func PrepareRequest",
		"method Adapter.AnthropicMessages", "method Adapter.GeminiGenerateContent", "method Adapter.OpenAIChatCompletions", "method Adapter.OpenAIResponses",
		"method ConversionError.Error", "method ConversionError.Unwrap", "method Protocol.Valid", "method Response.WriteTo", "method ResponseMeta.Diagnostics",
		"type Adapter", "type ConversionError", "type Diagnostic", "type EncodedError", "type Option", "type Protocol", "type ProtocolError",
		"type Request", "type RequestInfo", "type Response", "type ResponseMeta", "type RouteMode",
		"var ErrInvalidPayload", "var ErrInvalidPlan", "var ErrRouteNotFound", "var ErrUnsupported", "var ErrUpstreamResponse",
	}
	sort.Strings(want)
	if !reflect.DeepEqual(symbols, want) {
		t.Fatalf("public API changed\n got: %v\nwant: %v", symbols, want)
	}
}

func exportedReceiver(expression ast.Expr) (string, bool) {
	if pointer, ok := expression.(*ast.StarExpr); ok {
		expression = pointer.X
	}
	name, ok := expression.(*ast.Ident)
	if !ok || !name.IsExported() {
		return "", false
	}
	return name.Name, true
}
