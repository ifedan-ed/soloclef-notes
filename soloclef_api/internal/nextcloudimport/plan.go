package nextcloudimport

import (
	"context"
	"errors"
	"fmt"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/nextcloud"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/pictures"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/selection"
)

// Pick is one thing the person ticked in the picker.
type Pick struct {
	Path string
	// Folder is true for kind "folder"; a folder brings every file under it.
	Folder bool
}

// PlannedFile is one file an import will bring in.
type PlannedFile struct {
	Entry Entry
	// Folders is the chain of folders the document goes in, outer
	// first; empty for a file picked on its own.
	Folders []string
	// PickedFolder is the picked folder the file came from, "" for a file
	// picked on its own. Such a file reaches a notebook through its
	// folder's link, not one of its own.
	PickedFolder string
}

// Plan is what a pick comes to.
type Plan struct {
	Files []PlannedFile
	// Unsupported and TooLarge are the paths left out, and why.
	Unsupported []string
	TooLarge    []string
	// OverLimit: the pick holds more than MaxImportFiles files, or a walk
	// was cut short. Nothing of such a pick is imported.
	OverLimit bool
	// PickedFolders is each picked folder's path and the folder
	// it becomes, in the order picked.
	PickedFolders []PickedFolder
}

// PickedFolder is a picked Nextcloud folder and its name in the folder tree.
type PickedFolder struct {
	Path string
	Name string
}

// TotalBytes is the size of what will be downloaded, as far as the server
// said: a file with no stated size counts for nothing.
func (p Plan) TotalBytes() int64 {
	var total int64
	for _, file := range p.Files {
		if file.Entry.Size != nil {
			total += *file.Entry.Size
		}
	}
	return total
}

// errSelection is a pick that cannot be imported as it stands. Its text is
// for the person.
type errSelection struct{ message string }

func (e *errSelection) Error() string { return e.message }

// planner turns picks into a plan for one parser choice.
type planner struct {
	remote   nextcloud.Remote
	parsers  parserchoice.Registry
	chosen   selection.Selection
	maxBytes int64
	// vision is the import's use_vision_llm: a picture is then read by the
	// vision model, whatever the chosen parser reads.
	vision bool
}

// plan lists Nextcloud and decides, file by file.
//
// A file the chosen parser does not read, or one over the size limit, is
// reported and left out; it does not fail the import and does not count
// toward the cap. Whether a parser reads a file is asked of the same
// registry the indexing worker routes by, so a file this plan accepts is
// one the worker will not refuse for its type.
func (p planner) plan(ctx context.Context, picks []Pick) (Plan, error) {
	var plan Plan
	seen := map[string]bool{}

	consider := func(entry Entry, pickedFolder string) error {
		if seen[entry.Path] {
			// Picked twice: on its own and inside a picked folder, or
			// inside two picked folders one of which holds the other.
			return nil
		}
		seen[entry.Path] = true
		var unread *parserchoice.Refusal
		if !(p.vision && pictures.IsPicture(entry.Name)) {
			var err error
			if unread, err = parserchoice.Reads(p.parsers, p.chosen, entry.Name); err != nil {
				return err
			}
		}
		switch {
		case unread != nil:
			plan.Unsupported = append(plan.Unsupported, entry.Path)
		case entry.Size != nil && *entry.Size > p.maxBytes:
			// A file whose size the server did not state is let through
			// here and measured as it downloads.
			plan.TooLarge = append(plan.TooLarge, entry.Path)
		case len(plan.Files) >= MaxImportFiles:
			plan.OverLimit = true
		default:
			file := PlannedFile{Entry: entry, PickedFolder: pickedFolder}
			if pickedFolder != "" {
				file.Folders = folderChain(pickedFolder, entry.Path)
			}
			plan.Files = append(plan.Files, file)
		}
		return nil
	}

	pickedPaths := map[string]bool{}
	for _, pick := range picks {
		path, err := nextcloud.NormalizePath(pick.Path)
		if err != nil {
			return Plan{}, &errSelection{fmt.Sprintf("%q is not a path inside your Nextcloud files", pick.Path)}
		}
		if pickedPaths[path] {
			continue
		}
		pickedPaths[path] = true

		if !pick.Folder {
			entry, err := p.remote.Stat(ctx, path)
			if err != nil {
				return Plan{}, err
			}
			if entry.IsFolder {
				return Plan{}, &errSelection{fmt.Sprintf("%s is a folder, not a file", entry.Name)}
			}
			if err := consider(entry, ""); err != nil {
				return Plan{}, err
			}
			continue
		}

		if path == nextcloud.Root {
			return Plan{}, &errSelection{"Pick folders or files inside Nextcloud, not the whole drive"}
		}
		parts := nextcloud.PathParts(path)
		plan.PickedFolders = append(plan.PickedFolders, PickedFolder{Path: path, Name: parts[len(parts)-1]})
		// Files the parser does not read do not count toward the cap, so
		// the walk may look ten times further than it; past that it stops
		// and the pick is called too big.
		files, cut, err := p.remote.Walk(ctx, path, MaxImportFiles*10)
		if err != nil {
			return Plan{}, err
		}
		if cut {
			plan.OverLimit = true
		}
		for _, entry := range files {
			if err := consider(entry, path); err != nil {
				return Plan{}, err
			}
		}
	}
	return plan, nil
}

// folderChain is the picked folder's own name, then the sub-folders down to
// the file. Deeper than the folder tree allows, the extra levels fold into the
// deepest folder allowed.
func folderChain(pickedFolder, filePath string) []string {
	above := len(nextcloud.PathParts(nextcloud.ParentOf(pickedFolder)))
	chain := nextcloud.PathParts(nextcloud.ParentOf(filePath))[above:]
	if len(chain) > maxFolderDepth {
		chain = chain[:maxFolderDepth]
	}
	return chain
}

// isSelection reports whether an error is the pick's own fault.
func isSelection(err error) (*errSelection, bool) {
	var selectionError *errSelection
	found := errors.As(err, &selectionError)
	return selectionError, found
}
