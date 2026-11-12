package accountvision

import (
	"context"
	"errors"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/vision"
)

const (
	ada = "aaaaaaaa-0000-4000-8000-000000000001"
	ben = "bbbbbbbb-0000-4000-8000-000000000002"
	cat = "cccccccc-0000-4000-8000-000000000003"
)

// choices are the accounts' vision choices; broken fails every read.
type choices struct {
	chosen map[string]string
	broken bool
}

func (c choices) VisionChoice(_ context.Context, userID string) (string, error) {
	if c.broken {
		return "", errors.New("the database is down")
	}
	return c.chosen[userID], nil
}

// opener opens every name as itself, but an own choice of Cat's, whose
// row was removed.
type opener struct{ asked []string }

func (o *opener) Open(_ context.Context, userID, name string) (vision.Endpoint, error) {
	o.asked = append(o.asked, userID+"="+name)
	if userID == cat {
		return vision.Endpoint{}, &ownmodels.Unavailable{Message: "The model you chose from your own connection has been removed."}
	}
	return vision.Endpoint{Model: name}, nil
}

func TestTheAccountsChoiceElseTheDeploymentsElseNone(t *testing.T) {
	accounts := choices{chosen: map[string]string{ada: "own:4", cat: "own:9"}}
	open := &opener{}
	models := Models{Choices: accounts, Default: "site-vision", Open: open}
	if err := models.Check(); err != nil {
		t.Fatal(err)
	}
	for account, want := range map[string]string{ada: "own:4", ben: "site-vision"} {
		if opened, err := models.For(context.Background(), account); err != nil || opened.Model != want {
			t.Errorf("%s: %+v, %v", account, opened, err)
		}
	}
	// A chosen own model that cannot be called is said so; the
	// deployment's model is not asked instead.
	_, err := models.For(context.Background(), cat)
	if sentence, ok := Sentence(err); !ok || sentence != "The model you chose from your own connection has been removed." {
		t.Fatalf("err = %v", err)
	}
	if len(open.asked) != 3 {
		t.Fatalf("asked %v", open.asked)
	}

	models.Default = ""
	if _, err := models.For(context.Background(), ben); !errors.Is(err, ErrNone) {
		t.Fatalf("err = %v, want ErrNone", err)
	}
	// A choice that cannot be read is an error, never the default.
	models.Choices, models.Default = choices{broken: true}, "site-vision"
	if _, err := models.For(context.Background(), ada); err == nil || errors.Is(err, ErrNone) {
		t.Fatalf("err = %v", err)
	}
	if _, ok := Sentence(errors.New("other")); ok {
		t.Fatal("a failure of the moment was given a sentence")
	}
}

func TestModelsAreCheckedAtStartUp(t *testing.T) {
	if (Models{Choices: choices{}}).Check() == nil || (Models{Open: &opener{}}).Check() == nil {
		t.Fatal("an incomplete Models passed its check")
	}
}
