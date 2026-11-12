package folders

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"testing"
	"time"
)

// world is the rows a route test runs over, with what a test needs beside
// what a route needs: a way to put a document there, and a way to look at
// rows afterwards without asking the routes being tested.
type world interface {
	Rows
	// account makes an account and returns its id.
	account(t *testing.T) string
	// addDocument files a new document of the account's in the folder (nil
	// for none) with the given state, and returns its id.
	addDocument(t *testing.T, userID string, folderID *int64, state string) int64
	// document is where a document is filed and its state; exists is false
	// when no account has it.
	document(t *testing.T, documentID int64) (folderID *int64, state string, exists bool)
	// setMetadata writes a folder's metadata column, as JSON.
	setMetadata(t *testing.T, folderID int64, metadata string)
	// folder reads a folder whoever owns it; nil when nobody has it.
	folderRow(t *testing.T, folderID int64) *Folder
}

// memory is Rows held in maps: every row carries its owner, and every
// method compares it, as every statement of Store does. It enforces the
// same two constraints the table does, so the routes' refusals for them can
// be tested without a database.
type memory struct {
	mu        sync.Mutex
	next      int64
	accounts  int
	folders   map[int64]*memoryFolder
	documents map[int64]*memoryDocument
}

type memoryFolder struct {
	owner string
	Folder
}

type memoryDocument struct {
	owner string
	Document
}

func newMemory() *memory {
	return &memory{next: 100, folders: map[int64]*memoryFolder{}, documents: map[int64]*memoryDocument{}}
}

var _ world = (*memory)(nil)

func (m *memory) account(*testing.T) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.accounts++
	return "00000000-0000-4000-8000-00000000000" + string(rune('0'+m.accounts))
}

func (m *memory) addDocument(_ *testing.T, userID string, folderID *int64, state string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.next++
	status, _ := json.Marshal(map[string]string{"state": state})
	m.documents[m.next] = &memoryDocument{owner: userID, Document: Document{
		ID: m.next, FolderID: folderID, Title: "Document", Status: status, DocumentType: "FILE",
	}}
	return m.next
}

func (m *memory) document(_ *testing.T, documentID int64) (*int64, string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	document, exists := m.documents[documentID]
	if !exists {
		return nil, "", false
	}
	return document.FolderID, documentState(document.Status), true
}

func (m *memory) setMetadata(_ *testing.T, folderID int64, metadata string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.folders[folderID].Metadata = []byte(metadata)
}

func (m *memory) folderRow(_ *testing.T, folderID int64) *Folder {
	m.mu.Lock()
	defer m.mu.Unlock()
	if folder, exists := m.folders[folderID]; exists {
		copied := folder.Folder
		return &copied
	}
	return nil
}

// owned is the folder if it is this account's.
func (m *memory) owned(userID string, folderID int64) *memoryFolder {
	if folder, exists := m.folders[folderID]; exists && folder.owner == userID {
		return folder
	}
	return nil
}

func (m *memory) Folder(_ context.Context, userID string, folderID int64) (*Folder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if folder := m.owned(userID, folderID); folder != nil {
		copied := folder.Folder
		return &copied, nil
	}
	return nil, nil
}

func (m *memory) Folders(_ context.Context, userID string) ([]Folder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tree := []Folder{}
	for _, folder := range m.folders {
		if folder.owner == userID {
			tree = append(tree, folder.Folder)
		}
	}
	sort.Slice(tree, func(a, b int) bool {
		if tree[a].Position != tree[b].Position {
			return tree[a].Position < tree[b].Position
		}
		return tree[a].ID < tree[b].ID
	})
	return tree, nil
}

func (m *memory) Ancestors(_ context.Context, userID string, folderID int64) ([]Breadcrumb, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var chain []Breadcrumb
	for folder := m.owned(userID, folderID); folder != nil; {
		chain = append([]Breadcrumb{{ID: folder.ID, Name: folder.Name}}, chain...)
		if folder.ParentID == nil {
			break
		}
		folder = m.owned(userID, *folder.ParentID)
	}
	return chain, nil
}

func (m *memory) Depth(ctx context.Context, userID string, folderID int64) (int, error) {
	chain, err := m.Ancestors(ctx, userID, folderID)
	return len(chain), err
}

func (m *memory) SubtreeDepth(_ context.Context, userID string, folderID int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.below(userID, folderID), nil
}

func (m *memory) below(userID string, folderID int64) int {
	deepest := 0
	for _, folder := range m.folders {
		if folder.owner == userID && folder.ParentID != nil && *folder.ParentID == folderID {
			deepest = max(deepest, 1+m.below(userID, folder.ID))
		}
	}
	return deepest
}

func (m *memory) LastSiblingPosition(_ context.Context, userID string, parentID *int64) (*string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var last *string
	for _, folder := range m.folders {
		if folder.owner == userID && sameFolder(folder.ParentID, parentID) && (last == nil || folder.Position > *last) {
			position := folder.Position
			last = &position
		}
	}
	return last, nil
}

// refuse is the table's two constraints: a sibling of the same name, and a
// parent that is not this account's folder.
func (m *memory) refuse(userID string, self int64, parentID *int64, name string) error {
	if parentID != nil && m.owned(userID, *parentID) == nil {
		return ErrParentGone
	}
	for _, folder := range m.folders {
		if folder.owner == userID && folder.ID != self && folder.Name == name && sameFolder(folder.ParentID, parentID) {
			return ErrNameTaken
		}
	}
	return nil
}

func (m *memory) Insert(_ context.Context, userID string, folder Folder) (Folder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.refuse(userID, 0, folder.ParentID, folder.Name); err != nil {
		return Folder{}, err
	}
	m.next++
	folder.ID = m.next
	m.folders[folder.ID] = &memoryFolder{owner: userID, Folder: folder}
	return folder, nil
}

func (m *memory) Rename(_ context.Context, userID string, folderID int64, name string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	folder := m.owned(userID, folderID)
	if folder == nil {
		return nil
	}
	if err := m.refuse(userID, folderID, folder.ParentID, name); err != nil {
		return err
	}
	folder.Name, folder.UpdatedAt = name, at
	return nil
}

func (m *memory) Reparent(_ context.Context, userID string, folderID int64, parentID *int64, position string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	folder := m.owned(userID, folderID)
	if folder == nil {
		return nil
	}
	if err := m.refuse(userID, folderID, parentID, folder.Name); err != nil {
		return err
	}
	folder.ParentID, folder.Position, folder.UpdatedAt = parentID, position, at
	return nil
}

func (m *memory) Reposition(_ context.Context, userID string, folderID int64, position string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if folder := m.owned(userID, folderID); folder != nil {
		folder.Position, folder.UpdatedAt = position, at
	}
	return nil
}

func (m *memory) ClearWatched(_ context.Context, userID string, folderID int64, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	folder := m.owned(userID, folderID)
	if folder == nil {
		return nil
	}
	var metadata map[string]any
	if json.Unmarshal(folder.Metadata, &metadata) != nil || metadata["watched"] != true {
		return nil
	}
	metadata["watched"] = false
	folder.Metadata, _ = json.Marshal(metadata)
	folder.UpdatedAt = at
	return nil
}

func (m *memory) Documents(_ context.Context, userID string, documentIDs []int64) ([]Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	found := []Document{}
	for _, id := range documentIDs {
		if document, exists := m.documents[id]; exists && document.owner == userID {
			found = append(found, document.Document)
		}
	}
	sort.Slice(found, func(a, b int) bool { return found[a].ID < found[b].ID })
	return found, nil
}

func (m *memory) MoveDocuments(_ context.Context, userID string, documentIDs []int64, folderID *int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range documentIDs {
		if document, exists := m.documents[id]; exists && document.owner == userID {
			document.FolderID = folderID
		}
	}
	return nil
}

func (m *memory) MarkSubtreeDeleting(_ context.Context, userID string, folderID int64) (Marked, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var marked Marked
	if m.owned(userID, folderID) == nil {
		return marked, nil
	}
	// Level by level, each level in id order, as the statement orders it.
	level := []int64{folderID}
	inside := map[int64]bool{}
	for len(level) > 0 {
		sort.Slice(level, func(a, b int) bool { return level[a] < level[b] })
		marked.SubtreeIDs = append(marked.SubtreeIDs, level...)
		var next []int64
		for _, id := range level {
			inside[id] = true
			for _, folder := range m.folders {
				if folder.owner == userID && folder.ParentID != nil && *folder.ParentID == id {
					next = append(next, folder.ID)
				}
			}
		}
		level = next
	}
	for _, document := range m.documents {
		if document.owner != userID || document.FolderID == nil || !inside[*document.FolderID] ||
			documentState(document.Status) == "deleting" {
			continue
		}
		marked.Documents = append(marked.Documents, MarkedDocument{ID: document.ID, Status: document.Status})
		document.Status = []byte(`{"state": "deleting"}`)
	}
	sort.Slice(marked.Documents, func(a, b int) bool { return marked.Documents[a].ID < marked.Documents[b].ID })
	return marked, nil
}

func (m *memory) RestoreMarked(_ context.Context, userID string, marked Marked) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, was := range marked.Documents {
		document, exists := m.documents[was.ID]
		if exists && document.owner == userID && documentState(document.Status) == "deleting" {
			document.Status = was.Status
		}
	}
	return nil
}
