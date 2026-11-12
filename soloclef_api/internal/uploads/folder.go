package uploads

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/folders"
)

// FolderPath is the folder upload's route, where the web app's folder
// picker and the desktop app's folder sync already post.
const FolderPath = "/api/v1/documents/folder-upload"

// The folder form's own part names; parser, use_vision_llm and files are
// the upload form's.
const (
	fieldFolderName    = "folder_name"
	fieldRelativePaths = "relative_paths"
	fieldRootFolder    = "root_folder_id"
)

// What the folder route says, beside what the upload route says.
const (
	FolderFieldsFirst = "folder_name and relative_paths must be sent before the files: the upload is read in order, and nothing is stored before it is known where the files go"
	RootFolderAbsent  = "Root folder not found"
	// FolderStarted is the status the web app reads: the files were taken,
	// and indexing them is under way.
	FolderStarted = "processing"
)

// The reasons a file of a folder is left out, for the person reading the
// answer. Neither stops the rest of the folder.
const (
	skippedUnread = "not_read_by_parser"
	skippedEmpty  = "empty"
)

// relativePathsLimit bounds the relative_paths field. The web app sends at
// most ten paths a request and the desktop app the same; four megabytes
// holds a thousand of the longest paths allowed.
const relativePathsLimit = 4 << 20

// pathLimit is the longest relative path accepted, in bytes: what Linux
// allows a whole path to be.
const pathLimit = 4096

// FolderIdentity is what makes two folder uploads the same document: the
// folder's name, the file's path inside it, and the account, as sha256 of
// "LOCAL_FOLDER_FILE:{folder}:{path}:{user_id}". A folder synced again is
// recognised file by file through it, which is why neither the name nor
// the path is tidied before hashing.
func FolderIdentity(folderName, relativePath, userID string) string {
	sum := sha256.Sum256([]byte(folderFileType + ":" + folderName + ":" + relativePath + ":" + userID))
	return hex.EncodeToString(sum[:])
}

// folderFields are the folder form's text fields, as sent.
type folderFields struct {
	folderName string
	// paths is relative_paths, one per file, in the files' order. pathsSent
	// tells an empty list from a field that never arrived.
	paths     []folderPath
	pathsSent bool
	// rootFolderID is the folder the files go under, or nil when the
	// request names none and the folder is found or made by name.
	rootFolderID *int64
	parser       string
	vision       bool
	seen         map[string]bool
}

// folderPath is one file's place in the uploaded folder.
type folderPath struct {
	// relative is the path exactly as sent, which is part of the
	// document's identity.
	relative string
	// folders is the chain of folders between the top folder and the file,
	// and name the file's own name.
	folders []string
	name    string
}

// take reads one text field into the form: each of the five once, in one
// exact spelling, and an unknown one refused by name (fields.take says
// why).
func (f *folderFields) take(part *multipart.Part) *refusal {
	name := part.FormName()
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	if f.seen[name] {
		return refuse(400, "The %s field was sent more than once", name)
	}
	f.seen[name] = true

	limit := int64(fieldLimit)
	if name == fieldRelativePaths {
		limit = relativePathsLimit
	}
	// One byte past the limit, to tell a value of exactly the limit from a
	// longer one without reading whatever was sent.
	raw, err := io.ReadAll(io.LimitReader(part, limit+1))
	if err != nil {
		return refuse(400, Interrupted)
	}
	if int64(len(raw)) > limit {
		return refuse(400, "The %s field is too long", name)
	}

	switch name {
	case fieldFolderName:
		// Not trimmed: the name is part of every file's identity, and the
		// identity must be the one an earlier sync of this folder wrote.
		folder := string(raw)
		if problem := unusableName(folder); problem != "" || strings.TrimSpace(folder) == "" {
			return refuse(400, "folder_name must be a folder's name")
		}
		f.folderName = folder
	case fieldRelativePaths:
		var sent []string
		if err := json.Unmarshal(raw, &sent); err != nil || sent == nil {
			return refuse(400, "relative_paths must be a JSON list of the files' paths inside the folder")
		}
		paths, refused := readFolderPaths(sent)
		if refused != nil {
			return refused
		}
		f.paths, f.pathsSent = paths, true
	case fieldRootFolder:
		id, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
		// Any positive bigint is a number a folder could have; one that
		// names none is the 404 below, not a 400 here.
		if err != nil || id < 1 {
			return refuse(400, "root_folder_id must be a folder's number")
		}
		f.rootFolderID = &id
	case fieldParser:
		f.parser = strings.TrimSpace(string(raw))
	case fieldVision:
		switch strings.TrimSpace(string(raw)) {
		case "true":
			f.vision = true
		case "false":
			f.vision = false
		default:
			return refuse(400, "use_vision_llm must be true or false")
		}
	default:
		return refuse(400, "This folder upload form has no %q field; it takes folder_name, relative_paths, root_folder_id, use_vision_llm, parser and files", name)
	}
	return nil
}

// readFolderPaths checks every path of a request and splits each into its
// folders and its file name.
//
// A path is taken apart on "/" and each part must be a name a folder (or a
// document's title) can carry. Nothing is rewritten: the path is an
// identity, so one that cannot be used as it is refuses the request and
// says which. The depth is checked here too, before any file is read: the
// top folder is one level and each folder in a path one more.
func readFolderPaths(sent []string) ([]folderPath, *refusal) {
	paths := make([]folderPath, len(sent))
	repeated := make(map[string]bool, len(sent))
	deepest := 0
	for index, relative := range sent {
		if relative == "" || len(relative) > pathLimit {
			return nil, refuse(400, "relative_paths[%d] must be a path of at most %d bytes", index, pathLimit)
		}
		if repeated[relative] {
			// Two files at one path are one document, and which of them it
			// should hold is not this route's to guess.
			return nil, refuse(400, "%q is listed twice in relative_paths", relative)
		}
		repeated[relative] = true
		parts := strings.Split(relative, "/")
		for _, part := range parts {
			if problem := unusableName(part); problem != "" {
				return nil, refuse(400, "%q cannot be used as a path inside the folder: a part of it %s", relative, problem)
			}
		}
		paths[index] = folderPath{relative: relative, folders: parts[:len(parts)-1], name: parts[len(parts)-1]}
		deepest = max(deepest, len(parts)-1)
	}
	if levels := 1 + deepest; levels > folders.MaxDepth {
		return nil, refuse(400, "Folder structure too deep: %d levels exceeds the maximum of %d.", levels, folders.MaxDepth)
	}
	return paths, nil
}

// unusableName says why a name cannot be a folder's or a file's, or "" when
// it can. The rules are the ones the columns and the folder tree impose:
// 255 bytes, text, and no part that would mean "here" or "up".
func unusableName(name string) string {
	switch {
	case name == "":
		return "is empty"
	case name == "." || name == "..":
		return "is \".\" or \"..\""
	case len(name) > nameLimit:
		return "is longer than " + strconv.Itoa(nameLimit) + " bytes"
	case !utf8.ValidString(name):
		return "is not valid UTF-8"
	}
	for _, letter := range name {
		if unicode.IsControl(letter) {
			return "contains a control character"
		}
	}
	return ""
}
