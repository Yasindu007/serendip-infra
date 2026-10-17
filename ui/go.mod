// This file only fences ui/ off from the root Go module, so `go build ./...`
// and `go vet ./...` never walk ui/node_modules (any .go file an npm package
// ships would otherwise be compiled as part of serendip). No Go code lives
// here; the built console is embedded by cmd/serendipd/console.
module serendip.invalid/ui-fence

go 1.24
