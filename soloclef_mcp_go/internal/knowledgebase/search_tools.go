package knowledgebase

// Knowledge-base read tools: semantic search, list, and read one document.
//
// Every one of them reads the caller's own library: the API key or sign-in is
// the account, and the account is the whole scope.

import (
	"context"
	"fmt"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/rendering"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/restclient"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/tooldefinition"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/toolschema"
)

func readDefinitions(client *restclient.Client) []tooldefinition.Definition {
	return []tooldefinition.Definition{
		{
			Name:        "soloclef_search_knowledge_base",
			Title:       "Search knowledge base",
			Description: SearchKnowledgeBaseDescription,
			Annotations: tooldefinition.ReadOnlyClosed,
			Signature: toolschema.Signature{
				ArgumentsTitle: "search_knowledge_baseArguments",
				Parameters: []toolschema.Parameter{
					{
						Name:  "query",
						Title: "Query",
						Description: "Natural-language search, e.g. " +
							"'notebooklm user complaints'.",
						Kind:      toolschema.Text,
						Required:  true,
						MinLength: toolschema.Limit(1),
					},
					{
						Name:        "top_k",
						Title:       "Top K",
						Description: "Maximum documents to return.",
						Kind:        toolschema.Number,
						Default:     5,
						Minimum:     toolschema.Limit(1),
						Maximum:     toolschema.Limit(20),
					},
					documentTypesParameter(),
					rendering.FormatParameter(),
				},
			},
			Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
				return searchKnowledgeBase(ctx, client, arguments)
			},
		},
		{
			Name:        "soloclef_list_documents",
			Title:       "List documents",
			Description: ListDocumentsDescription,
			Annotations: tooldefinition.ReadOnlyClosed,
			Signature: toolschema.Signature{
				ArgumentsTitle: "list_documentsArguments",
				Parameters: []toolschema.Parameter{
					documentTypesParameter(),
					{
						Name:        "folder_id",
						Title:       "Folder Id",
						Description: "Only documents in this folder. Omit for all.",
						Kind:        toolschema.Number,
						Nullable:    true,
					},
					{
						Name:        "page",
						Title:       "Page",
						Description: "Zero-based page number.",
						Kind:        toolschema.Number,
						Default:     0,
						Minimum:     toolschema.Limit(0),
					},
					{
						Name:        "page_size",
						Title:       "Page Size",
						Description: "Documents per page.",
						Kind:        toolschema.Number,
						Default:     20,
						Minimum:     toolschema.Limit(1),
					},
					rendering.FormatParameter(),
				},
			},
			Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
				return listDocuments(ctx, client, arguments)
			},
		},
		{
			Name:        "soloclef_get_document",
			Title:       "Read one document",
			Description: GetDocumentDescription,
			Annotations: tooldefinition.ReadOnlyClosed,
			Signature: toolschema.Signature{
				ArgumentsTitle: "get_documentArguments",
				Parameters: []toolschema.Parameter{
					documentIDParameter(),
					rendering.FormatParameter(),
				},
			},
			Handle: func(ctx context.Context, arguments toolschema.Arguments) (string, error) {
				return getDocument(ctx, client, arguments)
			},
		},
	}
}

func searchKnowledgeBase(ctx context.Context, client *restclient.Client, arguments toolschema.Arguments) (string, error) {
	topK := arguments.Number("top_k")
	// Clamped again on the way out, as the Python original clamps it: the
	// schema already bounds it, and this is the value the backend is asked for.
	if topK < 1 {
		topK = 1
	}
	if topK > 20 {
		topK = 20
	}
	reply, err := client.Do(ctx, restclient.Call{
		Method: "POST",
		Path:   "/documents/search-semantic",
		JSONBody: pythonjson.NewObject(
			pythonjson.Field("query", pythonjson.Str(arguments.Text("query"))),
			pythonjson.Field("top_k", pythonjson.Num(topK)),
			pythonjson.Field("document_types", pythonjson.Strings(arguments.TextList("document_types"))),
		),
	})
	if err != nil {
		return "", err
	}
	items := pythonjson.Members(reply, "items")
	if rendering.Chosen(arguments) == rendering.JSON {
		return rendering.ToJSON(pythonjson.Value{Kind: pythonjson.Array, Array: items}), nil
	}
	return renderSearch(arguments.Text("query"), items), nil
}

func listDocuments(ctx context.Context, client *restclient.Client, arguments toolschema.Arguments) (string, error) {
	folder, folderGiven := arguments.OptionalNumber("folder_id")
	folderValue := restclient.Omitted("folder_id")
	if folderGiven {
		folderValue = restclient.Number("folder_id", folder)
	}
	reply, err := client.Do(ctx, restclient.Call{
		Method: "GET",
		Path:   "/documents",
		Query: []restclient.QueryParameter{
			restclient.Number("page", arguments.Number("page")),
			restclient.Number("page_size", arguments.Number("page_size")),
			restclient.Optional("document_types",
				strings.Join(arguments.TextList("document_types"), ",")),
			folderValue,
		},
	})
	if err != nil {
		return "", err
	}
	if rendering.Chosen(arguments) == rendering.JSON {
		return rendering.ToJSON(reply), nil
	}
	return renderDocumentList(reply), nil
}

func getDocument(ctx context.Context, client *restclient.Client,
	arguments toolschema.Arguments) (string, error) {
	path := fmt.Sprintf("/documents/%d", arguments.Number("document_id"))
	if rendering.Chosen(arguments) == rendering.JSON {
		document, err := client.Do(ctx, restclient.Call{Method: "GET", Path: path})
		if err != nil {
			return "", err
		}
		return rendering.Clip(rendering.ToJSON(document), rendering.DefaultClipChars), nil
	}
	// The Markdown form is an Open Knowledge Format concept: YAML frontmatter
	// then the body, which the backend renders when asked for text/markdown.
	concept, err := client.Do(ctx, restclient.Call{
		Method: "GET", Path: path, Accept: "text/markdown",
	})
	if err != nil {
		return "", err
	}
	return rendering.Clip(pythonjson.PythonText(concept), rendering.DefaultClipChars), nil
}

func renderSearch(query string, items []pythonjson.Value) string {
	if len(items) == 0 {
		return fmt.Sprintf("No matches for \"%s\".", query)
	}
	text := &rendering.Text{}
	text.Line("# %d result(s) for \"%s\"", len(items), query)
	text.Raw("")
	for _, hit := range items {
		// The place in the order, 1 the best. The search fuses a ranking by
		// meaning with one by keywords, and has no score that means
		// anything on its own, so none is printed.
		text.Line("## %s (id %s) — rank %s",
			pythonjson.Text(hit, "title", "Untitled"),
			pythonjson.Text(hit, "document_id", "None"),
			pythonjson.Text(hit, "rank", "None"))
		for _, chunk := range pythonjson.Members(hit, "chunks") {
			text.Line("> %s", rendering.Clip(
				strings.TrimSpace(pythonjson.Text(chunk, "content", "")), 500))
		}
		text.Raw("")
	}
	return text.Trimmed()
}

func renderDocumentList(reply pythonjson.Value) string {
	items := pythonjson.Members(reply, "items")
	if len(items) == 0 {
		return "No documents found."
	}
	text := &rendering.Text{}
	text.Raw("# Documents")
	text.Raw("")
	for _, document := range items {
		text.Line("- **%s** (id %s) · %s · updated %s",
			pythonjson.Text(document, "title", "Untitled"),
			pythonjson.Text(document, "id", "None"),
			pythonjson.Text(document, "document_type", "None"),
			pythonjson.Text(document, "updated_at", "None"))
	}
	total := pythonjson.Text(reply, "total", fmt.Sprintf("%d", len(items)))
	page := pythonjson.Text(reply, "page", "0")
	suffix := "_"
	if hasMore, ok := reply.Lookup("has_more"); ok && pythonjson.Truthy(hasMore) {
		suffix = " · more available_"
	}
	text.Raw("")
	text.Line("_Page %s · showing %d of %s%s", page, len(items), total, suffix)
	return text.Joined()
}
