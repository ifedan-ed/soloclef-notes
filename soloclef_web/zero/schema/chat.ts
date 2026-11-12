import { json, number, string, table } from "@rocicorp/zero";

export const newChatMessageTable = table("new_chat_messages")
	.columns({
		id: number(),
		role: string(),
		content: json(),
		threadId: number().from("thread_id"),
		authorId: string().optional().from("author_id"),
		createdAt: number().from("created_at"),
		source: string(),
		platformMetadata: json().optional().from("platform_metadata"),
		// Per-turn correlation id sourced from ``configurable.turn_id``
		// at streaming time. Required by the inline Revert button's
		// (chat_turn_id, tool_name, position) fallback in tool-fallback.tsx
		// — without it the live-collab Zero sync would clobber the
		// metadata we set during streaming and the button would vanish
		// the moment Zero re-syncs after the stream finishes.
		turnId: string().optional().from("turn_id"),
	})
	.primaryKey("id");

// Published only as the authz parent of chat messages, session state and
// Studio outputs (whereExists("thread")): its id and its owner, which is all
// the rule needs (queries/thread-access.ts). The title is resolved over REST
// (react-query) for the tab bar, not synced through Zero.
export const newChatThreadTable = table("new_chat_threads")
	.columns({
		id: number(),
		userId: string().from("user_id"),
	})
	.primaryKey("id");

export const chatSessionStateTable = table("chat_session_state")
	.columns({
		id: number(),
		threadId: number().from("thread_id"),
		aiRespondingToUserId: string().optional().from("ai_responding_to_user_id"),
		updatedAt: number().from("updated_at"),
	})
	.primaryKey("id");
