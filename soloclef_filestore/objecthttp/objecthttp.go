// Package objecthttp answers an HTTP request with an object from the file
// store: the whole of it, a byte range of it, or the news that the client
// already has it.
//
// It exists so that every route which streams a stored file -- an artifact's
// file, its download, a video slide's narration, and later a public share
// link's media and a podcast's audio -- speaks HTTP the same way, and so that
// the part a browser's player depends on is written and tested once:
//
//   - Content-Length on every answer, taken from the store, so a download has
//     a progress bar and a player knows the duration it can seek within;
//   - Range and 206 with Content-Range, so seeking a video or opening page
//     400 of a PDF fetches those bytes and not the file;
//   - ETag with If-None-Match (304) and If-Range;
//   - 416 for a range past the end, and only for that.
//
// Nothing is held in memory: an answer is a stream from the store copied to
// the socket, whatever the object's size.
//
// It lives beside the store and not inside a route's package because
// it knows nothing of artifacts, owners or permissions. A route decides
// *whether* a caller may have an object and under which headers; this decides
// only *how* the bytes travel.
package objecthttp

import (
	"context"
	"io"
	"net/http"
	"os"
	"strconv"

	"git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// Request is the part of an HTTP request that decides how an object is sent.
// It is a plain struct rather than an *http.Request so that a handler written
// against any router can fill it in.
type Request struct {
	// Method is GET or HEAD. A HEAD is answered with the headers a GET would
	// carry and no body, and the object is never opened.
	Method      string
	Range       string
	IfNoneMatch string
	IfRange     string
}

// Headers is what the route decides about the answer.
type Headers struct {
	// ContentType is the object's media type, from the row that owns it.
	ContentType string
	// ETag is the object's validator, quoted as it goes on the wire
	// (`"abc"`). Leave it empty and one is made from the object's size and
	// modification time, which is as strong a statement as a store with no
	// checksums can make.
	ETag string
	// CacheControl and Disposition are sent as given; empty sends none.
	CacheControl string
	Disposition  string
}

// Answer is a decided response: a status, its headers, and (for a 200 or a
// 206 to a GET) an open stream. It must be finished with Send, or released
// with Close if the caller decides not to send it.
type Answer struct {
	status  int
	headers http.Header
	body    io.ReadCloser
}

// Prepare decides the answer and opens the stream, without writing anything.
//
// That order is the point. Everything that can fail for a reason the client
// should be told about -- the object is gone, the store cannot be read --
// fails here, while the route can still answer with its own error envelope.
// The Python routes handed a lazy generator to StreamingResponse, so a
// missing file was discovered only after `200 OK` and the headers had gone
// out, and the client received a truncated success.
//
// The errors are the store's: filestore.ErrNotFound and ErrInvalidKey are the
// two a route should answer for; anything else is a fault of the store.
func Prepare(ctx context.Context, store filestore.Store, key string, request Request, headers Headers) (*Answer, error) {
	info, err := store.Stat(ctx, key)
	if err != nil {
		return nil, err
	}

	etag := headers.ETag
	if etag == "" {
		etag = `"` + strconv.FormatInt(info.Size, 16) + "-" +
			strconv.FormatInt(info.Modified.UnixNano(), 16) + `"`
	}
	out := http.Header{}
	out.Set("ETag", etag)
	out.Set("Accept-Ranges", "bytes")
	// A stored file is something a user or the agent wrote. Telling the
	// browser to believe the declared type and never guess one is what stops
	// a file named .txt from being run as a script on this origin.
	out.Set("X-Content-Type-Options", "nosniff")
	if headers.CacheControl != "" {
		out.Set("Cache-Control", headers.CacheControl)
	}

	// The client already holds this version: no body, no Content-Type, and
	// the validator again so its cache entry is refreshed.
	if NoneMatch(request.IfNoneMatch, etag) {
		return &Answer{status: http.StatusNotModified, headers: out}, nil
	}

	wanted := ByteRange{Kind: Whole, Offset: 0, Length: info.Size}
	if request.Range != "" && rangeStillValid(request.IfRange, etag) {
		wanted = ParseRange(request.Range, info.Size)
	}
	if wanted.Kind == Unsatisfiable {
		// The size is the one thing a client needs to ask again correctly.
		// Cache-Control is taken back off: an "immutable for a year" on an
		// error is an error a cache may keep for a year.
		out.Set("Content-Range", "bytes */"+strconv.FormatInt(info.Size, 10))
		out.Del("Cache-Control")
		return &Answer{status: http.StatusRequestedRangeNotSatisfiable, headers: out}, nil
	}

	if headers.ContentType != "" {
		out.Set("Content-Type", headers.ContentType)
	}
	if headers.Disposition != "" {
		out.Set("Content-Disposition", headers.Disposition)
	}
	out.Set("Content-Length", strconv.FormatInt(wanted.Length, 10))
	answer := &Answer{status: http.StatusOK, headers: out}
	if wanted.Kind == Partial {
		answer.status = http.StatusPartialContent
		out.Set("Content-Range", "bytes "+strconv.FormatInt(wanted.Offset, 10)+"-"+
			strconv.FormatInt(wanted.Offset+wanted.Length-1, 10)+"/"+strconv.FormatInt(info.Size, 10))
	}

	// A HEAD wants the headers above and nothing else, so nothing is opened.
	if request.Method == http.MethodHead {
		return answer, nil
	}
	if wanted.Kind == Partial {
		answer.body, err = store.OpenRange(ctx, key, wanted.Offset, wanted.Length)
	} else {
		answer.body, err = store.Open(ctx, key)
	}
	if err != nil {
		return nil, err
	}
	return answer, nil
}

// Status is the status Send will write: 200, 206, 304 or 416.
func (a *Answer) Status() int { return a.status }

// Send writes the status, the headers and the body, and closes the stream.
//
// An error from here is always *after* the status line has gone, so there is
// nothing left to tell the client; the caller's only job is to log it. The
// usual cause is the client itself going away mid-download, which is not a
// fault. The count is the body bytes written.
func (a *Answer) Send(response http.ResponseWriter) (int64, error) {
	defer a.Close()
	destination := response.Header()
	for name, values := range a.headers {
		destination[name] = values
	}
	response.WriteHeader(a.status)
	if a.body == nil {
		return 0, nil
	}
	// A whole file off the local store arrives as a bare *os.File, and is
	// handed straight to the destination's ReadFrom when it has one. That is
	// the only way the file reaches it as a file: io.Copy would ask the
	// source for WriteTo first, *os.File has one, and os then gives the
	// destination a wrapper that hides the file -- so the copy went through
	// a buffer in this process and sendfile never happened. Measured on a
	// 32 MB file over loopback (BenchmarkStreamingAFile, "the whole file off
	// a disk"): about 1250 MB/s copied, about 2000 MB/s handed over.
	//
	// Whether sendfile then happens is still the destination's doing: it has
	// to pass the file down to the connection (internal/rollingdeadline
	// does, a piece at a time under its moving deadline). A destination
	// with no ReadFrom takes the copy below.
	if file, isFile := a.body.(*os.File); isFile {
		if destination, offered := response.(io.ReaderFrom); offered {
			return destination.ReadFrom(file)
		}
	}
	// Everything else -- a byte range, whose reader offers its own WriteTo
	// and reaches the destination's ReadFrom that way, and a stream from an
	// object store -- is left to io.Copy to negotiate. Either way memory
	// use is constant in the object's size.
	return io.Copy(response, a.body)
}

// Close releases the stream of an answer that will not be sent. It is safe
// after Send.
func (a *Answer) Close() error {
	if a.body == nil {
		return nil
	}
	body := a.body
	a.body = nil
	return body.Close()
}
