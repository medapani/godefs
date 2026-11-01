package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
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

					printNode(w, fset, &copyDecl)

				case *ast.GenDecl:
					printDoc(w, d.Doc, true)

					switch d.Tok {
					case token.CONST, token.TYPE, token.VAR:
						copyDecl := *d
						copyDecl.Doc = nil

						if d.Tok == token.VAR {
							copyDecl.Specs = make([]ast.Spec, len(d.Specs))

							for i, spec := range d.Specs {
								vs, ok := spec.(*ast.ValueSpec)
								if !ok {
									copyDecl.Specs[i] = spec
									continue
								}

								copySpec := *vs
								copySpec.Values = make([]ast.Expr, len(vs.Values))

								for j, value := range vs.Values {
									copySpec.Values[j] = stripFuncBody(value)
								}

								copyDecl.Specs[i] = &copySpec
							}
						}

						printNode(w, fset, &copyDecl)
					}
				}
			}
		}
	}

	return nil
}

func stripFuncBody(expr ast.Expr) ast.Expr {
	switch e := expr.(type) {
	case *ast.FuncLit:
		return e.Type

	case *ast.CallExpr:
		copyExpr := *e
		copyExpr.Fun = stripFuncBody(e.Fun)
		copyExpr.Args = make([]ast.Expr, len(e.Args))

		for i, arg := range e.Args {
			copyExpr.Args[i] = stripFuncBody(arg)
		}
		return &copyExpr

	case *ast.ParenExpr:
		copyExpr := *e
		copyExpr.X = stripFuncBody(e.X)
		return &copyExpr

	case *ast.CompositeLit:
		copyExpr := *e
		copyExpr.Elts = make([]ast.Expr, len(e.Elts))

		for i, elt := range e.Elts {
			copyExpr.Elts[i] = stripFuncBody(elt)
		}
		return &copyExpr

	case *ast.KeyValueExpr:
		copyExpr := *e
		copyExpr.Value = stripFuncBody(e.Value)
		return &copyExpr
	}

	return expr
}

func printNode(w io.Writer, fset *token.FileSet, node ast.Node) {
	var buf bytes.Buffer

	if err := format.Node(&buf, fset, node); err != nil {
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

	for _, line := range strings.Split(strings.TrimSpace(doc.Text()), "\n") {
		fmt.Fprintf(w, "%s%s// %s%s\n", indentSpace, colorDim, line, colorReset)
	}
}
