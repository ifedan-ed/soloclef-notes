package uploads

// Prior is the document an earlier upload of the same name made.
type Prior struct {
	ID int64
	// State is the document's status state; "ready" when it has none.
	State string
	// Checksum is the SHA-256 of its newest stored original, or "" when it
	// has no original or none with a checksum (a document the Python tier
	// stored before checksums were kept).
	Checksum string
}

// Outcome is what an uploaded file became.
type Outcome int

const (
	// Created: a new document.
	Created Outcome = iota
	// Replaced: an existing document was given this file as its new
	// original and handed back to the indexer.
	Replaced
	// Retried: an existing document that had failed, uploaded again with
	// the same bytes. It is handed back to the indexer with the original it
	// already has; the second copy is not kept.
	Retried
	// Duplicate: the account already has this file, or is still indexing
	// a file of this name. Nothing changes; the copy is not kept.
	Duplicate
	// BeingDeleted: the document of this name is being deleted, so nothing
	// can be attached to it. The upload is refused; a moment later it would
	// be Created.
	BeingDeleted
)

// decide is the whole rule for an upload whose name the account has seen.
//
// The Python route decided by name alone: a ready document of the same name
// was a duplicate whatever the new file held, so a corrected file could not
// be uploaded without deleting the old document first. The bytes are known
// here (the store digests them as it writes), so "the same file" means the
// same bytes.
func decide(prior *Prior, checksum string) Outcome {
	if prior == nil {
		return Created
	}
	sameBytes := prior.Checksum != "" && prior.Checksum == checksum
	switch prior.State {
	case "deleting":
		return BeingDeleted
	case "pending", "processing":
		// A job is already on its way to this document. Queueing a second
		// would race it, and swapping the file under it would index one
		// file under the other's row.
		return Duplicate
	case "failed":
		if sameBytes {
			return Retried
		}
		return Replaced
	default:
		if sameBytes {
			return Duplicate
		}
		return Replaced
	}
}

// keepsFile reports whether the outcome keeps the object just stored. The
// others leave it for the route to remove.
func (o Outcome) keepsFile() bool { return o == Created || o == Replaced }

// queues reports whether the outcome hands the document to the indexer.
func (o Outcome) queues() bool { return o == Created || o == Replaced || o == Retried }
