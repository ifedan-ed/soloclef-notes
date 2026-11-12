// Package chunkflow has no code: it holds the test that shows parsed
// Markdown going into soloclef_chunker, the next step of indexing.
//
// It is a package of its own so that the chunker, which is built with cgo
// against chonkie's Rust core, is linked only into this test. The parsers
// themselves stay pure Go. Building this package's test needs the static
// library first (soloclef_chunker/AGENTS.md):
//
//	cargo build --release --manifest-path ../soloclef_chunker/chonkie/rust/Cargo.toml
package chunkflow
