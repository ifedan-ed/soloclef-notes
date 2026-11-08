package markdownmemory

import "git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"

// Build renders MemoryRead (app/services/memory/schemas.py), whose two fields
// are the document and this deployment's limits — the same body on all six
// routes, and the same body for a no-op as for a write, because a request
// carrying one of save_memory's NO_UPDATE sentinels is answered with the
// stored document and a 200.
func Build(memory string) *pythonjson.Object {
	limits := pythonjson.NewObject()
	limits.Set("soft", SoftLimit)
	limits.Set("hard", HardLimit)

	rendered := pythonjson.NewObject()
	rendered.Set("memory_md", memory)
	rendered.Set("limits", limits)
	return rendered
}
