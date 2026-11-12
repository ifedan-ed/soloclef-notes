package videojob

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
)

// The failure codes a job is marked with. Each is one the page's video card
// has a sentence for (soloclef_web/components/tool-ui/deliverable-job.tsx,
// FAILURE_MESSAGES); "cancelled" is written by the cancel path, not here.
const (
	codeGeneration   = "generation_failed"
	codeRender       = "render_failed"
	codeVerification = "verification_failed"
	codeDuration     = "duration_limit"
	codeQuota        = "quota_exceeded"
)

// failure is an error with the code the job is marked with. Each stage
// wraps what it returns in the code that stage stands for, so the code comes
// from where the job failed.
//
// The Python worker matched words in the exception's text instead ("render"
// anywhere meant render_failed, "verif" verification_failed), which put a
// model's unreadable reply about rendering under render_failed and needed a
// special case to undo. The code is a fact about the stage, not about the
// wording of whatever went wrong in it.
type failure struct {
	code string
	err  error
}

func (f *failure) Error() string { return f.code + ": " + f.err.Error() }
func (f *failure) Unwrap() error { return f.err }

// fail gives err the stage's code. An error that already carries one keeps
// it (the innermost stage knew best), and a cancel or a superseded attempt
// is passed on untouched: neither is a failure.
func fail(code string, err error) error {
	if err == nil || errors.Is(err, errCancelled) || errors.Is(err, errSuperseded) {
		return err
	}
	var already *failure
	if errors.As(err, &already) {
		return err
	}
	return &failure{code: code, err: err}
}

// codeOf is the code a failed job is marked with. A proxy that refused for
// want of credit (402) is quota_exceeded wherever in the job it happened:
// the card then says so instead of "try again".
func codeOf(err error) string {
	var refused *model.Error
	if errors.As(err, &refused) && refused.Status == 402 {
		return codeQuota
	}
	var known *failure
	if errors.As(err, &known) {
		return known.code
	}
	return codeGeneration
}

// maxDiagnostic is the internal_error column's width.
const maxDiagnostic = 2000

// Forms a credential takes in an error's text: a key or token named as one,
// and a URL with a user in it.
var (
	namedSecret    = regexp.MustCompile(`(?i)(api[_-]?key|authorization|password|secret|token|bearer)(\s*[:=]\s*|\s+)\S+`)
	urlCredentials = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/@\s]+@`)
)

// diagnostic is err as the row's internal_error: one line, no credential,
// within the column. It is for whoever reads the table; the person is shown
// the card's sentence for the code.
func diagnostic(err error) string {
	text := strings.Join(strings.Fields(err.Error()), " ")
	text = namedSecret.ReplaceAllString(text, "$1=[redacted]")
	text = urlCredentials.ReplaceAllString(text, "$1[redacted]@")
	if len(text) > maxDiagnostic {
		cut := maxDiagnostic
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut]
	}
	return text
}

// tail is the end of a command's output, which is where a failing build or
// render says why. It is what a repair is shown and what a row records.
func tail(output string, limit int) string {
	if len(output) <= limit {
		return output
	}
	cut := len(output) - limit
	for cut < len(output) && !utf8.RuneStart(output[cut]) {
		cut++
	}
	return output[cut:]
}
