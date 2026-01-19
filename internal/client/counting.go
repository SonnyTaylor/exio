package client

import (
	"io"
	"sync/atomic"
)

// CountingReader wraps an io.Reader and counts bytes read.
type CountingReader struct {
	reader io.Reader
	count  *atomic.Int64
}

// NewCountingReader creates a new CountingReader.
func NewCountingReader(r io.Reader, counter *atomic.Int64) *CountingReader {
	return &CountingReader{
		reader: r,
		count:  counter,
	}
}

// Read implements io.Reader.
func (c *CountingReader) Read(p []byte) (int, error) {
	n, err := c.reader.Read(p)
	if n > 0 {
		c.count.Add(int64(n))
	}
	return n, err
}

// CountingWriter wraps an io.Writer and counts bytes written.
type CountingWriter struct {
	writer io.Writer
	count  *atomic.Int64
}

// NewCountingWriter creates a new CountingWriter.
func NewCountingWriter(w io.Writer, counter *atomic.Int64) *CountingWriter {
	return &CountingWriter{
		writer: w,
		count:  counter,
	}
}

// Write implements io.Writer.
func (c *CountingWriter) Write(p []byte) (int, error) {
	n, err := c.writer.Write(p)
	if n > 0 {
		c.count.Add(int64(n))
	}
	return n, err
}

// CountingReadWriter wraps an io.ReadWriter and counts bytes in both directions.
type CountingReadWriter struct {
	*CountingReader
	*CountingWriter
}

// NewCountingReadWriter creates a new CountingReadWriter.
func NewCountingReadWriter(rw io.ReadWriter, readCounter, writeCounter *atomic.Int64) *CountingReadWriter {
	return &CountingReadWriter{
		CountingReader: NewCountingReader(rw, readCounter),
		CountingWriter: NewCountingWriter(rw, writeCounter),
	}
}
