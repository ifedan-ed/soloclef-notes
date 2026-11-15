// Package addsources is the chat's add_sources tool: in a notebook's chat,
// the assistant adds papers and web pages to that notebook's sources itself,
// by their identity (a PubMed id, a DOI, an arXiv id or an address), instead
// of telling the person to add them one click at a time.
//
// The adding is not done here. It is the research box's import
// (soloclef_api/internal/notebookresearch), reached through Adder, so a
// paper added from the chat and one picked in the box are found again,
// read, stored and indexed the same way: one import, not two.
//
// What is fixed by the server and not by the model: the account, the
// notebook and the conversation. The tool is built per turn for the turn's
// owner, the notebook the thread belongs to and the thread itself
// (assembly), and none is an argument the model could change. The import
// proves again, inside its transaction, that the notebook is the account's.
//
// # Papers run, pages ask
//
// A paper's identity (a PubMed or PubMed Central id, a DOI, an arXiv id, or
// the canonical link of one) goes to a fixed index in a fixed shape, so a
// call naming only papers runs without asking, as every tool does until the
// person marks it "ask first". A web page's address is a request to a
// server the model chose, from this server, and the model may have been
// told to choose it by text it read (a preprint's abstract, a page); data
// put in that address leaves with the request. So a call naming any page
// asks the person every time, whatever they have marked
// (tool.Tool.MustAsk), as the page reader would; and the import reads a
// page only for a call the person approved (Request.PagesApproved, from
// tool.Call.Approved), so the two decisions never rest on agreeing about the
// arguments. Which is which is read by one package for both
// (sourceidentity).
//
// Reading a source and indexing it take seconds to minutes, so the tool does
// not wait for either: it answers as soon as the sources are listed, says
// they are being read, and the documents become searchable in this same
// turn as each is indexed (documents.Joined). The model is told not to say
// it has read a source until a search finds its text.
package addsources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/documents"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/sourceidentity"
)

// ToolName is what the model calls, what the step display and the cards are
// keyed on, and the name an "ask first" mark is kept under.
const ToolName = "add_sources"

// ActivityKind is the step display's kind for a call: its line is "Adding
// sources", then Finished's count.
const ActivityKind = "notebook.add_sources"

// MaxPerCall is the most sources one call adds. Ten is a reading list's
// worth for one answer. Each new paper costs a paced request to PubMed or
// arXiv (keyless PubMed allows three a second across the whole server) and
// one of the few page reads the server runs at once across every import, so
// ten keep a call to seconds and leave room for other people's imports; and
// ten rows are as many as a card can list and still be read at a glance. A
// longer list is added over several calls, which the model is told.
const MaxPerCall = 10

// maxRefChars bounds one identity as sent: an address is at most
// sourceidentity.MaxAddressChars and an id far less.
const maxRefChars = sourceidentity.MaxAddressChars

// Outcome is what became of one identity the model sent.
type Outcome struct {
	// Ref is the identity as the model sent it, trimmed.
	Ref string `json:"ref"`
	// Status is StatusAdded, StatusAlready or StatusNotAdded.
	Status string `json:"status"`
	// DocumentID is the source's document, for an added one or one that
	// was a source already.
	DocumentID int64 `json:"document_id,omitempty"`
	// Title is the source's title as stored. A page not yet read is titled
	// with its address until the read says what it is called.
	Title string `json:"title,omitempty"`
	// URL is the address the source was found at.
	URL string `json:"url,omitempty"`
	// Reading says the source's text is still being read or indexed, so a
	// search does not find it yet.
	Reading bool `json:"reading,omitempty"`
	// Reason says, in plain words, why a source was not added.
	Reason string `json:"reason,omitempty"`
}

// The three outcomes.
const (
	StatusAdded    = "added"
	StatusAlready  = "already_a_source"
	StatusNotAdded = "not_added"
)

// ErrNotebookAbsent is the Adder's answer when the notebook is not the
// account's, or no longer exists.
var ErrNotebookAbsent = errors.New("addsources: the notebook is not the account's")

// Request is one call's sources, and whose and where they are added.
type Request struct {
	UserID     string
	NotebookID int64
	// ThreadID is the conversation the call was made in. A page's address
	// keeps its query and fragment only if the user wrote that exact
	// address in it; otherwise both are dropped before it is read, because
	// they are where a model told what to send would put the data.
	ThreadID int64
	Refs     []string
	// PagesApproved says the person approved this call. Without it a web
	// page is not read, and its outcome says so; papers are added either
	// way.
	PagesApproved bool
}

// Adder adds sources to one of an account's notebooks: the research box's
// import. It answers one outcome per identity, in the order given.
type Adder interface {
	Add(ctx context.Context, request Request) ([]Outcome, error)
	// UserWrote answers which of the addresses the account's user wrote
	// in a message of this conversation, as text they typed or pasted.
	// A page they gave themselves is theirs to add without a question.
	UserWrote(ctx context.Context, userID string, threadID int64, addresses []string) (map[string]bool, error)
}

// Result is what the tool answers: the model reads it, and the card draws
// from it (Card).
type Result struct {
	NotebookID int64     `json:"notebook_id"`
	Items      []Outcome `json:"items"`
	// Note is what the model is to make of a source still being read.
	Note string `json:"note,omitempty"`
}

// readingNote is said whenever a source in the answer is still being read.
const readingNote = "Sources marked reading are being read and indexed now. search_knowledge_base finds each once that is done, usually within a minute. Do not say you have read one until a search returns its text; until then say it was added and is being read."

const description = `Add papers and web pages to this notebook's sources, by identity: a PubMed id (PMID), a DOI, an arXiv id, a PubMed Central id, or a page's address. Each is fetched (a PubMed paper with its abstract, and its open full text from PubMed Central when there is one; an arXiv paper from its PDF) and becomes a source the notebook's search reads.

Use it when the user asks you to add sources, or when answering properly needs a paper's full text that the notebook does not hold. Send only identities taken from a search result in this conversation or given by the user; never compose one. At most 10 per call.

Papers, and pages whose address the user wrote in this conversation, are added at once. A call that names any other web page waits for the user to approve it, so send papers and pages in separate calls, and send a page only when the user asked for it or it is plainly what they need. A page's address is read without its query string or fragment unless the user wrote that exact address.

The answer says, for each, whether it was added, was already a source, or was not added and why. Tell the user what you added, by title.`

var parameters = json.RawMessage(`{"type":"object","properties":{"sources":{"type":"array","minItems":1,"maxItems":10,"items":{"type":"string"},"description":"Each a PMID (\"38123456\"), a DOI (\"10.1056/NEJMoa2034577\"), an arXiv id (\"2401.01234\"), a PubMed Central id (\"PMC1234567\") or an http(s) address."}},"required":["sources"]}`)

// Tool is add_sources for one turn: the account, the notebook and the
// conversation are fixed here. joined is told every document that became a
// source, so the turn's search reads it as soon as it is indexed.
//
// A call of papers asks for no approval of its own (tool.Auto): adding a
// paper is undone in one click, and the owner's rule is that every tool
// runs until the person marks it "ask first", which the settings page
// offers for this one. A call naming a web page asks every time
// (namesAPage; the package comment says why). It does not run beside other
// calls, because two of its calls racing would each report the other's
// paper as already a source.
func Tool(adder Adder, userID string, notebookID, threadID int64, joined *documents.Joined) tool.Tool {
	return tool.Tool{
		Name: ToolName, Description: description, Parameters: parameters,
		Approval: tool.Auto, MustAsk: func(args json.RawMessage) bool { return asksAboutAPage(adder, userID, threadID, args) },
		Run: func(ctx context.Context, call tool.Call) (string, error) {
			refs, err := readRefs(call.Args)
			if err != nil {
				return "", err
			}
			outcomes, err := adder.Add(ctx, Request{
				UserID: userID, NotebookID: notebookID, ThreadID: threadID, Refs: refs,
				PagesApproved: call.Approved,
			})
			if errors.Is(err, ErrNotebookAbsent) {
				return "", errors.New("this conversation's notebook no longer exists, so nothing was added; tell the user")
			}
			if err != nil {
				return "", err
			}
			result := Result{NotebookID: notebookID, Items: outcomes}
			for _, outcome := range outcomes {
				if outcome.DocumentID > 0 && outcome.Status != StatusNotAdded {
					joined.Add(outcome.DocumentID)
				}
				if outcome.Reading {
					result.Note = readingNote
				}
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				return "", err
			}
			return string(encoded), nil
		},
	}
}

// readRefs is the call's identities, each once, in the order sent, or why
// the call cannot be made. A list over the bound is refused whole rather
// than cut, so that the model knows to send the rest and never believes a
// paper was added that was not.
func readRefs(raw json.RawMessage) ([]string, error) {
	var args struct {
		Sources []string `json:"sources"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("the arguments are not readable: %w", err)
	}
	var refs []string
	seen := map[string]bool{}
	for _, ref := range args.Sources {
		ref = strings.TrimSpace(ref)
		if ref == "" || seen[ref] {
			continue
		}
		if utf8.RuneCountInString(ref) > maxRefChars {
			return nil, fmt.Errorf("an identity is longer than %d characters", maxRefChars)
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	switch {
	case len(refs) == 0:
		return nil, errors.New("name at least one source to add")
	case len(refs) > MaxPerCall:
		return nil, fmt.Errorf("at most %d sources can be added in one call, and %d were sent; send them in several calls", MaxPerCall, len(refs))
	}
	return refs, nil
}

// pagesNamed is the web pages a call's arguments name: addresses that are
// not a paper's canonical link (sourceidentity). Arguments that cannot be
// read name nothing; the call then fails in Run without adding anything.
func pagesNamed(args json.RawMessage) []string {
	refs, err := readRefs(args)
	if err != nil {
		return nil
	}
	var pages []string
	for _, ref := range refs {
		if read, problem := sourceidentity.Read(ref); problem == "" && read.Kind == sourceidentity.Page {
			pages = append(pages, read.Text)
		}
	}
	return pages
}

// whoWroteTimeout bounds the one lookup a call's question depends on.
const whoWroteTimeout = 5 * time.Second

// asksAboutAPage is the call's MustAsk: it asks when the call names a web
// page the person did not write themselves in this conversation (Daniel,
// 2026-11-15: "if you ask it to fetch, allow it"). A page the person typed
// or pasted is their request, and its address carries nothing the model
// put in it; a page the model picked up from a search result, a paper or
// another page is where a planted instruction would send the notebook's
// text, so the person sees it first. A lookup that fails asks: the
// question is the safe side to err on.
func asksAboutAPage(adder Adder, userID string, threadID int64, args json.RawMessage) bool {
	pages := pagesNamed(args)
	if len(pages) == 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), whoWroteTimeout)
	defer cancel()
	theirs, err := adder.UserWrote(ctx, userID, threadID, pages)
	if err != nil {
		return true
	}
	for _, page := range pages {
		if !theirs[page] {
			return true
		}
	}
	return false
}

// Added is how many sources a result added, for the step display's line.
// A result that cannot be read added nothing that can be counted.
func Added(resultText string) int {
	var result Result
	if json.Unmarshal([]byte(resultText), &result) != nil {
		return 0
	}
	added := 0
	for _, outcome := range result.Items {
		if outcome.Status == StatusAdded {
			added++
		}
	}
	return added
}

// Finished is the step display's line for a finished call: "Added 2
// sources".
func Finished(resultText string) string {
	switch added := Added(resultText); added {
	case 0:
		return "Added no sources"
	case 1:
		return "Added 1 source"
	default:
		return fmt.Sprintf("Added %d sources", added)
	}
}
