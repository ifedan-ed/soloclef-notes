package maildrafts

import (
	"fmt"
	"slices"
	"strings"
)

// The unusual points a send can have, each the word the card tells it by
// and the browser acknowledges it with.
const (
	UnusualManyRecipients = "many_recipients"
	UnusualManySends      = "many_sends"
	UnusualNewRecipients  = "new_recipients"
	// UnusualUncheckedRecipients is "whether these are new could not be
	// checked": the mailbox could not be asked. It is asked about as new
	// recipients are, because not knowing is not the same as knowing they
	// are not new.
	UnusualUncheckedRecipients = "recipients_unchecked"
	UnusualLargeAttachments    = "large_attachments"
)

// Unusual is one thing about a send the person confirms a second time. The
// card words it by Code with the figures given; Message is the server's
// own sentence, for a card that does not know the code.
type Unusual struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Count is the recipients or the sends in the past hour.
	Count int `json:"count,omitempty"`
	// Addresses are the new (or unchecked) recipients, every one of them.
	Addresses []string `json:"addresses,omitempty"`
	// Bytes is the attachments' total size.
	Bytes int64 `json:"bytes,omitempty"`
	// Limit is the threshold the point is past (recipients, sends an
	// hour, bytes), so the card says the server's figure, not its own.
	Limit int64 `json:"limit,omitempty"`
}

// fromContent is what is unusual about the message itself: how many it
// goes to and how much it carries. It depends on nothing but the content,
// which the go-ahead is bound to, so Send can work it out again exactly.
func fromContent(message Message) []Unusual {
	var out []Unusual
	if count := len(message.Recipients()); count > UnusualRecipients {
		out = append(out, Unusual{Code: UnusualManyRecipients, Count: count, Limit: UnusualRecipients,
			Message: fmt.Sprintf("This message goes to %d recipients. More than %d at once is unusual for one person's mail.", count, UnusualRecipients)})
	}
	if total := message.AttachmentBytes(); total > UnusualAttachmentBytes {
		out = append(out, Unusual{Code: UnusualLargeAttachments, Bytes: total, Limit: UnusualAttachmentBytes,
			Message: fmt.Sprintf("Its attachments are %.1f MB together, more than the %d MB past which many servers refuse a message.", float64(total)/(1<<20), UnusualAttachmentBytes>>20)})
	}
	return out
}

// fromPace is the pace point: the sends the owner began in the past hour,
// of which this would be one more. It is worked out again inside Send's
// transaction, where the count is exact.
func fromPace(sentLastHour int) []Unusual {
	if sentLastHour < UnusualSendsPerHour {
		return nil
	}
	return []Unusual{{Code: UnusualManySends, Count: sentLastHour, Limit: UnusualSendsPerHour,
		Message: fmt.Sprintf("You have sent %d messages from here in the past hour. More than %d in an hour is unusual.", sentLastHour, UnusualSendsPerHour)}}
}

// fromHistory is the recipients point: who the mailbox has never written
// to (never), or everyone it could not check (unchecked, when the mailbox
// could not be asked).
func fromHistory(never []string, unchecked []string) []Unusual {
	switch {
	case len(unchecked) > 0:
		return []Unusual{{Code: UnusualUncheckedRecipients, Count: len(unchecked), Addresses: unchecked,
			Message: "The mailbox could not be checked for whether you have written to these recipients before: " + strings.Join(unchecked, ", ")}}
	case len(never) > 0:
		return []Unusual{{Code: UnusualNewRecipients, Count: len(never), Addresses: never,
			Message: "You have never sent mail from this mailbox to: " + strings.Join(never, ", ")}}
	}
	return nil
}

// codes is the points' codes, sorted, as a go-ahead stores them.
func codes(points []Unusual) []string {
	out := make([]string, 0, len(points))
	for _, point := range points {
		out = append(out, point.Code)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// recipientsToCheck is the recipients whose history is worth asking the
// mailbox about: not the sender's own address, and not anyone on the
// message this replies to. Answering somebody who wrote to the person is
// everyday mail, whether or not the person ever wrote to them first; a
// reply that adds someone new is still checked (and is flagged on the card
// as outside the conversation besides).
func recipientsToCheck(message Message) []string {
	known := map[string]bool{strings.ToLower(message.From.Email): true}
	for _, participant := range message.ThreadParticipants {
		known[strings.ToLower(participant)] = true
	}
	out := []string{}
	for _, recipient := range message.Recipients() {
		if email := strings.ToLower(recipient.Email); !known[email] {
			known[email] = true
			out = append(out, recipient.Email)
		}
	}
	return out
}
