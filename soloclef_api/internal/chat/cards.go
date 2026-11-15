package chat

import (
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/uistream"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/chat/memory"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/addsources"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/automationdrafts"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/deliverables"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/figuretool"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/mailtools"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/pagereader"
)

// card decides what of a finished tool call reaches the browser.
//
// What a card returns is not only shown. It is saved in the message, synced
// to every open page of the thread, and published if the thread gets a public link.
// So this is an allowlist: a tool's output goes nowhere unless a case here
// names the fields that may. Everything else shows that the call finished
// and how long its result was, and nothing of the result.
func card(result event.Event) any {
	// Each package that makes a tool with a card of its own says which
	// fields that card draws; the result is decoded into those fields and
	// encoded again, so a field a tool might one day add does not leak by
	// default.
	if payload, mine := deliverables.ToolCard(result); mine {
		return payload
	}
	// A figure's card is drawn again from the call's own arguments: its
	// light and dark SVGs, which the model never read.
	if payload, mine := figuretool.Card(result); mine {
		return payload
	}
	if payload, mine := pagereader.Card(result); mine {
		return payload
	}
	if payload, mine := memory.Card(result); mine {
		return payload
	}
	// A mail draft's card carries its id only; the card asks for the draft
	// as its owner when it is drawn (mailtools.DraftCard).
	if payload, mine := mailtools.DraftCard(result); mine {
		return payload
	}
	// An automation draft's card carries its id only, for the same reason:
	// the card reads the draft, and whether it was created, when drawn.
	if payload, mine := automationdrafts.Card(result); mine {
		return payload
	}
	// What add_sources added, by document, so the card opens each source.
	if payload, mine := addsources.Card(result); mine {
		return payload
	}
	return uistream.LengthOnly(result)
}

// approvalCard is how a call waiting for approval is presented: the page's
// generic permission card. A tool with a card of its own would be named
// here by tool.
func approvalCard(event.Event) uistream.ApprovalCard {
	return uistream.ApprovalCard{InterruptType: "permission_ask"}
}

// unsavedArguments are the tools whose arguments are not saved with the
// message. update_memory's one argument is the whole memory document: saved
// on its card, a person's private memory would be in a thread's rows for
// anyone the thread is shared with.
//
// draft_email's arguments are a message's recipients and text: saved on
// its card they would be published with a shared thread, and the card
// shows the stored draft, never the arguments, so nothing needs them.
var unsavedArguments = []string{memory.ToolName, mailtools.DraftTool}
