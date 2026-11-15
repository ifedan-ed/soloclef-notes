package automationdrafts

import (
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_automations/definition"
)

// What an automation can do, in the words the model is given, one entry for
// each thing soloclef_automations/definition accepts. The keys are that
// package's own lists (definition.TriggerTypes, EventTypes, Actions,
// RetrievalScopes) and a test holds them equal: a trigger, event, action or
// scope added there and not described here fails the build's tests, and one
// described here that the definition no longer accepts does too. So what the
// chat promises is what saving accepts.
var (
	triggerWords = map[string]string{
		definition.TriggerSchedule: "on a schedule: a five-field cron (minute, hour, day of month, month, day of week; numbers only) in an IANA time zone. It repeats; the most often is once a minute.",
		definition.TriggerEvent:    "when an event happens (the events are listed below).",
	}
	eventWords = map[string]string{
		definition.DocumentEnteredFolder: "a document finishes processing in one of the user's folders (when_document_arrives_in_folder).",
	}
	actionWords = map[string]string{
		definition.AgentTask: "one assistant task: the task text is asked of the assistant at every run, with nobody present to answer questions, and its answer is the run's result.",
	}
	reachWords = map[string]string{
		"documents": "the user's own documents and notes (the default)",
		"web":       "the web only: web search with Exa or Tavily where the user connected one, and PubMed, arXiv, bioRxiv and medRxiv",
		"all":       "both the documents and the web",
	}
)

// cannot is what an automation cannot do, each with what it can do
// instead, so that the model says it plainly and offers the nearest thing.
// The first lines follow from the definition's lists above (only these
// triggers, only this action, cron's minute); the rest are what an
// automation's run is built with (soloclef_worker's automations assembly):
// no page reader, since reading a page asks every time and nobody is there;
// no knowledge-base specialist; no Nextcloud.
var cannot = []string{
	"Start on anything but the triggers above: not when an email arrives, a web page changes, a note is edited, a calendar event comes up or a message arrives elsewhere. Offer a schedule that checks for it instead.",
	"Run more often than once a minute, at a given second, or only once: a schedule repeats. For a single run, the user can save it and press Run now on the Automations page.",
	"Send email to anyone. It can email the user their own result (email_me); an email the assistant drafts during a run waits for the user to press Send.",
	"Post to another service or change anything outside this app, except through the tools of apps the user connected.",
	"Read a web page in full: reading a page asks the user first, and nobody is there to ask. It can search the web.",
	"Change or delete existing notes, documents or folders. It can keep each result as a new note in a notebook (save_results_to_notebook).",
	"Use Nextcloud files.",
	"Chain steps that depend on each other, run a step only under a condition, or do something after a failure: an automation is one task.",
}

// capabilities is the block of the prompt that says what an automation can
// and cannot do. sendsMail is whether this server sends mail at all.
func capabilities(sendsMail bool) string {
	var b strings.Builder
	b.WriteString("An automation runs:\n")
	for _, kind := range definition.TriggerTypes {
		b.WriteString("- " + triggerWords[kind] + "\n")
	}
	b.WriteString("Events an automation can wait for:\n")
	for _, kind := range definition.EventTypes {
		b.WriteString("- " + eventWords[kind] + "\n")
	}
	b.WriteString("What it does:\n")
	for _, action := range definition.Actions {
		b.WriteString("- " + actionWords[action] + "\n")
	}
	b.WriteString("What the task may read (reach):\n")
	for _, scope := range definition.RetrievalScopes {
		b.WriteString("- " + scope + ": " + reachWords[scope] + "\n")
	}
	b.WriteString("It may also be confined to one notebook's sources, keep each result as a note in a notebook, and email its result to the user.\n")
	if !sendsMail {
		b.WriteString("On this server email_me is not available: this server sends no mail. Offer a note instead.\n")
	}
	b.WriteString("An automation cannot:\n")
	for _, line := range cannot {
		b.WriteString("- " + line + "\n")
	}
	return b.String()
}
