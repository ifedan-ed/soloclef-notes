package mailattachments

import (
	"context"
	"fmt"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapmail"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/maildrafts"
)

// Resolve finds the file a request names among the owner's own and
// describes it as the draft will carry it. mostFile is the mail server's
// limit on one file (0: it states none); a file over it is refused here,
// before any of its bytes are read, in the server's own figures.
func (f *Files) Resolve(ctx context.Context, ownerID string, request Request, mostFile int64) (maildrafts.Attachment, error) {
	named := 0
	for _, set := range []bool{request.DocumentID != 0, request.ArtifactID != 0, strings.TrimSpace(request.Title) != "",
		request.MessageID != "" || request.Filename != ""} {
		if set {
			named++
		}
	}
	if named != 1 {
		return maildrafts.Attachment{}, refused("each attachment names exactly one of document_id, artifact_id, title, or a message's attachment (account_id, message_id and filename)")
	}
	switch {
	case request.DocumentID != 0:
		return f.document(ctx, ownerID, request.DocumentID, mostFile)
	case request.ArtifactID != 0:
		return f.artifact(ctx, ownerID, request.ArtifactID, mostFile)
	case strings.TrimSpace(request.Title) != "":
		return f.titled(ctx, ownerID, strings.TrimSpace(request.Title), mostFile)
	}
	return f.forwarded(ctx, ownerID, request, mostFile)
}

// within refuses a file over the server's limit on one file.
func within(name string, size, mostFile int64) error {
	if mostFile > 0 && size > mostFile {
		return refused(jmapmail.TooLargeToUpload(name, size, mostFile).Message + ". Tell the user.")
	}
	return nil
}

// documentLabel is how the model is shown a document to choose by.
func documentLabel(id int64, title string) string {
	return fmt.Sprintf("document_id=%d %q", id, title)
}
