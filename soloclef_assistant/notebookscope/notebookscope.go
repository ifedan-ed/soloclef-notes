// Package notebookscope reads what confines a turn to a notebook: the
// notebook's enabled sources, by name for the prompt and by document for
// the search's hard limit, and its "Configure chat" choices.
//
// Two tiers confine a turn to a notebook and must draw the same line. The
// API does for a notebook's own chat (the thread names the notebook), and
// the worker does for an automation's step that names one. It is here so
// that "which documents may this turn read" is one pair of statements: a
// second copy that forgot the owner on one join would be a notebook that
// reads another account's document in one tier only.
package notebookscope

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Sources is a notebook's enabled sources.
type Sources struct {
	// Titles names the enabled sources, documents first, in the order they
	// were added; a folder's name ends in "/".
	Titles []string
	// DocumentIDs is every document the notebook may read. Empty means
	// nothing is enabled, and then nothing may be read: it never means "no
	// limit".
	DocumentIDs []int64
}

// Settings is a notebook's title and its chat settings, as stored.
type Settings struct {
	Title        string
	Goal         string
	Instructions string
	Length       string
}

// Read is the enabled sources of one of the owner's notebooks. A notebook
// that is not the owner's has none.
func Read(ctx context.Context, db DBTX, ownerID string, notebookID int64) (Sources, error) {
	queries := New(db)
	listed, err := queries.NotebookSources(ctx, NotebookSourcesParams{UserID: ownerID, NotebookID: notebookID})
	if err != nil {
		return Sources{}, err
	}
	var out Sources
	var folders []string
	for _, source := range listed {
		if source.DocumentID != nil {
			out.Titles = append(out.Titles, source.Title)
		} else {
			folders = append(folders, source.Title)
		}
	}
	out.Titles = append(out.Titles, folders...)
	if len(listed) == 0 {
		return out, nil
	}
	out.DocumentIDs, err = queries.NotebookDocuments(ctx, NotebookDocumentsParams{UserID: ownerID, NotebookID: notebookID})
	return out, err
}

// SettingsOf is one of the owner's notebooks by id; found is false when it
// is not the owner's or is not there.
func SettingsOf(ctx context.Context, db DBTX, ownerID string, notebookID int64) (settings Settings, found bool, err error) {
	row, err := New(db).NotebookChatSettings(ctx, NotebookChatSettingsParams{NotebookID: notebookID, UserID: ownerID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{}, false, nil
	}
	if err != nil {
		return Settings{}, false, err
	}
	return Settings{Title: row.Title, Goal: row.ChatGoal, Instructions: row.ChatInstructions, Length: row.ResponseLength}, true, nil
}
