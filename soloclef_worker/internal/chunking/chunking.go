// Package chunking is soloclef_chunker as the indexing job uses it: the
// table-safe chunker, sized by the embedding profile.
//
// It is the one package of the worker that links the chunker's cgo library,
// so the job itself (package indexing) and its tests stay plain Go.
package chunking

import (
	"errors"
	"fmt"
	"path/filepath"

	"git.danvics.com/danvics/soloclef/soloclef_chunker/chonkie"
	"git.danvics.com/danvics/soloclef/soloclef_chunker/recursive"
	"git.danvics.com/danvics/soloclef/soloclef_chunker/tablesafe"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/embedding"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/indexing"
)

// The profile check (chunk size times this factor must fit the model) and the
// chunker must mean the same number. If either changes alone, one of these
// two array lengths is negative and the package does not compile.
var (
	_ [tablesafe.OversizeFactor - embedding.OversizeFactor]struct{}
	_ [embedding.OversizeFactor - tablesafe.OversizeFactor]struct{}
)

// TokenizerFile is the file a profile's vocabulary is read from, inside the
// folder the profile names.
const TokenizerFile = "tokenizer.json"

// Chunker cuts Markdown the way one embedding profile needs it cut. It is
// safe for concurrent use: the vocabulary is immutable once loaded.
type Chunker struct {
	chunker   tablesafe.Chunker
	tokenizer *chonkie.Tokenizer
}

// Open builds the chunker for a profile.
//
// tokenizerDirectory is where the deployment keeps vocabularies, one folder a
// profile. It is required when the profile counts tokens, and a file that is
// missing or unreadable is an error: chunks are never sized in characters
// instead, because the unit decides every boundary and a corpus cut in the
// wrong unit does not match its own index.
func Open(profile embedding.Profile, tokenizerDirectory string) (*Chunker, error) {
	switch profile.Sizer {
	case embedding.CharacterSizer:
		return &Chunker{chunker: tablesafe.Chunker{Size: profile.ChunkSize, Sizer: recursive.Characters{}}}, nil
	case embedding.TokenizerSizer:
		if tokenizerDirectory == "" {
			return nil, fmt.Errorf("chunking: profile %s counts tokens, and no tokenizer directory is set", profile.Name)
		}
		path := filepath.Join(tokenizerDirectory, profile.Tokenizer, TokenizerFile)
		tokenizer, err := chonkie.Open(path)
		if err != nil {
			return nil, fmt.Errorf("chunking: profile %s: reading %s: %w", profile.Name, path, err)
		}
		return &Chunker{
			chunker:   tablesafe.Chunker{Size: profile.ChunkSize, Sizer: tokenizer},
			tokenizer: tokenizer,
		}, nil
	default:
		return nil, fmt.Errorf("chunking: profile %s: %q is not a sizer", profile.Name, profile.Sizer)
	}
}

// Pieces cuts the Markdown into chunks, each with the span it was cut from.
func (c *Chunker) Pieces(markdown string) ([]indexing.Piece, error) {
	if c == nil {
		return nil, errors.New("chunking: no chunker")
	}
	cut, err := c.chunker.Pieces(markdown)
	if err != nil {
		return nil, err
	}
	pieces := make([]indexing.Piece, len(cut))
	for index, piece := range cut {
		pieces[index] = indexing.Piece{Text: piece.Text, Start: piece.Start, End: piece.End}
	}
	return pieces, nil
}

// Close frees the vocabulary.
func (c *Chunker) Close() error {
	if c.tokenizer == nil {
		return nil
	}
	return c.tokenizer.Close()
}
