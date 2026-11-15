// Package docker is the part of Docker's Engine API this service uses, spoken
// directly on the daemon's Unix socket: create, start, list and remove a
// container, check an image, and run a process in a container with its
// standard streams attached (exec.go).
//
// Why not Docker's Go SDK: it is dozens of modules (OpenTelemetry, gRPC,
// containerd types) for these eight calls, each of which is one JSON request.
// The API is versioned in the path, so pinning a version (apiVersion) keeps the
// shapes below stable across daemon upgrades the same way the SDK would.
package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// apiVersion is the Engine API version every request names. 1.44 is the
// oldest a current daemon accepts (Docker 29 dropped older ones) and has
// every field this package sends; naming it means a newer daemon answers in
// the shapes written here instead of its own latest.
const apiVersion = "v1.44"

// Client talks to one Docker daemon. It is safe for concurrent use.
type Client struct {
	socket string
	http   *http.Client
}

// New returns a client of the daemon listening on socket (a Unix socket's
// path, normally /var/run/docker.sock). Nothing is dialled until a call.
func New(socket string) *Client {
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socket)
	}
	return &Client{
		socket: socket,
		http: &http.Client{Transport: &http.Transport{
			DialContext: dial,
			// Commands and file transfers run side by side for many
			// sandboxes; the default of two idle connections per host would
			// redial the socket for most of them.
			MaxIdleConnsPerHost: 32,
			IdleConnTimeout:     90 * time.Second,
		}},
	}
}

// Error is an answer from the daemon that is not a success.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("docker: %d: %s", e.Status, e.Message)
}

// IsNotFound says the daemon has no such container, exec or image.
func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

// IsConflict says the daemon refused because of the object's state: a
// container name already taken, or an exec in a container that is not
// running.
func IsConflict(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusConflict
}

// call sends one request and decodes a 2xx answer into out (when not nil).
// in, when not nil, is sent as JSON.
func (c *Client) call(ctx context.Context, method, path string, query url.Values, in, out any) error {
	var body io.Reader
	if in != nil {
		encoded, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	address := "http://docker/" + apiVersion + path
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		return err
	}
	if in != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("docker: the daemon could not be reached on %s: %w", c.socket, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return readError(response)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return nil
	}
	if err := json.NewDecoder(response.Body).Decode(out); err != nil {
		return fmt.Errorf("docker: %s %s: the answer could not be read: %w", method, path, err)
	}
	return nil
}

// readError turns a failed answer into an *Error. The daemon's body is
// {"message": "..."}; anything else is kept as text.
func readError(response *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
	var decoded struct {
		Message string `json:"message"`
	}
	message := strings.TrimSpace(string(raw))
	if json.Unmarshal(raw, &decoded) == nil && decoded.Message != "" {
		message = decoded.Message
	}
	return &Error{Status: response.StatusCode, Message: message}
}

// Ping checks that the daemon answers.
func (c *Client) Ping(ctx context.Context) error {
	return c.call(ctx, http.MethodGet, "/_ping", nil, nil, nil)
}

// ImageExists checks that the daemon has an image by that reference. The
// service builds nothing and pulls nothing: the stack builds the sandbox
// image, and a missing one is a deployment error to report at start.
func (c *Client) ImageExists(ctx context.Context, reference string) error {
	return c.call(ctx, http.MethodGet, "/images/"+url.PathEscape(reference)+"/json", nil, nil, nil)
}
