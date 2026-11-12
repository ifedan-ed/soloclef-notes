package uploadvision

import (
	"context"
	"errors"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/modelchoices"
)

type stored struct {
	vision string
	err    error
}

func (s stored) Of(context.Context, string) (map[modelchoices.Purpose]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	chosen := map[modelchoices.Purpose]string{modelchoices.Chat: "chat-model"}
	if s.vision != "" {
		chosen[modelchoices.Vision] = s.vision
	}
	return chosen, nil
}

func TestAnAccountMayAskForVisionWhenItOrTheDeploymentHasAModel(t *testing.T) {
	for name, case_ := range map[string]struct {
		vision      Vision
		wantRefusal string
	}{
		"its own choice":   {Vision{Choices: stored{vision: "account-vision"}}, ""},
		"the deployment's": {Vision{Choices: stored{}, Default: "deployment-vision"}, ""},
		"neither":          {Vision{Choices: stored{}}, NoModel},
		// A model of its own connection is one the indexer opens, with the
		// deployment's or without.
		"its own connection's":          {Vision{Choices: stored{vision: "own:7"}}, ""},
		"its own connection's, and one": {Vision{Choices: stored{vision: "own:7"}, Default: "deployment-vision"}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			refusal, err := case_.vision.Refusal(context.Background(), "account")
			if err != nil || refusal != case_.wantRefusal {
				t.Fatalf("refusal = %q, %v", refusal, err)
			}
			available, err := case_.vision.Available(context.Background(), "account")
			if err != nil || available != (case_.wantRefusal == "") {
				t.Fatalf("available = %v, %v", available, err)
			}
		})
	}
}

func TestAChoiceThatCannotBeReadIsAnErrorNotARefusal(t *testing.T) {
	broken := errors.New("database down")
	vision := Vision{Choices: stored{err: broken}, Default: "deployment-vision"}
	if _, err := vision.Refusal(context.Background(), "account"); !errors.Is(err, broken) {
		t.Fatalf("error = %v", err)
	}
	if available, err := vision.Available(context.Background(), "account"); available || err == nil {
		t.Fatalf("available = %v, %v", available, err)
	}
}
