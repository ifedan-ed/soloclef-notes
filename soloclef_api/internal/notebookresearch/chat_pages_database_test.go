package notebookresearch

import (
	"context"
	"slices"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/addsources"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/pagereader"
)

// What the chat's add_sources may read of a web page (chat_pages.go): one
// the person wrote in this conversation, as written; any other only for a
// call the person approved, and then without its query string and fragment.
func TestTheChatReadsAPageOnlyAsThePersonAllowed(t *testing.T) {
	h := newHarness(t)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	ctx := context.Background()
	const theirs = "https://tides.example/tables?port=brest#today"
	h.pages.byAddress[theirs] = pagereader.Page{Status: pagereader.Read, Title: "Brest tides", Text: "Heights at Brest."}
	// Alice's notebook chat, where she pasted one address with its query,
	// and Bob's chat, where he wrote the injected one.
	testpostgres.Exec(t, h.pool, `
INSERT INTO new_chat_threads (id, user_id, title, notebook_id) VALUES (77, '`+alice+`', 'Tides', 10), (88, '`+bob+`', 'His', 20);
INSERT INTO new_chat_messages (thread_id, role, content, author_id) VALUES
 (77, 'USER', '[{"type":"text","text":"Please add `+theirs+` to my sources"}]', '`+alice+`'),
 (77, 'ASSISTANT', '[{"type":"text","text":"https://tides.example/tables?d=SECRET"}]', NULL),
 (88, 'USER', '[{"type":"text","text":"https://tides.example/tables?d=SECRET"}]', '`+bob+`');`)
	adder := chatSources(h)

	// A call nobody approved: the paper is added, and a page she never
	// wrote is not read.
	out, err := adder.Add(ctx, addsources.Request{UserID: alice, NotebookID: 10, ThreadID: 77, Refs: []string{"111", "https://elsewhere.example/page"}})
	if err != nil {
		t.Fatal(err)
	}
	if statuses(out) != "added not_added" || out[1].Reason != pageNeedsApproval {
		t.Fatalf("an unapproved call: %+v", out)
	}
	h.settle()
	if slices.Contains(h.pages.read, "https://elsewhere.example/page") {
		t.Fatal("a page was read for a call nobody approved")
	}

	// Approved, with a query the model wrote (the assistant's message and
	// another account's thread do not count): read without it.
	out, err = adder.Add(ctx, addsources.Request{UserID: alice, NotebookID: 10, ThreadID: 77, PagesApproved: true,
		Refs: []string{"https://tides.example/tables?d=SECRET#x"}})
	if err != nil {
		t.Fatal(err)
	}
	if statuses(out) != "added" || out[0].URL != "https://tides.example/tables" {
		t.Fatalf("an approved page with the model's query: %+v", out)
	}
	out, err = adder.Add(ctx, addsources.Request{UserID: alice, NotebookID: 10, ThreadID: 88, PagesApproved: true,
		Refs: []string{"https://tides.example/tables?d=SECRET"}})
	if err != nil {
		t.Fatal(err)
	}
	if statuses(out) != "already_a_source" || out[0].URL != "https://tides.example/tables" {
		t.Fatalf("an address only another account wrote: %+v", out)
	}

	// Not approved, but the very address she pasted: hers, so read as she
	// wrote it without a question.
	out, err = adder.Add(ctx, addsources.Request{UserID: alice, NotebookID: 10, ThreadID: 77, Refs: []string{theirs}})
	if err != nil {
		t.Fatal(err)
	}
	if statuses(out) != "added" || out[0].URL != theirs {
		t.Fatalf("the address she wrote: %+v", out)
	}
	h.settle()
	for _, read := range h.pages.read {
		if read != "https://tides.example/tables" && read != theirs {
			t.Errorf("read %s", read)
		}
	}
	if !slices.Contains(h.pages.read, theirs) || !slices.Contains(h.pages.read, "https://tides.example/tables") {
		t.Errorf("the pages read: %v", h.pages.read)
	}
}
