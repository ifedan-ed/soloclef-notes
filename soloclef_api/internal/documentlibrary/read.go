package documentlibrary

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// read answers one document: the JSON record, or the OKF concept when the
// caller asks for Markdown.
func (r Routes) read(x *exchange) {
	id, ok := x.documentID()
	if !ok {
		return
	}
	row, err := New(r.Pool).DocumentOfOwner(x.ctx.Context(), DocumentOfOwnerParams{ID: id, UserID: x.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		x.refuse(http.StatusNotFound, absent(strconv.FormatInt(id, 10)))
		return
	}
	if err != nil {
		x.fail("could not read the document", err)
		return
	}
	// The concept is the only view that is not JSON, and only a caller that
	// names it gets it: a browser's `*/*` is answered JSON.
	if strings.Contains(strings.ToLower(x.ctx.Header("Accept")), "text/markdown") {
		body := []byte(concept(row))
		x.response.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		x.response.Header().Set("Cache-Control", "private, no-store")
		x.response.Header().Set("Content-Length", strconv.Itoa(len(body)))
		// The answer depends on the Accept header, so a cache must not hand
		// the Markdown to a caller that asked for JSON.
		x.response.Header().Set("Vary", "Accept")
		x.response.WriteHeader(http.StatusOK)
		_, _ = x.response.Write(body)
		return
	}
	x.response.Header().Set("Vary", "Accept")
	x.send(http.StatusOK, fromRow(row))
}
