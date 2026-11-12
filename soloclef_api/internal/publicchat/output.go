package publicchat

import (
	"context"
	"sort"
	"strconv"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
)

// defaultMimeType is what a file with an empty type is listed as:
// `file.mime_type or "application/octet-stream"`.
const defaultMimeType = "application/octet-stream"

// primaryRole is ArtifactFileRole.PRIMARY's value, the main file of an
// artifact, which the manifest lists first.
const primaryRole = "primary"

// outputWithFiles is _output_with_files: the Studio output the link shares,
// with every file of its artifact -- as long as the snapshot's allowlist
// names that artifact.
//
// The output's text was frozen when the link was made. Its *files* were not:
// they are read here, each time the page is opened, off the artifact as it is
// now, along with its current generation. The comment in
// app/public_sharing/output_files.py says why (a link made before this
// existed still shows its output whole); the consequence is that an output
// regenerated after it was shared serves its new files to every link that
// already existed.
//
// It returns what the route puts under "output" -- not yet validated; the
// response model does that (modelOutput).
func outputWithFiles(ctx context.Context, rows Rows, token string, data *pythonjson.Object) (any, error) {
	stored := data.GetOrNil("output")
	output, isObject := stored.(*pythonjson.Object)
	if !isObject {
		// Returned as it is: null is a page with no output, and anything
		// else is for the response model to refuse.
		return stored, nil
	}

	// `not isinstance(artifact_id, int) or artifact_id not in (… or [])`:
	// the allowlist is only consulted for an id that is an int, so a string
	// allowlist is a TypeError for an integer id and nothing at all for a
	// text one.
	artifactID := output.GetOrNil("artifact_id")
	shared := false
	id, isInt := pythonInt(artifactID)
	if isInt {
		var err error
		shared, err = containsInt(data.GetOrNil("artifact_ids"), id)
		if err != nil {
			return nil, err
		}
	}
	merged := copied(output)
	if !shared {
		// {**output, "files": []}: a stored `files` is replaced, and a
		// stored `generation` is left for the model to read.
		merged.Set("files", []any{})
		return merged, nil
	}

	if _, isBool := artifactID.(bool); isBool {
		// `Artifact.id == True`. SQLAlchemy writes a boolean compared with a
		// column as the SQL literal, not as a parameter, and Postgres has no
		// `integer = boolean` operator: the statement the Python route sends
		// is refused when it is parsed, every time. There is no statement
		// this tier could send that fails the same way and none it should
		// send that succeeds, so the failure is answered directly.
		return nil, raised("`artifacts.id = true` is a statement Postgres refuses")
	}
	if !id.IsInt64() {
		return nil, requestnumber.ErrOutOfRange
	}
	generation, files, err := sharedOutputFiles(ctx, rows, token, id.Int64())
	if err != nil {
		return nil, err
	}
	// As the integer json.loads would have handed the model, so that the one
	// reader (modelInt) serves a stored generation and this one alike.
	merged.Set("generation", pythonjson.RawInt(strconv.FormatInt(generation, 10)))
	merged.Set("files", files)
	return merged, nil
}

// sharedOutputFiles is output_files.py:shared_output_files: the artifact's
// generation and its files in the in-app manifest's shape, each with the URL
// this link serves it at.
//
// An artifact that is gone is generation 1 with no files, and the files are
// then not asked for at all.
func sharedOutputFiles(ctx context.Context, rows Rows, token string, artifactID int64) (int64, []any, error) {
	generation, found, err := rows.ArtifactGeneration(ctx, artifactID)
	if err != nil {
		return 0, nil, err
	}
	if !found {
		return 1, []any{}, nil
	}
	stored, err := rows.ArtifactFiles(ctx, artifactID)
	if err != nil {
		return 0, nil, err
	}
	// `artifact.generation or 1`. A check constraint keeps the column above
	// zero, so this is the Python expression's shape rather than a case.
	if generation == 0 {
		generation = 1
	}

	// _ordered: the main file first, then by id. `id` is the primary key, so
	// the order is total and the statement needs none of its own.
	sort.SliceStable(stored, func(i, j int) bool {
		leftIsPreview, rightIsPreview := stored[i].Role != primaryRole, stored[j].Role != primaryRole
		if leftIsPreview != rightIsPreview {
			return !leftIsPreview
		}
		return stored[i].ID < stored[j].ID
	})

	files := make([]any, 0, len(stored))
	for _, file := range stored {
		mimeType := file.MimeType
		if mimeType == "" {
			mimeType = defaultMimeType
		}
		listed := pythonjson.NewObject()
		listed.Set("file_id", file.ID)
		listed.Set("role", file.Role)
		listed.Set("filename", file.Filename)
		listed.Set("mime_type", mimeType)
		listed.Set("size_bytes", file.Size)
		// shared_file_url: the token is percent-encoded here, unlike in the
		// video route's audio URL, which is a bare f-string.
		listed.Set("content_url", publicPrefix+quoted(token)+
			"/artifacts/"+strconv.FormatInt(artifactID, 10)+
			"/files/"+strconv.FormatInt(file.ID, 10)+"/content")
		files = append(files, listed)
	}
	return generation, files, nil
}

// copied is `{**value}`: the same keys in the same order, in a new object.
func copied(value *pythonjson.Object) *pythonjson.Object {
	copy := pythonjson.NewObject()
	for _, key := range value.Keys() {
		copy.Set(key, value.GetOrNil(key))
	}
	return copy
}
