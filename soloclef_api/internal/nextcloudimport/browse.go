package nextcloudimport

import (
	"context"
	"log/slog"
	"net/http"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/nextcloud"
)

// # The picker's two reads
//
// Before anything is imported the picker shows what is there: one folder
// at a time (browse), or whatever in the account has a name containing
// what was typed (search). Both are the caller's own connection read with
// its own app password, and both only read: nothing is written here or in
// Nextcloud.
//
// Nothing is cached. A person browsing wants what is there now, and a
// listing is one request to a server on the same network.

// The bounds of the two reads.
const (
	// maxSearchResults is how many matches a search answers: a list a
	// person can look through, and the picker shows no more.
	maxSearchResults = 50
	// A search of one letter matches most of an account.
	minQueryRunes = 2
	maxQueryRunes = 200
)

// shownEntry is one file or folder as the picker reads it. A value the
// server did not give is null, never a zero or "" that would read as one.
type shownEntry struct {
	Path        string  `json:"path"`
	Name        string  `json:"name"`
	IsFolder    bool    `json:"is_folder"`
	Size        *int64  `json:"size"`
	Modified    *string `json:"modified"`
	ContentType *string `json:"content_type"`
	ETag        *string `json:"etag"`
}

func shown(entries []Entry) []shownEntry {
	listed := make([]shownEntry, len(entries))
	for index, entry := range entries {
		listed[index] = shownEntry{
			Path: entry.Path, Name: entry.Name, IsFolder: entry.IsFolder, Size: entry.Size,
			ContentType: optional(entry.ContentType), ETag: optional(entry.ETag),
		}
		if entry.Modified != nil {
			listed[index].Modified = optional(entry.Modified.Format(isoTime))
		}
	}
	return listed
}

// registerReads mounts browse and search.
func (r Routes) registerReads(api huma.API) {
	type browse struct {
		ConnectorID string `path:"connector_id" doc:"The connection's id"`
		Path        string `query:"path" doc:"The folder to list; the account's root when left out"`
	}
	type search struct {
		ConnectorID string `path:"connector_id" doc:"The connection's id"`
		Query       string `query:"q" doc:"Part of a file or folder name"`
	}
	stream := func(serve func(*asking)) *huma.StreamResponse {
		return &huma.StreamResponse{Body: func(ctx huma.Context) {
			serve(&asking{routes: r, ctx: ctx, response: humawriter.New(ctx), requestID: requestcontext.ID(ctx.Context())})
		}}
	}
	huma.Register(api, huma.Operation{
		OperationID: "browse-nextcloud", Method: http.MethodGet, Path: BrowsePath,
		Summary: "The contents of one Nextcloud folder", Tags: []string{"connectors"},
	}, func(context.Context, *browse) (*huma.StreamResponse, error) { return stream(r.browse), nil })
	huma.Register(api, huma.Operation{
		OperationID: "search-nextcloud", Method: http.MethodGet, Path: SearchPath,
		Summary: "Nextcloud files and folders by name", Tags: []string{"connectors"},
	}, func(context.Context, *search) (*huma.StreamResponse, error) { return stream(r.search), nil })
}

// reading is what both reads begin with: the caller's own connection as an
// account. ok is false when the answer has been written.
func (a *asking) reading() (account Account, ok bool) {
	caller, signedIn := a.caller()
	if !signedIn {
		return Account{}, false
	}
	connectorID, numbered := a.number("connector_id", connectionAbsent)
	if !numbered {
		return Account{}, false
	}
	account, refused, err := a.routes.account(a.ctx.Context(), connectorID, caller.UserID)
	if err != nil {
		a.fail("the connection could not be read", err)
		return Account{}, false
	}
	if refused != nil {
		a.refuse(refused)
		return Account{}, false
	}
	return account, true
}

// remoteFailed answers a failure of the Nextcloud side: its own refusal
// when it has one, and a 500 otherwise.
func (a *asking) remoteFailed(doing string, err error) {
	if refused := remoteRefusal(err); refused != nil {
		// The cause may name the server; it is for the log.
		a.routes.Logger.LogAttrs(a.ctx.Context(), slog.LevelWarn, doing+" failed at Nextcloud",
			slog.String("request_id", a.requestID), slog.String("error", err.Error()))
		a.refuse(refused)
		return
	}
	a.fail(doing+" failed", err)
}

func (r Routes) browse(a *asking) {
	account, ok := a.reading()
	if !ok {
		return
	}
	asked := a.ctx.Query("path")
	if utf8.RuneCountInString(asked) > maxPathLength {
		a.refuse(refuse(http.StatusUnprocessableEntity, "path is at most %d characters", maxPathLength))
		return
	}
	// "" is the root: the picker opens there.
	folder, err := nextcloud.NormalizePath(asked)
	if err != nil {
		a.refuse(refuse(http.StatusBadRequest, "That is not a folder inside your Nextcloud files"))
		return
	}
	entries, err := r.Connect(account).List(a.ctx.Context(), folder)
	if err != nil {
		a.remoteFailed("listing a Nextcloud folder", err)
		return
	}
	a.send(struct {
		Path    string       `json:"path"`
		Entries []shownEntry `json:"entries"`
	}{folder, shown(entries)})
}

func (r Routes) search(a *asking) {
	account, ok := a.reading()
	if !ok {
		return
	}
	query := a.ctx.Query("q")
	if length := utf8.RuneCountInString(query); length < minQueryRunes || length > maxQueryRunes {
		a.refuse(refuse(http.StatusUnprocessableEntity, "q must be %d to %d characters", minQueryRunes, maxQueryRunes))
		return
	}
	entries, err := r.Connect(account).Search(a.ctx.Context(), query, maxSearchResults)
	if err != nil {
		a.remoteFailed("searching Nextcloud", err)
		return
	}
	a.send(struct {
		Query   string       `json:"query"`
		Entries []shownEntry `json:"entries"`
	}{query, shown(entries)})
}
