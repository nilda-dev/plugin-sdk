package nilda

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// THE PUBLIC SURFACE IS THE ONLY THING HERE THAT BECOMES PERMANENT.
//
// Core ratchets the size of its composition root, its untranslated-string count and its dead seams — all
// of them things that can be paid down later. This module is the one place where growth is not
// reversible: the day it is tagged v1.0.0, every exported name in it is a promise, and a name added by
// accident is as permanent as one added on purpose. Nothing was watching it.
//
// This is not a cap. It is a LEDGER: the file beside it lists what the surface is, and a change to it has
// to be a deliberate line in a diff rather than a side effect of writing a feature. Adding is cheap and
// expected — run with -update and read what the diff says you just promised. REMOVING is what this exists
// to make loud, because after v1.0 a removal is a breaking change for somebody.
//
// Read from the AST rather than `go doc`: no toolchain subprocess, and it sees exactly what a consumer
// can import — top-level exported types, funcs, consts, vars, plus the exported methods and fields of
// exported types, which are just as frozen as the names that carry them.
const surfaceLedger = "surface.txt"

func TestThePublicSurfaceIsTheOneWeMeantToPromise(t *testing.T) {
	checkSurfaceLedger(t, surfaceLedger, publicSurface(t))
}

// TestTheTestKitsSurfaceIsFrozenToo (the 2026-09-23 plugin hunt's M16). README calls nildatest "the supported
// kit" authors write their tests against, which makes its exported names as much a promise as the SDK's own —
// and nothing watched them.
func TestTheTestKitsSurfaceIsFrozenToo(t *testing.T) {
	checkSurfaceLedger(t, "nildatest/surface.txt", surfaceOf(t, "nildatest", "nildatest", 10))
}

func checkSurfaceLedger(t *testing.T, ledger string, got []string) {
	t.Helper()
	if os.Getenv("UPDATE_SURFACE") == "1" {
		if err := os.WriteFile(ledger, []byte(strings.Join(got, "\n")+"\n"), 0o644); err != nil {
			t.Fatalf("writing %s: %v", ledger, err)
		}
		t.Logf("%s rewritten with %d entries — READ THE DIFF: every line is something v1.0 will promise",
			ledger, len(got))
		return
	}
	raw, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("reading %s: %v\n\nGenerate it with: UPDATE_SURFACE=1 go test -run 'Surface' .",
			ledger, err)
	}
	var want []string
	for _, line := range strings.Split(string(raw), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			want = append(want, line)
		}
	}

	added, removed := diffSets(want, got)
	if len(added) == 0 && len(removed) == 0 {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "the public surface changed (%d entries, was %d).\n\n", len(got), len(want))
	if len(removed) > 0 {
		fmt.Fprintf(&b, "  REMOVED — after v1.0 this is a breaking change for every plugin that used it:\n")
		for _, s := range removed {
			fmt.Fprintf(&b, "    - %s\n", s)
		}
	}
	if len(added) > 0 {
		fmt.Fprintf(&b, "  ADDED — each of these is something a v1.0 tag would promise forever:\n")
		for _, s := range added {
			fmt.Fprintf(&b, "    + %s\n", s)
		}
	}
	fmt.Fprintf(&b, "\n  If every line above is intended: UPDATE_SURFACE=1 go test -run 'Surface' .\n"+
		"  and commit %s with them. That file is the record of what this module has agreed to keep.", ledger)
	t.Fatal(b.String())
}

// TestTheGeneratedContractStaysOutOfTheSurface. handshake.go's design is that the wire contract evolves
// behind ProtocolVersion; that is only true while the generated types are NOT part of the Go API. One
// exported signature naming a contract.* type puts plugin.pb.go inside whatever v1.0 promises.
//
// Three exceptions, NAMED with their reason rather than counted. Each is a place where hiding the type
// would be pretending: two are the Core<->plugin transport seam, which IS the protobuf, and the third
// cannot be built anywhere else. They are scoped out of the stable surface in README.md instead.
//
// The list is the point. It was written from a hand review that found ONE leak; the first run of this
// guard found the other two, both exported STRUCT FIELDS, which is not where anybody looks.
func TestTheGeneratedContractStaysOutOfTheSurface(t *testing.T) {
	allowed := map[string]string{
		// Core's handle on a running plugin. Core calls handle.Plugin.Init/HandleHook/HandleEvent with
		// contract request types — it is the other end of the gRPC, so the generated client is what it
		// holds. Hiding the field behind an accessor would move the same types one line and call it an API.
		"field PluginClient.Plugin": "the Core-side transport seam; Core IS the other end of the protocol",
		// The plugin-side service implementation, set by Serve and read by go-plugin's GRPCServer.
		"field GRPCPlugin.Impl": "the plugin-side transport seam, registered with the generated server",
		// The one test constructor. The fields that make a Core work — its host client and its API client —
		// are unexported, so no other package can build a working *Core — which is also why it cannot move
		// into nildatest.
		"func NewCoreForTest": "no other package can set Core's unexported fields",
	}
	for _, entry := range publicSurface(t) {
		if !strings.Contains(entry, "contract.") {
			continue
		}
		name := entry
		if i := strings.Index(entry, "("); i > 0 && strings.HasPrefix(entry, "func ") {
			name = entry[:i]
		} else if i := strings.Index(entry, " contract."); i > 0 {
			name = entry[:i]
		}
		if _, ok := allowed[strings.TrimSpace(name)]; ok {
			continue
		}
		t.Errorf("%s names a generated protobuf type in the public surface — a v1.0 tag would freeze "+
			"contract/plugin.pb.go with it, and the protocol could no longer move behind ProtocolVersion",
			entry)
	}
}

// publicSurface lists every exported name a consumer of this package can reach, sorted.
func publicSurface(t *testing.T) []string {
	t.Helper()
	return surfaceOf(t, ".", "nilda", 50)
}

// surfaceOf is publicSurface for any package directory of this module. floor is the fewest names a correct
// parse can find there.
func surfaceOf(t *testing.T, dir, name string, floor int) []string {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing the package: %v", err)
	}
	pkg, ok := pkgs[name]
	if !ok {
		t.Fatalf("package %s not found in %s; parsed %d package(s)", name, dir, len(pkgs))
	}

	var out []string
	for _, f := range pkg.Files {
		for _, d := range f.Decls {
			switch decl := d.(type) {
			case *ast.FuncDecl:
				out = append(out, funcEntry(fset, decl)...)
			case *ast.GenDecl:
				out = append(out, genEntries(fset, decl)...)
			}
		}
	}
	sort.Strings(out)

	// A floor, for the reason every generator in this ecosystem has one: a parser that silently found
	// nothing would rewrite the ledger empty and the guard would agree with anything forever.
	if len(out) < floor {
		t.Fatalf("only found %d exported names in %s; the parse is broken, not the surface", len(out), dir)
	}
	return out
}

func funcEntry(fset *token.FileSet, fn *ast.FuncDecl) []string {
	if fn.Name == nil || !fn.Name.IsExported() {
		return nil
	}
	if fn.Recv == nil {
		return []string{"func " + fn.Name.Name + params(fset, fn.Type)}
	}
	recv := typeString(fset, fn.Recv.List[0].Type)
	bare := strings.TrimPrefix(recv, "*")
	if !ast.IsExported(bare) {
		return nil // a method on an unexported type is not reachable
	}
	return []string{"method (" + recv + ") " + fn.Name.Name + params(fset, fn.Type)}
}

func genEntries(fset *token.FileSet, decl *ast.GenDecl) []string {
	var out []string
	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			if !s.Name.IsExported() {
				continue
			}
			out = append(out, "type "+s.Name.Name)
			out = append(out, structFields(fset, s)...)
			out = append(out, interfaceMethods(fset, s)...)
		case *ast.ValueSpec:
			kind := "const"
			if decl.Tok == token.VAR {
				kind = "var"
			}
			for _, n := range s.Names {
				if n.IsExported() {
					out = append(out, kind+" "+n.Name)
				}
			}
		}
	}
	return out
}

func structFields(fset *token.FileSet, s *ast.TypeSpec) []string {
	st, ok := s.Type.(*ast.StructType)
	if !ok || st.Fields == nil {
		return nil
	}
	var out []string
	for _, f := range st.Fields.List {
		for _, n := range f.Names {
			if n.IsExported() {
				out = append(out, "field "+s.Name.Name+"."+n.Name+" "+typeString(fset, f.Type)+jsonTagSuffix(f))
			}
		}
	}
	return out
}

// jsonTagSuffix puts the WIRE NAME on the ledger line, and it is not decoration.
//
// The Go name and the json tag are two different promises to two different audiences. A plugin recompiled
// against a new SDK follows a renamed Go FIELD; a plugin already built and deployed sends the old TAG
// forever. So renaming a tag while keeping the Go name breaks every installed plugin and changes nothing a
// compiler, a type check or this ledger could see — which was exactly true here until this line existed:
// `json:"badge"` was renamed to `json:"flag"` on both sides of the wire, the whole suite stayed green, and
// every deployed shop's badge would have gone silently blank.
//
// Recorded for every tagged field rather than for commerce's, because every wire struct in this module has
// the same property and picking one would guard the instance instead of the class.
func jsonTagSuffix(f *ast.Field) string {
	if f.Tag == nil {
		return ""
	}
	tag := reflect.StructTag(strings.Trim(f.Tag.Value, "`")).Get("json")
	name, _, _ := strings.Cut(tag, ",")
	if name == "" || name == "-" {
		return ""
	}
	return " json:" + name
}

func interfaceMethods(fset *token.FileSet, s *ast.TypeSpec) []string {
	it, ok := s.Type.(*ast.InterfaceType)
	if !ok || it.Methods == nil {
		return nil
	}
	var out []string
	for _, m := range it.Methods.List {
		ft, isFunc := m.Type.(*ast.FuncType)
		for _, n := range m.Names {
			if !n.IsExported() {
				continue
			}
			sig := ""
			if isFunc {
				sig = params(fset, ft)
			}
			out = append(out, "imethod "+s.Name.Name+"."+n.Name+sig)
		}
	}
	return out
}

// params renders a signature's argument and result types — the part that a caller's code depends on, and
// the part a rename or a reorder breaks.
func params(fset *token.FileSet, ft *ast.FuncType) string {
	render := func(fl *ast.FieldList) string {
		if fl == nil {
			return ""
		}
		var parts []string
		for _, f := range fl.List {
			n := len(f.Names)
			if n == 0 {
				n = 1
			}
			for i := 0; i < n; i++ {
				parts = append(parts, typeString(fset, f.Type))
			}
		}
		return strings.Join(parts, ", ")
	}
	out := "(" + render(ft.Params) + ")"
	if r := render(ft.Results); r != "" {
		out += " (" + r + ")"
	}
	return out
}

func typeString(fset *token.FileSet, e ast.Expr) string {
	var b strings.Builder
	if err := printExpr(&b, fset, e); err != nil {
		return "?"
	}
	return b.String()
}

func diffSets(want, got []string) (added, removed []string) {
	inWant := map[string]bool{}
	for _, s := range want {
		inWant[s] = true
	}
	inGot := map[string]bool{}
	for _, s := range got {
		inGot[s] = true
	}
	for _, s := range got {
		if !inWant[s] {
			added = append(added, s)
		}
	}
	for _, s := range want {
		if !inGot[s] {
			removed = append(removed, s)
		}
	}
	return added, removed
}

// printExpr renders one type expression. go/printer, so a signature reads the way it is written.
func printExpr(w *strings.Builder, fset *token.FileSet, e ast.Expr) error {
	return printer.Fprint(w, fset, e)
}

// TestTheReadmeCountsTheSurfaceItPromises. README.md states how many entries the promise covers, and a
// number written in prose drifts the first time somebody exports something. A compatibility policy whose
// own figure is stale is the kind of document a reader stops trusting for the parts that still matter.
func TestTheReadmeCountsTheSurfaceItPromises(t *testing.T) {
	raw, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("reading README.md: %v", err)
	}
	want := fmt.Sprintf("%d entries today", len(publicSurface(t)))
	if !strings.Contains(string(raw), want) {
		t.Errorf("README.md's compatibility section does not say %q — the surface moved and the promise "+
			"still describes the old one", want)
	}
	// And the kit's: its count, and every entry of it that names a generated type — the ones README scopes
	// out with contract/ — named there by method, so the exception cannot grow without the promise saying so.
	kit := surfaceOf(t, "nildatest", "nildatest", 10)
	readme := strings.Join(strings.Fields(string(raw)), " ")
	if want := fmt.Sprintf("`nildatest/surface.txt` (%d entries)", len(kit)); !strings.Contains(readme, want) {
		t.Errorf("README.md does not say %q — the kit's surface moved and the promise describes the old one", want)
	}
	words := map[int]string{1: "one", 2: "two", 3: "three", 4: "four", 5: "five", 6: "six", 7: "seven", 8: "eight", 9: "nine"}
	var generated int
	for _, entry := range kit {
		if !strings.Contains(entry, "contract.") && !strings.Contains(entry, "grpc.") {
			continue
		}
		generated++
		m := regexp.MustCompile(`^method \(\*Host\) (\w+)\(`).FindStringSubmatch(entry)
		if m == nil {
			t.Errorf("nildatest's %q names a generated type and is not a *Host method — README's exception "+
				"covers only those; decide what this one is and say so there", entry)
			continue
		}
		if !strings.Contains(readme, "`"+m[1]+"`") {
			t.Errorf("nildatest's *Host.%s names a generated type and README's exception does not list it", m[1])
		}
	}
	if w, ok := words[generated]; !ok || !strings.Contains(readme, w+" of its `*Host` methods") {
		t.Errorf("README.md does not say %q of nildatest's *Host methods name generated types", w)
	}
}
