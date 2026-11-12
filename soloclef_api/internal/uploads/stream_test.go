package uploads

import (
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice"
)

// pattern yields `size` bytes of a repeating pattern without holding them:
// the test's own side of the upload must not be what the memory measured.
type pattern struct {
	left int64
}

func (p *pattern) Read(buffer []byte) (int, error) {
	if p.left == 0 {
		return 0, io.EOF
	}
	if int64(len(buffer)) > p.left {
		buffer = buffer[:p.left]
	}
	for index := range buffer {
		buffer[index] = byte('a' + index%26)
	}
	p.left -= int64(len(buffer))
	return len(buffer), nil
}

// streamed is a multipart upload of one file of `size` bytes, written
// through a pipe as the client sends it, so neither end holds the file.
func streamed(name string, size int64) (io.Reader, string) {
	reader, writer := io.Pipe()
	form := multipart.NewWriter(writer)
	go func() {
		err := form.WriteField(fieldParser, "local")
		if err == nil {
			var target io.Writer
			if target, err = form.CreateFormFile(fieldFiles, name); err == nil {
				_, err = io.Copy(target, &pattern{left: size})
			}
		}
		if err == nil {
			err = form.Close()
		}
		writer.CloseWithError(err)
	}()
	return reader, form.FormDataContentType()
}

// The standard: an upload is never held in memory. A 256 MB file goes
// through the route into the store while the heap is sampled; the heap may
// grow by the copy buffers and the HTTP machinery and by nothing that
// scales with the file.
func TestAnUploadIsStreamedNotHeld(t *testing.T) {
	if testing.Short() {
		t.Skip("writes 256 MB to the temporary directory")
	}
	const size = 256 << 20
	// allowed is far above what streaming needs (well under a megabyte of
	// buffers) and far below the file: holding even one copy of it would
	// be sixteen times over.
	const allowed = 16 << 20
	s := newSiteWith(t, parserchoice.Limits{MaxFileBytes: size, MaxFiles: 1})

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	var peak atomic.Uint64
	stop := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		var now runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
				runtime.ReadMemStats(&now)
				if now.HeapAlloc > peak.Load() {
					peak.Store(now.HeapAlloc)
				}
			}
		}
	}()

	body, contentType := streamed("large.pdf", size)
	started := time.Now()
	got := s.post(body, contentType)
	took := time.Since(started)
	close(stop)
	<-sampled

	if got.status != http.StatusOK {
		t.Fatalf("status %d: %s", got.status, got.message)
	}
	held := s.titled("large.pdf")
	if held == nil || held.originals[0].Size != size {
		t.Fatalf("recorded %+v", held)
	}
	grew := int64(peak.Load()) - int64(before.HeapAlloc)
	t.Logf("256 MB uploaded in %s (%.0f MB/s); the heap peaked %.1f MB above where it began",
		took.Round(time.Millisecond), float64(size>>20)/took.Seconds(), float64(grew)/(1<<20))
	if grew > allowed {
		t.Errorf("the heap grew by %d bytes during a %d byte upload; the file is being held, not streamed", grew, size)
	}
}

// BenchmarkUpload is the whole route for one file: the multipart read, the
// streamed write to a local store with its SHA-256, the rows and the queue
// (both stand-ins). MB/s is the throughput; B/op is what an upload
// allocates, which must not grow with the file.
//
// Measured on the development host (AMD EPYC 9645, 20 threads), 2026-11-11,
// with `go test -run xxx -bench Upload -benchmem ./internal/uploads/`:
//
//	1 MB     4.4 ms/op   239 MB/s    93 KB/op    316 allocs/op
//	64 MB    178 ms/op   378 MB/s   134 KB/op   2363 allocs/op
//
// Sixty-four times the bytes cost 40 KB more: the allocation is the
// request's own, not the file's. TestAnUploadIsStreamedNotHeld puts the
// same fact as a limit: a 256 MB upload moved the heap by 0.3 MB.
func BenchmarkUpload(b *testing.B) {
	for _, megabytes := range []int64{1, 64} {
		b.Run(fmt.Sprintf("%dMB", megabytes), func(b *testing.B) {
			size := megabytes << 20
			s := newSiteWith(b, parserchoice.Limits{MaxFileBytes: size, MaxFiles: 1})
			b.SetBytes(size)
			b.ReportAllocs()
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				// A new name each time: the same name with the same bytes
				// would be a duplicate, which is a different path.
				body, contentType := streamed(fmt.Sprintf("file-%d.pdf", iteration), size)
				if got := s.post(body, contentType); got.status != http.StatusOK {
					b.Fatalf("status %d: %s", got.status, got.message)
				}
			}
		})
	}
}
