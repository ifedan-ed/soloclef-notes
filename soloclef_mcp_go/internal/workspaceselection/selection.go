package workspaceselection

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/callercredential"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/restclient"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/toolerror"
)

// maxTrackedIdentities bounds the per-caller selection store, so a flood of
// keys cannot grow memory without limit. An evicted caller re-resolves its
// default workspace on its next call.
const maxTrackedIdentities = 2048

// Selection resolves workspace references and tracks each caller's active
// choice.
//
// The choice lives in this process and nowhere else, which is what the
// published documentation promises: an API key's choice is shared by every
// agent using that key, a sign-in's choice is shared across that account's
// agents, and either lasts until the server restarts.
type Selection struct {
	client             *restclient.Client
	preferredReference string

	// Active selection is per caller: one shared slot would leak one user's
	// choice to every other user of a shared server.
	mu       sync.Mutex
	active   map[string]Workspace
	recency  []string
	capacity int
}

// New builds a selection store over one REST client.
func New(client *restclient.Client, preferredReference string) *Selection {
	return &Selection{
		client:             client,
		preferredReference: preferredReference,
		active:             map[string]Workspace{},
		capacity:           maxTrackedIdentities,
	}
}

// Active is the caller's current choice, if it has made one.
func (s *Selection) Active(ctx context.Context) (Workspace, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	workspace, ok := s.active[callercredential.Identity(ctx)]
	return workspace, ok
}

// Remember makes a workspace the default for the current caller's later calls.
func (s *Selection) Remember(ctx context.Context, workspace Workspace) Workspace {
	identity := callercredential.Identity(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active[identity] = workspace
	s.touch(identity)
	for len(s.recency) > s.capacity {
		evicted := s.recency[0]
		s.recency = s.recency[1:]
		delete(s.active, evicted)
	}
	return workspace
}

func (s *Selection) touch(identity string) {
	for index, seen := range s.recency {
		if seen == identity {
			s.recency = append(s.recency[:index], s.recency[index+1:]...)
			break
		}
	}
	s.recency = append(s.recency, identity)
}

// FetchAll lists every workspace the caller's credential can reach.
func (s *Selection) FetchAll(ctx context.Context) ([]Workspace, error) {
	reply, err := s.client.Do(ctx, restclient.Call{Method: "GET", Path: "/workspaces"})
	if err != nil {
		return nil, err
	}
	workspaces := []Workspace{}
	if reply.Kind != pythonjson.Array {
		return workspaces, nil
	}
	for _, row := range reply.Array {
		workspaces = append(workspaces, toWorkspace(row))
	}
	return workspaces, nil
}

// Resolve turns a name or id — or the caller's active or preferred default —
// into one workspace.
func (s *Selection) Resolve(ctx context.Context, reference string) (Workspace, error) {
	if strings.TrimSpace(reference) == "" {
		return s.resolveDefault(ctx)
	}
	matched, err := s.match(ctx, reference)
	if err != nil {
		return Workspace{}, err
	}
	return s.Remember(ctx, matched), nil
}

func (s *Selection) resolveDefault(ctx context.Context) (Workspace, error) {
	if active, ok := s.Active(ctx); ok {
		return active, nil
	}
	if s.preferredReference != "" {
		matched, err := s.match(ctx, s.preferredReference)
		if err != nil {
			return Workspace{}, err
		}
		return s.Remember(ctx, matched), nil
	}
	only, err := s.onlyWorkspaceOrPrompt(ctx)
	if err != nil {
		return Workspace{}, err
	}
	return s.Remember(ctx, only), nil
}

func (s *Selection) onlyWorkspaceOrPrompt(ctx context.Context) (Workspace, error) {
	workspaces, err := s.FetchAll(ctx)
	if err != nil {
		return Workspace{}, err
	}
	if len(workspaces) == 1 {
		return workspaces[0], nil
	}
	if len(workspaces) == 0 {
		return Workspace{}, toolerror.New(
			"No accessible workspaces. Confirm the token's account has a " +
				"workspace with API access enabled.")
	}
	return Workspace{}, toolerror.New(fmt.Sprintf(
		"No workspace selected. Choose one first with soloclef_select_workspace, "+
			"or pass 'workspace'. Available: %s.", NameList(workspaces)))
}

func (s *Selection) match(ctx context.Context, reference string) (Workspace, error) {
	workspaces, err := s.FetchAll(ctx)
	if err != nil {
		return Workspace{}, err
	}
	if id, numeric := AsID(reference); numeric {
		for _, workspace := range workspaces {
			if workspace.ID == id {
				return workspace, nil
			}
		}
		return Workspace{}, toolerror.New(fmt.Sprintf(
			"No workspace with id %d. Available: %s.", id, NameList(workspaces)))
	}
	return MatchByName(reference, workspaces)
}

func toWorkspace(row pythonjson.Value) Workspace {
	workspace := Workspace{MemberCount: 1}
	if id, ok := row.Lookup("id"); ok && id.Kind == pythonjson.Int {
		workspace.ID = int(id.Int)
	}
	if name, ok := row.Lookup("name"); ok && name.Kind == pythonjson.String {
		workspace.Name = name.String
	}
	if description, ok := row.Lookup("description"); ok && description.Kind == pythonjson.String {
		workspace.Description = description.String
	}
	if owner, ok := row.Lookup("is_owner"); ok && owner.Kind == pythonjson.Bool {
		workspace.IsOwner = owner.Bool
	}
	if count, ok := row.Lookup("member_count"); ok && count.Kind == pythonjson.Int {
		workspace.MemberCount = int(count.Int)
	}
	return workspace
}
