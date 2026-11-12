package documentfiles

import (
	"encoding/json"
	"testing"
)

func TestHowADocumentOpens(t *testing.T) {
	tests := []struct {
		name string
		row  ManifestOfRow
		want string
	}{
		{"a PDF opens as itself, whatever type it was stored under",
			ManifestOfRow{ID: 41, Title: "Tides", DocumentType: "FILE", State: "ready", FileID: 9, OriginalFilename: "Tides.PDF", MimeType: "application/octet-stream", SizeBytes: 120},
			`{"document_id":41,"title":"Tides","document_type":"FILE","status":"ready","presentation":"original","file":{"file_id":9,"filename":"Tides.PDF","mime_type":"application/pdf","size_bytes":120,"content_url":"/api/v1/documents/41/files/9/content"}}`},
		{"a workbook named without its extension is known by its stored type",
			ManifestOfRow{ID: 41, Title: "Budget", DocumentType: "FILE", State: "ready", FileID: 9, OriginalFilename: "budget", MimeType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", SizeBytes: 5},
			`{"document_id":41,"title":"Budget","document_type":"FILE","status":"ready","presentation":"original","file":{"file_id":9,"filename":"budget","mime_type":"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet","size_bytes":5,"content_url":"/api/v1/documents/41/files/9/content"}}`},
		{"a Word file opens as its text, with the original to download",
			ManifestOfRow{ID: 41, Title: "Letter", DocumentType: "FILE", State: "ready", FileID: 9, OriginalFilename: "letter.docx", SizeBytes: 5},
			`{"document_id":41,"title":"Letter","document_type":"FILE","status":"ready","presentation":"text","file":{"file_id":9,"filename":"letter.docx","mime_type":"application/octet-stream","size_bytes":5,"content_url":"/api/v1/documents/41/files/9/content"}}`},
		{"a note has no file and opens as text",
			ManifestOfRow{ID: 42, Title: "Shore notes", DocumentType: "NOTE", State: "ready"},
			`{"document_id":42,"title":"Shore notes","document_type":"NOTE","status":"ready","presentation":"text","file":null}`},
		{"a PDF whose file has not arrived yet is waited for",
			ManifestOfRow{ID: 43, Title: "Atlas.pdf", DocumentType: "FILE", State: "pending"},
			`{"document_id":43,"title":"Atlas.pdf","document_type":"FILE","status":"pending","presentation":"missing_original","file":null}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := json.Marshal(describe(test.row))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != test.want {
				t.Errorf("got  %s\nwant %s", got, test.want)
			}
		})
	}
}
