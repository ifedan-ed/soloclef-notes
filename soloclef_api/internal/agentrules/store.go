package agentrules

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
)

// errToolAbsent is a mark asked for on a tool the list does not show.
var errToolAbsent = errors.New("agentrules: no such tool")

// Store is the marks in Postgres. Every method takes the account first and
// passes it to every statement.
type Store struct {
	Pool *pgxpool.Pool
}

// Tools is every tool the account can mark, and whether it is marked: the
// catalogue first, then each connected app's tools in the order its server
// listed them. Two statements, whatever the number of apps.
func (s Store) Tools(ctx context.Context, userID string) ([]Tool, error) {
	queries := New(s.Pool)
	rows, err := queries.MarksOfOwner(ctx, userID)
	if err != nil {
		return nil, err
	}
	marked := make(map[Mark]bool, len(rows))
	for _, row := range rows {
		mark := Mark{Tool: row.Tool}
		if row.ConnectorID != nil {
			mark.ConnectorID = *row.ConnectorID
		}
		marked[mark] = true
	}
	apps, err := queries.AppsOfOwner(ctx, userID)
	if err != nil {
		return nil, err
	}

	tools := make([]Tool, 0, len(catalogue)+4*len(apps))
	for _, listed := range catalogue {
		tools = append(tools, Tool{Name: listed.name, Group: listed.group, AskFirst: marked[Mark{Tool: listed.name}]})
	}
	for _, app := range apps {
		id := app.ID
		for _, name := range listedNames(app.Tools) {
			tools = append(tools, Tool{Name: name, Group: app.Name, ConnectorID: &id, AskFirst: marked[Mark{ConnectorID: id, Tool: name}]})
		}
	}
	return tools, nil
}

// listedNames is the names in a connection's saved tool list, each once. An
// entry without a text name is skipped rather than failing the page: the
// chat skips the same list whole (mcptools, readCached), so such a server
// offers no tool to mark.
func listedNames(raw []byte) []string {
	var listed []struct {
		Name *string `json:"name"`
	}
	if json.Unmarshal(raw, &listed) != nil {
		return nil
	}
	names := make([]string, 0, len(listed))
	seen := make(map[string]bool, len(listed))
	for _, item := range listed {
		if item.Name == nil || *item.Name == "" || seen[*item.Name] {
			continue
		}
		seen[*item.Name] = true
		names = append(names, *item.Name)
	}
	return names
}

// Mark marks the tool "ask first". It answers errToolAbsent for a tool the
// list does not show: not one of the catalogue's, or not one the account's
// connection last listed.
func (s Store) Mark(ctx context.Context, userID string, mark Mark) error {
	if mark.ConnectorID == 0 {
		if !inCatalogue(mark.Tool) {
			return errToolAbsent
		}
		return New(s.Pool).MarkTool(ctx, MarkToolParams{UserID: userID, Tool: mark.Tool})
	}
	_, err := database.Write(ctx, s.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (struct{}, error) {
		queries := New(tx)
		if _, err := queries.LockAppTool(ctx, LockAppToolParams{ConnectorID: mark.ConnectorID, UserID: userID, Tool: mark.Tool}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return struct{}{}, errToolAbsent
			}
			return struct{}{}, err
		}
		return struct{}{}, queries.MarkTool(ctx, MarkToolParams{UserID: userID, ConnectorID: &mark.ConnectorID, Tool: mark.Tool})
	})
	return err
}

// Unmark takes the mark off. It is never refused for want of a tool: a
// person can always stop being asked, even about a tool a server no longer
// lists.
func (s Store) Unmark(ctx context.Context, userID string, mark Mark) error {
	if mark.ConnectorID == 0 {
		return New(s.Pool).UnmarkChatTool(ctx, UnmarkChatToolParams{UserID: userID, Tool: mark.Tool})
	}
	return New(s.Pool).UnmarkAppTool(ctx, UnmarkAppToolParams{UserID: userID, ConnectorID: &mark.ConnectorID, Tool: mark.Tool})
}
