// Package drivesyncjob is the message that asks the worker to bring an
// account's Google Drive files up to date: its name and its arguments, as
// the API writes them and the worker reads them.
//
// It is here so that the two cannot disagree. The API publishes the job
// when a person imports picked files or presses "sync now"; the worker
// publishes it for each connection whose schedule is due, and runs it
// (soloclef_worker/internal/drivesync). A key renamed on one side only
// would be a job that names no account, found when a file failed to
// arrive.
package drivesyncjob

import (
	"errors"
	"fmt"
	"math"
	"regexp"
)

// Task is the job's name on the queue. It is the name the Python task had,
// so a log search for it finds both.
const Task = "index_google_drive_files"

// MaxFiles is the most files one job may name: the most one import takes.
const MaxFiles = 50

// The keyword arguments.
const (
	keyConnector = "connector_id"
	keyUser      = "user_id"
	keyFiles     = "file_ids"
)

// Request is what one job is asked to do.
type Request struct {
	ConnectorID int64
	// UserID is the account the connection belongs to. Every statement the
	// job runs names it beside the connection's id, so a message that
	// names another account's connection finds nothing.
	UserID string
	// FileIDs are the Drive files to fetch now: the ones just picked. An
	// empty list asks for everything the account has imported to be
	// brought up to date from Drive's change feed.
	FileIDs []string
}

// Kwargs is the message's keyword arguments for a request.
func (r Request) Kwargs() map[string]any {
	files := make([]any, len(r.FileIDs))
	for index, id := range r.FileIDs {
		files[index] = id
	}
	return map[string]any{keyConnector: r.ConnectorID, keyUser: r.UserID, keyFiles: files}
}

// fileID is what a Drive file id looks like. The id goes into a URL's
// path, so nothing else is let through.
var fileID = regexp.MustCompile(`^[A-Za-z0-9_-]{10,200}$`)

// ValidFileID reports whether text could be a Drive file's id.
func ValidFileID(text string) bool { return fileID.MatchString(text) }

var accountID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Read is the request in a message's keyword arguments, as decoded from
// JSON. Anything missing or of the wrong shape is an error: a job that
// guessed an account would be worse than one that did nothing.
func Read(kwargs map[string]any) (Request, error) {
	number, isNumber := kwargs[keyConnector].(float64)
	if !isNumber || number < 1 || number != math.Trunc(number) || number > math.MaxInt64/2 {
		return Request{}, fmt.Errorf("drivesyncjob: %s is not a connection's id", keyConnector)
	}
	userID, isText := kwargs[keyUser].(string)
	if !isText || !accountID.MatchString(userID) {
		return Request{}, fmt.Errorf("drivesyncjob: %s is not an account's id", keyUser)
	}
	request := Request{ConnectorID: int64(number), UserID: userID}
	listed, isList := kwargs[keyFiles].([]any)
	if !isList {
		return Request{}, fmt.Errorf("drivesyncjob: %s is not a list", keyFiles)
	}
	if len(listed) > MaxFiles {
		return Request{}, fmt.Errorf("drivesyncjob: %d files named; one job takes at most %d", len(listed), MaxFiles)
	}
	for _, item := range listed {
		id, isText := item.(string)
		if !isText || !ValidFileID(id) {
			return Request{}, errors.New("drivesyncjob: a named file is not a Drive file id")
		}
		request.FileIDs = append(request.FileIDs, id)
	}
	return request, nil
}
