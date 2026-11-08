package workspaceselection_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/callercredential"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/restclient"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/workspaceselection"
)

const twoWorkspaces = `[
  {"id": 1, "name": "Research", "description": "Synthetic space", "is_owner": true,
   "member_count": 2},
  {"id": 2, "name": "Research Archive", "description": null, "is_owner": false,
   "member_count": 1}
]`

func selectionOver(t *testing.T, reply, preferred string) *workspaceselection.Selection {
	t.Helper()
	backend := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write([]byte(reply))
		}))
	t.Cleanup(backend.Close)
	client := restclient.New(backend.URL, time.Second, "ss_pat_test")
	return workspaceselection.New(client, preferred)
}

func caller(credential string) context.Context {
	return callercredential.Bind(context.Background(), credential, "")
}

func TestAnExactNameWins(t *testing.T) {
	selection := selectionOver(t, twoWorkspaces, "")
	chosen, err := selection.Resolve(caller("ss_pat_a"), "Research")
	if err != nil {
		t.Fatal(err)
	}
	if chosen.ID != 1 {
		t.Errorf("chose id %d, want the exact name's workspace", chosen.ID)
	}
}

func TestANameMatchesWithoutRegardToCase(t *testing.T) {
	selection := selectionOver(t, twoWorkspaces, "")
	chosen, err := selection.Resolve(caller("ss_pat_a"), "RESEARCH ARCHIVE")
	if err != nil {
		t.Fatal(err)
	}
	if chosen.ID != 2 {
		t.Errorf("chose id %d, want 2", chosen.ID)
	}
}

func TestAUniqueSubstringMatches(t *testing.T) {
	selection := selectionOver(t, twoWorkspaces, "")
	chosen, err := selection.Resolve(caller("ss_pat_a"), "archive")
	if err != nil {
		t.Fatal(err)
	}
	if chosen.ID != 2 {
		t.Errorf("chose id %d, want 2", chosen.ID)
	}
}

// "research" is a substring of both names but the exact-insensitive match comes
// first, so it is not ambiguous. A reference that matches only as a substring of
// several is.
func TestASubstringOfSeveralNamesIsRefused(t *testing.T) {
	selection := selectionOver(t, twoWorkspaces, "")
	_, err := selection.Resolve(caller("ss_pat_a"), "search")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	const want = "'search' matches several workspaces: Research (id 1), " +
		"Research Archive (id 2). Use a more specific name or the id."
	if err.Error() != want {
		t.Errorf("got:\n %s\nwant:\n %s", err.Error(), want)
	}
}

func TestACaseInsensitiveMatchBeatsASubstringOfSeveral(t *testing.T) {
	selection := selectionOver(t, twoWorkspaces, "")
	chosen, err := selection.Resolve(caller("ss_pat_a"), "research")
	if err != nil {
		t.Fatalf("'research' names one workspace exactly, bar its case: %v", err)
	}
	if chosen.ID != 1 {
		t.Errorf("chose id %d, want 1", chosen.ID)
	}
}

func TestAnUnknownNameListsWhatThereIs(t *testing.T) {
	selection := selectionOver(t, twoWorkspaces, "")
	_, err := selection.Resolve(caller("ss_pat_a"), "nowhere")
	const want = "No workspace named 'nowhere'. Available: Research (id 1), " +
		"Research Archive (id 2)."
	if err == nil || err.Error() != want {
		t.Errorf("got %v, want %s", err, want)
	}
}

func TestAnAllDigitReferenceIsAnID(t *testing.T) {
	selection := selectionOver(t, twoWorkspaces, "")
	chosen, err := selection.Resolve(caller("ss_pat_a"), "2")
	if err != nil {
		t.Fatal(err)
	}
	if chosen.ID != 2 {
		t.Errorf("chose id %d, want 2", chosen.ID)
	}
	_, err = selection.Resolve(caller("ss_pat_a"), "99")
	const want = "No workspace with id 99. Available: Research (id 1), " +
		"Research Archive (id 2)."
	if err == nil || err.Error() != want {
		t.Errorf("got %v, want %s", err, want)
	}
}

// A signed reference is a name, not an id, because the Python original decides
// with str.isdigit.
func TestASignedReferenceIsANameNotAnID(t *testing.T) {
	selection := selectionOver(t, twoWorkspaces, "")
	if _, numeric := workspaceselection.AsID("-1"); numeric {
		t.Error("-1 should not read as an id")
	}
	_, err := selection.Resolve(caller("ss_pat_a"), "-1")
	if err == nil || !strings.Contains(err.Error(), "No workspace named '-1'") {
		t.Errorf("got %v, want a refusal that treats it as a name", err)
	}
}

func TestWithNoSelectionAndSeveralWorkspacesTheCallerIsAsked(t *testing.T) {
	selection := selectionOver(t, twoWorkspaces, "")
	_, err := selection.Resolve(caller("ss_pat_a"), "")
	const want = "No workspace selected. Choose one first with " +
		"soloclef_select_workspace, or pass 'workspace'. Available: " +
		"Research (id 1), Research Archive (id 2)."
	if err == nil || err.Error() != want {
		t.Errorf("got %v, want %s", err, want)
	}
}

func TestASingleWorkspaceIsUsedWithoutAsking(t *testing.T) {
	selection := selectionOver(t,
		`[{"id": 5, "name": "Only One", "is_owner": true, "member_count": 1}]`, "")
	chosen, err := selection.Resolve(caller("ss_pat_a"), "")
	if err != nil {
		t.Fatal(err)
	}
	if chosen.ID != 5 {
		t.Errorf("chose id %d, want 5", chosen.ID)
	}
}

func TestNoAccessibleWorkspacesIsExplained(t *testing.T) {
	selection := selectionOver(t, `[]`, "")
	_, err := selection.Resolve(caller("ss_pat_a"), "")
	const want = "No accessible workspaces. Confirm the token's account has a " +
		"workspace with API access enabled."
	if err == nil || err.Error() != want {
		t.Errorf("got %v, want %s", err, want)
	}
}

func TestAConfiguredDefaultIsUsedWhenNothingIsSelected(t *testing.T) {
	selection := selectionOver(t, twoWorkspaces, "Research Archive")
	chosen, err := selection.Resolve(caller("ss_pat_a"), "")
	if err != nil {
		t.Fatal(err)
	}
	if chosen.ID != 2 {
		t.Errorf("chose id %d, want the configured default", chosen.ID)
	}
}

// The choice is remembered for the credential that made it. A shared slot would
// send one account's searches into another's workspace, which is the failure
// this is here to prevent.
func TestTheChoiceIsRememberedPerCredential(t *testing.T) {
	selection := selectionOver(t, twoWorkspaces, "")
	if _, err := selection.Resolve(caller("ss_pat_a"), "Research Archive"); err != nil {
		t.Fatal(err)
	}
	chosen, err := selection.Resolve(caller("ss_pat_a"), "")
	if err != nil {
		t.Fatal(err)
	}
	if chosen.ID != 2 {
		t.Errorf("the same credential got id %d, want its own choice", chosen.ID)
	}
	if _, err := selection.Resolve(caller("ss_pat_b"), ""); err == nil {
		t.Error("another credential inherited the first one's choice")
	}
}

// An agent's sign-in token is replaced every hour, so the choice is bound to the
// account's identity instead and survives the refresh.
func TestASignInChoiceIsRememberedForTheAccountNotTheToken(t *testing.T) {
	selection := selectionOver(t, twoWorkspaces, "")
	first := callercredential.Bind(context.Background(), "token-hour-one", "user:u-1")
	if _, err := selection.Resolve(first, "Research Archive"); err != nil {
		t.Fatal(err)
	}
	refreshed := callercredential.Bind(context.Background(), "token-hour-two", "user:u-1")
	chosen, err := selection.Resolve(refreshed, "")
	if err != nil {
		t.Fatalf("the choice did not survive a token refresh: %v", err)
	}
	if chosen.ID != 2 {
		t.Errorf("after the refresh the choice was id %d, want 2", chosen.ID)
	}
	other := callercredential.Bind(context.Background(), "token-other", "user:u-2")
	if _, err := selection.Resolve(other, ""); err == nil {
		t.Error("another account inherited the first one's choice")
	}
}

// Under stdio there is no request and no header, so every call is the one local
// caller and shares one choice.
func TestWithoutACredentialEveryCallIsTheSameLocalCaller(t *testing.T) {
	selection := selectionOver(t, twoWorkspaces, "")
	if _, err := selection.Resolve(context.Background(), "Research Archive"); err != nil {
		t.Fatal(err)
	}
	chosen, err := selection.Resolve(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if chosen.ID != 2 {
		t.Errorf("chose id %d, want the remembered one", chosen.ID)
	}
}

func TestADescriptionThatIsNullReadsAsNone(t *testing.T) {
	selection := selectionOver(t, twoWorkspaces, "")
	all, err := selection.FetchAll(caller("ss_pat_a"))
	if err != nil {
		t.Fatal(err)
	}
	if all[1].Description != "" {
		t.Errorf("a null description read as %q", all[1].Description)
	}
	if all[1].MemberCount != 1 || all[1].IsOwner {
		t.Errorf("row 2 read as %+v", all[1])
	}
}
