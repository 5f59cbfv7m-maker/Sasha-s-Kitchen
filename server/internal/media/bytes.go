package media

import (
	"bytes"
	"io"
)

// bytesReader adapts a derivative's in-memory body to the io.Reader the object
// store expects.
func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }
