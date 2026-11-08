package workspacetools

// The published descriptions of this slice's tools, taken from the Python
// server's docstrings unchanged. A client shows them to a model, so a reworded
// one is a different tool surface even where the behaviour is the same.

const ListWorkspacesDescription = `List the Soloclef Notes workspaces (search spaces) the account can access.

        Use this to discover which workspaces exist before selecting one, or
        when the user asks what search spaces they have. Returns each
        workspace's name, id, description, ownership, and member count, and
        marks the currently active one.
        `

const SelectWorkspaceDescription = `Set the active workspace (search space) that later tools default to.

        Use this when the user says which search space to work in ("use my
        Research space"), or after soloclef_list_workspaces when several
        exist. Once set, workspace-scoped tools use it unless given a
        different 'workspace'. Do NOT call it before every tool — once per
        session is enough.
        `
