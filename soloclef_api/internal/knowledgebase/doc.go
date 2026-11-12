// Package knowledgebase is the chat's knowledge-base specialist and its undo:
// the tools that write and change an account's notes, documents and folders
// from a conversation, the record kept of every change, and the two routes
// that take a change back:
//
//	POST /api/v1/threads/{thread_id}/revert/{action_id}          undo one change
//	POST /api/v1/threads/{thread_id}/revert-turn/{chat_turn_id}  undo a turn's
//
// # The specialist
//
// The main agent hands a task ("save these notes under Pharmacology", "move
// the lecture notes into Term 2") to a nested run with the tools of
// specialist.go: three that read the tree (list, find, read) and nine that
// change it. Writing, editing, renaming and moving ask first (tool.Ask, which
// the account's "always allow" rules can waive); deleting asks every time
// (tool.AlwaysAsk). A change to a note's text, and an undo that puts text
// back, re-indexes the note through the indexing worker's index_document job,
// after its commit. A rename or a move queues nothing: the index holds a
// note's passages and its owner, and every search reads the title and the
// folder from the rows, so re-indexing would re-read a file to change nothing.
//
// What may be changed is what can be given back exactly:
//
//   - A note's text is the note, so a note is written, edited and deleted. An
//     uploaded file's text comes from its file: editing it would make the two
//     disagree, and deleting it here would take a file an undo cannot return,
//     so those are refused with a sentence that says so.
//   - Any document is renamed and moved: its title and its folder are rows.
//   - A folder is made, renamed and moved, and deleted only when empty, so an
//     undo of the delete is the folder again and not a guess at its contents.
//
// # In a notebook's chat
//
// The web and the phone chat only inside notebooks, so the specialist is
// offered there too, held to the notebook's limit: a note it writes is filed
// in the notebook, as "Save to note" files one, and the text it reads (to
// read, to match words in, to edit) is that of the notebook's sources and
// the notes filed in it. Titles and folders are listed account-wide, as the
// source picker lists them, so anything may still be renamed and moved.
//
// # The record, in the change's own transaction
//
// Each change writes, in the one transaction that makes it: the row
// (agent_action_log) the chat's tool card and "undo this turn" read, and the
// item as it was before (document_revisions or folder_revisions). A change
// that commits is undoable and one that does not leaves no record; there is
// no moment in which one exists without the other. The row's
// reverse_descriptor holds the item as the change left it.
//
// # Undo refuses to overwrite a newer change
//
// An undo compares the item now with the item as the change left it, field
// by field, for the fields the undo would write. A note edited since its
// creation is not deleted by undoing the creation; a document renamed since
// it was moved is still moved back, because the move did not touch its
// title. A refusal is one sentence that says what changed. The Python undo
// wrote the snapshot over whatever was there.
//
// # What it does better than the Python
//
//   - The record and the change are one transaction. The Python wrote the
//     log row in one middleware and the revision in another, at the end of
//     the turn, best-effort for everything but deletes.
//   - Every statement names the owner, the undo routes included: an action is
//     found by its id, its thread and its account together.
//   - A note's passages are not copied into the revision. An undo puts the
//     text back and re-indexes it, so the passages are the indexer's own and
//     the revision is a fraction of the size.
//   - A deleted note comes back with its notebook filing, its pin and the
//     notebooks it was a source of.
package knowledgebase
