package documentlifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/uploads"
)

// folderAnswer is what the folder upload answered.
type folderAnswer struct {
	Message              string  `json:"message"`
	Status               string  `json:"status"`
	RootFolderID         int64   `json:"root_folder_id"`
	FileCount            int     `json:"file_count"`
	DocumentIDs          []int64 `json:"document_ids"`
	UnchangedDocumentIDs []int64 `json:"unchanged_document_ids"`
}

// uploadFolder posts a folder: its name, the root folder when one is named
// (0 for none), the parser, and the files as path-to-content pairs in order.
func (s *stack) uploadFolder(folder string, root int64, parser string, files ...[2]string) (int, folderAnswer, string) {
	s.t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	relative := make([]string, len(files))
	for index, file := range files {
		relative[index] = file[0]
	}
	encoded, _ := json.Marshal(relative)
	_ = writer.WriteField("folder_name", folder)
	_ = writer.WriteField("relative_paths", string(encoded))
	if root != 0 {
		_ = writer.WriteField("root_folder_id", fmt.Sprint(root))
	}
	if parser != "" {
		_ = writer.WriteField("parser", parser)
	}
	for _, file := range files {
		name := file[0][strings.LastIndex(file[0], "/")+1:]
		part, _ := writer.CreateFormFile("files", name)
		_, _ = io.WriteString(part, file[1])
	}
	_ = writer.Close()
	status, raw := s.call(http.MethodPost, uploads.FolderPath, writer.FormDataContentType(), &body)
	var answer folderAnswer
	if status == http.StatusOK {
		if err := json.Unmarshal([]byte(raw), &answer); err != nil {
			s.t.Fatalf("%v: %s", err, raw)
		}
	}
	return status, answer, raw
}

// folderPathOf is a folder's names from the top, joined with "/".
func (s *stack) folderPathOf(folderID int64) string {
	s.t.Helper()
	var path string
	err := s.pool.QueryRow(context.Background(), `
		WITH RECURSIVE up AS (
		    SELECT id, name, parent_id, 0 AS level FROM folders WHERE id = $1
		    UNION ALL
		    SELECT f.id, f.name, f.parent_id, up.level + 1 FROM folders f JOIN up ON f.id = up.parent_id
		)
		SELECT string_agg(name, '/' ORDER BY level DESC) FROM up`, folderID).Scan(&path)
	if err != nil {
		s.t.Fatal(err)
	}
	return path
}

// The folder upload against real services: the SQL the stand-ins assume
// (the folders found or made, the document's type, folder and metadata, the
// file row), the objects in MinIO, the messages on Redis, and what three
// requests at once do to one folder.
func TestAFolderUploadAgainstRealServices(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	const folder = "Field notes 2026"
	var root int64

	t.Run("the folders are made, each file is a pending document in its folder, and the jobs are queued", func(t *testing.T) {
		started := time.Now()
		status, answer, raw := s.uploadFolder(folder, 0, "datalab",
			[2]string{"index.md", "# Field notes\n"},
			[2]string{"sites/north/survey.pdf", "%PDF-1.7 the north survey"},
			[2]string{"sites/south.txt", "south site, first visit"})
		if status != http.StatusOK || len(answer.DocumentIDs) != 3 || answer.FileCount != 3 || answer.Status != "processing" {
			t.Fatalf("answered %d %s", status, raw)
		}
		t.Logf("a folder of 3 files in 3 folders was stored, recorded and queued in %s", time.Since(started).Round(time.Millisecond))
		root = answer.RootFolderID

		// The top folder: at the top level, marked as kept in step, with
		// the parser its files were sent to.
		var name string
		var parent *int64
		var watched, parser *string
		if err := s.pool.QueryRow(ctx, `SELECT name, parent_id, metadata ->> 'watched', metadata ->> 'parser' FROM folders WHERE id = $1 AND user_id = $2`,
			root, owner).Scan(&name, &parent, &watched, &parser); err != nil {
			t.Fatal(err)
		}
		if name != folder || parent != nil || watched == nil || *watched != "true" || parser == nil || *parser != "datalab" {
			t.Errorf("the top folder is %q under %v, watched %v, parser %v", name, parent, watched, parser)
		}

		for index, want := range []struct{ relative, title, folders, content string }{
			{"index.md", "index.md", folder, "# Field notes\n"},
			{"sites/north/survey.pdf", "survey.pdf", folder + "/sites/north", "%PDF-1.7 the north survey"},
			{"sites/south.txt", "south.txt", folder + "/sites", "south site, first visit"},
		} {
			id := answer.DocumentIDs[index]
			var title, kind, state, chosen, identity, rawHash, filePath string
			var folderID int64
			var mtime float64
			if err := s.pool.QueryRow(ctx, `
				SELECT title, document_type::text, status ->> 'state', parser, unique_identifier_hash, folder_id,
				       document_metadata ->> 'raw_file_hash', document_metadata ->> 'file_path',
				       (document_metadata ->> 'mtime')::float8
				FROM documents WHERE id = $1`, id).Scan(&title, &kind, &state, &chosen, &identity, &folderID, &rawHash, &filePath, &mtime); err != nil {
				t.Fatal(err)
			}
			if title != want.title || kind != "LOCAL_FOLDER_FILE" || state != "pending" || chosen != "datalab" ||
				identity != uploads.FolderIdentity(folder, want.relative, owner) || filePath != want.relative ||
				rawHash != digest(want.content) || time.Since(time.Unix(int64(mtime), 0)) > time.Minute {
				t.Errorf("%s was recorded as %q %s %s parser %s path %q mtime %v", want.relative, title, kind, state, chosen, filePath, mtime)
			}
			if got := s.folderPathOf(folderID); got != want.folders {
				t.Errorf("%s is in %q, want %q", want.relative, got, want.folders)
			}
			_, key, checksum, size := s.newestFile(id)
			if content, found := s.object(key); !found || content != want.content || checksum != digest(want.content) || size != int64(len(want.content)) {
				t.Errorf("%s is stored under %q as %q", want.relative, key, content)
			}
		}
		if queued := s.queued(); fmt.Sprint(queued) != fmt.Sprint(answer.DocumentIDs) {
			t.Errorf("queued %v, want %v", queued, answer.DocumentIDs)
		}
	})

	t.Run("syncing again indexes only the file that changed, and records the others as seen", func(t *testing.T) {
		var before float64
		if err := s.pool.QueryRow(ctx, `SELECT (document_metadata ->> 'mtime')::float8 FROM documents WHERE unique_identifier_hash = $1`,
			uploads.FolderIdentity(folder, "index.md", owner)).Scan(&before); err != nil {
			t.Fatal(err)
		}
		// The indexer finished all three.
		if _, err := s.pool.Exec(ctx, `UPDATE documents SET status = '{"state": "ready"}' WHERE folder_id IS NOT NULL AND document_type = 'LOCAL_FOLDER_FILE'`); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
		status, answer, raw := s.uploadFolder(folder, root, "datalab",
			[2]string{"index.md", "# Field notes\n"},
			[2]string{"sites/south.txt", "south site, second visit"})
		if status != http.StatusOK || len(answer.DocumentIDs) != 1 || len(answer.UnchangedDocumentIDs) != 1 || answer.RootFolderID != root {
			t.Fatalf("answered %d %s", status, raw)
		}
		var after float64
		var files int
		if err := s.pool.QueryRow(ctx, `
			SELECT (document_metadata ->> 'mtime')::float8, (SELECT count(*) FROM document_files WHERE document_id = d.id)
			FROM documents d WHERE id = $1`, answer.UnchangedDocumentIDs[0]).Scan(&after, &files); err != nil {
			t.Fatal(err)
		}
		if !(after > before) || files != 1 {
			t.Errorf("the unchanged file: seen at %v then %v, %d file rows", before, after, files)
		}
		if changed := s.document(answer.DocumentIDs[0]); changed.state != "pending" || changed.files != 2 || changed.contentHash != digest("south site, second visit") {
			t.Errorf("the changed file is %+v", changed)
		}
		if queued := s.queued(); fmt.Sprint(queued) != fmt.Sprint(answer.DocumentIDs) {
			t.Errorf("queued %v, want only the changed file %v", queued, answer.DocumentIDs)
		}
	})

	// The web app sends a folder as three requests at once, none of which
	// knows the top folder's id the first time.
	t.Run("three requests at once share one top folder and one of each folder under it", func(t *testing.T) {
		const together = "Sent together"
		answers := make([]folderAnswer, 3)
		statuses := make([]int, 3)
		var wait sync.WaitGroup
		for index := range answers {
			wait.Add(1)
			go func() {
				defer wait.Done()
				statuses[index], answers[index], _ = s.uploadFolder(together, 0, "",
					[2]string{fmt.Sprintf("shared/deep/file%d.md", index), fmt.Sprintf("file %d", index)},
					[2]string{fmt.Sprintf("top%d.md", index), fmt.Sprintf("top %d", index)})
			}()
		}
		wait.Wait()
		for index, status := range statuses {
			if status != http.StatusOK || answers[index].RootFolderID != answers[0].RootFolderID || len(answers[index].DocumentIDs) != 2 {
				t.Fatalf("request %d answered %d %+v", index, status, answers[index])
			}
		}
		rows, err := s.pool.Query(ctx, `
			WITH RECURSIVE tree AS (
			    SELECT id, name FROM folders WHERE id = $1
			    UNION ALL
			    SELECT f.id, f.name FROM folders f JOIN tree ON f.parent_id = tree.id
			)
			SELECT name FROM tree`, answers[0].RootFolderID)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatal(err)
			}
			names = append(names, name)
		}
		sort.Strings(names)
		if want := "Sent together deep shared"; strings.Join(names, " ") != want {
			t.Errorf("the tree holds %v, want exactly one of each: %s", names, want)
		}
		if queued := s.queued(); len(queued) != 6 {
			t.Errorf("%d jobs queued, want 6", len(queued))
		}
	})

	t.Run("a root folder that is another account's refuses the upload, as one never made does, and stores nothing", func(t *testing.T) {
		var before int
		_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM document_files`).Scan(&before)
		var others int64
		if err := s.pool.QueryRow(ctx, `
			INSERT INTO folders (user_id, name, position) VALUES ($1, 'Theirs', 'a0') RETURNING id`, stranger).Scan(&others); err != nil {
			t.Fatal(err)
		}
		for name, root := range map[string]int64{"another account's folder": others, "a folder never made": others + 1000} {
			status, _, raw := s.uploadFolder(folder, root, "", [2]string{"stray.md", "text"})
			if status != http.StatusNotFound || !strings.Contains(raw, uploads.RootFolderAbsent) {
				t.Errorf("%s: answered %d %s", name, status, raw)
			}
		}
		var after, theirs int
		_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM document_files`).Scan(&after)
		_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM documents WHERE folder_id = $1`, others).Scan(&theirs)
		if after != before || theirs != 0 {
			t.Errorf("file rows %d then %d; %d documents were put in the other account's folder", before, after, theirs)
		}
	})

	t.Run("ten 2 MB files, the web app's batch, measured", func(t *testing.T) {
		files := make([][2]string, 10)
		for index := range files {
			files[index] = [2]string{fmt.Sprintf("batch/part%d/file%d.md", index%3, index), strings.Repeat(fmt.Sprintf("line %d of a document\n", index), 2<<20/22)}
		}
		started := time.Now()
		status, answer, raw := s.uploadFolder("Measured", 0, "", files...)
		took := time.Since(started)
		if status != http.StatusOK || len(answer.DocumentIDs) != 10 {
			t.Fatalf("answered %d %s", status, raw)
		}
		t.Logf("10 files, %d MB, into 4 folders: stored in MinIO, recorded and queued in %s", 10*len(files[0][1])>>20, took.Round(time.Millisecond))
		s.queued()
	})
}
