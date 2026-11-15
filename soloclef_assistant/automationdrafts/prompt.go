package automationdrafts

import (
	"strings"
	"time"
)

// description is the tool's description. It carries the two rules the
// model must keep at the moment it decides to call the tool (ask before
// guessing; say what cannot be done), because a model reads a tool's own
// description most closely there. The prompt block (Prompt) carries the
// full list of what an automation can and cannot do.
const description = `Draft an automation: something the assistant does on its own on a schedule, or when a document arrives in a folder. It does NOT create it: it puts the draft on a card in front of the user, who can change any field and alone decides to press Create. Never say an automation was created.
Before calling, ask one short question if the request leaves out or blurs what the automation needs: what it should do; when (which days, what time, which time zone if the device's is not given; or which folder); where the result goes (emailed to the user, kept as a note in which notebook, or only on its page); what it may read (their documents, the web, or one notebook's sources). Never guess a day, a time, a zone, a notebook or a folder. Draft once it is clear.
If part of the request is something an automation cannot do (see <automations>), do not draft that part silently: say plainly what it cannot do and offer the nearest thing it can, then draft only with the user's agreement.
Call it only when the user asks for something to happen by itself, repeatedly or on an event. Do not ask for permission in prose: the card is the confirmation.`

// Prompt is the <automations> block of the system prompt for a turn that is
// offered the tool: what an automation can and cannot do, read from
// soloclef_automations' own lists (capabilities), how to ask, and the
// person's time zone.
func (t *Tools) Prompt(where Here) string {
	var b strings.Builder
	b.WriteString("You can draft automations with " + ToolName + ". The user reads the draft on a card and creates it there.\n")
	b.WriteString(capabilities(t.SendsMail))
	b.WriteString(`Before drafting:
- Ask a short clarifying question when the request leaves out or blurs something the automation needs: when it runs (days and time, or which folder), where the result goes (email, a note in which notebook, or only its page), or what it may read. Ask everything missing in one question. Do not guess, and do not draft until it is clear.
- When part of the request is beyond what an automation can do (the list above), say so plainly in one sentence and offer what is possible instead. Do not drop that part silently, and do not pretend a draft does it.
Writing the task: a complete instruction to the assistant, which runs with nobody to ask. Say what to look at and what to produce. Results reach the user through email_me or save_results_to_notebook, never through the task's words.
`)
	if zone := strings.TrimSpace(where.TimeZone); zone != "" {
		b.WriteString("The user's device is in the time zone " + zone + ". Use it for a schedule when the user names no other, and say so when you tell them the draft is ready.\n")
	} else {
		b.WriteString("The user's time zone is not known: when a schedule names a time and no zone, ask which time zone.\n")
	}
	return b.String()
}

// ValidTimeZone reports whether zone is an IANA time zone this server knows:
// what a chat request's time_zone must be. "Local" is refused: it is this
// server's zone, not the person's.
func ValidTimeZone(zone string) bool {
	if zone == "" || zone == "Local" || strings.TrimSpace(zone) != zone {
		return false
	}
	_, err := time.LoadLocation(zone)
	return err == nil
}
