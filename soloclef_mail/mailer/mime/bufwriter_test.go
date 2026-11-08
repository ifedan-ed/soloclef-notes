package mime

import (
	"bufio"
	"io"
)

func newBufWriter(w io.Writer) *bufio.Writer {
	return bufio.NewWriter(w)
}
