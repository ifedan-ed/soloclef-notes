package assembly

import (
	"context"
	"fmt"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// MenuTool is one entry of the composer's tool list (GET /agent/tools):
// a tool a chat turn of the account can be offered, which the person may
// switch off for their next messages (Turn.DisabledTools).
type MenuTool struct {
	Name        string
	Description string
	// Group heads the tool's connection in the menu: "Mail", "Google
	// Drive", "Nextcloud", or a connected app's name as the account named
	// it. "" for the chat's own tools.
	Group string
	// Connector is the connection's kind, for its icon: "mail",
	// "google_drive", "nextcloud" or "mcp"; "" with Group.
	Connector string
}

// The connection kinds a menu entry names.
const (
	ConnectorMail      = "mail"
	ConnectorDrive     = "google_drive"
	ConnectorNextcloud = "nextcloud"
	ConnectorApp       = "mcp"
)

// maxMenuDescription bounds one entry's description: the menu shows a
// line, and an app's description is a remote server's text.
const maxMenuDescription = 160

// Menu is the composer's tool list for one account: the chat's own
// switchable tools, then the tools of each thing the account connected and
// switched on, in the order Build offers them. It is what makes the Tools
// menu true to what a turn has: a mailbox, Drive, Nextcloud or an app is
// listed exactly when Build would offer its tools, so a switch in the menu
// always reaches a tool that exists.
//
// It reads what Build reads for those tools (a row per kind, and the
// connected apps' definitions, cached across turns by mcptools), and calls
// no app: an app whose tool list was never fetched is fetched as a turn
// would fetch it.
func (a Assembly) Menu(ctx context.Context, userID string) ([]MenuTool, error) {
	menu := make([]MenuTool, 0, len(switchable))
	seen := map[string]bool{}
	add := func(entry MenuTool) {
		// Build offers one tool per name, the first it meets (an app's tool
		// that takes a name the chat already offers is left out), so the
		// menu lists the same one.
		if seen[entry.Name] {
			return
		}
		seen[entry.Name] = true
		menu = append(menu, entry)
	}
	for _, own := range switchable {
		add(MenuTool{Name: own.Name, Description: own.Description})
	}
	for _, kind := range []struct {
		tools            AccountTools
		group, connector string
	}{
		{a.Mail, "Mail", ConnectorMail},
		{a.Drive, "Google Drive", ConnectorDrive},
		{a.Nextcloud, "Nextcloud", ConnectorNextcloud},
	} {
		if kind.tools == nil {
			continue
		}
		connected, err := kind.tools.Connected(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("assembly: reading the account's %s connection: %w", kind.group, err)
		}
		if !connected {
			continue
		}
		for _, t := range kind.tools.ForTurn(userID) {
			add(MenuTool{Name: t.Name, Description: menuDescription(t), Group: kind.group, Connector: kind.connector})
		}
	}
	apps, err := a.Connected.ForTurn(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("assembly: reading the account's connected apps: %w", err)
	}
	// Nothing is connected to by listing; Close only drops the turn's
	// (empty) set of connections.
	defer apps.Close()
	for _, t := range apps.Tools() {
		app, _ := apps.App(t.Name)
		add(MenuTool{Name: t.Name, Description: menuDescription(t), Group: oneLine(app, 60), Connector: ConnectorApp})
	}
	return menu, nil
}

// menuDescription is a tool's description as one menu line: its first
// sentence, or its first clause when that is all there is room for. The
// model's description goes on to say how to call the tool, which is not
// for the person.
func menuDescription(t tool.Tool) string {
	text := strings.Join(strings.Fields(t.Description), " ")
	for _, end := range []string{". ", ": "} {
		if at := strings.Index(text, end); at > 0 {
			text = text[:at]
		}
	}
	text = strings.TrimSuffix(text, ".")
	if text == "" {
		return t.Name
	}
	return oneLine(text, maxMenuDescription)
}
