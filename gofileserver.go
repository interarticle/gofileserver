// gofileserver is a simple Go file server that serves a single file system
// directory on a single port, logging requests to stderr.
//
// gofileserver is intended to serve streaming media reliably on the local
// network, since the default Go http server implementation does not enforce
// write timeouts, unlike more popular web servers like nginx. As such, it is
// also less suitable for serving an Internet site, since it is more prone to
// resoucre leakage.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
	"time"
)

const responseLogDelay = time.Second

var (
	rootDirectory = flag.String("root_directory", "", "Path to the folder to be served")
	bindAddress   = flag.String("bind_address", "", "IP and port to bind the server to")
)

var requestCounter int64

type responseLogger struct {
	Writer http.ResponseWriter

	RequestId   int64
	RemoteAddr  string
	RequestLine string
	UserAgent   string
	Range       string

	Status     int
	Written    int64
	WriteError error

	partialLogTimer *time.Timer
}

func (w *responseLogger) Init() {
	w.Status = http.StatusOK
}

func (w *responseLogger) Header() http.Header {
	return w.Writer.Header()
}

func (w *responseLogger) Write(b []byte) (int, error) {
	n, err := w.Writer.Write(b)
	w.Written += int64(n)
	if err != nil {
		w.WriteError = err
	}
	return n, err
}

func (w *responseLogger) WriteHeader(status int) {
	w.Status = status
	w.Writer.WriteHeader(status)
}

func (w *responseLogger) Log(suffix string) {
	log.Printf("%s \"%s\" \"%s\" \"%s\" %s", w.RemoteAddr, w.RequestLine, w.UserAgent, w.Range, suffix)
}

func (w *responseLogger) OnBeforeHandle() {
	w.partialLogTimer = time.AfterFunc(responseLogDelay,
		func() {
			w.Log(fmt.Sprintf("-> 0x%08x", w.RequestId))
		})
}

func (w *responseLogger) OnAfterHandle() {
	suffix := fmt.Sprintf("%d %d \"%v\"", w.Status, w.Written, w.WriteError)
	if !w.partialLogTimer.Stop() {
		suffix += fmt.Sprintf(" <- 0x%08x", w.RequestId)
	}
	w.Log(suffix)
}

type loggingHandler struct {
	Handler http.Handler
}

func (l loggingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rww := &responseLogger{
		Writer: w,

		RequestId:   atomic.AddInt64(&requestCounter, 1),
		RemoteAddr:  r.RemoteAddr,
		RequestLine: fmt.Sprintf("%s %s %s", r.Method, r.URL, r.Proto),
		UserAgent:   r.Header.Get("User-Agent"),
		Range:       r.Header.Get("Range"),
	}
	rww.Init()
	rww.OnBeforeHandle()
	defer rww.OnAfterHandle()
	l.Handler.ServeHTTP(rww, r)
}

func main() {
	flag.Parse()
	log.Print("Gowebserver started")
	http.Handle("/", loggingHandler{http.FileServer(http.Dir(*rootDirectory))})
	log.Fatal(http.ListenAndServe(*bindAddress, nil))
}
