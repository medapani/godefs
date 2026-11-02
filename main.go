package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/printer"
	"go/scanner"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// アプリケーションのバージョン（build時に埋め込まれる）
var appVersion = "dev"

const (
	colorDim   = "\033[2m"
	colorReset = "\033[0m"
)

func main() {
	version := flag.Bool("v", false, "バージョンを表示")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			"Usage: godefs [options] [directory]\n\n")
		fmt.Fprintln(flag.CommandLine.Output(), "Options:")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *version {
		fmt.Println(appVersion)
		return
	}

	if flag.NArg() > 1 {
		flag.Usage()
		os.Exit(2)
	}

	root := "."
	if flag.NArg() == 1 {
		root = flag.Arg(0)
	}

	if err := run(os.Stdout, root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(w io.Writer, root string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}

	// #nosec G703 -- ユーザー指定のディレクトリを再帰走査するCLIの仕様
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", root)
	}

	// #nosec G703 -- ユーザー指定のディレクトリを再帰走査するCLIの仕様
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if !d.IsDir() {
			return nil
		}

		if path != root {
			switch d.Name() {
			case ".git", "vendor", "node_modules":
				return filepath.SkipDir
			}
		}

		return inspectDir(w, root, path)
	})
}

func inspectDir(w io.Writer, root, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	fset := token.NewFileSet()
	files := make(map[string][]*ast.File)

	for _, entry := range entries {
		name := entry.Name()

		if entry.IsDir() ||
			!strings.HasSuffix(name, ".go") ||
			strings.HasSuffix(name, "_test.go") {
			continue
		}

		path := filepath.Join(dir, name)

		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}

		pkg := file.Name.Name
		files[pkg] = append(files[pkg], file)
	}

	if len(files) == 0 {
		return nil
	}

	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return err
	}

	packages := make([]string, 0, len(files))
	for pkg := range files {
		packages = append(packages, pkg)
	}
	sort.Strings(packages)

	for _, pkg := range packages {
		// パッケージコメントを表示
		for _, file := range files[pkg] {
			if file.Doc != nil {
				printDoc(w, file.Doc, false)
			}
		}

		fmt.Fprintf(w, "package %s (%s)\n", pkg, rel)

		for _, file := range files[pkg] {
			filename := filepath.Base(fset.Position(file.Pos()).Filename)
			fmt.Fprintf(w, "  %s\n", filename)

			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					printDoc(w, d.Doc, true)

					// 関数本体を表示しない
					copyDecl := *d
					copyDecl.Body = nil
					// 二重表示を防ぐ
					copyDecl.Doc = nil

					printNode(w, fset, &copyDecl, declarationComments(file.Comments, d, []*ast.BlockStmt{d.Body}))

				case *ast.GenDecl:
					printDoc(w, d.Doc, true)

					switch d.Tok {
					case token.CONST, token.TYPE, token.VAR:
						copyDecl := *d
						copyDecl.Doc = nil

						var omitted []*ast.BlockStmt
						if d.Tok == token.VAR {
							copyDecl.Specs = stripVarBodies(d.Specs, &omitted)
						}

						printNode(w, fset, &copyDecl, declarationComments(file.Comments, d, omitted))
					}
				}
			}
		}
	}

	return nil
}

// stripVarBodies は元のASTを変更せず、変数の無名関数本体を省く。
func stripVarBodies(specs []ast.Spec, omitted *[]*ast.BlockStmt) []ast.Spec {
	result := make([]ast.Spec, len(specs))

	for i, spec := range specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			result[i] = spec
			continue
		}

		copySpec := *vs
		// 初期値がない宣言はnilのままにし、余分な「=」を出さない。
		if vs.Values != nil {
			copySpec.Values = make([]ast.Expr, len(vs.Values))
			for j, value := range vs.Values {
				copySpec.Values[j] = stripFuncBody(value, omitted)
			}
		}

		result[i] = &copySpec
	}

	return result
}

func stripFuncBody(expr ast.Expr, omitted *[]*ast.BlockStmt) ast.Expr {
	switch e := expr.(type) {
	case *ast.FuncLit:
		*omitted = append(*omitted, e.Body)
		return e.Type

	case *ast.CallExpr:
		copyExpr := *e
		copyExpr.Fun = stripFuncBody(e.Fun, omitted)
		copyExpr.Args = make([]ast.Expr, len(e.Args))

		for i, arg := range e.Args {
			copyExpr.Args[i] = stripFuncBody(arg, omitted)
		}
		return &copyExpr

	case *ast.ParenExpr:
		copyExpr := *e
		copyExpr.X = stripFuncBody(e.X, omitted)
		return &copyExpr

	case *ast.CompositeLit:
		copyExpr := *e
		copyExpr.Elts = make([]ast.Expr, len(e.Elts))

		for i, elt := range e.Elts {
			copyExpr.Elts[i] = stripFuncBody(elt, omitted)
		}
		return &copyExpr

	case *ast.KeyValueExpr:
		copyExpr := *e
		copyExpr.Value = stripFuncBody(e.Value, omitted)
		return &copyExpr
	}

	return expr
}

// declarationComments は宣言内のコメントを残し、省略した本体内のものだけ除く。
func declarationComments(groups []*ast.CommentGroup, decl ast.Node, omitted []*ast.BlockStmt) []*ast.CommentGroup {
	var result []*ast.CommentGroup

	// ValueSpecなどに付いた行末コメントは宣言のEndより後にも存在する。
	trailing := make(map[*ast.Comment]bool)
	ast.Inspect(decl, func(node ast.Node) bool {
		group, ok := node.(*ast.CommentGroup)
		if ok && group.Pos() >= decl.End() {
			for _, comment := range group.List {
				trailing[comment] = true
			}
		}
		return true
	})

	for _, group := range groups {
		var comments []*ast.Comment
		for _, comment := range group.List {
			if comment.Pos() < decl.Pos() || (comment.End() > decl.End() && !trailing[comment]) {
				continue
			}

			hidden := false
			for _, body := range omitted {
				if body != nil && comment.Pos() >= body.Pos() && comment.End() <= body.End() {
					hidden = true
					break
				}
			}

			if !hidden {
				comments = append(comments, comment)
			}
		}

		if len(comments) > 0 {
			result = append(result, &ast.CommentGroup{List: comments})
		}
	}

	return result
}

func printNode(w io.Writer, fset *token.FileSet, node ast.Node, comments []*ast.CommentGroup) {
	var buf bytes.Buffer

	if err := format.Node(&buf, fset, &printer.CommentedNode{Node: node, Comments: comments}); err != nil {
		fmt.Fprintf(w, "    <format error: %v>\n", err)
		return
	}

	src := colorizeFieldComments(buf.String())

	for _, line := range strings.Split(strings.TrimSpace(src), "\n") {
		fmt.Fprintf(w, "    %s\n", line)
	}
	fmt.Fprintln(w)
}

func colorizeFieldComments(src string) string {
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))

	var s scanner.Scanner
	s.Init(file, []byte(src), nil, scanner.ScanComments)

	var buf strings.Builder
	last := 0

	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}

		if tok != token.COMMENT {
			continue
		}

		start := file.Offset(pos)
		end := start + len(lit)

		buf.WriteString(src[last:start])
		buf.WriteString(colorDim)
		buf.WriteString(src[start:end])
		buf.WriteString(colorReset)

		last = end
	}

	buf.WriteString(src[last:])
	return buf.String()
}

func printDoc(w io.Writer, doc *ast.CommentGroup, indent bool) {
	if doc == nil {
		return
	}

	indentSpace := ""
	if indent {
		indentSpace = "    "
	}

	// Text()はgo:embedなどのディレクティブを除去するため、元のコメントを使う。
	for _, comment := range doc.List {
		for _, line := range strings.Split(comment.Text, "\n") {
			fmt.Fprintf(w, "%s%s%s%s\n", indentSpace, colorDim, line, colorReset)
		}
	}
}
