// Command chunkdump chunks documents read as newline-delimited JSON and
// writes their chunks back the same way, so the Python chunker and this one
// can be run over the same corpus and compared.
//
// Text crosses the boundary base64-encoded: the comparison is about bytes,
// and two JSON encoders agreeing on how to escape a character is not
// something it should have to assume.
//
// Two settings, both of which decide where a chunk ends:
//
//	SOLOCLEF_TOKENIZER   Required. Either the path to the tokenizer.json the
//	                     backend bakes (EMBEDDING_TOKENIZER names its
//	                     directory), or the literal "character" for chonkie's
//	                     character tokenizer, which is what the Python side
//	                     counts with when EMBEDDING_TOKENIZER is unset.
//	                     There is no default: 512 characters and 512 BGE-M3
//	                     tokens are different amounts of text, and guessing
//	                     which would make a passing comparison mean nothing.
//
//	CHUNK_SIZE           Optional, EMBEDDING_CHUNK_SIZE's counterpart. Unset
//	                     it is 512, which is what chonkie defaults to and
//	                     what the Python side defaults to.
package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"git.danvics.com/danvics/soloclef/soloclef_chunker/chonkie"
	"git.danvics.com/danvics/soloclef/soloclef_chunker/recursive"
	"git.danvics.com/danvics/soloclef/soloclef_chunker/tablesafe"
)

type document struct {
	ID      string `json:"id"`
	TextB64 string `json:"text_b64"`
}

type chunked struct {
	ID        string   `json:"id"`
	ChunksB64 []string `json:"chunks_b64"`
}

// characterTokenizer is chonkie's own name for its character counter, spelled
// the way app/config/chunk_sizing.py spells it.
const characterTokenizer = "character"

func main() {
	size := recursive.DefaultChunkSize
	if raw := os.Getenv("CHUNK_SIZE"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			fail(fmt.Errorf("CHUNK_SIZE=%q is not a whole number of at least 1", raw))
		}
		size = parsed
	}

	sizer, releaseSizer, err := openSizer(os.Getenv("SOLOCLEF_TOKENIZER"))
	if err != nil {
		fail(err)
	}
	defer releaseSizer()

	chunker := tablesafe.Chunker{Size: size, Sizer: sizer}

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 1<<20), 1<<28)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	encoder := json.NewEncoder(out)

	for in.Scan() {
		line := in.Bytes()
		if len(line) == 0 {
			continue
		}
		var doc document
		if err := json.Unmarshal(line, &doc); err != nil {
			fail(err)
		}
		text, err := base64.StdEncoding.DecodeString(doc.TextB64)
		if err != nil {
			fail(err)
		}
		chunks, err := chunker.Chunks(string(text))
		if err != nil {
			fail(fmt.Errorf("document %s: %w", doc.ID, err))
		}
		encoded := make([]string, len(chunks))
		for i, chunk := range chunks {
			encoded[i] = base64.StdEncoding.EncodeToString([]byte(chunk))
		}
		if err := encoder.Encode(chunked{ID: doc.ID, ChunksB64: encoded}); err != nil {
			fail(err)
		}
	}
	if err := in.Err(); err != nil {
		fail(err)
	}
}

// openSizer resolves SOLOCLEF_TOKENIZER to the thing that counts a chunk's
// size, and a function that releases it.
func openSizer(name string) (recursive.Sizer, func(), error) {
	switch name {
	case "":
		return nil, nil, fmt.Errorf(
			"SOLOCLEF_TOKENIZER is not set. Give it the path to the backend's "+
				"tokenizer.json, or the literal %q for chonkie's character tokenizer",
			characterTokenizer)
	case characterTokenizer:
		return recursive.Characters{}, func() {}, nil
	default:
		tokenizer, err := chonkie.Open(name)
		if err != nil {
			return nil, nil, fmt.Errorf("SOLOCLEF_TOKENIZER=%q: %w", name, err)
		}
		return tokenizer, func() { _ = tokenizer.Close() }, nil
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "chunkdump:", err)
	os.Exit(1)
}
