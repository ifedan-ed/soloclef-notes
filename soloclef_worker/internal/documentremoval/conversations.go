package documentremoval

import (
	"context"
	"fmt"
)

// eraseConversations removes the chat loop's saved conversations of an
// account that is being erased (soloclef_agent_checkpoints).
//
// Why it is needed: a conversation is kept under a name, not an owner, so
// nothing cascades to it from the account row. Before this, an erased
// account's chats -- every message, every source a tool read -- stayed in
// the table for good. The names are found from the account's own rows, so
// this runs before the account row goes, and a run cut off between the two
// is finished by running the erasure again (it finds the same rows).
//
// The three families and who names them:
//
//   - a chat: "chat-<thread>", the specialists' threads beneath it
//     ("chat-<thread>/…") and the turn kept beside it ("chat-<thread>.turn")
//     -- soloclef_api/internal/chat (agentThread, turnKey);
//   - a Studio run: "studio-job-<job>-attempt-<n>" and the threads beneath
//     it -- soloclef_api/internal/studiogenerate (attemptConversation);
//   - an automation run: "automation-run-<run>-<step>-<attempt>" and the
//     threads beneath it -- internal/automationrun.
//
// Each is matched as a prefix range, which the table's text_pattern_ops
// index serves; '/' is followed by '0' and '-' by '.' in byte order, so
// [x/, x0) is every name beneath x and [x-, x.) every name that begins
// "x-". A chat's own name and its turn are matched exactly, so "chat-1"
// never reaches "chat-10".
func (r Removal) eraseConversations(ctx context.Context, userID string) error {
	for _, family := range []struct{ name, statement string }{
		{"chats", `
			DELETE FROM soloclef_agent_checkpoints AS c
			USING new_chat_threads AS t
			WHERE t.user_id = $1::uuid
			  AND (c.thread_id = 'chat-' || t.id
			       OR c.thread_id = 'chat-' || t.id || '.turn'
			       OR (c.thread_id ~>=~ ('chat-' || t.id || '/') AND c.thread_id ~<~ ('chat-' || t.id || '0')))`},
		{"Studio runs", `
			DELETE FROM soloclef_agent_checkpoints AS c
			USING deliverable_jobs AS j
			WHERE j.user_id = $1::uuid AND j.kind = 'artifact'
			  AND c.thread_id ~>=~ ('studio-job-' || j.id || '-attempt-')
			  AND c.thread_id ~<~ ('studio-job-' || j.id || '-attempt.')`},
		{"automation runs", `
			DELETE FROM soloclef_agent_checkpoints AS c
			USING automation_runs AS run
			JOIN automations AS a ON a.id = run.automation_id
			WHERE a.user_id = $1::uuid
			  AND c.thread_id ~>=~ ('automation-run-' || run.id || '-')
			  AND c.thread_id ~<~ ('automation-run-' || run.id || '.')`},
	} {
		if _, err := r.Pool.Exec(ctx, family.statement, userID); err != nil {
			return fmt.Errorf("the account's conversations of %s: %w", family.name, err)
		}
	}
	return nil
}
