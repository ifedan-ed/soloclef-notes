package filekind

import (
	"sort"
	"strings"
)

// MIME is the media type sent with an upload of a file with this extension.
//
// A table here and not the standard library's mime.TypeByExtension, which
// reads the host's /etc/mime.types: the same file would be labelled
// differently on a laptop and in a container.
func MIME(extension string) string {
	if mediaType, known := mediaTypes[extension]; known {
		return mediaType
	}
	// What HTTP prescribes for bytes of unknown type; the hosted parsers
	// then go by the filename.
	return "application/octet-stream"
}

var mediaTypes = map[string]string{
	".pdf":  "application/pdf",
	".doc":  "application/msword",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".ppt":  "application/vnd.ms-powerpoint",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".xls":  "application/vnd.ms-excel",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".odt":  "application/vnd.oasis.opendocument.text",
	".odp":  "application/vnd.oasis.opendocument.presentation",
	".ods":  "application/vnd.oasis.opendocument.spreadsheet",
	".epub": "application/epub+zip",
	".rtf":  "application/rtf",
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".tif":  "image/tiff",
	".tiff": "image/tiff",
	".bmp":  "image/bmp",
}

func sortedKeys(set map[string]Kind) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// ExtensionOf is the extension a file of this media type is given when its
// name has none: an email attachment called "scan" sent as application/pdf
// is routed as "scan.pdf". "" for a type the table does not have. Media
// type parameters ("; charset=…") are ignored and case does not matter.
func ExtensionOf(mediaType string) string {
	if cut := strings.IndexByte(mediaType, ';'); cut >= 0 {
		mediaType = mediaType[:cut]
	}
	return extensionsByType[strings.ToLower(strings.TrimSpace(mediaType))]
}

// extensionsByType is mediaTypes the other way round. Where two extensions
// share a type (.jpg and .jpeg, .tif and .tiff) the shorter is kept, for
// the same answer every run.
var extensionsByType = func() map[string]string {
	byType := map[string]string{}
	for extension, mediaType := range mediaTypes {
		if known, taken := byType[mediaType]; !taken || len(extension) < len(known) || (len(extension) == len(known) && extension < known) {
			byType[mediaType] = extension
		}
	}
	return byType
}()
