package deliverables

import "git.danvics.com/danvics/soloclef/soloclef_studio/artifactsave"

// The rows an artifact is saved as, and the store that writes them, are
// soloclef_studio/artifactsave's: the worker's video render saves the same
// rows, and one writer is what keeps the two tiers' artifacts alike. The
// names stay here so this package and its callers read as they did.
type (
	// Owner is who an artifact is made for: the turn's account and thread.
	// It is fixed by the server when the turn is built, from the request's
	// principal, and is not something the model's arguments can name.
	Owner = artifactsave.Owner
	// NewArtifact is what Database.Create writes.
	NewArtifact = artifactsave.NewArtifact
	// Created is the rows a save produced.
	Created = artifactsave.Created
	// Store is Database over the pool.
	Store = artifactsave.Store
)
