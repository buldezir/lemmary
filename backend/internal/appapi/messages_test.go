package appapi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"lemmary/backend/internal/i18n"
)

// Every fixed message this package hands to a user must have a German and a
// Russian entry. Collected from source so a new message cannot skip the catalog.
func TestUserMessagesAreTranslated(t *testing.T) {
	names, err := filepath.Glob("*.go")
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

	messages := map[string]bool{}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			if arg := messageArg(n); arg != nil {
				if s, ok := stringValue(arg, consts); ok {
					messages[s] = true
				}
			}
			return true
		})
	}
	if len(messages) == 0 {
		t.Fatal("found no messages; the walker no longer matches the call sites")
	}

	var misses []string
	for msg := range messages {
		for _, lang := range []string{"de", "ru"} {
			if i18n.T(lang, msg) == msg {
				misses = append(misses, lang+": "+strconv.Quote(msg))
			}
		}
	}
	if len(misses) > 0 {
		slices.Sort(misses)
		t.Errorf("%d untranslated messages:\n%s", len(misses), strings.Join(misses, "\n"))
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

// messageArg is the detail of writeError, the format of writeErrorf, or the
// message of i18n.T.
func messageArg(n ast.Node) ast.Expr {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return nil
	}
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		if (fun.Name == "writeError" || fun.Name == "writeErrorf") && len(call.Args) >= 3 {
			return call.Args[2]
		}
	case *ast.SelectorExpr:
		if pkg, ok := fun.X.(*ast.Ident); ok && pkg.Name == "i18n" && fun.Sel.Name == "T" && len(call.Args) == 2 {
			return call.Args[1]
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
