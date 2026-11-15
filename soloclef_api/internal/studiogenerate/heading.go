package studiogenerate

import (
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/notebooks"
)

// This file decides an output's own heading: the title printed on the
// poster, the deck's first slide, the report's cover, the video's opening
// slide. It is not the title the output is listed under (outputTitle).
//
// The two differ because the listing title names the kind, so the Studio
// list reads "Infographic: Apollo 11", while the artifact itself is about
// Apollo 11 and must not be headed "Infographic: Apollo 11". The listing
// title came out on the live site as the poster's heading
// ("Infographic: Apollo 11: the first Moon landing") because the specialist
// was handed it as the artifact's title.
//
// Neither the web dialog nor the installed phone builds let the person type
// a title: every one of them sends "<kind label>: <notebook title>", with
// the label in the app's language (six of them on the web) and ": " between.
// The phones cannot be changed, so the heading is not a new field the
// client sends. It is read from what every client already sends, by one
// exact rule rather than by knowing the labels in every language: a title
// that is "<something>: <this notebook's title>" is the dialog's untouched
// default, and its subject is the notebook. Any other title (a report
// suggestion's "Test the oxygen thresholds", or a title some client lets a
// person type) is the subject as given.

// heading is an output's own heading. An empty Text means the subject has
// no name yet (the notebook is still "Untitled notebook"): the maker is told
// to name it from the material, rather than print the placeholder.
type heading struct {
	Text string
}

// separator is what every Studio dialog puts between the kind's label and
// the notebook's title (generate-dialog.tsx and the phone's GenerateSheet;
// the worker's notebook namer retitles outputs by the same separator).
const separator = ": "

// outputHeading is the heading of an output asked for with request in a
// notebook titled notebookTitle.
func outputHeading(request Request, notebookTitle string) heading {
	asked, notebook := oneLine(request.Title), oneLine(notebookTitle)
	if asked != "" && !isDefaultListing(asked, notebook) {
		return heading{Text: asked}
	}
	if notebook == "" || notebooks.IsDefaultTitle(notebook) {
		return heading{}
	}
	return heading{Text: notebook}
}

// isDefaultListing reports whether a dialog's title is its untouched
// default. The exact rule, with N the notebook's title and both titles on
// one line: the title is L + ": " + P, where L (the kind's label) is not
// blank, and either
//
//   - P is N; or
//   - the title was cut: L + ": " + N is longer than the bound
//     (maxTitleChars), the title is exactly that full default cut
//     (cutTitle), and so P is the part of N the cut kept.
//
// The second case is a notebook titled past about 185 characters, whose
// default title the server cuts (title_bound.go); its heading is still the
// whole notebook title. Comparing with the cut of the full default, rather
// than asking only whether N starts with P, keeps a short title that merely
// begins like the notebook ("Quiz: Ti" in "Tides") from passing as the
// default.
//
// The notebook's title may itself hold the separator ("Apollo 11: the first
// Moon landing"), so L is tried at every separator in the title, never at
// the first alone.
func isDefaultListing(title, notebookTitle string) bool {
	if notebookTitle == "" {
		return false
	}
	for at := strings.Index(title, separator); at >= 0; {
		label := title[:at]
		if strings.TrimSpace(label) != "" {
			full := label + separator + notebookTitle
			if title == full || title == cutTitle(full) {
				return true
			}
		}
		next := strings.Index(title[at+len(separator):], separator)
		if next < 0 {
			break
		}
		at += len(separator) + next
	}
	return false
}

// line is the heading as the specialist's task states it.
func (h heading) line() string {
	if h.Text == "" {
		return "Title: none is chosen yet. Give it a short title that names its subject, taken from the source material."
	}
	return `Title: "` + h.Text + `"`
}
