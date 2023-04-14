package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/tools/go/packages"
)

const dotPath = `C:\Program Files\Graphviz\bin\dot.exe`

func main() {
	if len(os.Args) != 3 {
		fmt.Println("usage: gopackages <package> <graph.svg>")
		os.Exit(1)
	}
	rootPath := os.Args[1]
	outGraphPath := os.Args[2]

	if !strings.HasSuffix(outGraphPath, ".svg") {
		fmt.Println("graph path is supposed to end with .svg")
	}

	//
	// Build dependency graph
	//

	cfg := packages.Config{
		Mode: packages.NeedName | packages.NeedImports,
	}
	pkgs, err := packages.Load(&cfg, rootPath)
	if err != nil {
		panic(err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		os.Exit(1)
	}

	packageMap := make(map[string][]string)
	seen := make(map[string]bool)
	var queue []*packages.Package
	for _, pkg := range pkgs {
		queue = append(queue, pkg)
		seen[pkg.PkgPath] = true
	}

	for len(queue) > 0 {
		pkg := queue[0]
		queue = queue[1:]

		seenStdlib := false

		for _, importPkg := range pkg.Imports {
			canonPath := importPkg.PkgPath
			if !strings.Contains(canonPath, ".") {
				if seenStdlib {
					continue
				}
				seenStdlib = true
				canonPath = "stdlib"
			}

			packageMap[pkg.PkgPath] = append(packageMap[pkg.PkgPath], canonPath)

			if canonPath != "stdlib" && !seen[importPkg.PkgPath] {
				queue = append(queue, importPkg)
				seen[importPkg.PkgPath] = true
			}
		}

		if seenStdlib {
			seen["stdlib"] = true
		}
	}

	//
	// Output .dot
	//

	initialGraphPath := outGraphPath[:len(outGraphPath)-4] + ".dot"
	initialGraphFile, err := os.Create(initialGraphPath)
	if err != nil {
		panic(err)
	}

	fmt.Fprintln(initialGraphFile, "digraph gopackages {")
	fmt.Fprintln(initialGraphFile, `    rankdir="LR"`)
	fmt.Fprintln(initialGraphFile, `    node [shape="box",style="rounded"]`)

	for pkgName, dependencies := range packageMap {
		for _, depName := range dependencies {
			fmt.Fprintf(initialGraphFile, "    \"%s\" -> \"%s\";\n", pkgName, depName)
		}
	}

	fmt.Fprintln(initialGraphFile, "}")

	err = initialGraphFile.Close()
	if err != nil {
		panic(err)
	}

	//
	// Layout
	//

	layoutCmd := exec.Command(dotPath, initialGraphPath)
	layoutedGraphBytes, err := layoutCmd.Output()
	if err != nil {
		panic(err)
	}

	layoutedGraphPath := outGraphPath[:len(outGraphPath)-4] + "_layouted.dot"
	layoutedGraphFile, err := os.Create(layoutedGraphPath)
	if err != nil {
		panic(err)
	}

	layoutedGraphFile.Write(layoutedGraphBytes)

	layoutedGraphFile.Close()
}
