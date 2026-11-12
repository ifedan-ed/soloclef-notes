package notebooksources

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
)

// source is one source as both apps read it.
type source struct {
	ID           int64     `json:"id"`
	Kind         string    `json:"kind"`
	TargetID     int64     `json:"target_id"`
	Title        string    `json:"title"`
	DocumentType *string   `json:"document_type"`
	Status       *string   `json:"status"`
	Enabled      bool      `json:"enabled"`
	AddedAt      time.Time `json:"added_at"`
}

// sourcesOf is the notebook's sources, documents and folders together, in
// the order they were added and then by id.
func sourcesOf(ctx context.Context, db DBTX, userID string, notebookID int64) ([]source, error) {
	q := New(db)
	documents, err := q.DocumentSources(ctx, DocumentSourcesParams{NotebookID: notebookID, UserID: userID})
	if err != nil {
		return nil, err
	}
	folders, err := q.FolderSources(ctx, FolderSourcesParams{NotebookID: notebookID, UserID: userID})
	if err != nil {
		return nil, err
	}
	all := make([]source, 0, len(documents)+len(folders))
	for _, row := range documents {
		documentType, status := row.DocumentType, row.Status
		all = append(all, source{ID: row.ID, Kind: "document", TargetID: deref(row.DocumentID), Title: row.Title,
			DocumentType: &documentType, Status: &status, Enabled: row.Enabled, AddedAt: row.AddedAt})
	}
	for _, row := range folders {
		all = append(all, source{ID: row.ID, Kind: "folder", TargetID: deref(row.FolderID), Title: row.Name,
			Enabled: row.Enabled, AddedAt: row.AddedAt})
	}
	sort.SliceStable(all, func(i, j int) bool {
		if !all[i].AddedAt.Equal(all[j].AddedAt) {
			return all[i].AddedAt.Before(all[j].AddedAt)
		}
		return all[i].ID < all[j].ID
	})
	return all, nil
}

func deref(id *int64) int64 {
	if id == nil {
		return 0
	}
	return *id
}

// errNotebookAbsent and errSourceAbsent end a write's transaction with the
// 404 the route answers.
var (
	errNotebookAbsent = errors.New("notebook not found")
	errSourceAbsent   = errors.New("source not found")
)

// write runs a write in one transaction: it marks the notebook used (which
// also proves it is the caller's), runs work, and answers the sources as
// they then are.
func (x *exchange) write(notebookID int64, work func(context.Context, *Queries) error) ([]source, bool) {
	ctx := x.ctx.Context()
	listed, err := database.Write(ctx, x.routes.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) ([]source, error) {
		q := New(tx)
		touched, err := q.TouchNotebook(ctx, TouchNotebookParams{UsedAt: x.routes.now(), NotebookID: notebookID, UserID: x.userID})
		if err != nil {
			return nil, err
		}
		if touched == 0 {
			return nil, errNotebookAbsent
		}
		if err := work(ctx, q); err != nil {
			return nil, err
		}
		return sourcesOf(ctx, tx, x.userID, notebookID)
	})
	switch {
	case errors.Is(err, errNotebookAbsent):
		x.notFound(notebookAbsent)
		return nil, false
	case errors.Is(err, errSourceAbsent):
		x.notFound(sourceAbsent)
		return nil, false
	case err != nil:
		x.fail("could not change the notebook's sources", err)
		return nil, false
	}
	return listed, true
}

func (r Routes) list(x *exchange) {
	ids, ok := x.ids("notebook_id")
	if !ok {
		return
	}
	ctx := x.ctx.Context()
	owned, err := New(r.Pool).NotebookIsOwned(ctx, NotebookIsOwnedParams{NotebookID: ids[0], UserID: x.userID})
	if err != nil {
		x.fail("could not look for the notebook", err)
		return
	}
	if !owned {
		x.notFound(notebookAbsent)
		return
	}
	listed, err := sourcesOf(ctx, r.Pool, x.userID, ids[0])
	if err != nil {
		x.fail("could not read the notebook's sources", err)
		return
	}
	x.send(http.StatusOK, listed)
}

// add links documents and folders. Every target must be the caller's, or
// nothing is added: a target that is another account's is "not found",
// exactly like one that never existed.
func (r Routes) add(x *exchange) {
	ids, ok := x.ids("notebook_id")
	if !ok {
		return
	}
	var asked struct {
		Targets []struct {
			Kind string `json:"kind"`
			ID   int64  `json:"id"`
		} `json:"targets"`
	}
	if !x.body(&asked) {
		return
	}
	if len(asked.Targets) == 0 || len(asked.Targets) > maxTargets {
		x.invalid("targets", "Give between 1 and 500 targets")
		return
	}
	documents, folders := map[int64]bool{}, map[int64]bool{}
	for _, target := range asked.Targets {
		if target.ID <= 0 {
			x.invalid("targets", "Every target id should be greater than 0")
			return
		}
		switch target.Kind {
		case "document":
			documents[target.ID] = true
		case "folder":
			folders[target.ID] = true
		default:
			x.invalid("targets", "A target's kind should be 'document' or 'folder'")
			return
		}
	}
	documentIDs, folderIDs := keys(documents), keys(folders)
	now := r.now()
	named := false
	listed, ok := x.write(ids[0], func(ctx context.Context, q *Queries) error {
		ownedDocuments, err := q.OwnedDocumentCount(ctx, OwnedDocumentCountParams{Ids: documentIDs, UserID: x.userID})
		if err != nil {
			return err
		}
		ownedFolders, err := q.OwnedFolderCount(ctx, OwnedFolderCountParams{Ids: folderIDs, UserID: x.userID})
		if err != nil {
			return err
		}
		if ownedDocuments != int64(len(documentIDs)) || ownedFolders != int64(len(folderIDs)) {
			return errSourceAbsent
		}
		if err := q.LinkDocuments(ctx, LinkDocumentsParams{NotebookID: ids[0], UserID: x.userID, AddedAt: now, Ids: documentIDs}); err != nil {
			return err
		}
		if err := q.LinkFolders(ctx, LinkFoldersParams{NotebookID: ids[0], UserID: x.userID, AddedAt: now, Ids: folderIDs}); err != nil {
			return err
		}
		if len(documentIDs) == 0 {
			return nil
		}
		named, err = q.NamingOwed(ctx, NamingOwedParams{NotebookID: ids[0], UserID: x.userID, Ids: documentIDs})
		return err
	})
	if !ok {
		return
	}
	// After the commit, so the worker reads the sources this request added.
	// A notebook named "Untitled notebook" names itself from its first ready
	// source; one still being read asks when the indexer finishes it. The
	// name is a courtesy: a publish that fails is logged, never answered.
	if named && r.Naming != nil {
		if _, err := r.Naming.Enqueue(x.ctx.Context(), autoname(ids[0])); err != nil {
			r.Logger.Warn("could not ask for the notebook to be named", "notebook", ids[0], "error", err)
		}
	}
	x.send(http.StatusOK, listed)
}

// autoname is the worker's notebook_autoname(notebook, "source", null), a
// few seconds late so that sources added together are named from together:
// the message the chat's mentions publish (internal/chat/mentions.go) and
// the indexer's (soloclef_worker/internal/namerequest).
func autoname(notebookID int64) produce.Message {
	return produce.Message{Task: "notebook_autoname", Args: []any{notebookID, "source", nil}, Countdown: 4 * time.Second}
}

func keys(set map[int64]bool) []int64 {
	out := make([]int64, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// toggleOne ticks or unticks one source and answers it.
func (r Routes) toggleOne(x *exchange) {
	ids, ok := x.ids("notebook_id", "source_id")
	if !ok {
		return
	}
	var asked struct {
		Enabled *bool `json:"enabled"`
	}
	if !x.body(&asked) {
		return
	}
	if asked.Enabled == nil {
		x.invalid("enabled", "Field required")
		return
	}
	listed, ok := x.write(ids[0], func(ctx context.Context, q *Queries) error {
		changed, err := q.SetSourceEnabled(ctx, SetSourceEnabledParams{
			Enabled: *asked.Enabled, ID: ids[1], NotebookID: ids[0], UserID: x.userID})
		if err == nil && changed == 0 {
			return errSourceAbsent
		}
		return err
	})
	if !ok {
		return
	}
	for _, one := range listed {
		if one.ID == ids[1] {
			x.send(http.StatusOK, one)
			return
		}
	}
	// The link exists but its target does not: a dangling row the joins
	// leave out, which the schema's cascades should make impossible.
	x.notFound(sourceAbsent)
}

// toggleAll ticks or unticks every source of the notebook, or the listed
// ones; a listed id that is not this notebook's source changes nothing.
func (r Routes) toggleAll(x *exchange) {
	ids, ok := x.ids("notebook_id")
	if !ok {
		return
	}
	var asked struct {
		Enabled   *bool    `json:"enabled"`
		SourceIDs *[]int64 `json:"source_ids"`
	}
	if !x.body(&asked) {
		return
	}
	if asked.Enabled == nil {
		x.invalid("enabled", "Field required")
		return
	}
	params := SetSourcesEnabledParams{Enabled: *asked.Enabled, NotebookID: ids[0], UserID: x.userID, Ids: []int64{}}
	if asked.SourceIDs != nil {
		params.Listed, params.Ids = true, *asked.SourceIDs
	}
	listed, ok := x.write(ids[0], func(ctx context.Context, q *Queries) error {
		return q.SetSourcesEnabled(ctx, params)
	})
	if ok {
		x.send(http.StatusOK, listed)
	}
}

// remove unlinks one source. The document or folder it pointed at stays.
func (r Routes) remove(x *exchange) {
	ids, ok := x.ids("notebook_id", "source_id")
	if !ok {
		return
	}
	_, ok = x.write(ids[0], func(ctx context.Context, q *Queries) error {
		removed, err := q.UnlinkSource(ctx, UnlinkSourceParams{ID: ids[1], NotebookID: ids[0], UserID: x.userID})
		if err == nil && removed == 0 {
			return errSourceAbsent
		}
		return err
	})
	if ok {
		x.sendNothing()
	}
}
