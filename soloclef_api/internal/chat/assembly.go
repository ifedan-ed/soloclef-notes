package chat

import (
	"git.danvics.com/danvics/soloclef/soloclef_assistant/assembly"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/chat/threads"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/deliverables"
)

// The assembly of a turn's agent lives in soloclef_assistant/assembly, a
// module of its own, because the worker builds the same agent for an
// automation's run and `internal` cannot cross a module. These names are
// that package's, kept here so the routes and their tests read as they did:
// each is the same type, not a copy.
type (
	Turn           = assembly.Turn
	Built          = assembly.Built
	Assembler      = assembly.Assembler
	Assembly       = assembly.Assembly
	Limits         = assembly.Limits
	PromptRows     = assembly.PromptRows
	Connections    = assembly.Connections
	ConnectedTools = assembly.ConnectedTools
	Remembering    = assembly.Remembering
	TurnContext    = assembly.TurnContext
	Notebook       = assembly.Notebook
	Activity       = assembly.Activity
	Models         = assembly.Models
	Catalogue      = assembly.Catalogue
	Chosen         = assembly.Chosen
	ModelLister    = assembly.ModelLister
)

// pickerTools is the list GET /agent/tools answers: the assembly's
// switchable tools, every one on until the person switches it off.
func pickerTools() []threads.Tool {
	listed := assembly.SwitchableTools()
	tools := make([]threads.Tool, 0, len(listed))
	for _, t := range listed {
		tools = append(tools, threads.Tool{Name: t.Name, Description: t.Description, EnabledByDefault: true})
	}
	return tools
}

// ArtifactSpecialist is the artifact specialist as the assembly is given
// it: soloclef_assistant/deliverables' own adapter, which the worker hands
// its assembly too.
type ArtifactSpecialist = deliverables.PerTurn
