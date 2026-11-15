package maildrafts

// activitySource is what the activity log names as the entry's origin.
const activitySource = "mail_send"

// activity is one entry of the owner's activity log.
type activity struct {
	level, status, message string
	metadata               map[string]any
}

// activityOf is the entry a send writes: who it went to, from which
// mailbox, under which subject, and how it ended. Never the body: the log
// says what was done, the Sent folder holds what was said.
func activityOf(finished Finished) activity {
	message := finished.Draft.Message
	emails := func(addresses []Address) []string {
		out := make([]string, 0, len(addresses))
		for _, address := range addresses {
			out = append(out, address.Email)
		}
		return out
	}
	metadata := map[string]any{
		"draft_id":   finished.Draft.ID,
		"provider":   finished.Draft.Provider,
		"account_id": finished.Draft.AccountID,
		"from":       message.From.Email,
		"to":         emails(message.To),
		"cc":         emails(message.Cc),
		"bcc":        emails(message.Bcc),
		"subject":    message.Subject,
		"confirmed":  true,
	}
	if len(message.Attachments) > 0 {
		files := make([]map[string]any, 0, len(message.Attachments))
		for _, attachment := range message.Attachments {
			files = append(files, map[string]any{"name": attachment.Name, "size": attachment.Size, "sha256": attachment.SHA256})
		}
		metadata["attachments"] = files
	}
	if finished.Failure != "" {
		metadata["failure"] = finished.Failure
		return activity{level: "ERROR", status: "FAILED", message: "Email not sent: " + message.Subject, metadata: metadata}
	}
	if finished.ProviderMessageID != "" {
		metadata["provider_message_id"] = finished.ProviderMessageID
	}
	return activity{level: "INFO", status: "SUCCESS", message: "Email sent: " + message.Subject, metadata: metadata}
}
