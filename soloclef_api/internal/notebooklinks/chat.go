package notebooklinks

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"
)

// This file turns the notebook's stored chat into what a visitor is shown:
// the words of each turn and the things the chat made, and nothing else. No
// tool calls, searches, reasoning or authors. It reads; it never asks a
// model anything.

// artifactTool is the one card a visitor's page draws: something the chat
// made, by its artifact.
const artifactTool = "notebook_artifact"

// The tools whose finished result is something a visitor can open, by the
// names the chat saves them under (soloclef_assistant/deliverables).
const (
	saveTool    = "save_artifact"
	podcastTool = "generate_podcast"
	videoTool   = "enqueue_deliverable_job"
)

// madeRef names what a tool call made: an artifact outright, or the
// podcast or job that will have one when it finishes. The ids come out of
// stored content, so they are candidates until the database has confirmed
// each is the link owner's and finished (MadeArtifacts).
type madeRef struct {
	kind string // "artifact", "podcast" or "job"
	id   int64
}

// madeArtifact is a confirmed artifact as the card shows it.
type madeArtifact struct {
	id            int64
	title, format string
}

// chatMessage is one stored message, decoded once.
type chatMessage struct {
	role      string
	parts     []map[string]any
	createdAt time.Time
}

// decodeMessage reads a stored message's content: a list of parts, or bare
// text as the oldest rows hold it. Content that is neither has nothing a
// visitor could be shown, and one such row must not fail the page.
func decodeMessage(row SharedChatRow) chatMessage {
	message := chatMessage{role: strings.ToLower(row.Role), createdAt: row.CreatedAt}
	var content any
	if json.Unmarshal(row.Content, &content) != nil {
		return message
	}
	switch typed := content.(type) {
	case string:
		message.parts = []map[string]any{{"type": "text", "text": typed}}
	case []any:
		for _, item := range typed {
			if part, isObject := item.(map[string]any); isObject {
				message.parts = append(message.parts, part)
			}
		}
	}
	return message
}

func partText(part map[string]any) (string, bool) {
	if part["type"] != "text" {
		return "", false
	}
	text, _ := part["text"].(string)
	return text, true
}

// rowID reads a JSON number that could be a row id.
func rowID(value any) (int64, bool) {
	number, isNumber := value.(float64)
	if !isNumber || number < 1 || number != math.Trunc(number) || number > math.MaxInt32 {
		return 0, false
	}
	return int64(number), true
}

// madeBy is what a finished tool call made, if anything a visitor can open.
func madeBy(part map[string]any) (madeRef, bool) {
	if part["type"] != "tool-call" {
		return madeRef{}, false
	}
	result, isObject := part["result"].(map[string]any)
	if !isObject {
		return madeRef{}, false
	}
	tool, _ := part["toolName"].(string)
	if id, ok := rowID(result["artifact_id"]); ok {
		// A save that was refused still names the artifact it would have
		// replaced; only a save that happened made something.
		if tool == saveTool && result["status"] != "saved" {
			return madeRef{}, false
		}
		return madeRef{"artifact", id}, true
	}
	if id, ok := rowID(result["podcast_id"]); ok && tool == podcastTool {
		return madeRef{"podcast", id}, true
	}
	if id, ok := rowID(result["job_id"]); ok && tool == videoTool {
		return madeRef{"job", id}, true
	}
	return madeRef{}, false
}

// madeRefs is everything the messages' cards name, as the three id lists
// MadeArtifacts takes.
func madeRefs(messages []chatMessage) (artifacts, podcasts, jobs []int64) {
	seen := map[madeRef]bool{}
	for _, message := range messages {
		for _, part := range message.parts {
			ref, ok := madeBy(part)
			if !ok || seen[ref] {
				continue
			}
			seen[ref] = true
			switch ref.kind {
			case "artifact":
				artifacts = append(artifacts, ref.id)
			case "podcast":
				podcasts = append(podcasts, ref.id)
			case "job":
				jobs = append(jobs, ref.id)
			}
		}
	}
	return artifacts, podcasts, jobs
}

// citedIDs is every passage id the messages' text cites, each once.
// Only a marker that is all digits is a passage; a web address is not.
func citedIDs(messages []chatMessage) []int64 {
	seen := map[int64]bool{}
	var ids []int64
	for _, message := range messages {
		for _, part := range message.parts {
			text, isText := partText(part)
			if !isText {
				continue
			}
			rewriteMarkers(text, func(where string) (string, bool) {
				if id, err := strconv.ParseInt(where, 10, 64); err == nil && digits(where) && !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
				return "", false
			})
		}
	}
	return ids
}

// passagePlace is where a cited passage is: its document and page.
type passagePlace struct {
	documentID int64
	page       *int64
}

// liveCitation is one numbered citation as the page's chip shows it. It
// names the source, never the passage.
type liveCitation struct {
	SourceID int64  `json:"source_id"`
	Title    string `json:"title"`
	Page     *int64 `json:"page"`
}

// liveMessage is one turn as a visitor reads it.
type liveMessage struct {
	Role      string           `json:"role"`
	Content   []map[string]any `json:"content"`
	CreatedAt time.Time        `json:"created_at"`
}

// publicChat is the chat as a visitor sees it, with the citations its text
// now refers to by number.
//
// places holds only passages inside the notebook's visible sources, and
// titles those sources' titles. A citation found there is renumbered from 1
// in order of appearance; any other marker -- a passage outside the
// notebook's sources, a web address, anything unreadable -- is removed, so
// no passage id ever leaves and no chip leads nowhere. made holds only
// confirmed artifacts; a card for anything else is dropped. A message left
// with nothing to show is dropped too.
func publicChat(messages []chatMessage, places map[int64]passagePlace, titles map[int64]string,
	made map[madeRef]madeArtifact) ([]liveMessage, map[string]liveCitation) {
	numbers := map[int64]int{}
	citations := map[string]liveCitation{}
	renumber := func(where string) (string, bool) {
		if !digits(where) {
			return "", false
		}
		id, err := strconv.ParseInt(where, 10, 64)
		place, inScope := places[id]
		if err != nil || !inScope {
			return "", false
		}
		number, numbered := numbers[id]
		if !numbered {
			number = len(numbers) + 1
			numbers[id] = number
			citations[strconv.Itoa(number)] = liveCitation{
				SourceID: place.documentID, Title: titles[place.documentID], Page: place.page}
		}
		return "[citation:" + strconv.Itoa(number) + "]", true
	}

	shown := []liveMessage{}
	for _, message := range messages {
		if message.role != "user" && message.role != "assistant" {
			continue
		}
		content := []map[string]any{}
		for _, part := range message.parts {
			if text, isText := partText(part); isText {
				if clean := strings.TrimSpace(rewriteMarkers(text, renumber)); clean != "" {
					content = append(content, map[string]any{"type": "text", "text": clean})
				}
				continue
			}
			ref, ok := madeBy(part)
			artifact, confirmed := made[ref]
			if !ok || !confirmed {
				continue
			}
			// A new card, built from the confirmed row: nothing of the
			// stored call's arguments or result is carried over.
			content = append(content, map[string]any{
				"type":       "tool-call",
				"toolName":   artifactTool,
				"toolCallId": "artifact-" + strconv.Itoa(len(shown)) + "-" + strconv.Itoa(len(content)),
				"args":       map[string]any{},
				"result":     map[string]any{"artifact_id": artifact.id, "title": artifact.title, "format": artifact.format},
			})
		}
		if len(content) > 0 {
			shown = append(shown, liveMessage{Role: message.role, Content: content, CreatedAt: message.createdAt.UTC()})
		}
	}
	return shown, citations
}
