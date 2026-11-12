package drivetools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
)

// file is one Drive file as its metadata describes it.
type file struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
	// Modified is Drive's own text for the time (RFC 3339).
	Modified string `json:"modifiedTime"`
	// Link is the file's address in Drive, which is what a citation of it
	// opens.
	Link string `json:"webViewLink"`
	// Size is absent for a Google-native file, which has no bytes.
	Size    string `json:"size"`
	Trashed bool   `json:"trashed"`
}

// fileFields is what is asked about a file: only what is read.
const fileFields = "id,name,mimeType,modifiedTime,webViewLink,size,trashed"

// What Drive can say no with, in words for the model.
var (
	errAuth    = errors.New("Google Drive refused the user's connection; they may need to connect Google Drive again in Connectors")
	errGone    = errors.New("that file is not in the user's Google Drive, or is not one this app can reach (only files the user picked for this app, or that it made, are)")
	errRefused = errors.New("Google Drive would not allow this for that file")
)

// drive is Drive's API as one tool call uses it, with that call's token.
type drive struct {
	base, upload string
	token        string
	http         *http.Client
}

// do makes one authorised request and returns the reply when its status
// is 2xx. The caller closes the body. Any other status is one of the
// errors above; the reply's body is not put in one, because it can name
// files and accounts.
func (d drive) do(ctx context.Context, method, address, contentType string, body io.Reader) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		return nil, errUnavailable
	}
	request.Header.Set("Authorization", "Bearer "+d.token)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := d.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// The transport's error can repeat the address, which names the
		// file; its kind is enough.
		return nil, errUnavailable
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return response, nil
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	_ = response.Body.Close()
	switch response.StatusCode {
	case http.StatusUnauthorized:
		return nil, errAuth
	case http.StatusNotFound:
		return nil, errGone
	case http.StatusForbidden:
		return nil, errRefused
	}
	return nil, errUnavailable
}

// read makes a request whose reply is a small JSON object.
func (d drive) read(ctx context.Context, method, address, contentType string, body io.Reader, into any) error {
	response, err := d.do(ctx, method, address, contentType, body)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(into); err != nil {
		return errUnavailable
	}
	return nil
}

// quotedTerm is text as a string in Drive's query language, where a
// quote and a backslash are escaped with a backslash.
func quotedTerm(text string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(text) + "'"
}

// search lists the files this grant reaches whose name or text matches,
// most recently changed first. An empty query lists the most recent.
func (d drive) search(ctx context.Context, query string, limit int) ([]file, error) {
	conditions := "trashed = false and mimeType != 'application/vnd.google-apps.folder'"
	if query != "" {
		term := quotedTerm(query)
		conditions += " and (name contains " + term + " or fullText contains " + term + ")"
	}
	parameters := url.Values{
		"q": {conditions}, "pageSize": {fmt.Sprint(limit)}, "fields": {"files(" + fileFields + ")"},
		"supportsAllDrives": {"true"}, "includeItemsFromAllDrives": {"true"},
	}
	// Drive refuses an order with a full-text query; its own relevance
	// order is used then.
	if query == "" {
		parameters.Set("orderBy", "modifiedTime desc")
	}
	var listed struct {
		Files []file `json:"files"`
	}
	err := d.read(ctx, http.MethodGet, d.base+"/files?"+parameters.Encode(), "", nil, &listed)
	return listed.Files, err
}

// metadata is one file's metadata.
func (d drive) metadata(ctx context.Context, id string) (file, error) {
	var found file
	err := d.read(ctx, http.MethodGet, d.base+"/files/"+url.PathEscape(id)+"?"+url.Values{"fields": {fileFields}, "supportsAllDrives": {"true"}}.Encode(), "", nil, &found)
	return found, err
}

// content is up to most bytes of a file: its own, or its export as
// exportAs when that is not "". cut says the file is longer.
func (d drive) content(ctx context.Context, id, exportAs string, most int64) (text []byte, cut bool, err error) {
	address := d.base + "/files/" + url.PathEscape(id) + "?" + url.Values{"alt": {"media"}, "supportsAllDrives": {"true"}}.Encode()
	if exportAs != "" {
		address = d.base + "/files/" + url.PathEscape(id) + "/export?" + url.Values{"mimeType": {exportAs}}.Encode()
	}
	response, err := d.do(ctx, http.MethodGet, address, "", nil)
	if err != nil {
		return nil, false, err
	}
	defer response.Body.Close()
	held, err := io.ReadAll(io.LimitReader(response.Body, most+1))
	if err != nil {
		return nil, false, errUnavailable
	}
	if int64(len(held)) > most {
		return held[:most], true, nil
	}
	return held, false, nil
}

// create makes a Google-native file from content: Drive converts the
// uploaded bytes (sourceType) into the kind named by targetType.
func (d drive) create(ctx context.Context, name, targetType, sourceType string, content []byte) (file, error) {
	described, err := json.Marshal(map[string]string{"name": name, "mimeType": targetType})
	if err != nil {
		return file{}, errUnavailable
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, part := range []struct {
		kind string
		data []byte
	}{{"application/json; charset=UTF-8", described}, {sourceType, content}} {
		section, err := writer.CreatePart(textproto.MIMEHeader{"Content-Type": {part.kind}})
		if err != nil {
			return file{}, errUnavailable
		}
		_, _ = section.Write(part.data)
	}
	if err := writer.Close(); err != nil {
		return file{}, errUnavailable
	}
	var made file
	address := d.upload + "/files?" + url.Values{"uploadType": {"multipart"}, "fields": {fileFields}, "supportsAllDrives": {"true"}}.Encode()
	err = d.read(ctx, http.MethodPost, address, "multipart/related; boundary="+writer.Boundary(), &body, &made)
	return made, err
}

// trash moves a file to Drive's bin, where its owner can restore it.
func (d drive) trash(ctx context.Context, id string) (file, error) {
	var binned file
	address := d.base + "/files/" + url.PathEscape(id) + "?" + url.Values{"fields": {fileFields}, "supportsAllDrives": {"true"}}.Encode()
	err := d.read(ctx, http.MethodPatch, address, "application/json", strings.NewReader(`{"trashed":true}`), &binned)
	return binned, err
}
