package agentrules

// Availability is whether this deployment serves these four routes at all.
//
// Two settings, one answer: the routes are refused when the master
// kill-switch is on or the permission flag is off, and the kill-switch
// wins. They are read at start-up, as every other setting this tier reads
// is, so a changed flag takes a restart.
type Availability struct {
	// Disabled is SOLOCLEF_DISABLE_NEW_AGENT_STACK.
	Disabled bool
	// PermissionEnabled is SOLOCLEF_ENABLE_PERMISSION.
	PermissionEnabled bool
}

// Serves reports whether the routes answer at all.
func (a Availability) Serves() bool {
	return !a.Disabled && a.PermissionEnabled
}
