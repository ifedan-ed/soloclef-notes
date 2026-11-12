package studioviews

import (
	"net/http"
	"strings"
)

// The two ways a report is made. A written one has Markdown the reading
// view lays out in sections; an interactive one is a page of its own.
const (
	formatDocument    = "document"
	formatInteractive = "interactive"
)

// report is a finished report output, read once a request.
type report struct {
	row      ReadyOutputRow
	outline  outline
	format   string
	language string
}

// report reads the output as a report, or answers the 404: an output that
// is not a report has no reading view, whoever asks.
func (x *exchange) report() (report, bool) {
	row, ok := x.ready(reportAbsent)
	if !ok {
		return report{}, false
	}
	if row.Kind != "report" {
		x.refuse(http.StatusNotFound, reportAbsent)
		return report{}, false
	}
	format := formatDocument
	// The dialog's choice, or, for a report made before the choice was
	// stored, what its file is.
	if option(row.Options, "format") == formatInteractive || strings.HasPrefix(row.PrimaryType, "text/html") {
		format = formatInteractive
	}
	return report{
		row: row, outline: splitReport(row.Markdown), format: format,
		language: reportLanguage(option(row.Options, "language"), row.Markdown),
	}, true
}

// suggestion is one card as the viewer reads it.
type suggestion struct {
	ID             string `json:"id"`
	SectionIndex   int    `json:"section_index"`
	SectionHeading string `json:"section_heading"`
	Kind           string `json:"kind"`
	Title          string `json:"title"`
	Focus          string `json:"focus"`
	State          string `json:"state"`
	OutputID       *int64 `json:"output_id"`
}

// reportViewBody is the reading view.
type reportViewBody struct {
	OutputID   int64  `json:"output_id"`
	ArtifactID int64  `json:"artifact_id"`
	Generation int64  `json:"generation"`
	Format     string `json:"format"`
	Language   string `json:"language"`
	// "ready" once suggestions were written (possibly none); "unavailable"
	// when they could not be.
	SuggestionsStatus string       `json:"suggestions_status"`
	Suggestions       []suggestion `json:"suggestions"`
	Sources           []source     `json:"sources"`
}

// answer sends the view. A suggestion whose output the person has since
// deleted from the Studio is offered again as open: the card would
// otherwise point at nothing for ever.
func (x *exchange) answer(report report, stored *entry, ready bool) {
	body := reportViewBody{
		OutputID: report.row.ID, ArtifactID: report.row.ArtifactID, Generation: report.row.Generation,
		Format: report.format, Language: report.language,
		SuggestionsStatus: "unavailable", Suggestions: []suggestion{},
	}
	if ready {
		body.SuggestionsStatus = "ready"
	}
	if stored != nil && len(stored.Suggestions) > 0 {
		var linked []int64
		for _, item := range stored.Suggestions {
			if item.OutputID != nil {
				linked = append(linked, *item.OutputID)
			}
		}
		alive := map[int64]bool{}
		if len(linked) > 0 {
			rows, err := New(x.routes.Pool).LinkedOutputs(x.ctx.Context(), LinkedOutputsParams{
				NotebookID: x.notebookID, UserID: x.userID, OutputIds: linked})
			if err != nil {
				x.fail("could not look for the suggestions' outputs", err)
				return
			}
			for _, row := range rows {
				alive[row.ID] = true
			}
		}
		for _, item := range stored.Suggestions {
			shown := suggestion{
				ID: item.ID, SectionIndex: item.Section, SectionHeading: item.Heading,
				Kind: item.Kind, Title: item.Title, Focus: item.Focus, State: item.State,
			}
			switch {
			case item.State != stateAdded:
			case item.OutputID != nil && alive[*item.OutputID]:
				shown.OutputID = item.OutputID
			default:
				shown.State = stateOpen
			}
			body.Suggestions = append(body.Suggestions, shown)
		}
	}
	sources, ok := x.sources(report.row.SourceDocumentIds)
	if !ok {
		return
	}
	body.Sources = sources
	x.send(body)
}
