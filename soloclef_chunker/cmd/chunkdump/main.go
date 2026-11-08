// Command chunkdump chunks documents read as newline-delimited JSON and
// writes their chunks back the same way, so the Python chunker and this one
// can be run over the same corpus and compared.
//
// Text crosses the boundary base64-encoded: the comparison is about bytes,
// and two JSON encoders agreeing on how to escape a character is not
// something it should have to assume.
package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strconv"

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

func main() {
	size := recursive.DefaultChunkSize
	if raw := os.Getenv("CHUNK_SIZE"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			fail(fmt.Errorf("CHUNK_SIZE=%q is not a whole number of at least 1", raw))
		}
		size = parsed
	}

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
		chunks := tablesafe.Chunks(string(text), size)
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

func fail(err error) {
	fmt.Fprintln(os.Stderr, "chunkdump:", err)
	os.Exit(1)
}
