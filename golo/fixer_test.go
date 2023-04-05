package golo

import (
	"bytes"
	"fmt"
	"go/parser"
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFixer_FindRangeToFix(t *testing.T) {
	examples := map[string]string{
		"invalid_hex.go": `package main

import "fmt"

func main() {
	#fmt.Println(0xg)
#}`,
		"in_an_if.go": `package main

import "fmt"

func main() {
	if true {
		#fmt.Println(0xg)
	#}
}`,
		"unclosed_string.go": `package main

import "fmt"

func main() {
	#fmt.Println("oops
#}`,
		"singleline.go": `package main

import "fmt"
func main() { #fmt.Println("oops) #}
`,

		"doublesingle.go": `package main
func main() { #fmt.Println("oops) #}
type T struct {}
`,
		"noclosebrace.go": `package main
func main() { #fmt.Println("oops
#}#`,
		"nonsense.go": `package main

func main() {
	#i dont know why I bother...
#}
`,
		// to improve...
		"syntax_in_if.go": `package main

func main() {
	#if true {
		oh very no
	}
#}
`,
		// not yet supported cases...
		"tld.go": `##package main
hah what're you going to do?
func main() { }`,
		"arg_err.go": `##package main
func main(t r y) { }`,
		"name_err.go": `##package main
func () { }`,
	}

	for name, eg := range examples {
		content := []byte(eg + "\n")
		var tail []byte
		startIndex := bytes.IndexByte(content, '#')
		content = append(content[0:startIndex], content[startIndex+1:]...)
		endIndex := bytes.IndexByte(content, '#')
		if endIndex > -1 {

			tailEnd := bytes.IndexByte(content[endIndex+1:], '#')
			if tailEnd > -1 {
				tail = content[endIndex+1 : endIndex+1+tailEnd]
				content = append(content[0:endIndex], content[endIndex+1+tailEnd+1:]...)
			} else {
				content = append(content[0:endIndex], content[endIndex+1:]...)
			}
		}

		fset := &token.FileSet{}
		file, err := parser.ParseFile(fset, name, content, 0)

		e := err.(scanner.ErrorList)[0]

		foundStart, foundEnd, foundTail := (&Fixer{}).findRangeToFix(file, content, e.Pos.Offset)

		if foundStart != startIndex || foundEnd != endIndex || !bytes.Equal(tail, foundTail) {
			fmt.Println(name, ": Expected: ", startIndex, " -> ", endIndex)
			fmt.Println(eg)
			fmt.Println(name, ": Actual: ", foundStart, " -> ", foundEnd)
			os.Stdout.Write(content[:foundStart])
			os.Stdout.Write([]byte{'#'})
			os.Stdout.Write(content[foundStart:foundEnd])
			os.Stdout.Write([]byte{'#'})
			if len(foundTail) > 0 {
				os.Stdout.Write(foundTail)
				os.Stdout.Write([]byte{'#'})
			}
			os.Stdout.Write(content[foundEnd:])
			fmt.Println()
			t.Error()
		}
	}
}

func TestFixer_FixError(t *testing.T) {
	examples, err := os.ReadDir("../examples")
	if err != nil {
		t.Fatal(err)
	}

	for _, example := range examples {
		t.Run(example.Name(), func(t *testing.T) {
			testExample(t, example.Name())
		})
	}
}

func testExample(t *testing.T, example string) {
	expected := map[string][]byte{}
	filepath.WalkDir("../examples/"+example, func(path string, d fs.DirEntry, err error) error {
		fmt.Println(path)
		if strings.HasSuffix(path, ".golo") {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			abs, err := filepath.Abs(strings.TrimSuffix(path, ".golo"))
			if err != nil {
				return err
			}
			expected[abs] = content
			fmt.Println("expected[" + abs + "] = !")
		}

		return nil
	})

	f := &Fixer{mode: "run", verbose: false, Fixed: map[string][]byte{}}
	if err := f.Fix("../examples/" + example); err != nil {
		t.Fatal(err)
	}

	for k := range expected {
		if _, ok := f.Fixed[k]; !ok {
			t.Error("expected to have fixed: " + k + " but didn't")
		}
	}

	for k, content := range f.Fixed {
		exp, ok := expected[k]
		if !ok {
			fmt.Println("expected not to have fixed: " + k + " but did")
		} else if !bytes.Equal(content, exp) {
			fmt.Println("got wrong fix for " + k)
			fmt.Println("## expected ##")
			os.Stdout.Write(exp)
			fmt.Println("## actual ##")
			os.Stdout.Write(content)
		} else {
			continue
		}

		if os.Getenv("GOLO_FIX_TESTS") != "" {
			if err := os.WriteFile(k+".golo", content, 0o666); err != nil {
				t.Fatal(err)
			}
		} else {
			t.Error()
		}
	}

}
