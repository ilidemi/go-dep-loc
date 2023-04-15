package main

import (
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/goccy/go-graphviz"
	"github.com/goccy/go-json"
	"golang.org/x/tools/go/packages"
)

const dotPath = `C:\Program Files\Graphviz\bin\dot.exe`
const sccPath = `C:\Portable\scc.exe`

var knownHosts = []string{"github.com"}

type NodeColor struct {
	FillColor   string
	BorderColor string
}

var knownColorsByAuthor = map[string]NodeColor{
	"aws":        {"#FFBE5E", "#FF9900"},
	"golang.org": {"#A2EAEF", "#6AD6E3"},
	"stdlib":     {"#CCCCCC", "#AAAAAA"},
}

func main() {
	if len(os.Args) != 2 {
		fmt.Println("usage: gopackages <package>")
		os.Exit(1)
	}
	rootPath := os.Args[1]
	rootName := filepath.Base(rootPath)

	//
	// Build dependency graph
	//

	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	err = os.Chdir(rootPath)
	if err != nil {
		panic(err)
	}

	cfg := packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedImports | packages.NeedModule,
	}
	pkgs, err := packages.Load(&cfg, ".")
	if err != nil {
		panic(err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		os.Exit(1)
	}
	if len(pkgs) != 1 {
		panic(errors.New("Expected exactly one package"))
	}

	err = os.Chdir(cwd)
	if err != nil {
		panic(err)
	}

	moduleMap := make(map[string]map[string]bool)
	goFilesByModule := make(map[string]map[string]bool)
	packagesSeen := make(map[string]bool)
	var queue []*packages.Package
	queue = append(queue, pkgs[0])
	packagesSeen[pkgs[0].PkgPath] = true
	isRootPackage := true
	rootModuleName := pkgs[0].Module.Path

	getModulePath := func(pkg *packages.Package, rootModuleName string) string {
		firstTokenLen := strings.Index(pkg.PkgPath, "/")
		if firstTokenLen < 0 {
			firstTokenLen = len(pkg.PkgPath)
		}

		if !strings.HasPrefix(pkg.PkgPath, rootModuleName) && !strings.Contains(pkg.PkgPath[:firstTokenLen], ".") {
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
			modulePath = getModulePath(pkg, rootModuleName)
		}

		if _, ok := goFilesByModule[modulePath]; !ok {
			goFilesByModule[modulePath] = make(map[string]bool)
		}
		for _, goFile := range pkg.GoFiles {
			goFilesByModule[modulePath][goFile] = true
		}

		for _, importPkg := range pkg.Imports {
			importModulePath := getModulePath(importPkg, rootModuleName)

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

	type SccCommand struct {
		Id      int
		Module  string
		Command *exec.Cmd
	}

	type SccOutLine struct {
		Name      string
		CodeLines int `json:"Code"`
	}

	type SccResult struct {
		CmdId     int
		Module    string
		LineCount int
	}

	var sccCmds []SccCommand
	for module, goFiles := range goFilesByModule {
		goFilesList := make([]string, 0, len(goFiles))
		for goFile := range goFiles {
			goFilesList = append(goFilesList, goFile)
		}

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
			sccCmds = append(sccCmds, SccCommand{
				Id:      len(sccCmds),
				Module:  module,
				Command: sccCmd,
			})
		}
	}

	sccCmdChan := make(chan SccCommand, len(sccCmds))
	sccResultChan := make(chan SccResult, len(sccCmds))

	for _, sccCmd := range sccCmds {
		sccCmdChan <- sccCmd
	}
	close(sccCmdChan)

	for i := 0; i < runtime.NumCPU(); i++ {
		go func() {
			for sccCmd := range sccCmdChan {
				sccOutBytes, err := sccCmd.Command.Output()
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

						sccResultChan <- SccResult{
							CmdId:     sccCmd.Id,
							Module:    sccCmd.Module,
							LineCount: sccOutLine.CodeLines,
						}
						lineFound = true
						break
					}
				}
				if !lineFound {
					panic(errors.New("Couldn't find Go line"))
				}
			}
		}()
	}

	lineCountByModule := make(map[string]int)
	totalLineCount := 0
	lineCountSansStdlib := 0
	for i := 0; i < len(sccCmds); i++ {
		sccResult := <-sccResultChan
		lineCountByModule[sccResult.Module] += sccResult.LineCount
		totalLineCount += sccResult.LineCount
		if sccResult.Module != "stdlib" {
			lineCountSansStdlib += sccResult.LineCount
		}
	}

	minLineCount := math.MaxInt
	for _, lineCount := range lineCountByModule {
		if lineCount < minLineCount {
			minLineCount = lineCount
		}
	}

	//
	// Output .dot
	//

	initialGraphPath := rootName + ".dot"
	initialGraphFile, err := os.Create(initialGraphPath)
	if err != nil {
		panic(err)
	}

	fmt.Fprintln(initialGraphFile, "digraph gopackages {")
	fmt.Fprintln(initialGraphFile, `    rankdir="LR"`)
	fmt.Fprintln(initialGraphFile, `    node [shape="box",style="rounded,filled"]`)

	baseSize := 0.33
	for module, dependencies := range moduleMap {
		lineCount := lineCountByModule[module]
		moduleSize := baseSize * math.Sqrt(float64(lineCount)) / math.Sqrt(float64(minLineCount))
		fontSize := moduleSize * 6

		labelWrapAt := 15
		var labelBuilder strings.Builder
		currentLength := 0
		for _, r := range module {
			labelBuilder.WriteRune(r)
			currentLength += 1
			if currentLength >= labelWrapAt && r == '/' {
				labelBuilder.WriteRune('\n')
				currentLength = 0
			}
		}
		labelBuilder.WriteRune('\n')
		kloc := float64(lineCount) / 1000
		var klocStr string
		if kloc >= 10 {
			klocStr = fmt.Sprintf("%d", int(math.Round(kloc)))
		} else if kloc >= 0.1 {
			klocStr = fmt.Sprintf("%.1f", kloc)
		} else {
			klocStr = fmt.Sprintf("%.3f", kloc)
		}
		labelBuilder.WriteString(klocStr)
		labelBuilder.WriteString("K LOC")
		label := labelBuilder.String()

		authorStart := 0
		for _, knownHost := range knownHosts {
			if strings.HasPrefix(module, knownHost) {
				authorStart = len(knownHost) + 1
			}
		}
		authorEnd := authorStart + strings.Index(module[authorStart:], "/")
		if authorEnd == -1 {
			authorEnd = len(module)
		}
		author := module[authorStart:authorEnd]
		nodeColor, ok := knownColorsByAuthor[author]
		if !ok {
			hash := fnv.New32a()
			hash.Write([]byte{1}) // seed
			hash.Write([]byte(author))
			hashInt := hash.Sum32()

			r := hashInt & 0xFF
			g := (hashInt >> 8) & 0xFF
			b := (hashInt >> 16) & 0xFF

			const colorSqueeze = 0.33
			fillBase := (1 - colorSqueeze) * 255
			fillR := uint8(fillBase + colorSqueeze*float64(r))
			fillG := uint8(fillBase + colorSqueeze*float64(g))
			fillB := uint8(fillBase + colorSqueeze*float64(b))
			fillColor := fmt.Sprintf("#%02x%02x%02x", fillR, fillG, fillB)

			const borderColorOffset = -84
			borderR := uint8(int(fillR) + borderColorOffset)
			borderG := uint8(int(fillG) + borderColorOffset)
			borderB := uint8(int(fillB) + borderColorOffset)
			borderColor := fmt.Sprintf("#%02x%02x%02x", borderR, borderG, borderB)

			nodeColor = NodeColor{FillColor: fillColor, BorderColor: borderColor}
		}

		fmt.Fprintf(
			initialGraphFile,
			"    \"%s\" [width=%f,height=%f,fixedsize=true,fontsize=%f,label=\"%s\",fillcolor=\"%s\",color=\"%s\",fontname=\"Inter\"];\n",
			module, moduleSize, moduleSize, fontSize, label, nodeColor.FillColor, nodeColor.BorderColor,
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

	layoutedGraphPath := rootName + "_layouted.dot"
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

	render := func(layoutedGraphPath string, rootName string, format string) {
		outPath := rootName + "." + format
		cmd := exec.Command(dotPath, "-T"+format, "-o"+outPath, layoutedGraphPath)
		stdoutStderr, err := cmd.CombinedOutput()
		if err != nil {
			if stdoutStderr != nil {
				err = errors.Join(err, errors.New(string(stdoutStderr)))
			}
			panic(err)
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		render(layoutedGraphPath, rootName, "svg")
		wg.Done()
	}()

	go func() {
		render(layoutedGraphPath, rootName, "png")
		wg.Done()
	}()

	wg.Wait()

	fmt.Println("LOC total:", totalLineCount)
	fmt.Println("LOC not counting stdlib:", lineCountSansStdlib)
}
