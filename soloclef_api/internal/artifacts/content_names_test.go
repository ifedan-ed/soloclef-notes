package artifacts

import "testing"

func TestTheNameADownloadIsGiven(t *testing.T) {
	for name, test := range map[string]struct {
		title, stored, want string
	}{
		"the title, with the file's extension":           {"Cell biology", "deck.json", "Cell biology.json"},
		"the extension is lower-cased":                   {"Budget", "Q3.XLSX", "Budget.xlsx"},
		"a title that already ends in it":                {"Budget.xlsx", "book.xlsx", "Budget.xlsx"},
		"...in another case":                             {"Budget.XLSX", "book.xlsx", "Budget.xlsx"},
		"a title in another script is kept":              {"细胞生物学", "deck.json", "细胞生物学.json"},
		"an accented title is kept":                      {"Résumé d'été", "notes.pdf", "Résumé d_été.pdf"},
		"separators and wildcards become underscores":    {`a/b\c:d*e?f"g<h>i|j`, "x.pdf", "a_b_c_d_e_f_g_h_i_j.pdf"},
		"dots in a title are not extensions":             {"v1.2 plan", "plan.docx", "v1_2 plan.docx"},
		"a line break cannot start a header":             {"Notes\r\nSet-Cookie: x", "n.pdf", "Notes__Set-Cookie_ x.pdf"},
		"surrounding space goes":                         {"  Plan  ", "p.pdf", "Plan.pdf"},
		"an empty title":                                 {"", "deck.json", "artifact.json"},
		"a title of only punctuation is kept as written": {"???", "deck.json", "___.json"},
		"a title of only spaces":                         {"   ", "deck.json", "artifact.json"},
		"a stored file with no extension":                {"Plan", "README", "Plan"},
		"an extension that is not one":                   {"Plan", "archive.tar gz", "Plan"},
		"an extension too long to be one":                {"Plan", "x.abcdefghijklmnopq", "Plan"},
		"a hidden file's name is not an extension":       {"Plan", ".env", "Plan"},
		"...nor inside a directory":                      {"Plan", "dir/.env", "Plan"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := downloadName(test.title, test.stored); got != test.want {
				t.Errorf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestALongTitleIsCutAtEightyCharactersNotBytes(t *testing.T) {
	title := ""
	for range 100 {
		title += "é" // two bytes each
	}
	got := downloadName(title, "f.pdf")
	if runes := []rune(got); len(runes) != 80+len(".pdf") || string(runes[80:]) != ".pdf" {
		t.Errorf("%d characters: %q", len(runes), got)
	}
	// A cut that lands after a space leaves no trailing space.
	spaced := downloadName("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa b", "f.pdf")
	if spaced != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.pdf" {
		t.Errorf("got %q", spaced)
	}
	if got := markdownName("Field notes: day 1"); got != "Field notes_ day 1.md" {
		t.Errorf("got %q", got)
	}
}

func TestContentDisposition(t *testing.T) {
	for name, test := range map[string]struct {
		filename string
		inline   bool
		want     string
	}{
		"a plain name, to save": {"Plan.pdf", false,
			`attachment; filename="Plan.pdf"; filename*=UTF-8''Plan.pdf`},
		"a plain name, to show": {"Plan.pdf", true,
			`inline; filename="Plan.pdf"; filename*=UTF-8''Plan.pdf`},
		"spaces are encoded in the second form only": {"My plan.pdf", false,
			`attachment; filename="My plan.pdf"; filename*=UTF-8''My%20plan.pdf`},
		"an accent is dropped from the first and encoded in the second": {"Résumé.pdf", false,
			`attachment; filename="Rsum.pdf"; filename*=UTF-8''R%C3%A9sum%C3%A9.pdf`},
		"no ASCII at all still has a name before the dot": {"细胞.json", false,
			`attachment; filename="download.json"; filename*=UTF-8''%E7%BB%86%E8%83%9E.json`},
		"no ASCII and no extension": {"细胞", false,
			`attachment; filename="download"; filename*=UTF-8''%E7%BB%86%E8%83%9E`},
		"quotes and backslashes cannot end the quoted name": {`a"b\c.txt`, false,
			`attachment; filename="abc.txt"; filename*=UTF-8''a%22b%5Cc.txt`},
		"a line break cannot start a header": {"a\r\nX-Evil: 1.txt", false,
			`attachment; filename="aX-Evil: 1.txt"; filename*=UTF-8''a%0D%0AX-Evil%3A%201.txt`},
		"a slash is encoded": {"a/b.txt", false,
			`attachment; filename="a/b.txt"; filename*=UTF-8''a%2Fb.txt`},
		"a percent sign is encoded, so it cannot be read as an escape": {"100%.txt", false,
			`attachment; filename="100%.txt"; filename*=UTF-8''100%25.txt`},
	} {
		t.Run(name, func(t *testing.T) {
			if got := contentDisposition(test.filename, test.inline); got != test.want {
				t.Errorf("got  %s\nwant %s", got, test.want)
			}
		})
	}
}

// Only the two types whose viewers run nothing may be shown in the page.
func TestWhichTypesABrowserMayDisplay(t *testing.T) {
	for mediaType, inline := range map[string]bool{
		"application/pdf":          true,
		"video/mp4":                true,
		"image/svg+xml":            false,
		"image/png":                false,
		"text/html":                false,
		"application/json":         false,
		"application/octet-stream": false,
		"APPLICATION/PDF":          false,
		"":                         false,
	} {
		if inlineTypes[mediaType] != inline {
			t.Errorf("%q: inline is %v", mediaType, inlineTypes[mediaType])
		}
	}
}
