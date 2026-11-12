package artifacts

import (
	"io"
	"net/http"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// BenchmarkStreamingAFile is the cost of sending the 32 MB film to a client
// that reads as fast as it can, over a real TCP connection and through the
// whole chain of writers a request has in production (onTheWire). The MB/s
// column is the server and a loopback client together, so it understates
// what the server alone gained; it is the comparison between the rows that
// matters.
//
// The rows are the two ways a body can travel:
//
//   - sendfile: the store hands over a file and every writer in the chain
//     passes the copy down, so the kernel sends it in 256 KB pieces
//     (internal/rollingdeadline). This is every request off a local disk:
//     a ranged one, which is what a video player sends, and a whole file,
//     which is what the download button and an image tag send.
//   - copied: the bytes are read into a 32 KB buffer in this process and
//     written out. This was every request before the chain passed ReadFrom
//     down, and still is a stream from an object store.
//
// Measured on the development host (the host is shared and a run varies by
// a tenth, so it is the comparison between the rows that holds):
//
//	                               no ReadFrom   chain passes   file handed
//	                               in the chain  ReadFrom down  to ReadFrom
//	a ranged request off a disk    1245 MB/s     1880 MB/s      (unchanged)
//	the whole file off a disk      1250 MB/s     1250 MB/s      2000 MB/s
//	a store that is no disk        1245 MB/s     1230 MB/s      (unchanged)
//
// The first column is the tree in which humawriter and the request-id
// recorder had no ReadFrom, so nothing reached net/http's own; every row was
// a copy. Allocation per copied request fell from 48 KB to 16 KB in the
// second, because the buffer is pooled.
//
// The whole file stayed copied in the second column because of how it
// arrived, not because of anything in the chain: objecthttp handed io.Copy
// the *os.File, io.Copy asks the file to WriteTo first, and os then passes
// the destination a wrapper that hides the file from it. A ranged request
// does not go that way. The third column is objecthttp.Answer.Send handing
// the file to the destination's ReadFrom itself (-benchtime=2s -count=3:
// 1150-1320 MB/s before, 1780-2100 MB/s after, the ranged row measuring
// 1900-2150 in the same run).
//
// And the size of a sendfile piece, on the first row (rollingdeadline's
// sendfileChunk, which says why 256 KB was chosen over the fastest):
//
//	64 KB   1750 MB/s      1 MB   2200 MB/s      unbroken   1400 MB/s
//	256 KB  1780 MB/s      4 MB   1950 MB/s
func BenchmarkStreamingAFile(b *testing.B) {
	s, _ := filmStudio(b)
	for name, test := range map[string]struct {
		store  filestore.Store
		ranged bool
	}{
		"a ranged request off a disk": {s.files, true},
		"the whole file off a disk":   {s.files, false},
		"a store that is no disk":     {notADisk{s.files}, true},
	} {
		b.Run(name, func(b *testing.B) {
			served := onTheWire(b, s, test.store, s)
			client := &http.Client{}
			defer client.CloseIdleConnections()
			b.SetBytes(filmSize)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				request, err := http.NewRequest(http.MethodGet, "http://"+served.address+fileURL, nil)
				if err != nil {
					b.Fatal(err)
				}
				if test.ranged {
					request.Header.Set("Range", "bytes=0-")
				}
				response, err := client.Do(request)
				if err != nil {
					b.Fatal(err)
				}
				received, err := io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if err != nil || received != filmSize {
					b.Fatalf("%d bytes, %v", received, err)
				}
				<-served.answered
			}
		})
	}
}
