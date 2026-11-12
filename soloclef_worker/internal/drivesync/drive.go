package drivesync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// What Drive can say no with. They are told apart because the answer to
// each is different: connect again, pick again, or wait.
var (
	// errAuth: Drive refused the access token.
	errAuth = errors.New("drivesync: Google Drive refused the access token")
	// errGone: no such file, as far as this grant can see.
	errGone = errors.New("drivesync: the file is not in Google Drive, or is not this app's to read")
	// errRefused: Drive will not hand this over (too large to export, or
	// the owner forbade downloads).
	errRefused = errors.New("drivesync: Google Drive would not hand over the file")
	// errUnavailable: Drive is down, busy or unreachable.
	errUnavailable = errors.New("drivesync: Google Drive could not be reached")
	// errFeedPosition: the change feed no longer knows the page token.
	errFeedPosition = errors.New("drivesync: Google Drive no longer accepts the change feed's page token")
)

// remoteFile is one Drive file as its metadata describes it.
type remoteFile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
	// Modified is Drive's own text for the time (RFC 3339): it is only
	// ever compared for equality with what Drive said last time.
	Modified string `json:"modifiedTime"`
	// MD5 is empty for a Google-native file, which has no bytes.
	MD5     string `json:"md5Checksum"`
	Trashed bool   `json:"trashed"`
}

// change is one entry of the change feed.
type change struct {
	FileID string `json:"fileId"`
	// Removed: the file was deleted, or this app lost its access to it.
	Removed bool `json:"removed"`
	// File is absent when Removed.
	File *remoteFile `json:"file"`
}

// gone reports whether the change says the file is no longer there to read.
func (c change) gone() bool { return c.Removed || c.File == nil || c.File.Trashed }

// fileFields is what is asked about a file: only what is read.
const fileFields = "id,name,mimeType,modifiedTime,md5Checksum,trashed"

// feedPageSize is how many changes one request asks for. A sync that
// follows a quiet day is one page; Drive's own limit is 1000.
const feedPageSize = 200

// drive is Drive's API as one job uses it, with that job's access token.
type drive struct {
	// base is the API's root, https://www.googleapis.com/drive/v3 in a
	// deployment.
	base  string
	token string
	http  *http.Client
}

// get makes one authorised GET and returns the reply when it is a 200. The
// caller closes the body. Any other status is one of the errors above, and
// the reply's body is not put in it: it can name files and accounts.
func (d drive) get(ctx context.Context, path string, query url.Values) (*http.Response, error) {
	address := d.base + path
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, fmt.Errorf("drivesync: the request could not be built: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+d.token)
	response, err := d.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// The transport's error can repeat the address, which names the
		// file; its kind is enough.
		return nil, fmt.Errorf("%w (the request failed)", errUnavailable)
	}
	if response.StatusCode == http.StatusOK {
		return response, nil
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	_ = response.Body.Close()
	switch {
	case response.StatusCode == http.StatusUnauthorized:
		return nil, errAuth
	case response.StatusCode == http.StatusNotFound:
		return nil, errGone
	case response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500:
		return nil, fmt.Errorf("%w (it answered %d)", errUnavailable, response.StatusCode)
	case response.StatusCode == http.StatusForbidden:
		return nil, errRefused
	}
	return nil, unexpectedStatus(response.StatusCode)
}

// unexpectedStatus is a reply this package has no better name for.
type unexpectedStatus int

func (s unexpectedStatus) Error() string {
	return fmt.Sprintf("drivesync: Google Drive answered %d", int(s))
}

// read makes a GET whose reply is a small JSON object.
func (d drive) read(ctx context.Context, path string, query url.Values, into any) error {
	response, err := d.get(ctx, path, query)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(into); err != nil {
		return fmt.Errorf("%w (its reply could not be read)", errUnavailable)
	}
	return nil
}

// file is one file's metadata.
func (d drive) file(ctx context.Context, id string) (remoteFile, error) {
	var found remoteFile
	err := d.read(ctx, "/files/"+url.PathEscape(id), url.Values{"fields": {fileFields}, "supportsAllDrives": {"true"}}, &found)
	return found, err
}

// open is the file's bytes: its own, or its export as exportAs when that
// is not "". The caller closes the body.
func (d drive) open(ctx context.Context, id, exportAs string) (io.ReadCloser, error) {
	path, query := "/files/"+url.PathEscape(id), url.Values{"alt": {"media"}, "supportsAllDrives": {"true"}}
	if exportAs != "" {
		path, query = path+"/export", url.Values{"mimeType": {exportAs}}
	}
	response, err := d.get(ctx, path, query)
	if err != nil {
		return nil, err
	}
	return response.Body, nil
}

// startPageToken is the feed position that stands for "now".
func (d drive) startPageToken(ctx context.Context) (string, error) {
	var reply struct {
		Token string `json:"startPageToken"`
	}
	if err := d.read(ctx, "/changes/startPageToken", url.Values{"supportsAllDrives": {"true"}}, &reply); err != nil {
		return "", err
	}
	if reply.Token == "" {
		return "", fmt.Errorf("%w (it gave no page token)", errUnavailable)
	}
	return reply.Token, nil
}

// changes is one page of the feed from a token: the changes, and the token
// to ask with next. last is true when that token is the position to keep
// for next time, and false when there is another page to read now.
func (d drive) changes(ctx context.Context, token string) (page []change, next string, last bool, err error) {
	var reply struct {
		Changes  []change `json:"changes"`
		NextPage string   `json:"nextPageToken"`
		NewStart string   `json:"newStartPageToken"`
	}
	err = d.read(ctx, "/changes", url.Values{
		"pageToken": {token}, "pageSize": {fmt.Sprint(feedPageSize)},
		"fields":            {"nextPageToken,newStartPageToken,changes(fileId,removed,file(" + fileFields + "))"},
		"supportsAllDrives": {"true"}, "includeItemsFromAllDrives": {"true"},
	}, &reply)
	// Drive answers a token it no longer knows with 400 or 410.
	var status unexpectedStatus
	if errors.As(err, &status) && (status == http.StatusBadRequest || status == http.StatusGone) {
		return nil, "", false, errFeedPosition
	}
	if err != nil {
		return nil, "", false, err
	}
	switch {
	case reply.NextPage != "":
		return reply.Changes, reply.NextPage, false, nil
	case reply.NewStart != "":
		return reply.Changes, reply.NewStart, true, nil
	}
	return nil, "", false, fmt.Errorf("%w (a page of changes named no next token)", errUnavailable)
}
