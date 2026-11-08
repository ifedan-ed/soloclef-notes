package folders

import "net/http"

// StoreDelegate hands every folder **write** back to the Python tier when this
// deployment's git knowledge store is switched on.
//
// It is here because the inventory in custom/docs/go-api-tier.md missed a
// mechanism. Five of these routes call app/knowledge_store/service.py —
// record_created_folder on the create, record_moved_folder on the rename and
// the move, record_removed_folder on the delete — and that is a git repository
// per workspace, with a working copy, a lock and a commit, not a queue and not
// a row. A Go write cannot do it and must not pretend the folder tree and the
// store agree when they would not: a folder renamed here without the git move
// would be rebuilt at its old path.
//
// Each of those verbs self-guards on knowledge_store_enabled_for(workspace_id),
// which is the process-wide KNOWLEDGE_STORE_ENABLED **and** the workspace's own
// `knowledge_store_enabled` column. With the master switch off they are reads
// that return None and write nothing, and these routes are identical here —
// which is this deployment today: the switch is unset (its default is FALSE)
// and no workspace is flipped.
//
// So the condition is the master switch, read once from configuration rather
// than per request from the database. Two reasons it is the switch and not the
// per-workspace flag:
//
//   - a per-workspace decision costs a read on every folder write, on every
//     deployment, to answer a question that is "no" until someone turns the
//     store on at all;
//   - the create's workspace comes from the body, so a per-workspace decision
//     would have to validate the body before deciding, and a body this tier
//     forwards is one the Python tier must validate itself. Deciding before
//     anything is read keeps the forward safe: nothing has been written, so
//     the Python tier answers the whole request from the beginning and the
//     bytes are its own.
//
// The cost is that a deployment with the switch on serves its folder writes
// from the Python tier even for workspaces that are not flipped. That is a
// deployment mid-rollout, and it is the safe direction.
type StoreDelegate struct {
	// Enabled is KNOWLEDGE_STORE_ENABLED. False means this tier serves the
	// writes itself.
	Enabled bool
	// Covers is the five write routes, so the four reads are never forwarded:
	// the store's read side — folder_virtual_path — only reads rows, and this
	// tier's reads are identical whatever the switch says.
	Covers func(*http.Request) bool
	// Elsewhere is the Python tier (internal/pythondelegate).
	Elsewhere http.Handler
}

func (d StoreDelegate) Wrap(next http.Handler) http.Handler {
	if !d.Enabled {
		return next
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if d.Covers(request) {
			d.Elsewhere.ServeHTTP(response, request)
			return
		}
		next.ServeHTTP(response, request)
	})
}
