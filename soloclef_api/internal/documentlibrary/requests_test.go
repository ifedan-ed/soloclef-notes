package documentlibrary

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestReadFilter(t *testing.T) {
	seven := int64(7)
	tests := []struct {
		name   string
		query  string
		search bool
		// bad is the parameters refused, in order; empty for a request read.
		bad  []string
		want func(filter) bool
	}{
		{name: "nothing asked is the newest fifty", query: "",
			want: func(f filter) bool {
				return f.sort == "created_at" && f.descending && f.pageSize == 50 && f.skip == 0 && len(f.types) == 0 && f.types != nil
			}},
		{name: "the third page of twenty", query: "page=2&page_size=20",
			want: func(f filter) bool { return f.skip == 40 && f.page == 2 }},
		{name: "an offset wins over the page and names its own", query: "page=9&page_size=10&skip=25",
			want: func(f filter) bool { return f.skip == 25 && f.page == 2 }},
		{name: "types, with spaces and an empty piece", query: "document_types=NOTE,%20FILE,,",
			want: func(f filter) bool { return strings.Join(f.types, "+") == "NOTE+FILE" }},
		{name: "a type that does not exist", query: "document_types=NOTE,MEMO", bad: []string{"document_types"}},
		{name: "everything at once is no longer a page size", query: "page_size=-1", bad: []string{"page_size"}},
		{name: "a page size past the largest", query: "page_size=201", bad: []string{"page_size"}},
		{name: "a page that is not a number", query: "page=two", bad: []string{"page"}},
		{name: "documents in no folder", query: "folder_id=ROOT", want: func(f filter) bool { return f.unfiled && f.folderID == nil }},
		{name: "one folder", query: "folder_id=7", want: func(f filter) bool { return !f.unfiled && *f.folderID == seven }},
		{name: "a folder that is a word", query: "folder_id=inbox", bad: []string{"folder_id"}},
		{name: "by title, ascending", query: "sort_by=title&sort_order=asc",
			want: func(f filter) bool { return f.sort == "title" && !f.descending }},
		{name: "a column that is not sorted by", query: "sort_by=content&sort_order=up", bad: []string{"sort_by", "sort_order"}},
		{name: "a search needs a term", query: "", search: true, bad: []string{"title"}},
		{name: "a search term matches itself", query: "title=100%25_a%5Cb", search: true,
			want: func(f filter) bool { return *f.pattern == `%100\%\_a\\b%` && f.sort == "id" && !f.descending }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			query, err := url.ParseQuery(test.query)
			if err != nil {
				t.Fatal(err)
			}
			read, failures := readFilter(query, test.search)
			var refused []string
			for _, failure := range failures {
				refused = append(refused, failure.Location[1])
			}
			if strings.Join(refused, ",") != strings.Join(test.bad, ",") {
				t.Fatalf("refused %v, want %v", refused, test.bad)
			}
			if test.want != nil && !test.want(read) {
				t.Errorf("read %+v", read)
			}
		})
	}
}

func TestAnAdditionIsCheckedWhole(t *testing.T) {
	text := func(s string) *string { return &s }
	zero := int64(0)
	tests := []struct {
		name string
		body addition
		bad  []string
	}{
		{"a title and a text", addition{Title: text("  Tides \n today "), Content: text("Spring tides.")}, nil},
		{"nothing at all", addition{}, []string{"title", "content"}},
		{"a blank text", addition{Title: text("T"), Content: text(" \n ")}, []string{"content"}},
		{"a title too long", addition{Title: text(strings.Repeat("a", 501)), Content: text("x")}, []string{"title"}},
		{"a text with a NUL", addition{Title: text("T"), Content: text("a\x00b")}, []string{"content"}},
		{"a script for a source", addition{Title: text("T"), Content: text("x"), SourceURL: text("javascript:alert(1)")}, []string{"source_url"}},
		{"a blank source is no source", addition{Title: text("T"), Content: text("x"), SourceURL: text("  ")}, nil},
		{"folder zero", addition{Title: text("T"), Content: text("x"), FolderID: &zero}, []string{"folder_id"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			title, _, failures := test.body.check()
			var refused []string
			for _, failure := range failures {
				refused = append(refused, failure.Location[1])
			}
			if strings.Join(refused, ",") != strings.Join(test.bad, ",") {
				t.Fatalf("refused %v, want %v", refused, test.bad)
			}
			if test.name == "a title and a text" && title != "Tides today" {
				t.Errorf("the title was stored as %q", title)
			}
		})
	}
}

func TestAConceptCannotBeBrokenByItsTitle(t *testing.T) {
	changed := time.Date(2026, 11, 12, 9, 30, 0, 0, time.UTC)
	got := concept(DocumentOfOwnerRow{
		Title:        "Tides: a \"study\"\n---\ntype: Other",
		DocumentType: "CRAWLED_URL",
		DocumentMetadata: []byte(`{"url":"https://example.test/tides","description":" Twice a day. ",
			"tags":["sea","",7," moon "],"source":"javascript:alert(1)"}`),
		Body:      "\n# Tides\n\nSpring and neap.\n\n",
		CreatedAt: changed.Add(-time.Hour), UpdatedAt: &changed,
	})
	want := `---
type: "Web Page"
resource: "https://example.test/tides"
title: "Tides: a \"study\"\n---\ntype: Other"
description: "Twice a day."
tags:
- "sea"
- "moon"
timestamp: "2026-11-12T09:30:00Z"
---

# Tides

Spring and neap.
`
	if got != want {
		t.Errorf("the concept is\n%s\nwant\n%s", got, want)
	}

	// No metadata, no title, never changed, a type added later: the block
	// still has its one required key and the time the document was made.
	bare := concept(DocumentOfOwnerRow{DocumentType: "SOMETHING_NEW", Body: "x", CreatedAt: changed})
	if bare != "---\ntype: \"Document\"\ntimestamp: \"2026-11-12T09:30:00Z\"\n---\n\nx\n" {
		t.Errorf("the bare concept is %q", bare)
	}
}

func TestOneOddRowDoesNotFailAPage(t *testing.T) {
	for _, column := range []string{``, `null`, `[1,2]`, `"text"`, `{`} {
		if object, isObject := metadataOf([]byte(column)).(map[string]any); !isObject || len(object) != 0 {
			t.Errorf("metadata %q was answered as %v, want an empty object", column, metadataOf([]byte(column)))
		}
	}
	if read := statusOf([]byte(`{"state":"failed","reason":"No text"}`)); read.State != "failed" || *read.Reason != "No text" {
		t.Errorf("status read as %+v", read)
	}
	if got := firstRunes("añb", 2); got != "añ" {
		t.Errorf("the first two characters are %q", got)
	}
}
