package documents

import (
	"errors"
	"math"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// errMetadataIsNotAMapping is a `document_metadata` column
// DocumentWithChunksRead cannot be built from.
//
// The model declares `document_metadata: dict` with no default and no None,
// so a column that is SQL NULL, the JSON scalar null, a list, a string, a
// number or a boolean fails its validation. get_document_by_chunk_id builds
// the model inside its own try, so that is the 500 the route raises itself --
// with the CORS headers -- and not the one a response model raises after the
// route has returned. It is tested after all three chunk
// statements, because the model is the last thing the route builds.
var errMetadataIsNotAMapping = errors.New("documents: document_metadata is not an object")

// buildDocumentWithChunks renders DocumentWithChunksRead
// (app/schemas/documents.py) as get_document_by_chunk_id builds it.
//
// `content_preview`, `folder_id` and `status` are fixed: this answer is the
// document as a citation opens it, and the reader takes those three from
// the document list. The keys stay so the two answers have one shape.
func buildDocumentWithChunks(record Record, passages []Passage, total, start int64) (*pythonjson.Object, error) {
	metadata, err := readMetadata(record.Metadata)
	if err != nil {
		return nil, err
	}

	rendered := pythonjson.NewObject()
	rendered.Set("id", record.ID)
	rendered.Set("title", record.Title)
	rendered.Set("document_type", record.DocumentType)
	rendered.Set("document_metadata", metadata)
	rendered.Set("content", record.Content)
	rendered.Set("content_preview", "")
	rendered.Set("content_hash", record.ContentHash)
	rendered.Set("unique_identifier_hash", textOrNil(record.UniqueIdentifierHash))
	rendered.Set("created_at", pythonjson.PydanticDateTime(record.CreatedAt))
	if record.UpdatedAt == nil {
		rendered.Set("updated_at", nil)
	} else {
		rendered.Set("updated_at", pythonjson.PydanticDateTime(*record.UpdatedAt))
	}
	rendered.Set("folder_id", nil)
	rendered.Set("status", nil)
	rendered.Set("chunks", buildPassages(passages))
	rendered.Set("total_chunks", total)
	rendered.Set("chunk_start_index", start)
	return rendered, nil
}

// buildChunkPage renders PaginatedResponse[ChunkRead]: items, total, page,
// page_size, has_more.
func buildChunkPage(passages []Passage, total, page, pageSize int64, more bool) *pythonjson.Object {
	body := pythonjson.NewObject()
	body.Set("items", buildPassages(passages))
	body.Set("total", total)
	body.Set("page", page)
	body.Set("page_size", pageSize)
	body.Set("has_more", more)
	return body
}

// buildPassages renders each chunk as ChunkRead, whose field order is not the
// order anything is written in: the class is declared
// `ChunkRead(ChunkBase, IDModel, TimestampModel)` and pydantic collects fields
// from the last base to the first, so `created_at` comes first, then `id`,
// then ChunkBase's `content` and `document_id`, then the two the class
// declares itself.
func buildPassages(passages []Passage) []any {
	items := make([]any, 0, len(passages))
	for _, passage := range passages {
		item := pythonjson.NewObject()
		item.Set("created_at", pythonjson.PydanticDateTime(passage.CreatedAt))
		item.Set("id", passage.ID)
		item.Set("content", passage.Content)
		item.Set("document_id", passage.DocumentID)
		item.Set("page_start", numberOrNil(passage.PageStart))
		item.Set("page_end", numberOrNil(passage.PageEnd))
		items = append(items, item)
	}
	return items
}

// buildVersionList is the list comprehension list_document_versions returns.
// The route has no response model, so this is rendered by Starlette's
// json.dumps and a datetime is the isoformat() the route calls itself:
// "+00:00" where the two chunk routes write "Z".
func buildVersionList(versions []VersionSummary) []any {
	items := make([]any, 0, len(versions))
	for _, version := range versions {
		item := pythonjson.NewObject()
		item.Set("version_number", version.Number)
		item.Set("title", textOrNil(version.Title))
		item.Set("content_hash", version.ContentHash)
		item.Set("created_at", pythonjson.ISOFormat(version.CreatedAt))
		items = append(items, item)
	}
	return items
}

// buildVersion is the dict get_document_version returns, in the order it
// writes its keys: the markdown comes before the timestamp here, and is
// absent from the list above.
func buildVersion(version Version) *pythonjson.Object {
	item := pythonjson.NewObject()
	item.Set("version_number", version.Number)
	item.Set("title", textOrNil(version.Title))
	item.Set("content_hash", version.ContentHash)
	item.Set("source_markdown", textOrNil(version.SourceMarkdown))
	item.Set("created_at", pythonjson.ISOFormat(version.CreatedAt))
	return item
}

// buildRestored is restore_document_version's answer. Both numbers are the
// path's own, as the route read them.
func buildRestored(documentID, versionNumber int64) *pythonjson.Object {
	body := pythonjson.NewObject()
	body.Set("message", restoredMessage(versionNumber))
	body.Set("document_id", documentID)
	body.Set("restored_version", versionNumber)
	return body
}

// readMetadata reads the json column as pydantic reads it into a bare `dict`
// and writes it back.
//
// Two things about the writing are pydantic-core's and not json.dumps':
//
//   - a float is written through ryu, so 1e16 is "1e16" and 0.00001 is
//     "0.00001" (internal/pythonjson.PydanticFloat);
//   - a float that is not finite is written as `null`. json.loads reads a
//     number too large for a float64 as infinity, the column is a `json` one
//     and keeps whatever number was written, and pydantic's default for a
//     non-finite float *inside an untyped dict* is null -- it refuses one only
//     in a field declared as a float. internal/pythonjson.PydanticValues
//     keeps the infinity and its Marshal then refuses it, which is right for
//     that declared field and a 500 where this route answers 200; so the
//     infinities are taken out here, before it sees them.
func readMetadata(stored []byte) (any, error) {
	if len(stored) == 0 {
		return nil, errMetadataIsNotAMapping
	}
	value, err := pythonjson.Decode(stored)
	if err != nil {
		return nil, err
	}
	object, isObject := value.(*pythonjson.Object)
	if !isObject {
		return nil, errMetadataIsNotAMapping
	}
	return pythonjson.PydanticValues(nullNonFinite(object)), nil
}

// nullNonFinite copies a decoded value with every infinity replaced by null,
// keeping every key in its place.
func nullNonFinite(value any) any {
	switch typed := value.(type) {
	case float64:
		if math.IsInf(typed, 0) || math.IsNaN(typed) {
			return nil
		}
		return typed
	case []any:
		copied := make([]any, 0, len(typed))
		for _, item := range typed {
			copied = append(copied, nullNonFinite(item))
		}
		return copied
	case *pythonjson.Object:
		copied := pythonjson.NewObject()
		for _, key := range typed.Keys() {
			copied.Set(key, nullNonFinite(typed.GetOrNil(key)))
		}
		return copied
	}
	return value
}

func textOrNil(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func numberOrNil(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}
