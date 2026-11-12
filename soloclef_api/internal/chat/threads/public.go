package threads

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// This file builds what a public link serves: a frozen copy of a thread,
// with everything a stranger must not see taken out.
//
// The copy's shape (snapshot_data) is read by the public page and by the
// routes that serve a link (internal/publicchat), and is the shape links
// made before this package have, so it is kept exactly.

// publicTools are the tool calls whose cards a public page draws. Every
// other call is dropped: its arguments and result were for the person who
// asked, and can hold anything a tool read.
var publicTools = map[string]bool{
	"generate_image":              true,
	"generate_podcast":            true,
	"generate_video_presentation": true,
	"save_artifact":               true,
	// Cards of two tools that no longer exist, still in older threads.
	"generate_report": true,
	"generate_resume": true,
}

// internalKeys never leave in a public copy, wherever they appear inside a
// kept card: where a file is stored, and who an account is.
var internalKeys = map[string]bool{
	"storage_key": true, "audio_storage_key": true, "storage_backend": true,
	"file_path": true, "audio_file": true,
	"user_id": true, "created_by_id": true, "author_id": true,
	"email": true, "user_email": true,
	"api_key": true, "token": true,
}

// markerWord is what every source chip saved in an answer's text holds:
// "[citation:<where>]", where <where> is a passage's id or an address
// (package cite).
const markerWord = "citation:"

// publicText is an answer's text without its source markers.
//
// Every marker is removed, whatever it points at. A passage id is
// meaningless to a stranger and names a private document; an address is
// public, but a chip the public page cannot open is a broken control, and
// which addresses a person's question led to is theirs to share in words.
//
// A marker is an opening bracket, in either style a model writes ("[" or
// "【"), an optional zero-width space, the word, and everything up to the
// closing bracket. The spaces and tabs in front of it go with it, so that
// "x [citation:1]." becomes "x." and not "x .".
//
// Nothing else in the text is touched. The cleaning this replaces also
// collapsed every run of spaces and stripped the spaces around every line
// break, in the whole answer, which flattened the indentation of code
// blocks and nested lists in every public copy.
//
// It is a scan and not a regular expression because it runs over every
// answer of a shared thread: as an expression it was a third of the time a
// share took (BenchmarkShare).
func publicText(text string) string {
	at := strings.Index(text, markerWord)
	if at < 0 {
		return strings.TrimSpace(text)
	}
	var b strings.Builder
	b.Grow(len(text))
	rest := text
	for at >= 0 {
		start, opened := markerStart(rest[:at])
		length := -1
		if opened {
			// A marker is on one line: a bracket that closes on a later
			// line closes something else.
			if length = strings.IndexAny(rest[at:], "]】\n"); length >= 0 && rest[at+length] == '\n' {
				length = -1
			}
		}
		if length < 0 {
			// The word in prose, or a marker never closed: text, kept.
			b.WriteString(rest[:at+len(markerWord)])
			rest = rest[at+len(markerWord):]
			at = strings.Index(rest, markerWord)
			continue
		}
		b.WriteString(strings.TrimRight(rest[:start], " \t"))
		// Past the closing bracket, which is one or three bytes long.
		_, size := utf8.DecodeRuneInString(rest[at+length:])
		rest = rest[at+length+size:]
		// A marker straight after a marker: its spaces were already cut.
		at = strings.Index(rest, markerWord)
	}
	b.WriteString(rest)
	return strings.TrimSpace(b.String())
}

// markerStart reports where a marker ending in the word begins, given the
// text before the word: at an opening bracket, with at most a zero-width
// space between the two.
func markerStart(before string) (int, bool) {
	before = strings.TrimSuffix(before, "\u200b")
	for _, bracket := range []string{"[", "【"} {
		if strings.HasSuffix(before, bracket) {
			return len(before) - len(bracket), true
		}
	}
	return 0, false
}

// withoutInternalKeys is value with every internal key removed, at any
// depth.
func withoutInternalKeys(value any) any {
	switch typed := value.(type) {
	case *pythonjson.Object:
		out := pythonjson.NewObject()
		for _, key := range typed.Keys() {
			if !internalKeys[key] {
				out.Set(key, withoutInternalKeys(typed.GetOrNil(key)))
			}
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = withoutInternalKeys(item)
		}
		return out
	}
	return value
}

// publicParts is a message's content as a public page may show it: its
// text and the cards of publicTools, nothing else. Reasoning, status notes
// and every other tool call are dropped.
func publicParts(content []byte) ([]any, error) {
	decoded, err := pythonjson.Decode(content)
	if err != nil {
		return nil, fmt.Errorf("a message's content is not readable: %w", err)
	}
	textPart := func(text string) (*pythonjson.Object, bool) {
		clean := publicText(text)
		if clean == "" {
			return nil, false
		}
		part := pythonjson.NewObject()
		part.Set("type", "text")
		part.Set("text", clean)
		return part, true
	}
	out := []any{}
	switch typed := decoded.(type) {
	case string:
		// A message saved as bare text, as the oldest rows are.
		if part, ok := textPart(typed); ok {
			out = append(out, part)
		}
	case []any:
		for _, item := range typed {
			part, isObject := item.(*pythonjson.Object)
			if !isObject {
				continue
			}
			switch part.GetString("type", "") {
			case "text":
				if kept, ok := textPart(part.GetString("text", "")); ok {
					out = append(out, kept)
				}
			case "tool-call":
				if publicTools[part.GetString("toolName", "")] {
					out = append(out, withoutInternalKeys(part))
				}
			}
		}
	}
	return out, nil
}

// wholeNumber reads a JSON integer that fits a row id.
func wholeNumber(value any) (int64, bool) {
	raw, isInteger := value.(pythonjson.RawInt)
	if !isInteger {
		return 0, false
	}
	n, err := strconv.ParseInt(string(raw), 10, 32)
	return n, err == nil && n > 0
}

// card is one kept tool-call part and its result, if the result is an
// object.
type card struct {
	part   *pythonjson.Object
	result *pythonjson.Object
}

// cardsOf finds the tool-call parts among sanitised parts.
func cardsOf(parts []any) []card {
	var cards []card
	for _, item := range parts {
		part, _ := item.(*pythonjson.Object)
		if part == nil || part.GetString("type", "") != "tool-call" {
			continue
		}
		result, _ := part.GetOrNil("result").(*pythonjson.Object)
		cards = append(cards, card{part: part, result: result})
	}
	return cards
}

// publicMessage is one message of a public copy while it is being built.
type publicMessage struct {
	id        int64
	role      string
	parts     []any
	author    any
	createdAt time.Time
}

// referenced lists the podcasts and artifacts the messages' cards name, in
// first-seen order. They are candidates only: the ids come from content,
// and the store decides which are really this account's.
func referenced(messages []publicMessage) (podcasts, artifacts []int64) {
	seenPodcast, seenArtifact := map[int64]bool{}, map[int64]bool{}
	for _, message := range messages {
		for _, c := range cardsOf(message.parts) {
			if c.result == nil {
				continue
			}
			if id, ok := wholeNumber(c.result.GetOrNil("artifact_id")); ok && !seenArtifact[id] {
				seenArtifact[id] = true
				artifacts = append(artifacts, id)
			}
			if c.part.GetString("toolName", "") != "generate_podcast" {
				continue
			}
			if id, ok := wholeNumber(c.result.GetOrNil("podcast_id")); ok && !seenPodcast[id] {
				seenPodcast[id] = true
				podcasts = append(podcasts, id)
			}
		}
	}
	return podcasts, artifacts
}

// embedPodcasts marks each finished podcast's card ready and gives it the
// artifact its audio is served from, and returns the podcasts' own entries
// for the copy, with the artifacts they add.
//
// A card saved while its podcast was still being made says so for ever in
// the thread; the public copy is frozen now, so it says what is true now.
func embedPodcasts(messages []publicMessage, ready map[int64]Podcast) (entries []any, artifacts []int64, err error) {
	entries = []any{}
	embedded := map[int64]bool{}
	for _, message := range messages {
		for _, c := range cardsOf(message.parts) {
			if c.result == nil || c.part.GetString("toolName", "") != "generate_podcast" {
				continue
			}
			id, ok := wholeNumber(c.result.GetOrNil("podcast_id"))
			podcast, isReady := ready[id]
			if !ok || !isReady {
				continue
			}
			c.result.Set("status", "ready")
			if podcast.ArtifactID != nil {
				c.result.Set("artifact_id", *podcast.ArtifactID)
			}
			if embedded[id] {
				continue
			}
			embedded[id] = true
			entry := pythonjson.NewObject()
			entry.Set("original_id", podcast.ID)
			entry.Set("title", podcast.Title)
			var transcript any
			if len(podcast.Transcript) > 0 {
				if transcript, err = pythonjson.Decode(podcast.Transcript); err != nil {
					return nil, nil, fmt.Errorf("podcast %d's transcript is not readable: %w", podcast.ID, err)
				}
			}
			entry.Set("transcript", transcript)
			if podcast.ArtifactID != nil {
				entry.Set("artifact_id", *podcast.ArtifactID)
				artifacts = append(artifacts, *podcast.ArtifactID)
			} else {
				entry.Set("artifact_id", nil)
			}
			entries = append(entries, entry)
		}
	}
	return entries, artifacts, nil
}

// publicAuthor is all a stranger learns of an account: a display name.
func publicAuthor(displayName string) *pythonjson.Object {
	author := pythonjson.NewObject()
	author.Set("display_name", displayName)
	author.Set("avatar_url", nil)
	return author
}

// messageEntries is the messages as the copy stores them.
func messageEntries(messages []publicMessage) []any {
	out := make([]any, len(messages))
	for i, message := range messages {
		entry := pythonjson.NewObject()
		entry.Set("id", message.id)
		entry.Set("role", message.role)
		entry.Set("content", message.parts)
		entry.Set("author", message.author)
		entry.Set("created_at", pythonjson.ISOFormat(message.createdAt))
		out[i] = entry
	}
	return out
}

// contentHash is what makes "same content, same address" true: the hash of
// every message's id, role and public content, in id order.
//
// It is computed the way the routes before this package computed it
// (sorted keys, no spaces, non-ASCII escaped), so a thread shared before
// and shared again unchanged keeps its address across the move, where its
// public text is the same. It is not the same for an answer the old
// cleaning rewrote (see publicText): such a thread gets a new link, and its
// old one goes on working.
func contentHash(messages []publicMessage) (string, error) {
	ordered := append([]publicMessage(nil), messages...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].id < ordered[j].id })
	normalised := make([]any, len(ordered))
	for i, message := range ordered {
		entry := pythonjson.NewObject()
		entry.Set("id", message.id)
		entry.Set("role", message.role)
		entry.Set("content", message.parts)
		normalised[i] = entry
	}
	var b strings.Builder
	if err := writeCanonical(&b, normalised); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:]), nil
}

// writeCanonical writes value with object keys sorted, no spaces, and
// everything outside ASCII escaped.
func writeCanonical(b *strings.Builder, value any) error {
	switch typed := value.(type) {
	case []any:
		b.WriteByte('[')
		for i, item := range typed {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeCanonical(b, item); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case *pythonjson.Object:
		keys := append([]string(nil), typed.Keys()...)
		sort.Strings(keys)
		b.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeCanonical(b, key); err != nil {
				return err
			}
			b.WriteByte(':')
			if err := writeCanonical(b, typed.GetOrNil(key)); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		// A scalar: MarshalColumn is the ASCII-escaping writer, and has
		// no separators to add around one value.
		raw, err := pythonjson.MarshalColumn(value)
		if err != nil {
			return err
		}
		b.Write(raw)
	}
	return nil
}

// linkData is a public copy's stored document.
func linkData(title string, takenAt time.Time, author any, messages []publicMessage, podcasts []any, artifactIDs []int64) ([]byte, error) {
	ids := make([]any, len(artifactIDs))
	for i, id := range artifactIDs {
		ids[i] = id
	}
	data := pythonjson.NewObject()
	data.Set("title", title)
	data.Set("snapshot_at", pythonjson.ISOFormat(takenAt))
	data.Set("author", author)
	data.Set("messages", messageEntries(messages))
	data.Set("podcasts", podcasts)
	data.Set("artifact_ids", ids)
	return pythonjson.Marshal(data)
}
