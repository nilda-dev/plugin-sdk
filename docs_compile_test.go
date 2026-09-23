package nilda_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// PLUGIN_SDK.md opens with a promise:
//
//	"Every code example here is real: the types, methods and field names below exist in this module. The
//	 previous version of this document showed nilda.ServeApp, core.Datastore.DB(), core.Routes.Mount() and
//	 an OnContentSaved handler — none of which ever existed. Someone following it wrote code that did not
//	 compile."
//
// That promise was made in prose and kept by hand, which is the same arrangement that produced the four
// invented methods it apologises for. It had already slipped again: the getting-started example — the first
// code anybody copies — imported "encoding/json" and never used it, which in Go is a compile error. A
// developer's first act with this SDK was to fix our snippet.
//
// So the promise is a test now.
//
// What it checks is the part that is mechanically checkable and that actually bit us: every identifier a
// snippet reaches for through the `nilda.` package qualifier must exist in this package, and every snippet
// must at least PARSE as Go. It cannot type-check fragments — most examples are deliberately partial, a few
// lines lifted out of a function — but "the method you documented does not exist" and "this does not lex as
// Go" are the two failures that have actually shipped, and both are caught here.

var (
	goBlock  = regexp.MustCompile("(?s)```go\\n(.*?)```")
	qualifie = regexp.MustCompile(`\bnilda\.([A-Z][A-Za-z0-9_]*)`)
	// testKit is the same check for the test kit, which the payment guide's examples reach for as much as
	// the SDK itself.
	testKit = regexp.MustCompile(`\bnildatest\.([A-Z][A-Za-z0-9_]*)`)
)

// guides are the documents whose code examples an author copies.
var guides = []string{"docs/PLUGIN_SDK.md", "docs/PAYMENTS.md", "README.md"}

// TestDocumentedIdentifiersExist fails when the documentation names something this module does not export.
func TestDocumentedIdentifiersExist(t *testing.T) {
	exported := exportedNames(t, ".")
	kit := exportedNames(t, "nildatest")

	for _, doc := range guides {
		src, err := os.ReadFile(doc)
		if err != nil {
			t.Fatalf("reading %s: %v", doc, err)
		}
		// Code blocks only. The prose deliberately names things that do NOT exist — the header warns about
		// `nilda.ServeApp` and three other invented methods by name — and a check that could not tell a
		// warning from an example would force the documentation to stop describing its own history.
		for _, block := range goBlock.FindAllStringSubmatch(string(src), -1) {
			for _, m := range qualifie.FindAllStringSubmatch(block[1], -1) {
				name := m[1]
				if !exported[name] {
					t.Errorf("%s documents nilda.%s in a code example, and this module does not export it — "+
						"someone following the documentation writes code that does not compile", doc, name)
				}
			}
			for _, m := range testKit.FindAllStringSubmatch(block[1], -1) {
				if !kit[m[1]] {
					t.Errorf("%s documents nildatest.%s in a code example, and the test kit does not export it",
						doc, m[1])
				}
			}
		}
	}
}

// TestTheGettingStartedExampleIsWholeGo parses the complete programs in the documentation.
//
// Only the snippets that declare `package main` are parsed as files: those are the ones presented as a
// program a reader can copy whole, and they are the ones that must stand on their own. A fragment is
// wrapped in a function first, so a few lines lifted out of a method still get lexed.
func TestTheGettingStartedExampleIsWholeGo(t *testing.T) {
	if whole := parsesAsGo(t, "docs/PLUGIN_SDK.md"); whole == 0 {
		t.Error("no complete `package main` example in PLUGIN_SDK.md — the one thing a new author copies")
	}
	parsesAsGo(t, "docs/PAYMENTS.md")
}

// parsesAsGo checks every Go block of one document and returns how many were complete programs.
func parsesAsGo(t *testing.T, doc string) (whole int) {
	t.Helper()
	src, err := os.ReadFile(doc)
	if err != nil {
		t.Fatal(err)
	}
	blocks := goBlock.FindAllStringSubmatch(string(src), -1)
	if len(blocks) == 0 {
		t.Fatalf("found no Go blocks in %s — the extraction is broken and this test proves nothing", doc)
	}

	for i, b := range blocks {
		code := b[1]
		fset := token.NewFileSet()
		if strings.HasPrefix(strings.TrimSpace(code), "package ") {
			whole++
			if _, err := parser.ParseFile(fset, "snippet.go", code, parser.AllErrors); err != nil {
				t.Errorf("the complete program in Go block %d does not parse: %v", i+1, err)
			}
			// An unused import is a compile error, and it is the one that actually shipped. The parser will
			// not catch it, so check the shape directly: a program that imports a package it never qualifies.
			for _, imp := range unusedImports(code) {
				t.Errorf("Go block %d imports %q and never uses it — this does not compile", i+1, imp)
			}
			continue
		}
		wrapped := "package p\nfunc _() {\n" + code + "\n}\n"
		if _, err := parser.ParseFile(fset, "snippet.go", wrapped, parser.AllErrors); err != nil {
			// Declaration-level fragments (a method, a type) do not fit inside a function; try again at file
			// level before calling it broken.
			if _, err2 := parser.ParseFile(fset, "snippet.go", "package p\n"+code, parser.AllErrors); err2 != nil {
				t.Errorf("%s: Go block %d does not parse as Go, in either form:\n  in a function: %v\n  at file level: %v",
					doc, i+1, err, err2)
			}
		}
	}
	return whole
}

// unusedImports reports imports whose package name never appears as a qualifier in the body.
func unusedImports(code string) []string {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "snippet.go", code, parser.AllErrors)
	if err != nil {
		return nil // a parse failure is reported by the caller; nothing to add here
	}
	body := code
	if idx := strings.Index(code, ")"); idx > 0 {
		body = code[idx:] // everything after the import block
	}
	var out []string
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		name := path
		if i := strings.LastIndex(path, "/"); i >= 0 {
			name = path[i+1:]
		}
		if imp.Name != nil {
			name = imp.Name.Name
			if name == "_" || name == "." {
				continue
			}
		}
		if !strings.Contains(body, name+".") {
			out = append(out, path)
		}
	}
	return out
}

// exportedNames reads one package's own source for its exported identifiers. Reading the source rather
// than using reflection is what lets it see types, constants and functions alike — reflection only reaches
// values, and most of what the documentation names is a type.
func exportedNames(t *testing.T, dir string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.AllErrors)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for ident := range f.Scope.Objects {
			if ident != "" && ident[0] >= 'A' && ident[0] <= 'Z' {
				out[ident] = true
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("found no exported identifiers — the scan is broken and this test proves nothing")
	}
	return out
}
