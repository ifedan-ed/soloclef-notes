// Package toolmanifest is the offline check that every tool registers with a
// usable name, description and schema, and the dump used to compare this
// server's published surface with the Python server's.
//
// It runs without a backend or network: it only assembles the server and
// inspects the manifest a client would see.
package toolmanifest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/mcpserver"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/settings"
)

// Expected is every tool a client has in its configuration. A name that leaves
// this list breaks a setup silently, so the list is written out rather than
// derived from the code it checks.
var Expected = []string{
	// search-space selector
	"soloclef_list_workspaces",
	"soloclef_select_workspace",
	// scrapers (all platforms) and run history
	"soloclef_web_crawl",
	"soloclef_google_search",
	"soloclef_reddit_scrape",
	"soloclef_youtube_scrape",
	"soloclef_youtube_comments",
	"soloclef_tiktok_scrape",
	"soloclef_tiktok_comments",
	"soloclef_tiktok_user_search",
	"soloclef_tiktok_trending",
	"soloclef_google_maps_scrape",
	"soloclef_google_maps_reviews",
	"soloclef_amazon_scrape",
	"soloclef_instagram_scrape",
	"soloclef_instagram_details",
	"soloclef_indeed_scrape",
	"soloclef_walmart_scrape",
	"soloclef_walmart_reviews",
	"soloclef_list_scraper_runs",
	"soloclef_get_scraper_run",
	// knowledge-base management
	"soloclef_search_knowledge_base",
	"soloclef_list_documents",
	"soloclef_get_document",
	"soloclef_add_document",
	"soloclef_upload_file",
	"soloclef_update_document",
	"soloclef_delete_document",
	// academic search
	"soloclef_pubmed_search",
	"soloclef_arxiv_search",
	"soloclef_preprint_search",
	"soloclef_scholar_search",
}

// minDescriptionChars is the length below which a description is too thin for a
// client to route on.
const minDescriptionChars = 40

// Collect assembles the server offline and returns the tools a client would be
// listed, in registration order.
//
// Listed through a real session over an in-memory transport rather than read off
// the server's own table, so the payload is the one a client receives.
func Collect() ([]*mcp.Tool, error) {
	server, _, err := mcpserver.Build(settings.Settings{
		BaseURL:        "http://localhost:8000",
		APIKey:         "ss_pat_manifest",
		APIPrefix:      "/api/v1",
		TimeoutSeconds: 5,
		Host:           "127.0.0.1",
		Port:           8080,
	})
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	clientSide, serverSide := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverSide, nil)
	if err != nil {
		return nil, err
	}
	defer serverSession.Close()
	session, err := mcp.NewClient(
		&mcp.Implementation{Name: "toolmanifest", Version: "0.1.0"}, nil,
	).Connect(ctx, clientSide, nil)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	return listed.Tools, nil
}

// Problems returns what is wrong with the manifest; empty means it is healthy.
func Problems(tools []*mcp.Tool) []string {
	byName := map[string]*mcp.Tool{}
	for _, tool := range tools {
		byName[tool.Name] = tool
	}
	var problems []string
	for _, name := range Expected {
		tool, present := byName[name]
		if !present {
			problems = append(problems, fmt.Sprintf("missing tool: %s", name))
			continue
		}
		if len(tool.Description) < minDescriptionChars {
			problems = append(problems, fmt.Sprintf(
				"%s: description is %d characters, too thin to route on",
				name, len(tool.Description)))
		}
		if tool.Title == "" {
			problems = append(problems, fmt.Sprintf("%s: no title", name))
		}
		if problem := schemaProblem(name, tool.InputSchema); problem != "" {
			problems = append(problems, problem)
		}
	}
	for name := range byName {
		if !expected(name) {
			problems = append(problems, fmt.Sprintf("unexpected tool: %s", name))
		}
	}
	sort.Strings(problems)
	return problems
}

func expected(name string) bool {
	for _, known := range Expected {
		if known == name {
			return true
		}
	}
	return false
}

func schemaProblem(name string, schema any) string {
	raw, err := json.Marshal(schema)
	if err != nil {
		return fmt.Sprintf("%s: input schema will not marshal: %v", name, err)
	}
	var decoded struct {
		Type       string         `json:"type"`
		Title      string         `json:"title"`
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return fmt.Sprintf("%s: input schema is not an object: %v", name, err)
	}
	if decoded.Type != "object" {
		return fmt.Sprintf("%s: input schema type is %q, not object", name, decoded.Type)
	}
	if decoded.Title == "" {
		return fmt.Sprintf("%s: input schema has no title", name)
	}
	return ""
}

// Dump renders the manifest as the comparison against the Python server reads
// it: the tools/list payload, indented, with members sorted.
func Dump(tools []*mcp.Tool) ([]byte, error) {
	var out bytes.Buffer
	out.WriteString("[\n")
	for index, tool := range tools {
		encoded, err := json.MarshalIndent(tool, "  ", "  ")
		if err != nil {
			return nil, err
		}
		out.WriteString("  ")
		out.Write(encoded)
		if index < len(tools)-1 {
			out.WriteString(",")
		}
		out.WriteString("\n")
	}
	out.WriteString("]\n")
	return out.Bytes(), nil
}
