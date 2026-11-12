package documents

// The four reads about all of the caller's documents rather than one: the
// status poll, the counts by type, the watched folders and the title search
// (titles_route.go, which names its own path). None takes an owner from the
// request: the owner is the caller.
const (
	StatusPath         = "/api/v1/documents/status"
	TypeCountsPath     = "/api/v1/documents/type-counts"
	WatchedFoldersPath = "/api/v1/documents/watched-folders"
)

// invalidDocumentID is `f"Invalid document id: {value}"`, the status poll's
// 400. The value is the piece of the list as the route held it: stripped,
// and otherwise exactly what the request sent.
const invalidDocumentIDPrefix = "Invalid document id: "
