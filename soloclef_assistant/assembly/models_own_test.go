package assembly

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
)

// The proxy's names, one of them spelt like an own choice.
type proxyNames []string

func (p proxyNames) Names(context.Context) ([]string, error) { return p, nil }

// choices is each account's stored chat choice.
type choices map[string]string

func (c choices) ChatModel(_ context.Context, userID string) (string, error) { return c[userID], nil }

// ownRows is OwnModels with a menu per account, recording what was opened.
type ownRows struct {
	menus  map[string][]ownmodels.Option
	opened []string
	fail   error
}

func (o *ownRows) Menu(_ context.Context, userID string) ([]ownmodels.Option, error) {
	return o.menus[userID], nil
}

func (o *ownRows) Open(_ context.Context, userID string, id int64) (model.Model, error) {
	if o.fail != nil {
		return nil, o.fail
	}
	o.opened = append(o.opened, fmt.Sprintf("%s/%d", userID, id))
	return named("own"), nil
}

// named is a model that is only a name, so a test can see which was opened.
type named string

func (named) Generate(context.Context, model.Request, func(model.Chunk)) (model.Response, error) {
	return model.Response{}, nil
}

const (
	ada = "aaaaaaaa-0000-4000-8000-000000000001"
	ben = "bbbbbbbb-0000-4000-8000-000000000002"
)

func ownModels(chosen choices, own *ownRows) *Models {
	return &Models{
		Catalogue: proxyNames{"proxy-chat", "own:99"}, Choices: chosen, Default: "proxy-chat",
		Open: func(name string) (model.Model, error) { return named("proxy:" + name), nil },
		Own:  own,
	}
}

func TestAnAccountsOwnModelsJoinItsMenuAndAnswerItsTurn(t *testing.T) {
	own := &ownRows{menus: map[string][]ownmodels.Option{
		ada: {
			{ID: "own:7", RowID: 7, Label: "Her model", Chat: true},
			// A model that only draws is not a chat model.
			{ID: "own:8", RowID: 8, Label: "Her painter", Images: true},
		},
	}}
	models := ownModels(choices{ada: "own:7", ben: "own:7"}, own)
	if err := models.Check(); err != nil {
		t.Fatal(err)
	}

	chosen, err := models.Look(context.Background(), ada)
	if err != nil {
		t.Fatal(err)
	}
	// The proxy's own:99 is left out: it would be read as an own model.
	if fmt.Sprint(chosen.Models) != "[proxy-chat own:7]" || !chosen.Offered || len(chosen.Own) != 1 {
		t.Fatalf("Ada's menu = %v, offered %v, own %v", chosen.Models, chosen.Offered, chosen.Own)
	}
	opened, name, err := models.For(context.Background(), ada)
	if err != nil || name != "own:7" || opened != named("own") || fmt.Sprint(own.opened) != "["+ada+"/7]" {
		t.Fatalf("Ada's turn opened %v %q %v (%v)", opened, name, err, own.opened)
	}

	// Ben stored Ada's id; his menu has no such model, so his turn is
	// refused with the sentence, and nothing of Ada's is opened.
	_, _, err = models.For(context.Background(), ben)
	var refused *ModelError
	if !errors.As(err, &refused) || refused.Unreachable || !strings.Contains(refused.Message, "a model of your own connection, is no longer offered") {
		t.Fatalf("Ben's turn = %v", err)
	}
	if len(own.opened) != 1 {
		t.Fatalf("Ben's turn opened %v", own.opened)
	}

	// A proxy name still goes through the proxy.
	proxied, _, err := ownModels(choices{}, own).For(context.Background(), ada)
	if err != nil || proxied != named("proxy:proxy-chat") {
		t.Fatalf("the default opened %v, %v", proxied, err)
	}
}

func TestAnAutomationMayNameAnOwnModelOfItsOwner(t *testing.T) {
	own := &ownRows{menus: map[string][]ownmodels.Option{ada: {{ID: "own:7", RowID: 7, Label: "Her model", Chat: true}}}}
	models := ownModels(choices{}, own)
	withdrawn, err := models.NotOffered(context.Background(), ada, []string{"own:7", "proxy-chat", "gone"})
	if err != nil || fmt.Sprint(withdrawn) != "[gone]" {
		t.Fatalf("NotOffered for Ada = %v, %v", withdrawn, err)
	}
	if withdrawn, _ := models.NotOffered(context.Background(), ben, []string{"own:7"}); fmt.Sprint(withdrawn) != "[own:7]" {
		t.Fatalf("Ben may name Ada's model: %v", withdrawn)
	}
	if opened, _, err := models.Named(context.Background(), ada, "own:7"); err != nil || opened != named("own") {
		t.Fatalf("Named = %v, %v", opened, err)
	}
	if _, _, err := models.Named(context.Background(), ben, "own:7"); err == nil {
		t.Fatal("Ben's automation ran on Ada's model")
	}
}

func TestAnOwnModelThatCannotBeCalledSaysWhy(t *testing.T) {
	own := &ownRows{
		menus: map[string][]ownmodels.Option{ada: {{ID: "own:7", RowID: 7, Label: "Her model", Chat: true}}},
		fail:  &ownmodels.Unavailable{Message: "The key of your connection for Her model can no longer be read."},
	}
	_, _, err := ownModels(choices{ada: "own:7"}, own).For(context.Background(), ada)
	var refused *ModelError
	if !errors.As(err, &refused) || !strings.Contains(refused.Message, "can no longer be read") {
		t.Fatalf("err = %v", err)
	}
}
