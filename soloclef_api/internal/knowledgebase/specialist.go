package knowledgebase

import (
	"errors"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/assembly"
)

// The specialist's own limits. A task is a handful of changes (make a
// folder, write three notes into it, move two documents), each a read and a
// write: forty steps is that several times over, and a model that has not
// finished in forty is looping. Five minutes is one stretch of work between
// approvals; time spent waiting for a person is not counted.
const (
	specialistSteps   = 40
	specialistTimeout = 5 * time.Minute
)

// Writer is the knowledge-base specialist as the chat's assembly is given it
// (assembly.KnowledgeWriter).
type Writer struct {
	Changes Changes
}

// Check reports what is missing. Called once at start-up.
func (w Writer) Check() error { return w.Changes.Check() }

// Delegate is the specialist for one turn: its own agent, on the turn's
// model, with the tools bound to the turn's account and thread. Building it
// is a few map inserts (agent.New), so nothing is kept between turns.
func (w Writer) Delegate(chat model.Model, turn assembly.KnowledgeOwner) (agent.Delegate, error) {
	switch {
	case turn.UserID == "" || turn.ThreadID == 0:
		return agent.Delegate{}, errors.New("knowledgebase: a turn's specialist needs its account and its thread")
	case turn.Store == nil:
		return agent.Delegate{}, errors.New("knowledgebase: a turn's specialist needs the checkpoint store")
	}
	nested, err := agent.New(agent.Config{
		Model:  chat,
		System: systemPrompt,
		Tools:  w.Changes.tools(ownerOf(turn)),
		// The reads may run side by side; every change runs alone, in the
		// order the model gave (tool.Tool.Parallel).
		MaxSteps: specialistSteps, MaxParallelTools: 4,
		Store:   turn.Store,
		Trusted: turn.Trusted,
	})
	if err != nil {
		return agent.Delegate{}, err
	}
	return agent.Delegate{Name: assembly.KnowledgeBaseName, Description: delegateDescription, Agent: nested, Timeout: specialistTimeout}, nil
}

// ownerOf is the turn's account, thread and, in a notebook's chat, the
// notebook and what it may read.
func ownerOf(turn assembly.KnowledgeOwner) owner {
	who := owner{userID: turn.UserID, threadID: turn.ThreadID}
	if notebook := turn.Notebook; notebook != nil {
		id := notebook.ID
		who.notebookID, who.readable = &id, make(map[int64]bool, len(notebook.DocumentIDs))
		for _, documentID := range notebook.DocumentIDs {
			who.readable[documentID] = true
		}
	}
	return who
}

// Prompt is the main agent's <knowledge_base> block.
func (Writer) Prompt() string { return mainPrompt }

// Activities are the step display's lines for the specialist and each of
// its tools.
func (Writer) Activities() map[string]assembly.Activity {
	return map[string]assembly.Activity{
		assembly.KnowledgeBaseName: {Kind: "knowledge.edit", Active: "Working in your documents", Completed: "Worked in your documents", Category: "file", Icon: "folder-open"},
		listFolderTool:             {Kind: "knowledge.list", Active: "Looking through a folder", Completed: "Looked through a folder", Category: "file", Icon: "folder-tree"},
		findDocumentsTool:          {Kind: "knowledge.find", Active: "Finding documents", Completed: "Found documents", Category: "file", Icon: "folder-search"},
		readDocumentTool:           {Kind: "knowledge.read", Active: "Reading a document", Completed: "Read a document", Category: "file", Icon: "file-text"},
		createNoteTool:             {Kind: "knowledge.note.create", Active: "Writing a note", Completed: "Wrote a note", Category: "file", Icon: "file-plus"},
		editNoteTool:               {Kind: "knowledge.note.edit", Active: "Editing a note", Completed: "Edited a note", Category: "file", Icon: "file-pen"},
		renameDocumentTool:         {Kind: "knowledge.document.rename", Active: "Renaming a document", Completed: "Renamed a document", Category: "file", Icon: "file-pen"},
		moveDocumentTool:           {Kind: "knowledge.document.move", Active: "Moving a document", Completed: "Moved a document", Category: "file", Icon: "folder-open"},
		deleteNoteTool:             {Kind: "knowledge.note.delete", Active: "Deleting a note", Completed: "Deleted a note", Category: "file", Icon: "file-x"},
		createFolderTool:           {Kind: "knowledge.folder.create", Active: "Making a folder", Completed: "Made a folder", Category: "file", Icon: "folder-plus"},
		renameFolderTool:           {Kind: "knowledge.folder.rename", Active: "Renaming a folder", Completed: "Renamed a folder", Category: "file", Icon: "folder-open"},
		moveFolderTool:             {Kind: "knowledge.folder.move", Active: "Moving a folder", Completed: "Moved a folder", Category: "file", Icon: "folder-tree"},
		deleteFolderTool:           {Kind: "knowledge.folder.delete", Active: "Deleting a folder", Completed: "Deleted a folder", Category: "file", Icon: "folder-x"},
	}
}

const delegateDescription = "Make changes in the user's documents and folders: write a note, edit a note's text, rename or move a document, make, rename or move a folder, delete a note or an empty folder. Give it the whole task: what to write (the full text), which documents or folders (by name, or by id if a search gave one), and where things go."

// mainPrompt tells the main agent what the specialist is for and what each
// change costs the person: a card to approve.
const mainPrompt = `To change the user's documents and folders, hand the task to knowledge_base: saving something as a note, editing a note, renaming or moving documents, organising folders, deleting a note or an empty folder. Put everything it needs in the task, including the full text of anything to write: it sees nothing of this conversation.
Only do this when the user asks for a change. Each change is shown to the user for approval before it is made, and each can be undone from the chat afterwards, so do not ask for permission in prose first.
Uploaded files are not edited or deleted from the chat, only renamed and moved.`

// systemPrompt is the specialist's own.
const systemPrompt = `You make changes in the user's knowledge base: their notes, documents and folders. You see only the task you were given.

How to work:
1. Find what the task is about. Use list_folder to walk the folders and find_documents to find a document by its title or words in it. Every item has an id; act on ids you have seen, never on guesses.
2. Before editing a note, read it with read_document, and copy the passage to replace exactly.
3. Before making a folder or a note, look at where it is going: follow the user's existing folders and naming, and do not make a second folder of a name that exists.
4. Make the changes the task asks for, and no others. Each change is shown to the user for approval; if one is declined, do not make it again and do not work around it.
5. If the task is ambiguous (several documents fit and nothing says which), make no change and say what you found, so the user can choose.

What can be changed:
- Notes are written, edited and deleted.
- Any document is renamed and moved.
- Folders are made, renamed and moved, and deleted when empty.
- An uploaded file's text is not edited and an uploaded file is not deleted here; say so plainly.

Finish with a short plain account of what you changed (names and where), and of anything you could not do and why. Do not mention tool names or ids.`
