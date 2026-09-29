package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Every fixed message the backend hands a user must be catalogued. Collected
// from source, so a new message cannot skip the catalog.
func TestUserMessagesAreCatalogued(t *testing.T) {
	dirs, err := filepath.Glob("../*")
	if err != nil {
		t.Fatal(err)
	}
	messages := map[string]string{}
	for _, dir := range dirs {
		collectMessages(t, dir, messages)
	}
	if len(messages) < 100 {
		t.Fatalf("found %d messages; the walker no longer matches the call sites", len(messages))
	}

	var misses []string
	for msg, where := range messages {
		if _, ok := catalog[msg]; !ok {
			misses = append(misses, where+": "+strconv.Quote(msg))
		}
	}
	if len(misses) > 0 {
		slices.Sort(misses)
		t.Errorf("%d uncatalogued messages:\n%s", len(misses), strings.Join(misses, "\n"))
	}
}

func collectMessages(t *testing.T, dir string, messages map[string]string) {
	names, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	consts := map[string]ast.Expr{}
	var files []*ast.File
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
		collectConsts(f, consts)
	}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			if arg := messageArg(n); arg != nil {
				if s, ok := stringValue(arg, consts); ok {
					messages[s] = fset.Position(arg.Pos()).String()
				}
			}
			return true
		})
	}
}

func collectConsts(f *ast.File, consts map[string]ast.Expr) {
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, name := range vs.Names {
				if i < len(vs.Values) {
					consts[name.Name] = vs.Values[i]
				}
			}
		}
	}
}

// messageArg is the user-facing text of a call or literal that carries one:
// appapi's writeError and writeErrorf, settings' errInvalid, setup's
// invalidAdmin, and T or Errorf from this package.
func messageArg(n ast.Node) ast.Expr {
	switch n := n.(type) {
	case *ast.CompositeLit:
		if id, ok := n.Type.(*ast.Ident); ok && id.Name == "invalidAdmin" && len(n.Elts) == 1 {
			return n.Elts[0]
		}
	case *ast.CallExpr:
		return callMessageArg(n)
	}
	return nil
}

func callMessageArg(call *ast.CallExpr) ast.Expr {
	arg := func(i int) ast.Expr {
		if i < len(call.Args) {
			return call.Args[i]
		}
		return nil
	}
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		switch fun.Name {
		case "writeError", "writeErrorf":
			return arg(2)
		case "errInvalid", "Errorf":
			return arg(0)
		case "T":
			return arg(1)
		}
	case *ast.SelectorExpr:
		if pkg, ok := fun.X.(*ast.Ident); ok && pkg.Name == "i18n" {
			switch fun.Sel.Name {
			case "Errorf":
				return arg(0)
			case "T":
				return arg(1)
			}
		}
	}
	return nil
}

func stringValue(x ast.Expr, consts map[string]ast.Expr) (string, bool) {
	switch x := x.(type) {
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			s, err := strconv.Unquote(x.Value)
			return s, err == nil
		}
	case *ast.Ident:
		if c, ok := consts[x.Name]; ok {
			return stringValue(c, consts)
		}
	case *ast.BinaryExpr:
		if x.Op == token.ADD {
			l, lok := stringValue(x.X, consts)
			r, rok := stringValue(x.Y, consts)
			return l + r, lok && rok
		}
	}
	return "", false
}
