package knowledgebase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/folders"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/notes"
)

// The reading tools' names.
const (
	listFolderTool    = "list_folder"
	findDocumentsTool = "find_documents"
	readDocumentTool  = "read_document"
)

// The sizes of what the reading tools show: a folder's first hundred
// documents (newest edit first), twenty matches for a search, and a
// document's text twenty thousand characters at a time (about five
// thousand tokens), which is a long note whole and a long file in parts.
const (
	listDocuments = 100
	findMatches   = 20
	readChars     = 20_000
)

// serverFailure is what the model is told when a change fails for a reason
// of the server's. The cause goes to the log; a model shown "ERROR: deadlock
// detected" would only pass it on.
const serverFailure = "The server could not do this just now, and nothing was changed. Say so to the user; it can be tried again."

// tools are the specialist's tools for one owner.
func (c Changes) tools(who owner) []tool.Tool {
	read := func(name, description, parameters string, run func(context.Context, tool.Call) (string, error)) tool.Tool {
		return tool.Tool{Name: name, Description: description, Parameters: json.RawMessage(parameters),
			Approval: tool.Auto, Parallel: true, Run: c.guard(name, run)}
	}
	write := func(name, description, parameters string, approval tool.Approval, run func(context.Context, tool.Call) (string, error)) tool.Tool {
		// Never Parallel: "make the folder, then put the note in it" in one
		// batch means that order.
		return tool.Tool{Name: name, Description: description, Parameters: json.RawMessage(parameters),
			Approval: approval, Run: c.guard(name, run)}
	}
	return []tool.Tool{
		read(listFolderTool, "List one folder: the folders and documents directly in it, each with its id. Without folder_id, the top level.",
			`{"type":"object","properties":{"folder_id":{"type":["integer","null"],"description":"The folder to list; omit or null for the top level."}},"additionalProperties":false}`,
			func(ctx context.Context, call tool.Call) (string, error) { return c.listFolder(ctx, who, call) }),
		read(findDocumentsTool, "Find documents by title or by words in their text. Returns up to 20, each with its id and folder.",
			`{"type":"object","properties":{"query":{"type":"string","description":"Words of the title, or words the text contains."}},"required":["query"],"additionalProperties":false}`,
			func(ctx context.Context, call tool.Call) (string, error) { return c.findDocuments(ctx, who, call) }),
		read(readDocumentTool, "Read a document's text, 20000 characters at a time.",
			`{"type":"object","properties":{"document_id":{"type":"integer"},"offset":{"type":"integer","minimum":0,"description":"Where to start, in characters; 0 for the beginning."}},"required":["document_id"],"additionalProperties":false}`,
			func(ctx context.Context, call tool.Call) (string, error) { return c.readDocument(ctx, who, call) }),

		write(createNoteTool, "Create a note: a document whose text is Markdown you write.",
			`{"type":"object","properties":{"title":{"type":"string","description":"The note's title; empty to take it from the first line of the text."},"content":{"type":"string","description":"The note's text, in Markdown."},"folder_id":{"type":["integer","null"],"description":"The folder to put it in; omit or null for no folder."}},"required":["title","content"],"additionalProperties":false}`,
			tool.Ask, func(ctx context.Context, call tool.Call) (string, error) {
				var args struct {
					Title    string          `json:"title"`
					Content  string          `json:"content"`
					FolderID json.RawMessage `json:"folder_id"`
				}
				if err := decode(call.Args, &args); err != nil {
					return "", err
				}
				folderID, err := optionalID(args.FolderID, "folder_id")
				if err != nil {
					return "", err
				}
				if err := checkNote(args.Title, args.Content); err != nil {
					return "", err
				}
				return c.createNote(ctx, who, call, strings.TrimSpace(args.Title), args.Content, folderID)
			}),
		write(editNoteTool, "Edit a note's text. Either replace one passage (old_text, which must appear in the note exactly once, and new_text), or replace the whole text (content). Read the note first.",
			`{"type":"object","properties":{"document_id":{"type":"integer"},"old_text":{"type":"string"},"new_text":{"type":"string"},"content":{"type":"string","description":"The whole new text, instead of old_text and new_text."}},"required":["document_id"],"additionalProperties":false}`,
			tool.Ask, func(ctx context.Context, call tool.Call) (string, error) {
				var args struct {
					DocumentID int64   `json:"document_id"`
					OldText    *string `json:"old_text"`
					NewText    *string `json:"new_text"`
					Content    *string `json:"content"`
				}
				if err := decode(call.Args, &args); err != nil {
					return "", err
				}
				edit, err := editOf(args.OldText, args.NewText, args.Content)
				if err != nil {
					return "", err
				}
				return c.editNote(ctx, who, call, args.DocumentID, edit)
			}),
		write(renameDocumentTool, "Give a document (a note or any other) a new title.",
			`{"type":"object","properties":{"document_id":{"type":"integer"},"title":{"type":"string"}},"required":["document_id","title"],"additionalProperties":false}`,
			tool.Ask, func(ctx context.Context, call tool.Call) (string, error) {
				var args struct {
					DocumentID int64  `json:"document_id"`
					Title      string `json:"title"`
				}
				if err := decode(call.Args, &args); err != nil {
					return "", err
				}
				title, err := cleanTitle(args.Title)
				if err != nil {
					return "", err
				}
				return c.renameDocument(ctx, who, call, args.DocumentID, title)
			}),
		write(moveDocumentTool, "Move a document (a note or any other) into a folder, or out of every folder.",
			`{"type":"object","properties":{"document_id":{"type":"integer"},"folder_id":{"type":["integer","null"],"description":"The folder to move it into, or null for no folder."}},"required":["document_id","folder_id"],"additionalProperties":false}`,
			tool.Ask, func(ctx context.Context, call tool.Call) (string, error) {
				var args struct {
					DocumentID int64           `json:"document_id"`
					FolderID   json.RawMessage `json:"folder_id"`
				}
				if err := decode(call.Args, &args); err != nil {
					return "", err
				}
				folderID, err := requiredID(args.FolderID, "folder_id")
				if err != nil {
					return "", err
				}
				return c.moveDocument(ctx, who, call, args.DocumentID, folderID)
			}),
		write(deleteNoteTool, "Delete a note. Other kinds of document are not deleted from the chat.",
			`{"type":"object","properties":{"document_id":{"type":"integer"}},"required":["document_id"],"additionalProperties":false}`,
			// Every time: one approval of a delete says nothing about the
			// next, and "always allow" must not reach one.
			tool.AlwaysAsk, func(ctx context.Context, call tool.Call) (string, error) {
				var args struct {
					DocumentID int64 `json:"document_id"`
				}
				if err := decode(call.Args, &args); err != nil {
					return "", err
				}
				return c.deleteNote(ctx, who, call, args.DocumentID)
			}),

		write(createFolderTool, "Create a folder.",
			`{"type":"object","properties":{"name":{"type":"string"},"parent_folder_id":{"type":["integer","null"],"description":"The folder to create it in; omit or null for the top level."}},"required":["name"],"additionalProperties":false}`,
			tool.Ask, func(ctx context.Context, call tool.Call) (string, error) {
				var args struct {
					Name     string          `json:"name"`
					ParentID json.RawMessage `json:"parent_folder_id"`
				}
				if err := decode(call.Args, &args); err != nil {
					return "", err
				}
				name, err := cleanFolderName(args.Name)
				if err != nil {
					return "", err
				}
				parent, err := optionalID(args.ParentID, "parent_folder_id")
				if err != nil {
					return "", err
				}
				return c.createFolder(ctx, who, call, name, parent)
			}),
		write(renameFolderTool, "Give a folder a new name.",
			`{"type":"object","properties":{"folder_id":{"type":"integer"},"name":{"type":"string"}},"required":["folder_id","name"],"additionalProperties":false}`,
			tool.Ask, func(ctx context.Context, call tool.Call) (string, error) {
				var args struct {
					FolderID int64  `json:"folder_id"`
					Name     string `json:"name"`
				}
				if err := decode(call.Args, &args); err != nil {
					return "", err
				}
				name, err := cleanFolderName(args.Name)
				if err != nil {
					return "", err
				}
				return c.renameFolder(ctx, who, call, args.FolderID, name)
			}),
		write(moveFolderTool, "Move a folder, with everything in it, into another folder or to the top level.",
			`{"type":"object","properties":{"folder_id":{"type":"integer"},"parent_folder_id":{"type":["integer","null"],"description":"The folder to move it into, or null for the top level."}},"required":["folder_id","parent_folder_id"],"additionalProperties":false}`,
			tool.Ask, func(ctx context.Context, call tool.Call) (string, error) {
				var args struct {
					FolderID int64           `json:"folder_id"`
					ParentID json.RawMessage `json:"parent_folder_id"`
				}
				if err := decode(call.Args, &args); err != nil {
					return "", err
				}
				parent, err := requiredID(args.ParentID, "parent_folder_id")
				if err != nil {
					return "", err
				}
				return c.moveFolder(ctx, who, call, args.FolderID, parent)
			}),
		write(deleteFolderTool, "Delete an empty folder.",
			`{"type":"object","properties":{"folder_id":{"type":"integer"}},"required":["folder_id"],"additionalProperties":false}`,
			tool.AlwaysAsk, func(ctx context.Context, call tool.Call) (string, error) {
				var args struct {
					FolderID int64 `json:"folder_id"`
				}
				if err := decode(call.Args, &args); err != nil {
					return "", err
				}
				return c.deleteFolder(ctx, who, call, args.FolderID)
			}),
	}
}

// guard turns a failure into what the model may read: a refusal as its own
// sentence, anything else as serverFailure with the cause logged. A stopped
// run is passed through as it is, so the loop sees the cancellation.
func (c Changes) guard(name string, run func(context.Context, tool.Call) (string, error)) tool.Func {
	return func(ctx context.Context, call tool.Call) (string, error) {
		answer, err := run(ctx, call)
		var refused refusal
		switch {
		case err == nil, errors.As(err, &refused), ctx.Err() != nil:
			return answer, err
		}
		c.Logger.ErrorContext(ctx, "a knowledge-base tool failed", "tool", name, "call_id", call.ID, "error", err.Error())
		return "", errors.New(serverFailure)
	}
}

// decode reads a call's arguments, refusing a field the tool does not have:
// a "path" where "folder_id" is meant would otherwise be dropped in silence
// and the note filed at the top level.
func decode(args json.RawMessage, into any) error {
	decoder := json.NewDecoder(bytes.NewReader(args))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return refuse("The arguments are not what this tool takes (%s). Check the tool's parameters and call it again.", err.Error())
	}
	return nil
}

// optionalID reads an id that may be left out or null, meaning none.
func optionalID(raw json.RawMessage, name string) (*int64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var id int64
	if err := json.Unmarshal(raw, &id); err != nil {
		return nil, refuse("%s must be a folder's id (a whole number) or null.", name)
	}
	return &id, nil
}

// requiredID reads an id that must be given and may be null: a move must
// say where to, and "nowhere" is a place it may say.
func requiredID(raw json.RawMessage, name string) (*int64, error) {
	if len(raw) == 0 {
		return nil, refuse("%s is required: a folder's id, or null.", name)
	}
	return optionalID(raw, name)
}

// checkNote refuses what Postgres or the notes would: a title past the
// notes' limit, a text past their size, and a NUL, which Postgres stores in
// no text.
func checkNote(title, content string) error {
	switch {
	case utf8.RuneCountInString(strings.TrimSpace(title)) > notes.MaxTitleLength:
		return refuse("The title is longer than %d characters.", notes.MaxTitleLength)
	case len(content) > notes.MaxTextBytes:
		return refuse("The text is longer than a note may be (%d bytes).", notes.MaxTextBytes)
	case strings.ContainsRune(title, 0) || strings.ContainsRune(content, 0):
		return refuse("The text contains a NUL character, which cannot be stored.")
	}
	return nil
}

func cleanTitle(title string) (string, error) {
	title = strings.Join(strings.Fields(title), " ")
	if title == "" {
		return "", refuse("The title is empty.")
	}
	return title, checkNote(title, "")
}

func cleanFolderName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", refuse("The folder name is empty.")
	case utf8.RuneCountInString(name) > folders.MaxNameLength:
		return "", refuse("The folder name is longer than %d characters.", folders.MaxNameLength)
	case strings.ContainsRune(name, 0):
		return "", refuse("The folder name contains a NUL character.")
	}
	return name, nil
}

// editOf is the one edit the arguments describe.
func editOf(oldText, newText, content *string) (noteEdit, error) {
	switch {
	case content != nil && (oldText != nil || newText != nil):
		return noteEdit{}, refuse("Give either content, or old_text and new_text, not both.")
	case content != nil:
		if err := checkNote("", *content); err != nil {
			return noteEdit{}, err
		}
		return noteEdit{content: content}, nil
	case oldText == nil || newText == nil:
		return noteEdit{}, refuse("Give old_text and new_text together, or content.")
	case *oldText == "":
		return noteEdit{}, refuse("old_text is empty. Give the passage to replace, copied exactly from the note.")
	}
	if err := checkNote("", *newText); err != nil {
		return noteEdit{}, err
	}
	return noteEdit{oldText: *oldText, newText: *newText}, nil
}

// ---------------------------------------------------------------------------
// Reading
// ---------------------------------------------------------------------------

func (c Changes) listFolder(ctx context.Context, who owner, call tool.Call) (string, error) {
	var args struct {
		FolderID json.RawMessage `json:"folder_id"`
	}
	if err := decode(call.Args, &args); err != nil {
		return "", err
	}
	folderID, err := optionalID(args.FolderID, "folder_id")
	if err != nil {
		return "", err
	}
	q := New(c.Pool)
	folderTree, err := readTree(ctx, q, who.userID)
	if err != nil {
		return "", err
	}
	subfolders := folderTree.top
	if folderID != nil {
		if _, found := folderTree.byID[*folderID]; !found {
			return "", refuse("There is no folder with id %d.", *folderID)
		}
		subfolders = folderTree.children[*folderID]
	}
	documents, err := q.DocumentsInFolder(ctx, DocumentsInFolderParams{UserID: who.userID, FolderID: folderID, PageSize: listDocuments + 1})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if folderID == nil {
		b.WriteString("The top level (/).\n")
	} else {
		fmt.Fprintf(&b, "Folder %s (folder %d).\n", folderTree.path(folderID), *folderID)
	}
	if len(subfolders) == 0 {
		b.WriteString("Folders: none.\n")
	} else {
		b.WriteString("Folders:\n")
		for _, folder := range subfolders {
			fmt.Fprintf(&b, "- [folder %d] %s\n", folder.ID, folder.Name)
		}
	}
	if len(documents) == 0 {
		b.WriteString("Documents: none.\n")
		return b.String(), nil
	}
	b.WriteString("Documents:\n")
	for i, document := range documents {
		if i == listDocuments {
			break
		}
		b.WriteString(documentLine(document.ID, document.Title, document.DocumentType, document.State) + "\n")
	}
	if len(documents) > listDocuments {
		total, err := q.CountDocumentsInFolder(ctx, CountDocumentsInFolderParams{UserID: who.userID, FolderID: folderID})
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "…and %d more, the least recently edited. Use find_documents to look for one by name.\n", total-listDocuments)
	}
	return b.String(), nil
}

func (c Changes) findDocuments(ctx context.Context, who owner, call tool.Call) (string, error) {
	var args struct {
		Query string `json:"query"`
	}
	if err := decode(call.Args, &args); err != nil {
		return "", err
	}
	query := strings.TrimSpace(args.Query)
	if query == "" {
		return "", refuse("The query is empty.")
	}
	q := New(c.Pool)
	matches, err := q.FindDocuments(ctx, FindDocumentsParams{
		UserID: who.userID, TitlePattern: "%" + likeEscaped(query) + "%", Query: query, PageSize: findMatches,
		Confined: who.confined(), Readable: readableIDs(who), NotebookID: who.notebookID,
	})
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return fmt.Sprintf("No document's title or text matches %q.", query), nil
	}
	folderTree, err := readTree(ctx, q, who.userID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, match := range matches {
		fmt.Fprintf(&b, "%s in %s\n", documentLine(match.ID, match.Title, match.DocumentType, match.State), folderTree.path(match.FolderID))
	}
	return b.String(), nil
}

// readableIDs is the notebook's readable documents as the statement takes
// them; empty in a plain chat, where the statement does not look.
func readableIDs(who owner) []int64 {
	ids := make([]int64, 0, len(who.readable))
	for id := range who.readable {
		ids = append(ids, id)
	}
	return ids
}

// mayRead refuses, in a notebook's chat, the text of a document that chat
// may not read: one that is neither a source of the notebook nor a note
// filed in it. The notebook's own chat is confined to its sources, and the
// specialist is not a way round that. Its title and folder are still
// listed: they are what the source picker shows too.
func mayRead(ctx context.Context, q *Queries, who owner, documentID int64) error {
	if !who.confined() || who.readable[documentID] {
		return nil
	}
	filed, err := q.FiledInNotebook(ctx, FiledInNotebookParams{ID: documentID, UserID: who.userID, NotebookID: who.notebookID})
	if err != nil {
		return err
	}
	if !filed {
		return refuse("This chat belongs to a notebook, and document %d is neither one of its sources nor a note filed in it, so its text is not read here. It can still be renamed or moved.", documentID)
	}
	return nil
}

// likeEscaped makes the words a literal for ILIKE: a person searching for
// "100%" means the percent sign.
func likeEscaped(text string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(text)
}

func (c Changes) readDocument(ctx context.Context, who owner, call tool.Call) (string, error) {
	var args struct {
		DocumentID int64 `json:"document_id"`
		Offset     int   `json:"offset"`
	}
	if err := decode(call.Args, &args); err != nil {
		return "", err
	}
	if args.Offset < 0 {
		return "", refuse("offset cannot be negative.")
	}
	q := New(c.Pool)
	if err := mayRead(ctx, q, who, args.DocumentID); err != nil {
		return "", err
	}
	document, err := q.DocumentToRead(ctx, DocumentToReadParams{ID: args.DocumentID, UserID: who.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", refuse("There is no document with id %d.", args.DocumentID)
	}
	if err != nil {
		return "", err
	}
	folderTree, err := readTree(ctx, q, who.userID)
	if err != nil {
		return "", err
	}
	text := []rune(document.Text)
	start := min(args.Offset, len(text))
	end := min(start+readChars, len(text))
	var b strings.Builder
	fmt.Fprintf(&b, "%s in %s\n", documentLine(document.ID, document.Title, document.DocumentType, document.State), folderTree.path(document.FolderID))
	switch {
	case len(text) == 0:
		b.WriteString("It has no text yet.\n")
		return b.String(), nil
	case start == 0 && end == len(text):
		fmt.Fprintf(&b, "Its text, %d characters:\n", len(text))
	default:
		fmt.Fprintf(&b, "Characters %d to %d of %d:\n", start, end, len(text))
	}
	b.WriteString("<text>\n" + string(text[start:end]) + "\n</text>\n")
	if end < len(text) {
		fmt.Fprintf(&b, "More follows: read again with offset %d.\n", end)
	}
	return b.String(), nil
}
