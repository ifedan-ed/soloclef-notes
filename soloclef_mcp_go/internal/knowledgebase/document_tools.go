package knowledgebase

// Knowledge-base write tools: add a note, upload a file, update, and delete.
//
// Add and upload write into the caller's own library; update and delete address
// one of the caller's documents by id.

import (
	"context"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/callercredential"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/restclient"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/tooldefinition"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/toolerror"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/toolschema"
)

func writeDefinitions(client *restclient.Client) []tooldefinition.Definition {
	return []tooldefinition.Definition{
		{
			Name:        "soloclef_add_document",
			Title:       "Add a note",
			Description: AddDocumentDescription,
			Annotations: tooldefinition.WriteClosed,
			Signature: toolschema.Signature{
				ArgumentsTitle: "add_documentArguments",
				Parameters: []toolschema.Parameter{
					{
						Name:        "title",
						Title:       "Title",
						Description: "Short descriptive title for the note.",
						Kind:        toolschema.Text,
						Required:    true,
						MinLength:   toolschema.Limit(1),
					},
					{
						Name:        "content",
						Title:       "Content",
						Description: "The note's body; plain text or markdown.",
						Kind:        toolschema.Text,
						Required:    true,
						MinLength:   toolschema.Limit(1),
					},
					{
						Name:        "source_url",
						Title:       "Source Url",
						Description: "Where the text came from, if anywhere.",
						Kind:        toolschema.Text,
						Nullable:    true,
					},
				},
			},
			Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
				return addDocument(ctx, client, arguments)
			},
		},
		{
			Name:        "soloclef_upload_file",
			Title:       "Upload a file",
			Description: UploadFileDescription,
			Annotations: tooldefinition.WriteClosed,
			Signature: toolschema.Signature{
				ArgumentsTitle: "upload_fileArguments",
				Parameters: []toolschema.Parameter{
					{
						Name:  "file_path",
						Title: "File Path",
						Description: "Path to a local file, e.g. " +
							"'C:/Users/me/report.pdf' or '~/notes/summary.md'.",
						Kind:     toolschema.Text,
						Required: true,
					},
					{
						Name:  "use_vision_llm",
						Title: "Use Vision Llm",
						Description: "True reads scanned or image-heavy files with a " +
							"vision model (slower).",
						Kind:    toolschema.Flag,
						Default: false,
					},
				},
			},
			Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
				return uploadFile(ctx, client, arguments)
			},
		},
		{
			Name:        "soloclef_update_document",
			Title:       "Replace a document's content",
			Description: UpdateDocumentDescription,
			Annotations: tooldefinition.WriteClosed,
			Signature: toolschema.Signature{
				ArgumentsTitle: "update_documentArguments",
				Parameters: []toolschema.Parameter{
					documentIDParameter(),
					{
						Name:        "content",
						Title:       "Content",
						Description: "New full text; replaces the existing content entirely.",
						Kind:        toolschema.Text,
						Required:    true,
						MinLength:   toolschema.Limit(1),
					},
				},
			},
			Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
				return updateDocument(ctx, client, arguments)
			},
		},
		{
			Name:        "soloclef_delete_document",
			Title:       "Delete a document",
			Description: DeleteDocumentDescription,
			Annotations: tooldefinition.DeleteClosed,
			Signature: toolschema.Signature{
				ArgumentsTitle: "delete_documentArguments",
				Parameters:     []toolschema.Parameter{documentIDParameter()},
			},
			Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
				return deleteDocument(ctx, client, arguments)
			},
		},
	}
}

func addDocument(ctx context.Context, client *restclient.Client, arguments toolschema.Arguments) (string, error) {
	sourceURL, given := arguments.OptionalText("source_url")
	_, err := client.Do(ctx, restclient.Call{
		Method:   "POST",
		Path:     "/documents",
		JSONBody: noteBody(arguments.Text("title"), arguments.Text("content"), sourceURL, given),
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"Queued '%s' for indexing. It will be searchable once processing completes.",
		arguments.Text("title")), nil
}

// remoteUploadRefused is what a remote caller is told instead of having a
// path on this server's disk read for them.
const remoteUploadRefused = "soloclef_upload_file reads a file from the computer this server runs " +
	"on, so it works only when the server runs on your own computer (stdio). Over the " +
	"remote server, send the text with soloclef_add_document, or upload the file in the app."

func uploadFile(ctx context.Context, client *restclient.Client, arguments toolschema.Arguments) (string, error) {
	// A credential bound to the request means the caller is remote
	// (streamable HTTP): their file_path names a file on this server, not on
	// their machine. Reading it would let any API key holder copy the
	// server's own files (/proc/self/environ, the image, any mount) into
	// their library, so the path is refused before it is touched. Under
	// stdio nothing is bound, the server is the caller's own process, and
	// the path is theirs to read.
	if callercredential.Credential(ctx) != "" {
		return "", toolerror.New(remoteUploadRefused)
	}
	path := arguments.Text("file_path")
	upload, err := readUpload(path)
	if err != nil {
		return "", err
	}
	reply, err := client.Do(ctx, restclient.Call{
		Method: "POST",
		Path:   "/documents/fileupload",
		FormFields: []restclient.FormField{
			{Name: "use_vision_llm", Value: boolText(arguments.Flag("use_vision_llm"))},
		},
		Files: []restclient.Upload{upload},
	})
	if err != nil {
		return "", err
	}
	pending := pythonjson.Number(reply, "pending_files", 0)
	skipped := pythonjson.Number(reply, "skipped_duplicates", 0)
	note := ""
	if skipped != 0 && pending == 0 {
		note = " (already present, skipped)"
	}
	return fmt.Sprintf(
		"Uploaded '%s'%s. It will be searchable once processing completes.",
		filepath.Base(path), note), nil
}

func updateDocument(ctx context.Context, client *restclient.Client,
	arguments toolschema.Arguments) (string, error) {
	documentID := arguments.Number("document_id")
	// One call. The save keeps the document's title when it is sent none,
	// so the document no longer has to be read first to pin it: a content
	// replace never reheadings a note, and never did.
	_, err := client.Do(ctx, restclient.Call{
		Method: "POST",
		Path:   fmt.Sprintf("/documents/%d/save", documentID),
		JSONBody: pythonjson.NewObject(
			pythonjson.Field("source_markdown", pythonjson.Str(arguments.Text("content"))),
		),
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Updated document %d.", documentID), nil
}

func deleteDocument(ctx context.Context, client *restclient.Client,
	arguments toolschema.Arguments) (string, error) {
	documentID := arguments.Number("document_id")
	if _, err := client.Do(ctx, restclient.Call{
		Method: "DELETE", Path: fmt.Sprintf("/documents/%d", documentID),
	}); err != nil {
		return "", err
	}
	return fmt.Sprintf("Deleted document %d.", documentID), nil
}

// readUpload reads the file and names its type the way Python's mimetypes does:
// the bare type with no charset parameter, and application/octet-stream when the
// extension is unknown.
func readUpload(path string) (restclient.Upload, error) {
	expanded, err := expandHome(path)
	if err != nil {
		return restclient.Upload{}, toolerror.New(fmt.Sprintf("No file at '%s'.", path))
	}
	info, err := os.Stat(expanded)
	if err != nil || !info.Mode().IsRegular() {
		return restclient.Upload{}, toolerror.New(fmt.Sprintf("No file at '%s'.", path))
	}
	content, err := os.ReadFile(expanded)
	if err != nil {
		return restclient.Upload{}, toolerror.New(fmt.Sprintf("No file at '%s'.", path))
	}
	contentType := "application/octet-stream"
	if guessed := mime.TypeByExtension(filepath.Ext(expanded)); guessed != "" {
		contentType = strings.TrimSpace(strings.Split(guessed, ";")[0])
	}
	return restclient.Upload{
		FieldName:   "files",
		FileName:    filepath.Base(expanded),
		ContentType: contentType,
		Content:     content,
	}, nil
}

func expandHome(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/")), nil
	}
	return path, nil
}

// boolText is Python's str(bool).lower(), which is what the form field carries.
func boolText(flag bool) string {
	if flag {
		return "true"
	}
	return "false"
}
