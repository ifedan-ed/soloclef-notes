package drivefile

import "testing"

const (
	alice = "11111111-1111-4111-8111-111111111111"
	bob   = "22222222-2222-4222-8222-222222222222"
)

func TestOneFileIsOneDocumentPerAccount(t *testing.T) {
	if Identity("file-1", alice) != Identity("file-1", alice) {
		t.Error("the same file gave two identities")
	}
	if Identity("file-1", alice) == Identity("file-1", bob) || Identity("file-1", alice) == Identity("file-2", alice) {
		t.Error("two documents share an identity")
	}
	// Pinned: a changed formula would make every imported file a new
	// document on its next import.
	if got := Identity("file-1", alice); got != "6de99f8e5570056c43e04bf450de43a99ce77f43afb15f6384b3a9b1d706e850" {
		t.Errorf("identity = %q", got)
	}
	if PlaceholderHash(Identity("file-1", alice)) == Identity("file-1", alice) {
		t.Error("the placeholder is the identity itself")
	}
}

func TestHowEachKindOfFileIsStored(t *testing.T) {
	for _, tc := range []struct {
		name, mime string
		want       Stored
		kept       bool
	}{
		{"Report.pdf", "application/pdf", Stored{Name: "Report.pdf", ContentType: "application/pdf"}, true},
		{"notes", "", Stored{Name: "notes"}, true},
		{"Plan", GoogleDoc, Stored{"Plan.docx", exports[GoogleDoc].mime, exports[GoogleDoc].mime}, true},
		{"Plan.DOCX", GoogleDoc, Stored{"Plan.DOCX", exports[GoogleDoc].mime, exports[GoogleDoc].mime}, true},
		{"Budget 2026", GoogleSheet, Stored{"Budget 2026.xlsx", exports[GoogleSheet].mime, exports[GoogleSheet].mime}, true},
		{"Deck", GoogleSlides, Stored{"Deck.pdf", "application/pdf", "application/pdf"}, true},
		{"A folder", GoogleFolder, Stored{}, false},
		{"A link", Shortcut, Stored{}, false},
		{"Survey", googlePrefix + "form", Stored{}, false},
	} {
		got, kept := StoredAs(tc.name, tc.mime)
		if got != tc.want || kept != tc.kept {
			t.Errorf("StoredAs(%q, %q) = %+v, %v", tc.name, tc.mime, got, kept)
		}
	}
}
