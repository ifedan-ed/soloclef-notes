package restclient_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/restclient"
)

type capture struct {
	method      string
	path        string
	query       string
	accept      string
	clientTag   string
	contentType string
	body        string
}

func recording(t *testing.T, reply string) (*restclient.Client, *capture) {
	t.Helper()
	seen := &capture{}
	backend := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			raw, _ := io.ReadAll(request.Body)
			seen.method = request.Method
			seen.path = request.URL.Path
			seen.query = request.URL.RawQuery
			seen.accept = request.Header.Get("Accept")
			seen.clientTag = request.Header.Get("X-SurfSense-Client")
			seen.contentType = request.Header.Get("Content-Type")
			seen.body = string(raw)
			_, _ = writer.Write([]byte(reply))
		}))
	t.Cleanup(backend.Close)
	return restclient.New(backend.URL+"/api/v1", time.Second, "ss_pat_test"), seen
}

// The header name keeps that exact spelling. A rebrand's find-and-replace once
// turned it into one with a space in it, which is not a legal header name, and
// every request the client made was refused before it left the process.
func TestEveryRequestCarriesTheClientHeader(t *testing.T) {
	client, seen := recording(t, `{}`)
	if _, err := client.Do(context.Background(),
		restclient.Call{Method: "GET", Path: "/workspaces"}); err != nil {
		t.Fatal(err)
	}
	if seen.clientTag != "mcp" {
		t.Errorf("X-SurfSense-Client is %q, want \"mcp\"", seen.clientTag)
	}
	if seen.accept != "application/json" {
		t.Errorf("Accept is %q", seen.accept)
	}
	if seen.path != "/api/v1/workspaces" {
		t.Errorf("path is %q", seen.path)
	}
}

// Written order, not sorted order: httpx sends the parameters as the call wrote
// them, and a request that arrives in another order is another request in a log
// and in a replayed capture.
func TestQueryParametersKeepTheOrderTheCallWroteThem(t *testing.T) {
	client, seen := recording(t, `{}`)
	_, err := client.Do(context.Background(), restclient.Call{
		Method: "GET",
		Path:   "/documents",
		Query: []restclient.QueryParameter{
			restclient.Number("workspace_id", 1),
			restclient.Number("page", 1),
			restclient.Number("page_size", 5),
			restclient.Text("document_types", "FILE"),
			restclient.Number("folder_id", 4),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	const want = "workspace_id=1&page=1&page_size=5&document_types=FILE&folder_id=4"
	if seen.query != want {
		t.Errorf("query is\n %s\nwant\n %s", seen.query, want)
	}
}

// An omitted parameter is left out rather than sent empty, because the API
// parses "" as a value and fails on, for example, int("") for a folder id.
func TestAnOmittedQueryParameterIsNotSent(t *testing.T) {
	client, seen := recording(t, `{}`)
	_, err := client.Do(context.Background(), restclient.Call{
		Method: "GET",
		Path:   "/documents",
		Query: []restclient.QueryParameter{
			restclient.Number("page", 0),
			restclient.Omitted("folder_id"),
			restclient.Optional("document_types", ""),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen.query != "page=0" {
		t.Errorf("query is %q, want \"page=0\"", seen.query)
	}
}

func TestAJSONBodyKeepsItsMemberOrder(t *testing.T) {
	client, seen := recording(t, `{}`)
	_, err := client.Do(context.Background(), restclient.Call{
		Method: "POST",
		Path:   "/documents/search-semantic",
		JSONBody: pythonjson.NewObject(
			pythonjson.Field("workspace_id", pythonjson.Num(1)),
			pythonjson.Field("query", pythonjson.Str("x")),
			pythonjson.Field("top_k", pythonjson.Num(5)),
			pythonjson.Field("document_types", pythonjson.Nothing()),
		),
	})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"workspace_id":1,"query":"x","top_k":5,"document_types":null}`
	if seen.body != want {
		t.Errorf("body is\n %s\nwant\n %s", seen.body, want)
	}
	if seen.contentType != "application/json" {
		t.Errorf("content type is %q", seen.contentType)
	}
}

func TestAnUploadIsSentAsMultipartFormData(t *testing.T) {
	client, seen := recording(t, `{"pending_files": 1}`)
	_, err := client.Do(context.Background(), restclient.Call{
		Method: "POST",
		Path:   "/documents/fileupload",
		FormFields: []restclient.FormField{
			{Name: "workspace_id", Value: "1"},
			{Name: "use_vision_llm", Value: "false"},
			{Name: "processing_mode", Value: "basic"},
		},
		Files: []restclient.Upload{{
			FieldName:   "files",
			FileName:    "synthetic.md",
			ContentType: "text/markdown",
			Content:     []byte("body"),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(seen.contentType, "multipart/form-data; boundary=") {
		t.Errorf("content type is %q", seen.contentType)
	}
	for _, fragment := range []string{
		`name="workspace_id"`, `name="use_vision_llm"`, `name="processing_mode"`,
		`name="files"; filename="synthetic.md"`, "Content-Type: text/markdown", "body",
	} {
		if !strings.Contains(seen.body, fragment) {
			t.Errorf("the form does not carry %q:\n%s", fragment, seen.body)
		}
	}
}

func TestAnAcceptOverrideReachesTheBackend(t *testing.T) {
	client, seen := recording(t, "---\ntitle: x\n---\n")
	reply, err := client.Do(context.Background(), restclient.Call{
		Method: "GET", Path: "/documents/7", Accept: "text/markdown",
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen.accept != "text/markdown" {
		t.Errorf("Accept is %q", seen.accept)
	}
	// A reply that is not JSON comes back as the text it is, which is how the
	// Python client's body parsing behaves.
	if reply.Kind != pythonjson.String || reply.String != "---\ntitle: x\n---\n" {
		t.Errorf("reply is %+v, want the markdown as text", reply)
	}
}

func TestAnEmptyReplyIsNothing(t *testing.T) {
	client, _ := recording(t, "")
	reply, err := client.Do(context.Background(),
		restclient.Call{Method: "DELETE", Path: "/documents/7"})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Kind != pythonjson.Null {
		t.Errorf("reply is %+v, want nothing", reply)
	}
}
