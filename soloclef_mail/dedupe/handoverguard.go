package dedupe

import "context"

// HandoverGuard is one handover per attempt: what has been given to the mail
// server. app/mail_queue/dedupe/handover_guard.py.
//
// The mark goes down after the send returns, not before. A process that dies
// mid-send then leaves no mark and its message goes out again, which is the
// trade taken deliberately: twice-sent is a second email in an inbox,
// never-sent is a locked-out account waiting for a password reset that will
// not come. What is left is a window of one Redis SET between the mail
// server's acceptance and the mark, in place of the whole SMTP conversation.
//
// A claim with no mark means a sender is giving the message to the mail server
// right now -- or was, when it died. The two cannot be told apart from here,
// so the run that recovers held mail drops those claims (package recovery) and
// the message is offered again.
type HandoverGuard struct {
	Keys Keys
}

// Claim takes this attempt to send it. It returns "" when this sender may
// send, Claimed when another sender has it, and Sent when the mail server
// already took it; in the last two the caller must not send.
func (g HandoverGuard) Claim(ctx context.Context, mailID string, attempt int) (string, error) {
	return g.Keys.SetIfAbsent(ctx, HandoverKey(mailID, attempt), Claimed, Window)
}

// MarkSent records that the mail server took it, whatever happens next.
func (g HandoverGuard) MarkSent(ctx context.Context, mailID string, attempt int) error {
	return g.Keys.Set(ctx, HandoverKey(mailID, attempt), Sent, Window)
}

// Release drops a claim whose sender is gone, and never a mark that the mail
// server took the message.
//
// Read and delete are two commands rather than one script because this runs
// only at the start of a run, before any of its senders has taken a message,
// and under the one-sender-process rule nothing else is touching the key.
func (g HandoverGuard) Release(ctx context.Context, mailID string, attempt int) error {
	key := HandoverKey(mailID, attempt)
	held, err := g.Keys.Get(ctx, key)
	if err != nil || held != Claimed {
		return err
	}
	return g.Keys.Delete(ctx, key)
}
