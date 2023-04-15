package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/goccy/go-graphviz"
	"github.com/goccy/go-json"
	"golang.org/x/tools/go/packages"
)

const dotPath = `C:\Program Files\Graphviz\bin\dot.exe`
const sccPath = `C:\Portable\scc.exe`

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
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedImports | packages.NeedModule,
	}
	pkgs, err := packages.Load(&cfg, rootPath)
	if err != nil {
		panic(err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		os.Exit(1)
	}
	if len(pkgs) != 1 {
		panic(errors.New("Expected exactly one package"))
	}

	moduleMap := make(map[string]map[string]bool)
	goFilesByModule := make(map[string]map[string]bool)
	packagesSeen := make(map[string]bool)
	var queue []*packages.Package
	queue = append(queue, pkgs[0])
	packagesSeen[pkgs[0].PkgPath] = true
	isRootPackage := true
	rootName := pkgs[0].Module.Path

	getModulePath := func(pkg *packages.Package, rootName string) string {
		firstTokenLen := strings.Index(pkg.PkgPath, "/")
		if firstTokenLen < 0 {
			firstTokenLen = len(pkg.PkgPath)
		}

		if !strings.HasPrefix(pkg.PkgPath, rootName) && !strings.Contains(pkg.PkgPath[:firstTokenLen], ".") {
			return "stdlib"
		} else if pkg.Module != nil {
			return pkg.Module.Path
		} else {
			return pkg.PkgPath + " (no module)"
		}
	}

	for len(queue) > 0 {
		pkg := queue[0]
		queue = queue[1:]

		var modulePath string
		if isRootPackage {
			modulePath = pkg.PkgPath
			isRootPackage = false
		} else {
			modulePath = getModulePath(pkg, rootName)
		}

		if _, ok := goFilesByModule[modulePath]; !ok {
			goFilesByModule[modulePath] = make(map[string]bool)
		}
		for _, goFile := range pkg.GoFiles {
			goFilesByModule[modulePath][goFile] = true
		}

		for _, importPkg := range pkg.Imports {
			importModulePath := getModulePath(importPkg, rootName)

			if _, ok := moduleMap[modulePath]; !ok {
				moduleMap[modulePath] = make(map[string]bool)
			}
			if modulePath != importModulePath {
				moduleMap[modulePath][importModulePath] = true
			}

			if !packagesSeen[importPkg.PkgPath] {
				queue = append(queue, importPkg)
				packagesSeen[importPkg.PkgPath] = true
			}
		}
	}

	//
	// Compute line counts
	//

	type SccOutLine struct {
		Name      string
		CodeLines int `json:"Code"`
	}

	lineCountByModule := make(map[string]int)
	minLineCount := math.MaxInt
	for module, goFiles := range goFilesByModule {
		goFilesList := make([]string, 0, len(goFiles))
		for goFile := range goFiles {
			goFilesList = append(goFilesList, goFile)
		}

		lineCount := 0

		batchSize := 300
		for batchStart := 0; batchStart < len(goFiles); batchStart += batchSize {
			batchEnd := batchStart + batchSize
			if batchEnd > len(goFiles) {
				batchEnd = len(goFiles)
			}
			batch := goFilesList[batchStart:batchEnd]

			sccArgs := make([]string, 0, 2+len(batch))
			sccArgs = append(sccArgs, "--format", "json")
			for _, goFile := range batch {
				sccArgs = append(sccArgs, goFile)
			}
			sccCmd := exec.Command(sccPath, sccArgs...)
			fmt.Println("scc", module)
			sccOutBytes, err := sccCmd.Output()
			if err != nil {
				panic(err)
			}

			var sccOutLines []SccOutLine
			err = json.Unmarshal(sccOutBytes, &sccOutLines)
			if err != nil {
				panic(err)
			}

			lineFound := false
			for _, sccOutLine := range sccOutLines {
				if sccOutLine.Name == "Go" {
					if sccOutLine.CodeLines == 0 {
						panic(errors.New("Go lines are empty"))
					}

					lineCount += sccOutLine.CodeLines
					lineFound = true
					break
				}
			}
			if !lineFound {
				panic(errors.New("Couldn't find Go line"))
			}
		}

		if lineCount < minLineCount {
			minLineCount = lineCount
		}

		lineCountByModule[module] = lineCount
	}

	//
	// Output .dot
	//

	baseSize := 0.5
	sizeByModule := make(map[string]float64)
	for module, lineCount := range lineCountByModule {
		moduleSize := baseSize * math.Sqrt(float64(lineCount)) / math.Sqrt(float64(minLineCount))
		sizeByModule[module] = moduleSize
	}

	initialGraphPath := outGraphPath[:len(outGraphPath)-4] + ".dot"
	initialGraphFile, err := os.Create(initialGraphPath)
	if err != nil {
		panic(err)
	}

	fmt.Fprintln(initialGraphFile, "digraph gopackages {")
	fmt.Fprintln(initialGraphFile, `    rankdir="LR"`)
	fmt.Fprintln(initialGraphFile, `    node [shape="box",style="rounded"]`)

	for module, dependencies := range moduleMap {
		moduleSize := sizeByModule[module]
		fontSize := moduleSize * 7

		labelWrapAt := 15
		var lb strings.Builder
		currentLength := 0
		for _, r := range module {
			lb.WriteRune(r)
			currentLength += 1
			if currentLength >= labelWrapAt && r == '/' {
				lb.WriteRune('\n')
				currentLength = 0
			}
		}
		label := lb.String()

		fmt.Fprintf(
			initialGraphFile, "    \"%s\" [width=%f,height=%f,fixedsize=true,fontsize=%f,label=\"%s\"];\n",
			module, moduleSize, moduleSize, fontSize, label,
		)
		for dependency := range dependencies {
			fmt.Fprintf(initialGraphFile, "    \"%s\" -> \"%s\";\n", module, dependency)
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

	graph, err := graphviz.ParseBytes(layoutedGraphBytes)
	if err != nil {
		panic(err)
	}
	for node := graph.FirstNode(); node != nil; node = graph.NextNode(node) {
		widthStr := node.Get("width")
		width, err := strconv.ParseFloat(widthStr, 64)
		if err != nil {
			panic(err)
		}

		heightStr := node.Get("height")
		height, err := strconv.ParseFloat(heightStr, 64)
		if err != nil {
			panic(err)
		}

		pos := node.Get("pos")
		posTokens := strings.Split(pos, ",")
		if len(posTokens) != 2 {
			panic(errors.New("Pos has more than 2 tokens"))
		}

		x, err := strconv.ParseFloat(posTokens[0], 64)
		if err != nil {
			panic(err)
		}

		y, err := strconv.ParseFloat(posTokens[1], 64)
		if err != nil {
			panic(err)
		}

		_, _, _, _ = x, y, width, height
		// fmt.Printf("%s: x=%f y=%f w=%f h=%f\n", node.Name(), x, y, width, height)
	}

	//
	// Render
	//

	renderCmd := exec.Command(dotPath, "-Tsvg", "-o"+outGraphPath, layoutedGraphPath)
	err = renderCmd.Run()
	if err != nil {
		panic(err)
	}

	outPngPath := outGraphPath[:len(outGraphPath)-4] + ".png"
	renderPngCmd := exec.Command(dotPath, "-Tpng", "-o"+outPngPath, layoutedGraphPath)
	err = renderPngCmd.Run()
	if err != nil {
		panic(err)
	}
}
