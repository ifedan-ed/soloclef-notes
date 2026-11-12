package publicchat

import (
	"context"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// untitled is what a snapshot with no `title` key is shown as. A key that is
// there and holds null is not this: `data.get("title", "Untitled")` only
// falls back for a missing key, and the model then refuses the null.
const untitled = "Untitled"

// The three roles a message may have: NewChatMessageRole's values. The model
// holds a StrEnum, which lax mode fills from exactly one of these strings --
// "USER" and " user" are refused.
var messageRoles = map[string]bool{"user": true, "assistant": true, "system": true}

// The five ways a shared file can be shown: PublicSharedFile.display's
// Literal, matched exactly.
var fileDisplays = map[string]bool{
	"image": true, "pdf": true, "audio": true, "video": true, "file": true,
}

// BuildPage is get_public_chat from the point where it has a live snapshot's
// data, followed by what FastAPI does with what it returns: validation
// against PublicChatResponse and serialisation by pydantic-core.
//
// The two are kept in the Python order -- build first, statements included,
// and validate afterwards -- so that a snapshot the model will refuse still
// issues the statements the route issued on its way there.
//
// The answer has five keys, in the model's order. `kind` is decided by the
// row and not by the data: a snapshot of a Studio output is "output" with no
// messages, whatever `snapshot_data` says about itself, and anything else is
// "chat" with no output.
func BuildPage(ctx context.Context, rows Rows, token string, found Snapshot, data *pythonjson.Object) (*pythonjson.Object, error) {
	var title any = untitled
	if stored, present := data.Get("title"); present {
		title = stored
	}
	createdAt := data.GetOrNil("snapshot_at")

	kind := "chat"
	var messages []any
	var output any
	if found.OutputID != nil {
		kind = "output"
		// `"messages": []`, a literal: the snapshot's own are not read.
		messages = []any{}
		shared, err := outputWithFiles(ctx, rows, token, data)
		if err != nil {
			return nil, err
		}
		output = shared
	} else {
		upcast, err := upcastLegacyFormats(ctx, rows, data)
		if err != nil {
			return nil, err
		}
		redacted, err := redactMessages(upcast)
		if err != nil {
			return nil, err
		}
		messages = redacted
		// The dict the route returns has no "output" key on this branch, so
		// the model's default applies: null.
	}

	// From here on it is the response model.
	thread, err := modelThread(title, createdAt)
	if err != nil {
		return nil, err
	}
	modelled := make([]any, 0, len(messages))
	for _, message := range messages {
		written, err := modelMessage(message)
		if err != nil {
			return nil, err
		}
		modelled = append(modelled, written)
	}
	writtenOutput, err := modelOutput(output)
	if err != nil {
		return nil, err
	}

	page := pythonjson.NewObject()
	page.Set("kind", kind)
	page.Set("thread", thread)
	page.Set("messages", modelled)
	page.Set("output", writtenOutput)
	// `snapshot.expires_at.isoformat()`, which the model parses back into the
	// same instant: a timestamptz column is always UTC when it reaches
	// Python, so this is the one datetime here that is always written "Z".
	if found.ExpiresAt == nil {
		page.Set("expires_at", nil)
	} else {
		page.Set("expires_at", pythonjson.PydanticDateTime(*found.ExpiresAt))
	}
	return page, nil
}

// modelThread is PublicChatThread: a title that is a string and a datetime.
// `snapshot_at` has no fallback, so a snapshot without one is refused.
func modelThread(title, createdAt any) (*pythonjson.Object, error) {
	text, err := modelText(title, "thread.title")
	if err != nil {
		return nil, err
	}
	moment, err := modelMoment(createdAt, "thread.created_at")
	if err != nil {
		return nil, err
	}
	thread := pythonjson.NewObject()
	thread.Set("title", text)
	thread.Set("created_at", moment)
	return thread, nil
}

// modelMessage is PublicChatMessage: `role`, `content`, `author` and
// `created_at`, in that order, and nothing else a stored message carries --
// its `id` is not a field of the model and so is not sent.
func modelMessage(value any) (*pythonjson.Object, error) {
	message, isObject := value.(*pythonjson.Object)
	if !isObject {
		return nil, refused("a message that is not an object")
	}
	role, isText := message.GetOrNil("role").(string)
	if !isText || !messageRoles[role] {
		return nil, refused("message.role")
	}
	// `content: Any` has no default, so the key has to be *there*; null is a
	// value of Any and is written as null.
	content, present := message.Get("content")
	if !present {
		return nil, refused("message.content")
	}
	author, err := modelAuthor(message.GetOrNil("author"))
	if err != nil {
		return nil, err
	}
	createdAt, err := modelMoment(message.GetOrNil("created_at"), "message.created_at")
	if err != nil {
		return nil, err
	}

	written := pythonjson.NewObject()
	written.Set("role", role)
	written.Set("content", modelAny(content))
	written.Set("author", author)
	written.Set("created_at", createdAt)
	return written, nil
}

// modelAuthor is `author: PublicAuthor | None = None`.
//
// By the time the model sees an author, redactMessages has already replaced
// every one that was an object with a display name and a null avatar, or
// with null. So the model is left to refuse two things: an author that was
// never an object (a string, a list, a number, a bool) and a display name
// that is not text.
func modelAuthor(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	author, isObject := value.(*pythonjson.Object)
	if !isObject {
		return nil, refused("message.author")
	}
	written := pythonjson.NewObject()
	for _, field := range []string{"display_name", "avatar_url"} {
		// Both fields are `str | None = None`.
		stored := author.GetOrNil(field)
		if stored == nil {
			written.Set(field, nil)
			continue
		}
		text, err := modelText(stored, "message.author."+field)
		if err != nil {
			return nil, err
		}
		written.Set(field, text)
	}
	return written, nil
}

// modelOutput is `output: PublicSharedOutput | None = None`: null, or the
// eight fields of the model in its order. Anything the stored output carries
// beyond those is dropped.
func modelOutput(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	output, isObject := value.(*pythonjson.Object)
	if !isObject {
		return nil, refused("an output that is not an object")
	}
	written := pythonjson.NewObject()
	// Four required strings. Empty is a string; missing and null are not.
	for _, field := range []string{"title", "kind", "format", "markdown"} {
		text, err := modelText(output.GetOrNil(field), "output."+field)
		if err != nil {
			return nil, err
		}
		written.Set(field, text)
	}
	artifactID, err := modelInt(output.GetOrNil("artifact_id"), "output.artifact_id")
	if err != nil {
		return nil, err
	}
	written.Set("artifact_id", artifactID)

	file, err := modelFile(output.GetOrNil("file"))
	if err != nil {
		return nil, err
	}
	written.Set("file", file)

	// `generation: int = 1`: the default is for a missing key only.
	generation := pythonjson.RawInt("1")
	if stored, present := output.Get("generation"); present {
		generation, err = modelInt(stored, "output.generation")
		if err != nil {
			return nil, err
		}
	}
	written.Set("generation", generation)

	// outputWithFiles always sets `files`, to an empty list or to rows this
	// package shaped itself, so there is nothing here for the model to
	// refuse and nothing of a stored `files` left to read.
	files, _ := output.GetOrNil("files").([]any)
	if files == nil {
		files = []any{}
	}
	written.Set("files", files)
	return written, nil
}

// modelFile is `file: PublicSharedFile | None = None`: the main file alone,
// as it was when the link was made, kept for apps older than `files`.
func modelFile(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	file, isObject := value.(*pythonjson.Object)
	if !isObject {
		return nil, refused("output.file")
	}
	filename, err := modelText(file.GetOrNil("filename"), "output.file.filename")
	if err != nil {
		return nil, err
	}
	mimeType, err := modelText(file.GetOrNil("mime_type"), "output.file.mime_type")
	if err != nil {
		return nil, err
	}
	size, err := modelInt(file.GetOrNil("size_bytes"), "output.file.size_bytes")
	if err != nil {
		return nil, err
	}
	display, isText := file.GetOrNil("display").(string)
	if !isText || !fileDisplays[display] {
		return nil, refused("output.file.display")
	}
	written := pythonjson.NewObject()
	written.Set("filename", filename)
	written.Set("mime_type", mimeType)
	written.Set("size_bytes", size)
	written.Set("display", display)
	return written, nil
}
