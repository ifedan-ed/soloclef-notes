package knowledgebase

// The body the note tool sends to POST /documents.
//
// The API has a first-class note: a title, the text, and where the text came
// from if anywhere. The tool used to wrap a note in the browser extension's
// saved-page envelope, with a visit time and a made-up page address, because
// that was the only door there was; a note then looked like a web page that
// had never existed.

import "git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/pythonjson"

// noteBody is a note as the create route reads it. source_url is sent as
// null when the caller gave none, never as an invented address.
func noteBody(title, content, sourceURL string, sourceGiven bool) pythonjson.Value {
	return pythonjson.NewObject(
		pythonjson.Field("title", pythonjson.Str(title)),
		pythonjson.Field("content", pythonjson.Str(content)),
		pythonjson.Field("source_url", pythonjson.OptionalStr(sourceURL, sourceGiven && sourceURL != "")),
	)
}
