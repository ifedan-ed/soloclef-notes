package artifacts

// The names a download is saved under, for the other packages that send an
// artifact's file: the public notebook link sends the same file to a
// visitor and must name it the same way (internal/notebooklinks).

// DownloadName is an artifact's title made safe as a filename, with the
// stored file's extension (downloadName).
func DownloadName(title, storedFilename string) string { return downloadName(title, storedFilename) }

// MarkdownName is the name of the download for an artifact with no file:
// its document, as Markdown (markdownName).
func MarkdownName(title string) string { return markdownName(title) }
