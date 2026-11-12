package chat

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
)

// ownMenus is each account's own models; it opens none (opening is
// soloclef_assistant/ownmodels' and is tested there against a stub
// endpoint).
type ownMenus map[string][]ownmodels.Option

func (o ownMenus) Menu(_ context.Context, userID string) ([]ownmodels.Option, error) {
	return o[userID], nil
}

func (o ownMenus) Open(context.Context, string, int64) (model.Model, error) {
	return nil, &ownmodels.Unavailable{Message: "not opened in this test"}
}

// The model menu offers an account's own models, marked and labelled, and
// only to that account.
func TestTheModelMenuOffersAnAccountsOwnModelsToItAlone(t *testing.T) {
	a := newApp(t, modeltest.NewScript(), nil)
	a.service.Models.Own = ownMenus{ada: {{ID: "own:7", RowID: 7, Label: "Her model", Connection: "models.example.org", Chat: true}}}

	read := a.call(ada, http.MethodGet, modelPath, "")
	if read.status != 200 || !strings.Contains(read.raw, `{"id":"own:7","label":"Her model","own":true,"connection":"models.example.org"}`) {
		t.Fatalf("Ada's menu: %d %s", read.status, read.raw)
	}
	if chosen := a.call(ada, http.MethodPut, modelPath, `{"model":"own:7"}`); chosen.status != 200 || chosen.body["selected"] != "own:7" || chosen.body["available"] != true {
		t.Fatalf("Ada choosing her own model: %d %s", chosen.status, chosen.raw)
	}
	if theirs := a.call(ben, http.MethodGet, modelPath, ""); strings.Contains(theirs.raw, "own:7") {
		t.Fatalf("Ben's menu offers Ada's model: %s", theirs.raw)
	}
	if refused := a.call(ben, http.MethodPut, modelPath, `{"model":"own:7"}`); refused.status != 422 {
		t.Fatalf("Ben choosing Ada's model: %d %s", refused.status, refused.raw)
	}
}
