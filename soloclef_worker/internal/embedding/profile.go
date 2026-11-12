// Package embedding turns chunk texts into vectors through the model proxy,
// and holds the one table that says which model that is.
//
// # One setting
//
// SOLOCLEF_EMBEDDING_PROFILE names a Profile. A profile carries everything
// that has to change together when the embedding model changes: the model's
// name at the proxy, the length of its vectors, the vocabulary a chunk's size
// is counted in, the chunk size, and how requests are batched. The Python
// tier spread these over five settings that had to agree and worked because
// someone was careful; here they cannot disagree, because they are one row.
//
// There is no default profile. Unset, or a name that is not in the table,
// stops the process and lists the names that exist.
//
// # Only through the proxy
//
// Every request goes to the LiteLLM proxy's OpenAI-shaped /embeddings route.
// Nothing here knows a provider's address or holds a provider's key, and
// nothing embeds with a second model when the first fails.
package embedding

import (
	"fmt"
	"slices"
	"strings"
)

// SizerKind says what a chunk's size is counted in.
type SizerKind string

const (
	// TokenizerSizer counts the tokens of the profile's own vocabulary, read
	// from a tokenizer.json. It is the right unit: the model's window is in
	// these tokens.
	TokenizerSizer SizerKind = "tokenizer"
	// CharacterSizer counts Unicode code points. It is for a model whose
	// vocabulary is not published, and it is stated, never fallen back to: a
	// tokenizer file that is missing stops the process.
	CharacterSizer SizerKind = "characters"
)

// Profile is one embedding choice, whole.
type Profile struct {
	// Name is what the setting says.
	Name string
	// Model is the name the proxy serves the model under.
	Model string
	// Dimension is the length of the vectors the model returns, and so the
	// size the Qdrant collection was built with.
	Dimension int
	// Sizer is the unit ChunkSize and MaxInput are in.
	Sizer SizerKind
	// Tokenizer is the folder, under the deployment's tokenizer directory,
	// that holds this profile's tokenizer.json. Empty for CharacterSizer.
	Tokenizer string
	// ChunkSize is the most a chunk may hold, in the sizer's units.
	ChunkSize int
	// MaxInput is the most the model accepts in one text, in the same units.
	MaxInput int
	// BatchSize is how many texts go in one request.
	BatchSize int
	// Concurrency is how many requests are in flight at once.
	Concurrency int
}

// profiles is the table. Adding an embedding choice is adding a row here and
// a route at the proxy; nothing else in the Go tier changes.
var profiles = []Profile{
	{
		// BAAI/bge-m3 as the proxy serves it. 512 tokens a chunk is what the
		// corpus has been cut to since 2026-11-09. 32 texts a request and four
		// requests at once are the numbers measured against the hosted model
		// (0.9 to 2.0 s a request); its host refuses larger batches under
		// load, which is what Client's halving is for.
		Name:        "bge-m3",
		Model:       "cloudflare-bge-m3",
		Dimension:   1024,
		Sizer:       TokenizerSizer,
		Tokenizer:   "bge-m3",
		ChunkSize:   512,
		MaxInput:    8192,
		BatchSize:   32,
		Concurrency: 4,
	},
}

// OversizeFactor is how far over ChunkSize the table chunker lets one chunk
// run before it cuts a row (tablesafe.OversizeFactor). It is repeated here
// as a number so that this package does not link the chunker's cgo library
// only to read a constant; package chunking refuses to compile if the two
// ever differ.
const OversizeFactor = 4

// ProfileNamed is the profile the setting names, or an error that lists the
// ones that exist.
func ProfileNamed(name string) (Profile, error) {
	name = strings.TrimSpace(name)
	for _, profile := range profiles {
		if profile.Name == name {
			if err := profile.Validate(); err != nil {
				return Profile{}, err
			}
			return profile, nil
		}
	}
	known := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		known = append(known, profile.Name)
	}
	slices.Sort(known)
	if name == "" {
		return Profile{}, fmt.Errorf("embedding: no profile is named; the profiles are: %s", strings.Join(known, ", "))
	}
	return Profile{}, fmt.Errorf("embedding: %q is not an embedding profile; the profiles are: %s", name, strings.Join(known, ", "))
}

// Validate refuses a profile that could not work, so a bad row in the table
// is a refused start and not a document cut off by the host without anyone
// being told.
func (p Profile) Validate() error {
	switch {
	case p.Name == "" || p.Model == "":
		return fmt.Errorf("embedding: a profile needs a name and a model, got %q and %q", p.Name, p.Model)
	case p.Dimension < 1:
		return fmt.Errorf("embedding: profile %s: the dimension must be positive, got %d", p.Name, p.Dimension)
	case p.Sizer != TokenizerSizer && p.Sizer != CharacterSizer:
		return fmt.Errorf("embedding: profile %s: %q is not a sizer", p.Name, p.Sizer)
	case p.Sizer == TokenizerSizer && p.Tokenizer == "":
		return fmt.Errorf("embedding: profile %s counts tokens and names no tokenizer", p.Name)
	case p.ChunkSize < 1 || p.BatchSize < 1 || p.Concurrency < 1:
		return fmt.Errorf("embedding: profile %s: chunk size, batch size and concurrency must all be positive", p.Name)
	case p.ChunkSize*OversizeFactor > p.MaxInput:
		// A wide table row may fill a chunk of OversizeFactor times the chunk
		// size. If the model takes less, the host truncates it silently.
		return fmt.Errorf("embedding: profile %s: a table chunk may reach %d units and the model accepts %d",
			p.Name, p.ChunkSize*OversizeFactor, p.MaxInput)
	}
	return nil
}
