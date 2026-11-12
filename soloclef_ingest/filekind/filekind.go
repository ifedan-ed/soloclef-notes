// Package filekind sorts a file by its extension into the formats that are
// read in Go and the ones that need a document parser.
//
// The extension is the only signal, deliberately: it is what the upload
// dialog can check before a byte is sent, so the answer a person sees when
// they pick a file and the answer the worker reaches later are the same one.
package filekind

import (
	"path"
	"strings"
)

// Kind is how a file is read.
type Kind int

const (
	// Document needs a document parser: the one its source chose.
	Document Kind = iota
	// Text is already text: plain text, Markdown and source files.
	Text
	// CommaSeparated and TabSeparated become a Markdown table.
	CommaSeparated
	TabSeparated
	// HTML becomes Markdown.
	HTML
)

// Extension is name's extension in lower case with its dot, or "".
func Extension(name string) string {
	return strings.ToLower(path.Ext(name))
}

// Of is the Kind of a file with this extension (as Extension returns it).
// An extension nobody has heard of is a Document: whether anything reads it
// is the chosen parser's answer to give, with its own list of what it does.
func Of(extension string) Kind {
	if kind, known := builtin[extension]; known {
		return kind
	}
	return Document
}

// ReadInGo lists every extension parsed without a document parser, sorted.
// The upload dialog shows them as accepted whatever parser is chosen.
func ReadInGo() []string {
	return append([]string(nil), readInGo...)
}

var builtin, readInGo = func() (map[string]Kind, []string) {
	kinds := map[string]Kind{
		".csv": CommaSeparated, ".tsv": TabSeparated,
		".html": HTML, ".htm": HTML, ".xhtml": HTML,
	}
	for _, extension := range textExtensions {
		kinds[extension] = Text
	}
	return kinds, sortedKeys(kinds)
}()

// textExtensions are files that are text already. The list is the one the
// Python tier accepted, so nothing a person could upload before is refused.
var textExtensions = []string{
	".md", ".markdown", ".txt", ".text", ".json", ".jsonl", ".yaml", ".yml",
	".toml", ".ini", ".cfg", ".conf", ".xml", ".css", ".scss", ".less", ".sass",
	".py", ".pyw", ".pyi", ".pyx", ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs",
	".java", ".kt", ".kts", ".scala", ".groovy", ".c", ".h", ".cpp", ".cxx",
	".cc", ".hpp", ".hxx", ".cs", ".fs", ".fsx", ".go", ".rs", ".rb", ".php",
	".pl", ".pm", ".lua", ".swift", ".m", ".mm", ".r", ".jl", ".sh", ".bash",
	".zsh", ".fish", ".bat", ".cmd", ".ps1", ".sql", ".graphql", ".gql", ".env",
	".gitignore", ".dockerignore", ".editorconfig", ".makefile", ".cmake",
	".log", ".rst", ".tex", ".bib", ".org", ".adoc", ".asciidoc", ".vue",
	".svelte", ".astro", ".tf", ".hcl", ".proto",
}
