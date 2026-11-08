// Package restclient is the authenticated transport to a Soloclef Notes
// backend's REST API.
//
// It sends requests to fully-formed paths, returns the parsed reply, and turns
// any transport or HTTP failure into a readable toolerror.Error. No key is
// baked into the shared client: the caller's own is read from the request's
// context on every call.
package restclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/callercredential"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/toolerror"
)

// Client issues authenticated requests against the backend's API base.
type Client struct {
	apiBase string
	// fallbackAPIKey is the environment's key, used under stdio where there is
	// no request header to read one from.
	fallbackAPIKey string
	http           *http.Client
}

// New builds a client for one API base.
func New(apiBase string, timeout time.Duration, fallbackAPIKey string) *Client {
	return &Client{
		apiBase:        apiBase,
		fallbackAPIKey: fallbackAPIKey,
		http:           &http.Client{Timeout: timeout},
	}
}

// Upload is one file sent as multipart form data.
type Upload struct {
	FieldName   string
	FileName    string
	ContentType string
	Content     []byte
}

// Call is one request: a method, a path under the API base, and whichever of
// the bodies that request carries.
type Call struct {
	Method string
	Path   string
	// Query holds the query parameters; a nil value is omitted, because sending
	// one empty makes the API parse "" as a value and fail.
	Query []QueryParameter
	// JSONBody is sent as an application/json body.
	JSONBody any
	// FormFields and Files are sent together as multipart form data.
	FormFields []FormField
	Files      []Upload
	// Accept overrides the Accept header for this call.
	Accept string
}

// QueryParameter is one query parameter, kept as a pair so the order a request
// is written in is the order it is sent in.
type QueryParameter struct {
	Name  string
	Value *string
}

// FormField is one multipart text field.
type FormField struct {
	Name  string
	Value string
}

// Text makes a query parameter with a value.
func Text(name, value string) QueryParameter { return QueryParameter{Name: name, Value: &value} }

// Number makes a query parameter from an integer.
func Number(name string, value int) QueryParameter {
	return Text(name, fmt.Sprintf("%d", value))
}

// Omitted makes a query parameter that is left out of the request.
func Omitted(name string) QueryParameter { return QueryParameter{Name: name} }

// Optional makes a query parameter that is left out when the value is empty.
func Optional(name, value string) QueryParameter {
	if value == "" {
		return Omitted(name)
	}
	return Text(name, value)
}

// Do sends a request and returns the parsed reply, or a toolerror.Error.
func (c *Client) Do(ctx context.Context, call Call) (pythonjson.Value, error) {
	credential := callercredential.Credential(ctx)
	if credential == "" {
		credential = c.fallbackAPIKey
	}
	if credential == "" {
		return pythonjson.Value{}, toolerror.New(
			"No Soloclef Notes API key supplied. Send it as an 'Authorization: " +
				"Bearer ss_pat_...' header (remote server), or set the " +
				"SURFSENSE_API_KEY environment variable (stdio).")
	}

	request, err := c.build(ctx, call, credential)
	if err != nil {
		return pythonjson.Value{}, toolerror.New(err.Error())
	}
	response, err := c.http.Do(request)
	if err != nil {
		return pythonjson.Value{}, toolerror.New(fmt.Sprintf(
			"Could not reach Soloclef Notes at %s: %v. Confirm the backend is "+
				"running and SURFSENSE_BASE_URL is correct.", c.apiBase, err))
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return pythonjson.Value{}, toolerror.New(fmt.Sprintf(
			"Could not reach Soloclef Notes at %s: %v. Confirm the backend is "+
				"running and SURFSENSE_BASE_URL is correct.", c.apiBase, err))
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return parseBody(body), nil
	}
	return pythonjson.Value{}, toolerror.New(explainFailure(response.StatusCode, body))
}

func (c *Client) build(ctx context.Context, call Call, credential string) (*http.Request, error) {
	address := c.apiBase + call.Path
	if query := encodeQuery(call.Query); query != "" {
		address += "?" + query
	}

	var body io.Reader
	contentType := ""
	switch {
	case len(call.Files) > 0 || len(call.FormFields) > 0:
		encoded, boundary, err := encodeMultipart(call.FormFields, call.Files)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
		contentType = "multipart/form-data; boundary=" + boundary
	case call.JSONBody != nil:
		encoded, err := encodeJSON(call.JSONBody)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
		contentType = "application/json"
	}

	request, err := http.NewRequestWithContext(ctx, call.Method, address, body)
	if err != nil {
		return nil, err
	}
	accept := "application/json"
	if call.Accept != "" {
		accept = call.Accept
	}
	request.Header.Set("Accept", accept)
	// X-SurfSense-Client lets the backend tell API-key traffic that came
	// through this server from a raw script, so "documents added via MCP" and
	// "searches via MCP" stay queryable. The header name is server-to-server
	// and must keep that exact spelling, which no rebrand may touch: a space is
	// not a legal header-name character.
	request.Header.Set("X-SurfSense-Client", "mcp")
	request.Header.Set("Authorization", "Bearer "+credential)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	return request, nil
}

// encodeQuery writes the parameters in the order the call declared them, which
// is the order httpx sends them in. url.Values.Encode would sort them, and a
// request that reaches the backend in a different order is a different request
// in a log, a cache key and a replayed capture.
func encodeQuery(parameters []QueryParameter) string {
	var pairs []string
	for _, parameter := range parameters {
		if parameter.Value == nil {
			continue
		}
		pairs = append(pairs,
			url.QueryEscape(parameter.Name)+"="+url.QueryEscape(*parameter.Value))
	}
	return strings.Join(pairs, "&")
}

func encodeMultipart(fields []FormField, files []Upload) ([]byte, string, error) {
	var out bytes.Buffer
	writer := multipart.NewWriter(&out)
	for _, field := range fields {
		if err := writer.WriteField(field.Name, field.Value); err != nil {
			return nil, "", err
		}
	}
	for _, file := range files {
		headers := textproto.MIMEHeader{}
		headers.Set("Content-Disposition", fmt.Sprintf(
			`form-data; name=%q; filename=%q`, file.FieldName, file.FileName))
		headers.Set("Content-Type", file.ContentType)
		part, err := writer.CreatePart(headers)
		if err != nil {
			return nil, "", err
		}
		if _, err := part.Write(file.Content); err != nil {
			return nil, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return out.Bytes(), writer.Boundary(), nil
}

// parseBody mirrors the Python client: an empty body is nothing, a JSON body is
// the parsed value, and anything else is the text as it stands.
func parseBody(body []byte) pythonjson.Value {
	if len(body) == 0 {
		return pythonjson.Value{Kind: pythonjson.Null}
	}
	value, err := pythonjson.DecodeAny(body)
	if err != nil {
		return pythonjson.Value{Kind: pythonjson.String, String: string(body)}
	}
	return value
}

// encodeJSON writes a request body with member order preserved, so a payload
// built as an ordered object reaches the backend in that order.
func encodeJSON(payload any) ([]byte, error) {
	value, err := pythonjson.FromGo(payload)
	if err != nil {
		return nil, err
	}
	return []byte(pythonjson.Compact(value)), nil
}
