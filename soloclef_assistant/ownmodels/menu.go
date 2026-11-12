package ownmodels

// MenuEntry is one entry of a model menu as every menu answers it (the
// chat's, the quick and studio pickers', the picture roles'), so that every
// menu says the same thing about the same model. A proxy model is its name
// as both id and label, and own false. A model of the account's own
// connection is its stored name (Choice), the label the person gave it, and
// the host of the connection it is on.
type MenuEntry struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Own        bool   `json:"own"`
	Connection string `json:"connection,omitempty"`
}

// MenuEntries is a menu's entries: one per name, in order, an own model
// labelled from own.
func MenuEntries(names []string, own []Option) []MenuEntry {
	described := make(map[string]Option, len(own))
	for _, option := range own {
		described[option.ID] = option
	}
	entries := make([]MenuEntry, len(names))
	for i, name := range names {
		if option, isOwn := described[name]; isOwn {
			entries[i] = MenuEntry{ID: name, Label: option.Label, Own: true, Connection: option.Connection}
			continue
		}
		entries[i] = MenuEntry{ID: name, Label: name}
	}
	return entries
}
