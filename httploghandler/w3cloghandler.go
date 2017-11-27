// Package httploghandler exposes a http.Handler that logs detailed http access
// logs to a go http server.
package httploghandler

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type LogFormat int

const (
	LogFormatExtendedLegacy LogFormat = iota
)

type Option func(h *Handler) error

type Handler struct {
	handler   http.Handler
	logWriter *W3CFormatWriter

	wg                  *sync.WaitGroup
	provisionalLogDelay time.Duration

	requestCounter *int64
}

func NewHandler(handler http.Handler, format LogFormat, opts ...Option) (*Handler, error) {
	if format != LogFormatExtendedLegacy {
		return nil, errors.New("unsupported log format")
	}

	h := &Handler{
		handler:             handler,
		wg:                  new(sync.WaitGroup),
		provisionalLogDelay: time.Second,
		requestCounter:      new(int64),
	}
	for _, opt := range opts {
		err := opt(h)
		if err != nil {
			return nil, err
		}
	}

	if h.logWriter == nil {
		return nil, errors.New("a log writer must be specified")
	}
	return h, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.wg.Add(1)
	defer h.wg.Done()
	requestId := atomic.AddInt64(h.requestCounter, 1)
	if requestId == 1 {
		h.writeFileHeader()
	}
	rww := &w3cLogger{
		Writer:    w,
		LogWriter: h.logWriter,
		WaitGroup: h.wg,

		RequestId:  requestId,
		RemoteAddr: r.RemoteAddr,
		Method:     r.Method,
		URI:        r.URL.String(),
		Protocol:   r.Proto,
		UserAgent:  r.Header.Get("User-Agent"),
		Range:      r.Header.Get("Range"),

		ProvisionalLogDelay: h.provisionalLogDelay,
	}
	rww.Init()
	rww.OnBeforeHandle()
	defer rww.OnAfterHandle()
	h.handler.ServeHTTP(rww, r)
}

func (h *Handler) writeFileHeader() {
	h.logWriter.WriteComment("Version: 1.0")
	h.logWriter.WriteComment(fmt.Sprintf("Date: %s %s", time.Now().Format(w3cDate), time.Now().Format(w3cTime)))
	h.logWriter.WriteComment("Fields: date time c-ip cs-method cs-uri x-cs-protocol cs(User-Agent) cs(Range) sc-status bytes time-taken x-write-error x-async-association")

}

func WithLogWriter(w io.Writer) Option {
	return WithW3CFormatWriter(NewW3CFormatWriter(w))
}

func WithW3CFormatWriter(w *W3CFormatWriter) Option {
	return func(h *Handler) error {
		h.logWriter = w
		return nil
	}
}

func WithWaitGroup(wg *sync.WaitGroup) Option {
	return func(h *Handler) error {
		h.wg = wg
		return nil
	}
}

func WithProvisionalLogDelay(delay time.Duration) Option {
	return func(h *Handler) error {
		h.provisionalLogDelay = delay
		return nil
	}
}
