package smtp

import (
	"fmt"

	"git.danvics.com/danvics/soloclef/soloclef_mail/mailer"
)

// Which failure a refusal is depends on which command was refused, which is
// why the conversation is driven one step at a time rather than handed to a
// helper: smtplib tells SMTPSenderRefused from SMTPRecipientsRefused from
// SMTPResponseException by the step it was on, and the queue retries them
// differently. A recipient refused with 5xx is the only permanent one of the
// three, and reporting our own refused sender as a rejected recipient would
// tell someone their address is bad when the fault is ours.

func unreachable(err error) error {
	// Connect failure, disconnect, DNS, timeout.
	return fmt.Errorf("%w: could not reach the mail server: %w", mailer.ErrUnavailable, err)
}

func misconfigured(reason string) error {
	return fmt.Errorf("%w: %s", mailer.ErrMisconfigured, reason)
}

// authFailure is SMTPAuthenticationError's split: 454 is the server's own
// passing trouble, a 5xx is our credentials. Never surfaced as a rejected
// email.
func authFailure(err error) error {
	if code := responseCode(err); code != 0 && code < 500 {
		return fmt.Errorf("%w: SMTP authentication temporarily failed (%d)",
			mailer.ErrUnavailable, code)
	}
	if responseCode(err) == 0 {
		return unreachable(err)
	}
	return misconfigured("SMTP authentication failed; check the provider credentials")
}

// senderRefused is SMTPSenderRefused: a 5xx is almost always an unverified
// From domain, which is ours to fix; a 4xx (421 rate limit, say) passes.
func senderRefused(err error, sender string) error {
	code := responseCode(err)
	switch {
	case code == 0:
		return unreachable(err)
	case code < 500:
		return fmt.Errorf("%w: the mail server temporarily refused the sender (%d)",
			mailer.ErrUnavailable, code)
	default:
		return fmt.Errorf("%w: the mail server refused the sender %q", mailer.ErrMisconfigured, sender)
	}
}

// recipientRefused is SMTPRecipientsRefused: permanent only when the server
// said so. One recipient per message here, so "all refused with 5xx" and "this
// one was" are the same test.
func recipientRefused(err error) error {
	code := responseCode(err)
	switch {
	case code == 0:
		return unreachable(err)
	case code >= 500:
		return fmt.Errorf("%w: recipient refused by the mail server: %d", mailer.ErrRejected, code)
	default:
		return fmt.Errorf("%w: recipient temporarily refused: %d", mailer.ErrUnavailable, code)
	}
}

// refusal is SMTPResponseException: anything else the server answered.
func refusal(err error, step string) error {
	code := responseCode(err)
	switch {
	case code == 0:
		return unreachable(err)
	case code >= 500:
		return fmt.Errorf("%w: the mail server permanently refused the message at %s (%d)",
			mailer.ErrRejected, step, code)
	default:
		return fmt.Errorf("%w: the mail server temporarily refused the message at %s (%d)",
			mailer.ErrUnavailable, step, code)
	}
}
