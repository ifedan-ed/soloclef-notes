// Package knowledgebase holds the knowledge-base tools: semantic search plus
// the document lifecycle — list, read, add text, upload a file, update, and
// delete — over a workspace's knowledge base.
//
// Read tools are in search_tools.go, mutations in document_tools.go.
package knowledgebase

import "git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/toolschema"

// documentIDParameter addresses a document by an id that is unique across the
// account, so the tools that take it need no workspace.
func documentIDParameter() toolschema.Parameter {
	return toolschema.Parameter{
		Name:  "document_id",
		Title: "Document Id",
		Description: "Document id from soloclef_search_knowledge_base or " +
			"soloclef_list_documents results.",
		Kind:     toolschema.Number,
		Required: true,
	}
}

func documentTypesParameter() toolschema.Parameter {
	return toolschema.Parameter{
		Name:  "document_types",
		Title: "Document Types",
		Description: "Restrict to these document types, e.g. " +
			"['FILE', 'CRAWLED_URL', 'YOUTUBE_VIDEO']. Omit for all types.",
		Kind:     toolschema.TextList,
		Nullable: true,
	}
}
