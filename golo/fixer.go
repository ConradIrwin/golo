package golo

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"os/exec"
	"strings"
	"sync"

	"golang.org/x/exp/slices"
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/packages"
)

// Fixer attempts to populate Fixed (a map from filename to updated content) by repeatingly
// loading the packages given and altering the sourcecode.
// Fixed can be passed to the go compiler using the `-overlay` flags.
type Fixer struct {
	mode    string
	verbose bool
	Fixed   map[string][]byte
}

func NewFixer(mode string, verbose bool, fixed map[string][]byte) *Fixer {
	f := &Fixer{
		mode:    mode,
		verbose: verbose,
		Fixed:   fixed,
	}
	if fixed == nil {
		f.Fixed = map[string][]byte{}
	}
}

// Fix attempts to fix the go packages given.
// It updates f.Fixed
func (f *Fixer) Fix(pkgNames ...string) error {
	for i := 0; i < 10; i++ {
		config := &packages.Config{
			Mode:      packages.NeedTypes | packages.NeedSyntax,
			ParseFile: f.parseFile,
			Overlay:   f.Fixed,
		}
		if f.mode == "test" {
			config.Tests = true
		}
		pkgs, err := packages.Load(config, pkgNames...)

		if err != nil {
			return fmt.Errorf("packages.Load failed: %w", err)
		}

		fixed := false

		for _, pkg := range pkgs {
			if f, err := f.fixPkg(pkg); err != nil {
				return err
			} else if f {
				fixed = true
			}
		}
		if !fixed {
			return nil
		}
	}
	return nil
}

func (f *Fixer) fixPkg(pkg *packages.Package) (bool, error) {
	if len(pkg.TypeErrors) == 0 {
		return false, nil
	}

	// TODO: handle more than one error per iteration (easy for separate files...)
	e := pkg.TypeErrors[0]
	fi := e.Fset.File(e.Pos)
	position := fi.PositionFor(e.Pos, false)

	offset := position.Offset
	var file *ast.File
	var content []byte
	var err error

	for _, ast := range pkg.Syntax {
		if ast.Pos() <= e.Pos && ast.End() >= e.Pos {
			file = ast
		}
	}

	// This happens for CGO builds
	if strings.HasPrefix(position.Filename, goCache()) {
		position = fi.PositionFor(e.Pos, true)
		content, err = f.readFile(position.Filename)
		if err != nil {
			return false, err
		}

		lno := 1
		cno := 0
		for i, b := range content {
			if b == '\n' {
				lno += 1
			} else if lno == position.Line {
				cno += 1
				if cno == position.Column {
					offset = i
					break
				}
			}
		}

		// the file in the syntax tree is the rewritten one, load the right one for fixing.
		file, err = parser.ParseFile(e.Fset, position.Filename, content, 0)
		if err != nil {
			return false, nil
		}
	} else {
		content, err = f.readFile(position.Filename)
		if err != nil {
			return false, err
		}
	}

	if f.fixError(file, position.Filename, content, offset, e.Msg) {
		fmt.Println("golo: " + strings.ReplaceAll(e.Error(), "\n", "\ngolo: "))
		return true, nil
	}

	return false, nil
}

func (f *Fixer) readFile(filename string) ([]byte, error) {
	if ret, ok := f.Fixed[filename]; ok {
		return ret, nil
	}
	return os.ReadFile(filename)
}

func (f *Fixer) parseFile(fset *token.FileSet, filename string, content []byte) (*ast.File, error) {
	// bail after 10 times around to avoid infinite looping if we're not helping
	i := 0
	for {
		i++
		file, err := parser.ParseFile(fset, filename, content, 0)
		if err == nil {
			return file, nil
		}

		errs, ok := err.(scanner.ErrorList)
		if i >= 10 || !ok || len(errs) == 0 {
			return file, err
		}

		e := errs[0]

		fixed := f.fixError(file, filename, content, e.Pos.Offset, e.Msg)
		if !fixed {
			return file, err
		}
		content = f.Fixed[filename]
		fmt.Println("golo: " + strings.ReplaceAll(e.Error(), "\n", "\ngolo: "))
	}
}

func newLinesInRange(s []byte) string {
	n := []byte{}
	for _, b := range s {
		if b == '\n' || b == '\r' {
			n = append(n, b)
		}
	}
	return string(n)
}

func (f *Fixer) fixError(file *ast.File, filename string, content []byte, offset int, msg string) bool {
	// We handle these cases specially because they can be caused by other changes that we made.
	// (also, yolo)
	if strings.Contains(msg, "imported and not used") {
		return f.fixUnusedImport(file, filename, content, offset)
	}
	if strings.Contains(msg, "declared and not used") {
		return f.fixUnusedVar(file, filename, content, offset)
	}
	if strings.Contains(msg, "no new variables on left side of :=") {
		return f.fixUselessAssignment(file, filename, content, offset)
	}

	// If we have something we can't fix, find the affected range and panic() when hit at runtime
	start, end, tail := f.findRangeToFix(file, content, offset)
	if start == end {
		if f.verbose {
			fmt.Println("golo:  error outside of function declaration: ", msg)
		}
		return false
	}

	if start > offset || end < offset {
		if f.verbose {
			fmt.Println("golo: range doesn't include error:", start, offset, end)
		}
		return false
	}

	newlinesBefore := newLinesInRange(content[start:offset])
	newlinesAfter := newLinesInRange(content[offset:end])
	newCode := newlinesBefore + "panic(" + fmt.Sprintf("%#v", msg) + ")" + newlinesAfter

	return f.update(filename, content[0:start], []byte(newCode), tail, content[end:])
}

func (f *Fixer) update(filename string, content ...[]byte) bool {
	f.Fixed[filename] = bytes.Join(content, nil)
	return true
}

func (f *Fixer) fixUnusedImport(file *ast.File, filename string, content []byte, offset int) bool {
	pos := file.FileStart + token.Pos(offset)

	declIdx := slices.IndexFunc(file.Decls, func(d ast.Decl) bool {
		return d.Pos() <= pos && d.End() >= pos
	})
	if declIdx == -1 {
		return false
	}
	decl, ok := file.Decls[declIdx].(*ast.GenDecl)
	if !ok {
		return false
	}
	if decl.Tok != token.IMPORT {
		return false
	}

	specIdx := slices.IndexFunc(decl.Specs, func(d ast.Spec) bool {
		return d.Pos() <= pos && d.End() >= pos
	})
	if specIdx == -1 {
		return false
	}
	spec, ok := decl.Specs[specIdx].(*ast.ImportSpec)
	if !ok {
		return false
	}

	insertPos := int(spec.Path.Pos() - file.FileStart)
	delLen := 0
	if spec.Name != nil {
		insertPos = int(spec.Name.Pos() - file.FileStart)
		delLen = int(spec.Name.End()-spec.Name.Pos()) + 1
	}

	return f.update(filename, content[:insertPos], []byte("_ "), content[insertPos+delLen:])
}

func (f *Fixer) fixUnusedVar(file *ast.File, filename string, content []byte, offset int) bool {
	pos := file.FileStart + token.Pos(offset)
	var ident *ast.Ident
	astutil.Apply(file, func(c *astutil.Cursor) bool {
		if c.Node() != nil && c.Node().Pos() <= pos && c.Node().End() >= pos {
			if n, ok := c.Node().(*ast.Ident); ok {
				ident = n
			}
		}
		return ident == nil
	}, nil)

	if ident == nil {
		return false
	}

	insertPos := int(ident.Pos() - file.FileStart)
	return f.update(filename, content[:insertPos], []byte("_"), content[insertPos+int(ident.End()-ident.Pos()):])
}

func (f *Fixer) fixUselessAssignment(file *ast.File, filename string, content []byte, offset int) bool {
	pos := file.FileStart + token.Pos(offset)
	var assign *ast.AssignStmt
	astutil.Apply(file, func(c *astutil.Cursor) bool {
		if c.Node() != nil && c.Node().Pos() <= pos && c.Node().End() >= pos {
			if n, ok := c.Node().(*ast.AssignStmt); ok {
				assign = n
			}
		}
		return assign == nil
	}, nil)

	if assign == nil || assign.Tok != token.DEFINE {
		return false
	}

	tokOff := int(assign.TokPos - file.FileStart)
	return f.update(filename, content[:tokOff], content[tokOff+1:])
}

func (f *Fixer) findRangeToFix(file *ast.File, content []byte, offset int) (int, int, []byte) {
	pos := file.FileStart + token.Pos(offset)
	statement, block, fnBody := f.findEnclosing(file, pos)

	offsetOf := func(t token.Pos) int {
		return int(t - file.FileStart)
	}
	// By default take from the start of the current statement to the end of the enclosing block
	// (it's ususally the case that any code after the broken statement is broken once the statement is removed)
	if block != nil && block.Rbrace.IsValid() {
		return offsetOf(statement.Pos()), offsetOf(block.End()) - 1, nil
	}

	// TODO: support syntax errors outside of function bodys
	if fnBody == nil || statement == nil {
		return 0, 0, nil
	}

	// TODO: push syntax errors down into nested blocks when the rest of the
	// declaration is well formed
	if block != fnBody {
		for _, stmt := range fnBody.List {
			if stmt.Pos() < pos {
				statement = stmt
			} else {
				break
			}
		}
	}

	// The likely function close brace: \n}
	// OR, if it's missing, the likely next TopLevelDecl (func, type, var, const) or comment:
	start := offsetOf(statement.Pos())
	tail := content[start:]
	wasNewline := false
	end := bytes.IndexFunc(tail, func(r rune) bool {
		if bytes.ContainsRune([]byte("}fvct/"), r) {
			return wasNewline
		}
		wasNewline = r == '\n' || r == '\r'
		return false
	})

	// found the likely function close brace: \n}
	if end > -1 && tail[end] == '}' {
		return start, start + end, nil
	}

	if end == -1 {
		end = len(tail) - 1
	}

	wasNewline = false
	closeBrace := bytes.LastIndexFunc(tail[:end], func(r rune) bool {
		if r == '}' {
			return wasNewline
		}
		wasNewline = r == '\n' || r == '\r'
		return false
	})
	// Found a close-brace at the end of the line
	if closeBrace > -1 {
		return start, start + closeBrace, nil
	}

	// Found next declaration (or EOF) with no brace.
	return start, start + end, []byte{'}'}
}

func (f *Fixer) findEnclosing(file *ast.File, pos token.Pos) (stmt ast.Stmt, block *ast.BlockStmt, fnBody *ast.BlockStmt) {
	astutil.Apply(file, func(c *astutil.Cursor) bool {
		if c.Node() == nil {
			return false
		}
		if c.Node().Pos() > pos {
			return false
		}
		switch n := c.Node().(type) {
		case *ast.BlockStmt:
			if n.End() >= pos || !n.Rbrace.IsValid() {
				block = n
			}
		case *ast.FuncDecl:
			if n.End() >= pos || !n.Body.Rbrace.IsValid() {
				fnBody = n.Body
			}
		case ast.Stmt:
			if block == c.Parent() {
				stmt = n
			}
		}

		return true
	}, func(c *astutil.Cursor) bool { return block == nil || block != c.Node() })

	return
}

var once sync.Once
var _goCache string

func goCache() string {
	once.Do(func() {
		out, err := exec.Command("go", "env", "GOCACHE").CombinedOutput()
		if err != nil {
			panic(err)
		}
		_goCache = strings.TrimSpace(string(out))
	})
	return _goCache
}
