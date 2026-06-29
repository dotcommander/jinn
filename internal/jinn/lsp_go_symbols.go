package jinn

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// goASTSymbols renders a shallow Go outline without requiring gopls. It is a
// fallback for lsp_query symbols, not a replacement for semantic LSP data.
func goASTSymbols(absPath string) (string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, absPath, nil, 0)
	if err != nil {
		return "", fmt.Errorf("parse go symbols: %w", err)
	}

	var sb strings.Builder
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			kind := "Function"
			if d.Recv != nil {
				kind = "Method"
			}
			writeGoSymbol(&sb, fset, kind, d.Name.Name, d.Pos())
		case *ast.GenDecl:
			writeGoGenDeclSymbols(&sb, fset, d)
		}
	}

	out := strings.TrimRight(sb.String(), "\n")
	if out == "" {
		return "no symbols found", nil
	}
	return out, nil
}

func writeGoGenDeclSymbols(sb *strings.Builder, fset *token.FileSet, decl *ast.GenDecl) {
	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			writeGoSymbol(sb, fset, goTypeSymbolKind(s.Type), s.Name.Name, s.Pos())
		case *ast.ValueSpec:
			kind := "Variable"
			if decl.Tok == token.CONST {
				kind = "Constant"
			}
			for _, name := range s.Names {
				writeGoSymbol(sb, fset, kind, name.Name, name.Pos())
			}
		}
	}
}

func goTypeSymbolKind(expr ast.Expr) string {
	switch expr.(type) {
	case *ast.StructType:
		return "Struct"
	case *ast.InterfaceType:
		return "Interface"
	default:
		return "Type"
	}
}

func writeGoSymbol(sb *strings.Builder, fset *token.FileSet, kind, name string, pos token.Pos) {
	if name == "" || name == "_" {
		return
	}
	line := fset.Position(pos).Line
	fmt.Fprintf(sb, "%s %s (line %d)\n", kind, name, line)
}
