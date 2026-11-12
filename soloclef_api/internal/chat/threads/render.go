package threads

import (
	"bytes"
	"encoding/json"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// The answers, in the field names their readers use. The readers:
//
//   - threadRecord: soloclef_web/lib/chat/thread-persistence.ts ThreadRecord
//     (create, update, and "full").
//   - a message record (writeMessage): the same file's MessageRecord and
//     the phone's messageRecordSchema
//     (soloclef_mobile/src/lib/api/schemas.ts), for the history a thread is
//     opened with.
//   - createdLink: soloclef_web/features/public-sharing/model/created-link.ts
//     and contracts/types/chat-threads.types.ts.
//   - listedLink: contracts/types/chat-threads.types.ts
//     publicChatSnapshotInfo.
//   - Tool: soloclef_web/lib/apis/agent-tools-api.service.ts.
//
// Times are written the way each reader was already being sent them: "Z"
// with a six-digit fraction for the records, "+00:00" for a link's
// created_at.

// encode writes value as JSON without escaping "<", ">" and "&": message
// content is stored JSON handed on as it is.
func encode(value any) ([]byte, error) {
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// threadRecord is a thread as create, update and "full" answer it. It says
// nothing of whose it is: a thread is only ever answered to its owner.
type threadRecord struct {
	Title     string `json:"title"`
	Archived  bool   `json:"archived"`
	ID        int64  `json:"id"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func recordOf(thread Thread) threadRecord {
	return threadRecord{
		Title: thread.Title, Archived: thread.Archived, ID: thread.ID,
		CreatedAt: pythonjson.PydanticDateTime(thread.CreatedAt), UpdatedAt: pythonjson.PydanticDateTime(thread.UpdatedAt),
	}
}

// listedThread is one row of the list and of a search. Its two times are
// camelCase where every other answer's are snake_case; that is the shape
// the route has always had, so it is the shape kept.
type listedThread struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Archived  bool   `json:"archived"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

func listed(threads []Thread) []listedThread {
	out := make([]listedThread, len(threads))
	for i, thread := range threads {
		out[i] = listedThread{
			ID: thread.ID, Title: thread.Title, Archived: thread.Archived,
			CreatedAt: pythonjson.PydanticDateTime(thread.CreatedAt), UpdatedAt: pythonjson.PydanticDateTime(thread.UpdatedAt),
		}
	}
	return out
}

type threadList struct {
	Threads  []listedThread `json:"threads"`
	Archived []listedThread `json:"archived_threads"`
}

type usageSummary struct {
	PromptTokens     int64           `json:"prompt_tokens"`
	CompletionTokens int64           `json:"completion_tokens"`
	TotalTokens      int64           `json:"total_tokens"`
	CostMicros       int64           `json:"cost_micros"`
	ModelBreakdown   json.RawMessage `json:"model_breakdown"`
}

// messageHead is a message without its content: everything of a message
// record that is small.
type messageHead struct {
	// Role is lower case on the wire ("user"), upper in the column.
	Role string `json:"role"`
}

// messageTail is the rest of a message record, after its content.
type messageTail struct {
	ID                int64         `json:"id"`
	CreatedAt         string        `json:"created_at"`
	ThreadID          int64         `json:"thread_id"`
	AuthorID          *string       `json:"author_id"`
	AuthorDisplayName *string       `json:"author_display_name"`
	AuthorAvatarURL   *string       `json:"author_avatar_url"`
	TokenUsage        *usageSummary `json:"token_usage"`
	TurnID            *string       `json:"turn_id"`
}

// writeMessage writes one message record:
//
//	{"role":…,"content":…,"id":…,"created_at":…,"thread_id":…,"author_id":…,
//	 "author_display_name":…,"author_avatar_url":…,"token_usage":…,"turn_id":…}
//
// The content is written as the bytes the database returned. It is the
// stored jsonb, so it is JSON already; handing it to the encoder as a value
// has it parsed and rewritten byte by byte to prove that again, which was
// over half of what opening a long thread cost this process
// (BenchmarkHistory). So the record is put together here: the small fields
// through the encoder, the content as it is.
func writeMessage(b *bytes.Buffer, message Message) error {
	head, err := encode(messageHead{Role: strings.ToLower(message.Role)})
	if err != nil {
		return err
	}
	tail := messageTail{
		ID: message.ID, CreatedAt: pythonjson.PydanticDateTime(message.CreatedAt), ThreadID: message.ThreadID,
		AuthorID: message.AuthorID, AuthorDisplayName: message.AuthorName, AuthorAvatarURL: message.AuthorAvatar,
		TurnID: message.TurnID,
	}
	if usage := message.Usage; usage != nil {
		tail.TokenUsage = &usageSummary{
			PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens,
			TotalTokens: usage.TotalTokens, CostMicros: usage.CostMicros, ModelBreakdown: usage.Breakdown,
		}
		if len(usage.Breakdown) == 0 {
			tail.TokenUsage.ModelBreakdown = json.RawMessage("null")
		}
	}
	rest, err := encode(tail)
	if err != nil {
		return err
	}
	content := message.Content
	if len(content) == 0 {
		// The column is NOT NULL, so this is a row no query returned
		// content for; null is the honest value.
		content = []byte("null")
	}
	// head is {"role":"…"} and rest is {"id":…}: the content goes between
	// them, in the place of the brace each gives up.
	b.Write(head[:len(head)-1])
	b.WriteString(`,"content":`)
	b.Write(content)
	b.WriteByte(',')
	b.Write(rest[1:])
	return nil
}

// writeMessages writes the records as a JSON list.
func writeMessages(b *bytes.Buffer, messages []Message) error {
	b.WriteByte('[')
	for i, message := range messages {
		if i > 0 {
			b.WriteByte(',')
		}
		if err := writeMessage(b, message); err != nil {
			return err
		}
	}
	b.WriteByte(']')
	return nil
}

// historyBody is {"messages":[…]}.
func historyBody(messages []Message) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(`{"messages":`)
	if err := writeMessages(&b, messages); err != nil {
		return nil, err
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// messagesBody is the bare list.
func messagesBody(messages []Message) ([]byte, error) {
	var b bytes.Buffer
	err := writeMessages(&b, messages)
	return b.Bytes(), err
}

// fullBody is a thread record with "messages" after its own fields.
func fullBody(thread Thread, messages []Message) ([]byte, error) {
	record, err := encode(recordOf(thread))
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.Write(record[:len(record)-1])
	b.WriteString(`,"messages":`)
	if err := writeMessages(&b, messages); err != nil {
		return nil, err
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

type createdLink struct {
	SnapshotID int64   `json:"snapshot_id"`
	ShareToken string  `json:"share_token"`
	PublicURL  string  `json:"public_url"`
	IsNew      bool    `json:"is_new"`
	ExpiresAt  *string `json:"expires_at"`
}

type listedLink struct {
	ID           int64  `json:"id"`
	ShareToken   string `json:"share_token"`
	PublicURL    string `json:"public_url"`
	CreatedAt    string `json:"created_at"`
	MessageCount int64  `json:"message_count"`
}

type linkList struct {
	Snapshots []listedLink `json:"snapshots"`
}

type said struct {
	Message string `json:"message"`
}
