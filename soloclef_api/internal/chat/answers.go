package chat

import (
	"encoding/json"
	"net/http"
	"time"
)

// stopRetry is how long after a Stop the page is told to ask for the status
// again: long enough for a turn to save what it had.
const stopRetry = 300 * time.Millisecond

// turnState answers Stop. The field names are the ones the apps read.
type turnState struct {
	Status       string `json:"status"`
	ErrorCode    string `json:"error_code"`
	Message      string `json:"message"`
	RetryAfterMS *int64 `json:"retry_after_ms"`
	RetryAfterAt *int64 `json:"retry_after_at"`
}

// turnStatus answers "is a turn running". active_turn_id and the retry
// fields are always null: the apps read only status, and the fields stay so
// that an app that looks for them finds them.
type turnStatus struct {
	Status       string  `json:"status"`
	ActiveTurnID *string `json:"active_turn_id"`
	RetryAfterMS *int64  `json:"retry_after_ms"`
	RetryAfterAt *int64  `json:"retry_after_at"`
}

// pendingAnswer answers "what is waiting": the paused turn's message and
// one object per waiting call.
type pendingAnswer struct {
	AssistantMessageID *int64            `json:"assistant_message_id"`
	Pending            []json.RawMessage `json:"pending_interrupts"`
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		http.Error(response, "internal error", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_, _ = response.Write(body)
}
