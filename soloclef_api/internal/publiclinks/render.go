package publiclinks

import (
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// Build renders one link with the field names the sharing panel parses.
//
// The two datetimes are RFC 3339 in UTC with a "Z", which is what the panel
// has always been sent.
//
// `created_by_user_id` is the owner, and so always the caller. It is kept
// because the panel offers the revoke button for a link whose maker is the
// viewer; `created_by_name` is gone, because the only name it could now hold
// is the reader's own.
func Build(link Link, frontendURL string, now time.Time) *pythonjson.Object {
	rendered := pythonjson.NewObject()
	rendered.Set("id", link.ID)
	rendered.Set("share_token", link.ShareToken)
	rendered.Set("public_url", frontendURL+"/public/"+link.ShareToken)
	rendered.Set("created_at", pythonjson.PydanticDateTime(link.CreatedAt))
	if link.ExpiresAt == nil {
		rendered.Set("expires_at", nil)
	} else {
		rendered.Set("expires_at", pythonjson.PydanticDateTime(*link.ExpiresAt))
	}
	rendered.Set("is_expired", Expired(link.ExpiresAt, now))
	if link.NotebookOutputID != nil {
		rendered.Set("kind", "output")
	} else {
		rendered.Set("kind", "chat")
	}
	rendered.Set("message_count", link.Messages)
	rendered.Set("thread_id", orNull(link.ThreadID))
	rendered.Set("thread_title", link.Title())
	rendered.Set("notebook_id", orNull(link.NotebookID))
	rendered.Set("created_by_user_id", link.OwnerID)
	return rendered
}

// BuildList is the list's body: one key holding the links, in the order the
// statement returned them.
func BuildList(links []Link, frontendURL string, now time.Time) *pythonjson.Object {
	items := make([]any, 0, len(links))
	for _, link := range links {
		items = append(items, Build(link, frontendURL, now))
	}
	rendered := pythonjson.NewObject()
	rendered.Set("snapshots", items)
	return rendered
}

// Message is the revoke's whole response body.
func Message() *pythonjson.Object {
	rendered := pythonjson.NewObject()
	rendered.Set("message", revoked)
	return rendered
}

// orNull keeps a nil *int64 from being stored as a non-nil interface, which
// would render as something other than null.
func orNull(number *int64) any {
	if number == nil {
		return nil
	}
	return *number
}
