go-dep-loc
------

A tool to visualize dependencies of a Go project and size them by LOC count. Quick and dirty export of a project for [Handmande Network Visibility Jam](https://handmade.network/jam).


### Setup

Install [GraphViz](https://graphviz.org/download/) and [scc](https://github.com/boyter/scc). Optionally, install [Inter](https://rsms.me/inter/) for a nicer font, or pass one of the existing ones as an option.

Then, `go build .`


### Usage

Make sure the project you want to visualize is checked out someplace local and its build succeeds. Pass a path to the desired package to the tool - it should be a directory with .go files, with go.mod in it or in one of the parent dirs.

```
gopdepvis -dot /path/to/dot -scc /path/to/scc [-fontname "Comic Sans MS"] /path/to/local/project
```

Once it's finished, it will spit out a .png and an .svg into the current dir, as well as a couple of .dot files.
