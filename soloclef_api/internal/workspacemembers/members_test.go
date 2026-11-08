package workspacemembers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// The two fixtures are one request to the running Python tier:
// testdata/roster.json is what its three queries read out of
// workspace_memberships, workspace_roles and "user" — the memberships in the
// order the query returned them — and testdata/members.json is the response
// body it returned for them, byte for byte.
//
// Nothing in either is an account's own text. The capture was taken against a
// database seeded for this comparison, because this repository has a public
// copy: the names, the addresses and the workspace names were written for the
// capture, and were chosen to carry what decides the bytes — curly quotes, an
// em dash, <, &, a non-ASCII letter, a null display name, an empty avatar url,
// a membership with no role, a timestamp with microseconds, one with a half
// second and one with none. Nothing was replaced afterwards, so there is no
// untouched capture that differs from this one.
// custom/docs/go-api-tier.md records how the two tiers were compared.

type storedRoster struct {
	Memberships []struct {
		ID          int64  `json:"id"`
		UserID      string `json:"user_id"`
		WorkspaceID int64  `json:"workspace_id"`
		RoleID      *int64 `json:"role_id"`
		IsOwner     bool   `json:"is_owner"`
		JoinedAt    string `json:"joined_at"`
		CreatedAt   string `json:"created_at"`
	} `json:"memberships"`
	Roles []struct {
		ID           int64    `json:"id"`
		Name         string   `json:"name"`
		Description  *string  `json:"description"`
		Permissions  []string `json:"permissions"`
		IsDefault    bool     `json:"is_default"`
		IsSystemRole bool     `json:"is_system_role"`
		WorkspaceID  int64    `json:"workspace_id"`
		CreatedAt    string   `json:"created_at"`
	} `json:"roles"`
	Accounts []struct {
		ID          string  `json:"id"`
		Email       string  `json:"email"`
		DisplayName *string `json:"display_name"`
		AvatarURL   *string `json:"avatar_url"`
		LastLogin   *string `json:"last_login"`
	} `json:"accounts"`
}

func loadRoster(t *testing.T) Roster {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "roster.json"))
	if err != nil {
		t.Fatalf("reading the captured rows: %v", err)
	}
	var stored storedRoster
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("parsing the captured rows: %v", err)
	}

	roster := Roster{
		Memberships: make([]Membership, 0, len(stored.Memberships)),
		Roles:       map[int64]Role{},
		Accounts:    map[string]Account{},
	}
	for _, row := range stored.Memberships {
		roster.Memberships = append(roster.Memberships, Membership{
			ID:          row.ID,
			UserID:      row.UserID,
			WorkspaceID: row.WorkspaceID,
			RoleID:      row.RoleID,
			IsOwner:     row.IsOwner,
			JoinedAt:    moment(t, row.JoinedAt),
			CreatedAt:   moment(t, row.CreatedAt),
		})
	}
	for _, row := range stored.Roles {
		roster.Roles[row.ID] = Role{
			ID:           row.ID,
			Name:         row.Name,
			Description:  row.Description,
			Permissions:  row.Permissions,
			IsDefault:    row.IsDefault,
			IsSystemRole: row.IsSystemRole,
			WorkspaceID:  row.WorkspaceID,
			CreatedAt:    moment(t, row.CreatedAt),
		}
	}
	for _, row := range stored.Accounts {
		account := Account{
			Email:       row.Email,
			DisplayName: row.DisplayName,
			AvatarURL:   row.AvatarURL,
		}
		if row.LastLogin != nil {
			at := moment(t, *row.LastLogin)
			account.LastLogin = &at
		}
		roster.Accounts[row.ID] = account
	}
	return roster
}

func moment(t *testing.T, text string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		t.Fatalf("%q is not a timestamp: %v", text, err)
	}
	return at
}

func TestBuildReproducesThePythonResponseByteForByte(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("testdata", "members.json"))
	if err != nil {
		t.Fatalf("reading the captured response: %v", err)
	}

	got, err := pythonjson.Marshal(Build(loadRoster(t)))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("the response differs from the Python tier's.\ngot:  %s\nwant: %s",
			firstDifference(string(got), string(want)), firstDifference(string(want), string(got)))
	}
}

// firstDifference quotes the 120 bytes around the first byte that differs,
// because the whole body is thousands of bytes long.
func firstDifference(text, other string) string {
	at := 0
	for at < len(text) && at < len(other) && text[at] == other[at] {
		at++
	}
	start := max(at-60, 0)
	end := min(at+60, len(text))
	return text[start:end]
}

func TestBuildKeepsTheOrderTheMembershipsArriveIn(t *testing.T) {
	// The array's order is the query's, because the Python route's query has no
	// ORDER BY either; reordering here would be a different response.
	roster := loadRoster(t)
	roster.Memberships[0], roster.Memberships[3] = roster.Memberships[3], roster.Memberships[0]

	got, err := pythonjson.Marshal(Build(roster))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got[:10]) != `[{"id":5,"` {
		t.Errorf("the first member is not the first row: %s", got[:40])
	}
}

func TestBuildAnswersNullsForAMembershipWhoseAccountIsGone(t *testing.T) {
	// The Python route reads the account per membership and puts None in all
	// four fields when there is no row, rather than failing.
	roster := Roster{
		Memberships: []Membership{{ID: 9, UserID: "00000000-0000-4000-8000-000000000001", WorkspaceID: 1}},
		Roles:       map[int64]Role{},
		Accounts:    map[string]Account{},
	}
	got, err := pythonjson.Marshal(Build(roster))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	const want = `[{"id":9,"user_id":"00000000-0000-4000-8000-000000000001","workspace_id":1,` +
		`"role_id":null,"is_owner":false,"joined_at":"0001-01-01T00:00:00Z",` +
		`"created_at":"0001-01-01T00:00:00Z","role":null,"user_email":null,` +
		`"user_display_name":null,"user_avatar_url":null,"user_last_login":null}]`
	if string(got) != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestBuildAnswersAnEmptyArrayForAWorkspaceWithNoMembers(t *testing.T) {
	got, err := pythonjson.Marshal(Build(Roster{}))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != "[]" {
		t.Errorf("got %s, want []", got)
	}
}
