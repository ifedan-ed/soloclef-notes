package documentfiles

// ViewedType is the media type the web app's viewers open a file of this
// name and stored type as, or "" for one that is shown as its text
// (viewedType). Exported for the public notebook link, which opens a source
// to a visitor exactly as the app opens it to its owner
// (internal/notebooklinks).
func ViewedType(filename, storedType string) string { return viewedType(filename, storedType) }
