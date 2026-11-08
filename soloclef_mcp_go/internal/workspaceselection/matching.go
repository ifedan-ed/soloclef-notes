// Package workspaceselection resolves a workspace reference and remembers the
// caller's choice.
//
// Every workspace-scoped tool takes a workspace by name or id, or omits it to
// use the active one. That keeps ids out of the conversation: the model or the
// person speaks a name, it is resolved here, and the choice is remembered for
// later calls.
package workspaceselection

import (
	"fmt"
	"strconv"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/toolerror"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/toolschema"
)

// Workspace is a Soloclef Notes workspace, the product's "search space".
type Workspace struct {
	ID          int
	Name        string
	Description string
	IsOwner     bool
	MemberCount int
}

// ReferenceParameterDescription is the one description every workspace-scoped
// tool shows, so the published schemas agree with each other.
const ReferenceParameterDescription = "Workspace name or id, e.g. 'Research' or '3'. " +
	"Omit to use the active workspace (set with soloclef_select_workspace)."

// ReferenceParameterTitle is the field title the published schema carries.
const ReferenceParameterTitle = "Workspace"

// MatchByName matches on name: exact, then without regard to case, then a
// unique substring.
func MatchByName(reference string, workspaces []Workspace) (Workspace, error) {
	needle := strings.TrimSpace(reference)
	for _, workspace := range workspaces {
		if workspace.Name == needle {
			return workspace, nil
		}
	}
	lowered := strings.ToLower(needle)
	for _, workspace := range workspaces {
		if strings.ToLower(workspace.Name) == lowered {
			return workspace, nil
		}
	}
	var partial []Workspace
	for _, workspace := range workspaces {
		if strings.Contains(strings.ToLower(workspace.Name), lowered) {
			partial = append(partial, workspace)
		}
	}
	if len(partial) == 1 {
		return partial[0], nil
	}
	if len(partial) > 1 {
		return Workspace{}, toolerror.New(fmt.Sprintf(
			"'%s' matches several workspaces: %s. Use a more specific name or the id.",
			reference, NameList(partial)))
	}
	return Workspace{}, toolerror.New(fmt.Sprintf(
		"No workspace named '%s'. Available: %s.", reference, NameList(workspaces)))
}

// AsID returns the reference as an id, or false when it is not numeric.
//
// Numeric means every character is a digit, as Python's str.isdigit decides it,
// so a signed or spaced reference is a name rather than an id.
func AsID(reference string) (int, bool) {
	text := strings.TrimSpace(reference)
	if text == "" {
		return 0, false
	}
	for _, char := range text {
		if char < '0' || char > '9' {
			return 0, false
		}
	}
	id, err := strconv.Atoi(text)
	if err != nil {
		return 0, false
	}
	return id, true
}

// NameList renders workspaces as a readable "name (id N)" list.
func NameList(workspaces []Workspace) string {
	parts := make([]string, 0, len(workspaces))
	for _, workspace := range workspaces {
		parts = append(parts, fmt.Sprintf("%s (id %d)", workspace.Name, workspace.ID))
	}
	return strings.Join(parts, ", ")
}

// ReferenceParameter is the workspace parameter every workspace-scoped tool
// carries, declared once so each tool publishes the same name and description.
func ReferenceParameter() toolschema.Parameter {
	return toolschema.Parameter{
		Name:        "workspace",
		Title:       ReferenceParameterTitle,
		Description: ReferenceParameterDescription,
		Kind:        toolschema.Text,
		Nullable:    true,
	}
}
