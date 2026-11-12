package documentlifecycle

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/documentdeletion"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/modelchoices"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice/parserchoicetest"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/uploadvision"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_filestore"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

type noQueue struct{}

func (noQueue) Enqueue(context.Context, produce.Message) (string, error) { return "", nil }

// whole is a complete set of dependencies. The pool names an address
// nothing listens on: registering routes must not need the database.
func whole(t *testing.T) Dependencies {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://nobody@127.0.0.1:1/none")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store, err := filestore.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stores, err := filestore.NewStores(store)
	if err != nil {
		t.Fatal(err)
	}
	return Dependencies{
		Pool: pool, Queue: noQueue{}, Files: stores,
		Parsers:     parserchoicetest.Registry(t, parse.Local),
		Vectors:     documentdeletion.Qdrant{URL: "http://127.0.0.1:1", Collection: "chunks", HTTP: http.DefaultClient},
		Limits:      parserchoice.Limits{MaxFileBytes: 50 << 20, MaxFiles: 20},
		UploadStall: time.Minute,
		Vision:      uploadvision.Vision{Choices: modelchoices.Rows{Pool: pool}},
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// What cmd/api counts, limits and guards is Served(); what answers is what
// Register mounted. If the two differ, a route is either answered without
// being rate-limited or listed without existing.
func TestServedIsExactlyWhatRegisterMounts(t *testing.T) {
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	if err := Register(api, whole(t)); err != nil {
		t.Fatal(err)
	}
	var mounted []string
	for path, item := range api.OpenAPI().Paths {
		for method, operation := range map[string]*huma.Operation{
			http.MethodGet: item.Get, http.MethodPost: item.Post, http.MethodPut: item.Put,
			http.MethodPatch: item.Patch, http.MethodDelete: item.Delete,
		} {
			if operation != nil {
				mounted = append(mounted, method+" "+path)
			}
		}
	}
	served := Served()
	sort.Strings(mounted)
	sort.Strings(served)
	if strings.Join(mounted, "\n") != strings.Join(served, "\n") {
		t.Errorf("Register mounted\n%s\nand Served lists\n%s", strings.Join(mounted, "\n"), strings.Join(served, "\n"))
	}
}

func TestRegisterRefusesAnIncompleteSetOfDependencies(t *testing.T) {
	for name, remove := range map[string]func(*Dependencies){
		"no pool":             func(d *Dependencies) { d.Pool = nil },
		"no queue":            func(d *Dependencies) { d.Queue = nil },
		"no file stores":      func(d *Dependencies) { d.Files = nil },
		"no parser registry":  func(d *Dependencies) { d.Parsers = nil },
		"no logger":           func(d *Dependencies) { d.Logger = nil },
		"no per-file limit":   func(d *Dependencies) { d.Limits.MaxFileBytes = 0 },
		"no file-count limit": func(d *Dependencies) { d.Limits.MaxFiles = 0 },
		"no stall timeout":    func(d *Dependencies) { d.UploadStall = 0 },
		"no vector store":     func(d *Dependencies) { d.Vectors.URL = "" },
		"no collection":       func(d *Dependencies) { d.Vectors.Collection = "" },
	} {
		t.Run(name, func(t *testing.T) {
			dependencies := whole(t)
			remove(&dependencies)
			api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
			if err := Register(api, dependencies); err == nil {
				t.Fatal("registered anyway")
			}
			// Refused before anything was mounted: a tier that started with
			// half of these routes would answer 500 on the other half.
			if len(api.OpenAPI().Paths) != 0 {
				t.Errorf("%d paths were mounted before the refusal", len(api.OpenAPI().Paths))
			}
		})
	}
}
