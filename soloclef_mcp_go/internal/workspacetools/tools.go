// Package workspacetools is the search-space selector: discover workspaces and
// choose the active one.
//
// A workspace — the product calls it a "search space" — scopes every other
// tool. These two tools let a client list what is available and pick one by
// name, so the rest of the conversation needs no ids.
package workspacetools

import (
	"context"
	"fmt"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/rendering"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/tooldefinition"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/toolschema"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/workspaceselection"
)

// Definitions are the workspace selector tools, in the order they are published.
func Definitions(selection *workspaceselection.Selection) []tooldefinition.Definition {
	return []tooldefinition.Definition{
		{
			Name:        "soloclef_list_workspaces",
			Title:       "List workspaces",
			Description: ListWorkspacesDescription,
			Annotations: tooldefinition.ReadOnlyClosed,
			Signature: toolschema.Signature{
				ArgumentsTitle: "list_workspacesArguments",
				Parameters:     []toolschema.Parameter{rendering.FormatParameter()},
			},
			Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
				return listWorkspaces(ctx, selection, arguments)
			},
		},
		{
			Name:        "soloclef_select_workspace",
			Title:       "Select active workspace",
			Description: SelectWorkspaceDescription,
			Annotations: tooldefinition.ReadOnlyClosed,
			Signature: toolschema.Signature{
				ArgumentsTitle: "select_workspaceArguments",
				Parameters: []toolschema.Parameter{{
					Name:  "workspace",
					Title: "Workspace",
					Description: "Workspace name or numeric id; matching is " +
						"case-insensitive and a unique partial name works. " +
						"Example: 'Research'.",
					Kind:     toolschema.Text,
					Required: true,
				}},
			},
			Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
				return selectWorkspace(ctx, selection, arguments)
			},
		},
	}
}

func listWorkspaces(ctx context.Context, selection *workspaceselection.Selection,
	arguments toolschema.Arguments) (string, error) {
	workspaces, err := selection.FetchAll(ctx)
	if err != nil {
		return "", err
	}
	if rendering.Chosen(arguments) == rendering.JSON {
		rows := pythonjson.Value{Kind: pythonjson.Array}
		for _, workspace := range workspaces {
			rows.Array = append(rows.Array, asJSON(workspace))
		}
		return rendering.ToJSON(rows), nil
	}
	active, hasActive := selection.Active(ctx)
	return renderList(workspaces, active, hasActive), nil
}

func selectWorkspace(ctx context.Context, selection *workspaceselection.Selection,
	arguments toolschema.Arguments) (string, error) {
	selected, err := selection.Resolve(ctx, arguments.Text("workspace"))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"Active workspace is now '%s' (id %d). Other tools will use it unless "+
			"you pass a different 'workspace'.", selected.Name, selected.ID), nil
}

func renderList(workspaces []workspaceselection.Workspace,
	active workspaceselection.Workspace, hasActive bool) string {
	if len(workspaces) == 0 {
		return "No accessible workspaces."
	}
	text := &rendering.Text{}
	text.Raw("# Workspaces")
	text.Raw("")
	for _, workspace := range workspaces {
		marker := ""
		if hasActive && active.ID == workspace.ID {
			marker = " — active"
		}
		role := "member"
		if workspace.IsOwner {
			role = "owner"
		}
		text.Line("- **%s** (id %d)%s", workspace.Name, workspace.ID, marker)
		if workspace.Description != "" {
			text.Line("  - %s", workspace.Description)
		}
		text.Line("  - %s, %d member(s)", role, workspace.MemberCount)
	}
	return text.Joined()
}

func asJSON(workspace workspaceselection.Workspace) pythonjson.Value {
	description := pythonjson.Nothing()
	if workspace.Description != "" {
		description = pythonjson.Str(workspace.Description)
	}
	return pythonjson.NewObject(
		pythonjson.Field("id", pythonjson.Num(workspace.ID)),
		pythonjson.Field("name", pythonjson.Str(workspace.Name)),
		pythonjson.Field("description", description),
		pythonjson.Field("is_owner", pythonjson.Flag(workspace.IsOwner)),
		pythonjson.Field("member_count", pythonjson.Num(workspace.MemberCount)),
	)
}
