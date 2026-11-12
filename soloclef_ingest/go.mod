// Document parsing for the Go indexing pipeline: bytes in, Markdown out.
//
// A module of its own for the reason soloclef_chunker is one: which Go tier
// ends up owning indexing is not settled, `internal` cannot cross a module,
// and a second copy of a parser client is a second set of behaviours.
module git.danvics.com/danvics/soloclef/soloclef_ingest

go 1.24.4

require (
	github.com/JohannesKaufmann/html-to-markdown/v2 v2.5.0
	golang.org/x/net v0.48.0
	golang.org/x/text v0.32.0
)

require (
	github.com/JohannesKaufmann/dom v0.2.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.2.0 // indirect
	github.com/hhrutter/lzw v1.0.0 // indirect
	github.com/hhrutter/pkcs7 v0.2.0 // indirect
	github.com/hhrutter/tiff v1.0.2 // indirect
	github.com/mattn/go-runewidth v0.0.19 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	golang.org/x/crypto v0.46.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
)

require (
	git.danvics.com/danvics/soloclef/soloclef_chunker v0.0.0
	github.com/pdfcpu/pdfcpu v0.11.1
	golang.org/x/image v0.34.0
)

// The chunker is the sibling module in this repository; chunkflow's test
// feeds parsed Markdown into it.
replace git.danvics.com/danvics/soloclef/soloclef_chunker => ../soloclef_chunker
