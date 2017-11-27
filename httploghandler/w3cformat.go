package httploghandler

import (
	"bytes"
	"encoding/csv"
	"io"
	"sync"
)

type flusher interface {
	Flush() error
}

type W3CFormatWriter struct {
	writer  io.Writer
	flusher flusher

	csvWriter *csv.Writer
	buffer    bytes.Buffer
	mutex     sync.Mutex
}

func NewW3CFormatWriter(w io.Writer) *W3CFormatWriter {
	wr := &W3CFormatWriter{
		writer: w,
	}
	if flusher, ok := w.(flusher); ok {
		wr.flusher = flusher
	}

	wr.csvWriter = csv.NewWriter(&wr.buffer)
	wr.csvWriter.Comma = ' '
	return wr
}

func (w *W3CFormatWriter) Write(fields []string) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	w.writeFields(fields)
	w.sendBuffer()
}
func (w *W3CFormatWriter) writeFields(fields []string) {
	err := w.csvWriter.Write(fields)
	if err != nil {
		panic(err)
	}
	w.csvWriter.Flush()
	err = w.csvWriter.Error()
	if err != nil {
		panic(err)
	}
}

func (w *W3CFormatWriter) sendBuffer() {
	_, err := w.writer.Write(w.buffer.Bytes())
	if err != nil {
		panic(err)
	}
	w.buffer.Reset()
	if w.flusher != nil {
		w.flusher.Flush()
	}
}

func (w *W3CFormatWriter) WriteCommented(fields []string) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	_, err := w.buffer.Write([]byte("#"))
	if err != nil {
		panic(err)
	}
	w.writeFields(fields)
	w.sendBuffer()
}

func (w *W3CFormatWriter) WriteComment(comment string) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	_, err := w.buffer.Write([]byte("#" + comment + "\n"))
	if err != nil {
		panic(err)
	}
	w.sendBuffer()
}
