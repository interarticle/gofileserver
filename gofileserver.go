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
	"log"
	"net/http"
	"sync/atomic"
)

var requestCounter uint32

var (
	rootDirectory = flag.String("root_directory", "", "Path to the folder to be served")
	bindAddress   = flag.String("bind_address", "", "IP and port to bind the server to")
)

type ResponseWriterWrapper struct {
	w http.ResponseWriter

	Status     int
	Written    int64
	WriteError error
}

func newResponseWriterWrapper(w http.ResponseWriter) *ResponseWriterWrapper {
	return &ResponseWriterWrapper{
		w:      w,
		Status: http.StatusOK,
	}
}

func (w *ResponseWriterWrapper) Header() http.Header {
	return w.w.Header()
}

func (w *ResponseWriterWrapper) Write(b []byte) (int, error) {
	n, err := w.w.Write(b)
	w.Written += int64(n)
	if err != nil {
		w.WriteError = err
	}
	return n, err
}

func (w *ResponseWriterWrapper) WriteHeader(status int) {
	w.Status = status
	w.w.WriteHeader(status)
}

type LoggingHandler struct {
	Handler http.Handler
}

func (l LoggingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestId := atomic.AddUint32(&requestCounter, 1)
	log.Printf("Start %08x %s %s %s %s \"%s\" %s", requestId, r.RemoteAddr, r.Method, r.URL, r.Proto, r.Header.Get("User-Agent"), r.Header.Get("Range"))
	rww := newResponseWriterWrapper(w)
	defer func() {
		log.Printf("Finish %08x %d %d written %v", requestId, rww.Status, rww.Written, rww.WriteError)
	}()
	l.Handler.ServeHTTP(rww, r)
}

func main() {
	flag.Parse()
	log.Print("Gowebserver started")
	http.Handle("/", LoggingHandler{http.FileServer(http.Dir(*rootDirectory))})
	log.Fatal(http.ListenAndServe(*bindAddress, nil))
}
