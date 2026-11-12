package chat

import (
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/uistream"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/chat/memory"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/deliverables"
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
	if payload, mine := pagereader.Card(result); mine {
		return payload
	}
	if payload, mine := memory.Card(result); mine {
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

var unsavedArguments = []string{memory.ToolName}
