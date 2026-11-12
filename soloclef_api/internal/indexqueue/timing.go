package indexqueue

import "time"

// markTimeout bounds the one statement that marks a document failed after
// its job could not be queued. It runs outside the request's context, so it
// needs a limit of its own.
const markTimeout = 10 * time.Second
