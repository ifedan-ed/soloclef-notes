package indexing

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_filestore"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/embedding"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/indexstore"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/vectorindex"
)

// problem is why a job could not finish, in the two forms it is needed in.
type problem struct {
	// reason is for the person looking at the failed document. It says what
	// happened in their terms and, where there is something they can do,
	// what. It never carries an address, a key or a stack.
	reason string
	// retryable is true when the same job may succeed later with nothing
	// changed: a service was down or busy.
	retryable bool
	// err is the technical error, for the log.
	err error
}

// describe turns an error from any step into a problem.
//
// The default, for an error nothing here recognises, is "may pass": the
// steps' own errors are all recognised below, so what is left is the
// database and the network, and a document should not be failed for good by
// a connection that dropped.
func describe(err error) *problem {
	var (
		parsing   *parse.Error
		embedded  *embedding.Failure
		vectorial *vectorindex.Failure
	)
	switch {
	case errors.Is(err, indexstore.ErrGone):
		return &problem{err: err}
	case errors.Is(err, indexstore.ErrChanged):
		return &problem{reason: "The document was being indexed twice at once.", retryable: true, err: err}
	case errors.As(err, &parsing):
		return &problem{reason: parserReason(parsing), retryable: parsing.Retryable(), err: err}
	case errors.As(err, &embedded):
		if embedded.Retryable {
			return &problem{reason: "The embedding service did not answer, so the document could not be made searchable.", retryable: true, err: err}
		}
		return &problem{reason: fmt.Sprintf("The embedding service refused the document (status %d). An administrator needs to check the embedding settings.", embedded.Status), err: err}
	case errors.As(err, &vectorial):
		if vectorial.Retryable {
			return &problem{reason: "The search index did not answer, so the document could not be made searchable.", retryable: true, err: err}
		}
		return &problem{reason: fmt.Sprintf("The search index refused the document (status %d). An administrator needs to check the search index.", vectorial.Status), err: err}
	default:
		return &problem{reason: "The server could not finish indexing the document.", retryable: true, err: err}
	}
}

// describeFetch is describe for a failure to open the file in storage.
func describeFetch(err error) *problem {
	switch {
	case errors.Is(err, filestore.ErrNotFound):
		return &problem{reason: "The uploaded file is no longer in storage. Upload it again.", err: err}
	case errors.Is(err, filestore.ErrBackendNotRead), errors.Is(err, filestore.ErrInvalidKey):
		// The row names a store this process was not given, or a key that
		// never was one. Neither mends itself.
		return &problem{reason: "The server cannot reach the place this file was stored. An administrator needs to check the file storage settings.", err: err}
	default:
		return &problem{reason: "File storage did not answer, so the file could not be read.", retryable: true, err: err}
	}
}

// parserNames is how each parser is named to a person.
var parserNames = map[parse.ID]string{
	parse.Local:        "The local parser",
	parse.Datalab:      "Datalab",
	parse.LlamaParse:   "LlamaParse",
	parse.Unstructured: "Unstructured",
	parse.Vision:       "The vision model",
}

// parserReason is a parser's refusal as a sentence. The parsers' messages
// are already written to be read (soloclef_ingest/parse.Error.Message); this
// names the parser when one was chosen, and makes a sentence of it.
func parserReason(failure *parse.Error) string {
	message := sentence(failure.Message)
	if name, chosen := parserNames[failure.Parser]; chosen {
		return name + ": " + lowerFirst(message)
	}
	return message
}

// sentence capitalises the first letter and ends the text with a full stop.
func sentence(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "The file could not be read."
	}
	first, size := utf8.DecodeRuneInString(text)
	text = string(unicode.ToUpper(first)) + text[size:]
	if !strings.HasSuffix(text, ".") {
		text += "."
	}
	return text
}

func lowerFirst(text string) string {
	first, size := utf8.DecodeRuneInString(text)
	return string(unicode.ToLower(first)) + text[size:]
}
