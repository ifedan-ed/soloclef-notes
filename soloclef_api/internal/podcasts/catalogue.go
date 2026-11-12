package podcasts

import (
	"context"
	"errors"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/voices"
)

// Catalogue is the voices the deployment's speech service has
// (*voices.Catalogue). It is an interface so that a test can name a roster
// without a model proxy.
type Catalogue interface {
	Roster(ctx context.Context) (*voices.Roster, error)
}

// Sampler is the audible sample of a voice (*voices.Samples). It is an
// interface so that no test of this package can reach a speech provider.
type Sampler interface {
	Of(ctx context.Context, voice voices.Voice) (voices.Sample, error)
}

// catalogueSentence is what a caller is told when the catalogue could not
// answer, and whether the error is one of the catalogue's at all. There are
// three, and they are told apart because they are fixed by different people:
// nobody, by waiting, and the operator.
func catalogueSentence(err error) (string, bool) {
	var unreachable *voices.UnreachableError
	var wrong *voices.MisconfiguredError
	switch {
	case errors.Is(err, voices.ErrNotConfigured):
		return noSpeechService, true
	case errors.As(err, &unreachable):
		return voicesUnavailable, true
	case errors.As(err, &wrong):
		return voicesMisconfigured, true
	}
	return "", false
}
