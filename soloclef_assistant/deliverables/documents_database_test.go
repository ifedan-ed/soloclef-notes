package deliverables

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// TestBuiltDocumentsOverARealDatabase saves a built document with its
// preview through the generated statements against the real constraints,
// and reads it back through the package that serves it to the page. It
// needs the throwaway Postgres database_test.go describes
// (databaseVariable), and truncates what it seeds.
func TestBuiltDocumentsOverARealDatabase(t *testing.T) {
	address := os.Getenv(databaseVariable)
	if address == "" {
		t.Skipf("%s is not set; this test needs a throwaway Postgres", databaseVariable)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, address)
	if err != nil {
		t.Fatalf("opening the database: %v", err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, seed); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	local, err := filestore.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	names := 0
	saver := Saver{
		Rows: Store{Pool: pool}, Files: local, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		// A different name per file, as in production: two files of one
		// artifact must never share a key.
		NewID: func() string { names++; return "0000000000000000000000000000000" + strconv.Itoa(names) },
		Index: func(context.Context, int64, string) error { return nil },
	}
	owner := Owner{ThreadID: 1, UserID: ben}
	read := savedRows{Pool: pool}
	deck, preview := []byte("PK\x03\x04 a deck"), []byte("%PDF-1.7 its slides")

	t.Run("a deck is two files: itself and the PDF the page shows", func(t *testing.T) {
		saved, err := saver.SaveDocument(ctx, owner, "d1/s1", BuiltDocument{Format: FormatPPTX, Title: "Cell division", Markdown: "# Cell division\n\nMitosis.", Data: deck, Preview: preview})
		if err != nil {
			t.Fatalf("saving: %v", err)
		}
		manifest, err := read.ManifestOf(ctx, ben, saved.ArtifactID)
		if err != nil || manifest == nil {
			t.Fatalf("manifest: %+v, %v", manifest, err)
		}
		if manifest.Format != "pptx" || manifest.Title != "Cell division" || manifest.Markdown != "# Cell division\n\nMitosis." || len(manifest.Files) != 2 {
			t.Fatalf("manifest = %+v", manifest)
		}
		// The primary first, as the page's viewer expects, then the preview.
		primary, shown := manifest.Files[0], manifest.Files[1]
		id := strconv.FormatInt(saved.ArtifactID, 10)
		if primary.Role != "primary" || primary.Filename != "cell-division.pptx" || primary.MimeType != documentFormats[FormatPPTX].mime || primary.ID != saved.Files[0].FileID ||
			primary.Key != "artifacts/"+ben+"/"+id+"/primary/00000000000000000000000000000001.pptx" {
			t.Errorf("primary = %+v", primary)
		}
		if shown.Role != "preview" || shown.Filename != "cell-division.pdf" || shown.MimeType != "application/pdf" || shown.ID != saved.Files[1].FileID ||
			shown.Key != "artifacts/"+ben+"/"+id+"/preview/00000000000000000000000000000002.pdf" || saved.Files[1].Role != "preview" || saved.Files[1].SizeBytes != len(preview) {
			t.Errorf("preview = %+v, saved %+v", shown, saved.Files)
		}
		for key, want := range map[string][]byte{primary.Key: deck, shown.Key: preview} {
			body, err := local.Open(ctx, key)
			if err != nil {
				t.Fatalf("%s: %v", key, err)
			}
			got, _ := io.ReadAll(body)
			_ = body.Close()
			if !bytes.Equal(got, want) {
				t.Errorf("%s holds %q", key, got)
			}
		}
		var verified bool
		if err := pool.QueryRow(ctx, "SELECT (metadata->'verification'->>'verified')::bool FROM artifacts WHERE id = $1", saved.ArtifactID).Scan(&verified); err != nil || !verified {
			t.Errorf("verified = %v, %v", verified, err)
		}
	})

	t.Run("a report with no preview is stored verified", func(t *testing.T) {
		saved, err := saver.SaveDocument(ctx, owner, "d1/s2", BuiltDocument{Format: FormatPDF, Title: "Report", Markdown: "# Report", Data: []byte("%PDF-1.7 x")})
		if err != nil || len(saved.Files) != 1 || saved.Files[0].Role != "primary" {
			t.Fatalf("saved %+v, %v", saved, err)
		}
		var verified bool
		if err := pool.QueryRow(ctx, "SELECT (metadata->'verification'->>'verified')::bool FROM artifacts WHERE id = $1", saved.ArtifactID).Scan(&verified); err != nil || !verified {
			t.Errorf("verified = %v, %v", verified, err)
		}
	})

	t.Run("a preview that cannot be stored leaves nothing behind", func(t *testing.T) {
		rows, stored := count(t, pool, "artifacts"), count(t, pool, "artifact_files")
		failing := saver
		failing.Files = &secondPutFails{Writer: local}
		if _, err := failing.SaveDocument(ctx, owner, "d1/s3", BuiltDocument{Format: FormatDOCX, Title: "Lost", Markdown: "x", Data: []byte("PK doc"), Preview: preview}); err == nil {
			t.Fatal("the save succeeded")
		}
		if count(t, pool, "artifacts") != rows || count(t, pool, "artifact_files") != stored {
			t.Error("rows were left by a save that failed")
		}
		// The primary had been stored before the preview failed, and is
		// removed again.
		writer := failing.Files.(*secondPutFails)
		if len(writer.put) != 1 || len(writer.removed) != 1 || writer.removed[0] != writer.put[0] {
			t.Errorf("put %v, removed %v", writer.put, writer.removed)
		}
	})
}

// secondPutFails is a file store whose second Put fails.
type secondPutFails struct {
	filestore.Writer
	put, removed []string
}

func (s *secondPutFails) Put(ctx context.Context, key string, data []byte, contentType string) error {
	if len(s.put) == 1 {
		return errors.New("the store is full")
	}
	s.put = append(s.put, key)
	return s.Writer.Put(ctx, key, data, contentType)
}

func (s *secondPutFails) Remove(ctx context.Context, key string) error {
	s.removed = append(s.removed, key)
	return s.Writer.Remove(ctx, key)
}
