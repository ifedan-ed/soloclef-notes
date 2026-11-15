package agentrules

import (
	"git.danvics.com/danvics/soloclef/soloclef_assistant/academic"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/addsources"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/documents"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/drivetools"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/mailtools"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/nextcloudtools"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/pagereader"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/websearch"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/knowledgebase"
)

// entry is one of the chat's own tools and the heading it is listed under.
type entry struct{ name, group string }

// catalogue is every tool of the chat's own that a person can mark, in the
// order the page lists them. Each name is the tool package's own constant,
// so a renamed tool cannot leave a mark behind under its old name, and
// internal/chat's TestEveryOfferedToolIsInTheCatalogue holds the list to the
// tools a fully connected chat is offered.
//
// Tools of services the person has not connected are listed too: a mark can
// be set before the connection, and it applies when the tool appears.
//
// Three kinds of tool are left out on purpose. The memory tool, because
// memory is its own settings page and its behaviour is the owner's to decide
// separately. The artifact maker's own tools (making a deck, a chart, a
// file), which run inside one hand-over to that specialist and only ever
// make the file asked for. And the tools whose card is itself the question
// (create_automation, draft_email): they only prepare something the person
// creates or sends, or discards, on the card, so no mark applies to them
// (soloclef_assistant/assembly, cardIsTheQuestion).
var catalogue = func() []entry {
	// Adding papers and pages to a notebook's sources is listed with the
	// documents it adds to: a person who wants to approve each paper too
	// marks it "ask first" here. A call that names a web page asks every
	// time whatever is marked (addsources, namesAPage), so the mark is
	// about papers: without it they are added without asking, like every
	// tool's calls.
	list := []entry{{documents.ToolName, "Your documents"}, {addsources.ToolName, "Your documents"}}
	for _, name := range knowledgebase.ToolNames() {
		list = append(list, entry{name, "Changing your notes and folders"})
	}
	list = append(list,
		entry{websearch.Exa.ToolName(), "The web"},
		entry{websearch.Tavily.ToolName(), "The web"},
		entry{pagereader.ToolName, "The web"},
		entry{academic.PubMedTool, "Research papers"},
		entry{academic.ArxivTool, "Research papers"},
		entry{academic.PreprintTool, "Research papers"},
		entry{mailtools.ListAccountsTool, "Mail"},
		entry{mailtools.ListFoldersTool, "Mail"},
		entry{mailtools.SearchTool, "Mail"},
		entry{mailtools.ReadTool, "Mail"},
		entry{mailtools.ThreadTool, "Mail"},
		entry{drivetools.SearchTool, "Google Drive"},
		entry{drivetools.ReadTool, "Google Drive"},
		entry{drivetools.CreateTool, "Google Drive"},
		entry{drivetools.TrashTool, "Google Drive"},
		entry{nextcloudtools.SearchTool, "Nextcloud"},
		entry{nextcloudtools.ReadTool, "Nextcloud"},
		entry{nextcloudtools.CreateTool, "Nextcloud"},
		entry{nextcloudtools.TrashTool, "Nextcloud"},
	)
	return list
}()

// inCatalogue reports whether name is one of the chat's own tools a person
// can mark. A loop, not a map: the list is thirty names and is asked once
// per request.
func inCatalogue(name string) bool {
	for _, listed := range catalogue {
		if listed.name == name {
			return true
		}
	}
	return false
}

// CatalogueNames is every name in the catalogue, for the chat's test that
// holds the catalogue to the tools a chat is offered.
func CatalogueNames() []string {
	names := make([]string, len(catalogue))
	for i, listed := range catalogue {
		names[i] = listed.name
	}
	return names
}
