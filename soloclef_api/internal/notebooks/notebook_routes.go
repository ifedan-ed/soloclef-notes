package notebooks

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

// The six routes of a notebook's own row. Each is the caller's own notebook
// or a 404: the statements name the caller as the owner, and nothing else is
// checked.

// RegisterNotebookList serves the list.
func (r Routes) RegisterNotebookList(api huma.API) {
	register[noPath](api, r, http.MethodGet, NotebooksPath,
		"list-notebooks", "Every notebook the caller owns", r.listNotebooks)
}

// listNotebooks is the caller's notebooks, most recently used first, and
// then what each row's answer needs (described).
func (r Routes) listNotebooks(x *exchange) {
	ctx := x.ctx.Context()
	notebooks, err := listNotebooks(ctx, r.Database, x.caller.UserID)
	if err != nil {
		x.fail("could not read the caller's notebooks", err)
		return
	}
	reads, err := r.described(ctx, x.caller.UserID, notebooks)
	if err != nil {
		x.fail("could not describe the caller's notebooks", err)
		return
	}
	x.sendCached(http.StatusOK, reads)
}

// RegisterNotebookRead serves one notebook.
func (r Routes) RegisterNotebookRead(api huma.API) {
	register[notebookPath](api, r, http.MethodGet, NotebookPath,
		"read-notebook", "One notebook", r.readNotebook)
}

func (r Routes) readNotebook(x *exchange) {
	id, ok := x.notebookParameter()
	if !ok {
		return
	}
	ctx := x.ctx.Context()
	var notebook *Notebook
	var err error
	if !id.nameless {
		notebook, err = loadNotebook(ctx, r.Database, x.caller.UserID, id.value)
	}
	if !x.found(notebook, err, "could not look for the notebook") {
		return
	}
	x.describeOne(*notebook, x.sendCached)
}

// RegisterNotebookCreate serves the create.
func (r Routes) RegisterNotebookCreate(api huma.API) {
	register[noPath](api, r, http.MethodPost, NotebooksPath,
		"create-notebook", "Make a notebook", r.createNotebook)
}

// createNotebook writes one row, the caller's, and answers what the INSERT
// returned.
//
// A notebook that has just been made has no sources and is not about to
// name itself, so its answer needs no further statement: the counts are
// zero and `title_pending` is false.
func (r Routes) createNotebook(x *exchange) {
	requested, failures, err := readNotebookCreation(
		x.ctx.Header("Content-Type"), requestbody.Buffered(x.ctx.Context()))
	if err != nil {
		x.fail("a body reached the route that the body reader should have turned away", err)
		return
	}
	if len(failures) > 0 {
		x.invalid(failures)
		return
	}

	// Only a default title lets the notebook name itself later, and the icon
	// follows the title: both sources are "auto" or both are "user".
	source := "user"
	if isDefaultTitle(requested.Title) {
		source = "auto"
	}
	row, err := New(r.Database).InsertNotebook(x.ctx.Context(), InsertNotebookParams{
		// The owner is the signed-in caller and nothing the body says.
		UserID:      x.caller.UserID,
		Title:       requested.Title,
		Icon:        requested.Icon,
		TitleSource: source,
		IconSource:  source,
		WrittenAt:   r.now(),
	})
	if err != nil {
		x.fail("could not write the notebook", err)
		return
	}
	x.send(http.StatusCreated, notebookRead(Notebook(row), sourceTotals{}, false))
}

// RegisterNotebookDelete serves the delete.
func (r Routes) RegisterNotebookDelete(api huma.API) {
	register[notebookPath](api, r, http.MethodDelete, NotebookPath,
		"delete-notebook", "Remove a notebook", r.deleteNotebook)
}

// deleteNotebook removes the notebook with its source links, studio outputs
// and public link. The documents it read from stay, and so do its notes and
// its chat, which become unfiled.
func (r Routes) deleteNotebook(x *exchange) {
	id, ok := x.notebookParameter()
	if !ok {
		return
	}
	var removed int64
	if !id.nameless {
		var err error
		removed, err = New(r.Database).DeleteNotebook(x.ctx.Context(),
			DeleteNotebookParams{ID: id.value, UserID: x.caller.UserID})
		if err != nil {
			x.fail("could not delete the notebook", err)
			return
		}
	}
	if removed == 0 {
		// Never made, already deleted, or another account's.
		x.notFound(notebookAbsent)
		return
	}
	x.sendNothing()
}

// RegisterFeature serves the pin.
func (r Routes) RegisterFeature(api huma.API) {
	register[notebookPath](api, r, http.MethodPut, FeaturePath,
		"feature-notebook", "Pin a notebook to the top of the caller's list", r.feature)
}

// RegisterUnfeature serves the unpin.
func (r Routes) RegisterUnfeature(api huma.API) {
	register[notebookPath](api, r, http.MethodDelete, FeaturePath,
		"unfeature-notebook", "Remove the pin", r.unfeature)
}

// feature pins the notebook. Pinning one that is pinned already changes
// nothing and is answered the same.
func (r Routes) feature(x *exchange) {
	r.pin(x, func(id int64) (*Notebook, error) {
		at := r.now()
		row, err := New(r.Database).FeatureNotebook(x.ctx.Context(),
			FeatureNotebookParams{FeaturedAt: &at, ID: id, UserID: x.caller.UserID})
		return pinned(Notebook(row), err)
	})
}

// unfeature removes the pin, with no complaint when there was none.
func (r Routes) unfeature(x *exchange) {
	r.pin(x, func(id int64) (*Notebook, error) {
		row, err := New(r.Database).UnfeatureNotebook(x.ctx.Context(),
			UnfeatureNotebookParams{ID: id, UserID: x.caller.UserID})
		return pinned(Notebook(row), err)
	})
}

// pinned is a guarded UPDATE's row as a notebook, or its error with no
// notebook. The two statements return differently named rows of the same
// columns; this is where they become one type.
func pinned(notebook Notebook, err error) (*Notebook, error) {
	if err != nil {
		return nil, err
	}
	return &notebook, nil
}

// pin is what the two feature routes share: the id, one guarded UPDATE that
// answers the row as it now is (no row is somebody else's notebook, or
// none), and then the notebook described.
func (r Routes) pin(x *exchange, change func(id int64) (*Notebook, error)) {
	id, ok := x.notebookParameter()
	if !ok {
		return
	}
	var notebook *Notebook
	var err error
	if !id.nameless {
		notebook, err = change(id.value)
	}
	if !x.found(notebook, err, "could not change the pin") {
		return
	}
	// No Cache-Control: only the two GETs set it.
	x.describeOne(*notebook, x.send)
}

// describeOne answers one notebook with its counts and naming state.
func (x *exchange) describeOne(notebook Notebook, send func(int, any)) {
	reads, err := x.routes.described(x.ctx.Context(), x.caller.UserID, []Notebook{notebook})
	if err != nil {
		x.fail("could not describe the notebook", err)
		return
	}
	send(http.StatusOK, reads[0])
}
