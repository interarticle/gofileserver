// gofileserver is a simple Go file server that serves a single file system
// directory on a single port, logging requests to stderr and syslog.
//
// gofileserver is intended to serve streaming media reliably on the local
// network, since the default Go http server implementation does not enforce
// write timeouts, unlike more popular web servers like nginx. As such, it is
// also less suitable for serving an Internet site, since it is more prone to
// resoucre leakage.
package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"log"
	"log/syslog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const responseLogDelay = time.Second

const w3cDate = "2006-01-02"
const w3cTime = "15:04:05"

var (
	rootDirectory = flag.String("root_directory", "", "Path to the folder to be served")
	bindAddress   = flag.String("bind_address", "", "IP and port to bind the server to")
	logToSyslog   = flag.Bool("log_to_syslog", false, "Whether or not to log to Syslog")
)

var requestCounter int64

type w3cLogWriter struct {
	Writer io.Writer

	csvWriter *csv.Writer
	buffer    bytes.Buffer
	mutex     sync.Mutex
}

func newW3cLogWriter(w io.Writer) *w3cLogWriter {
	wr := &w3cLogWriter{
		Writer: w,
	}

	wr.csvWriter = csv.NewWriter(&wr.buffer)
	wr.csvWriter.Comma = ' '
	return wr
}

func (w *w3cLogWriter) Write(fields []string) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	w.writeFields(fields)
	w.sendBuffer()
}
func (w *w3cLogWriter) writeFields(fields []string) {
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

func (w *w3cLogWriter) sendBuffer() {
	_, err := w.Writer.Write(w.buffer.Bytes())
	if err != nil {
		panic(err)
	}
	w.buffer.Reset()
}

func (w *w3cLogWriter) WriteCommented(fields []string) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	_, err := w.buffer.Write([]byte("#"))
	if err != nil {
		panic(err)
	}
	w.writeFields(fields)
	w.sendBuffer()
}

func (w *w3cLogWriter) WriteComment(comment string) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	_, err := w.buffer.Write([]byte("#" + comment + "\n"))
	if err != nil {
		panic(err)
	}
	w.sendBuffer()
}

type responseLogger struct {
	Writer http.ResponseWriter

	Logger    *w3cLogWriter
	WaitGroup *sync.WaitGroup

	RequestId  int64
	RemoteAddr string
	Method     string
	URI        string
	Protocol   string
	UserAgent  string
	Range      string

	Status     int
	Written    int64
	WriteError error

	Started time.Time

	partialLogTimer *time.Timer
}

func (w *responseLogger) Init() {
	w.Status = http.StatusOK
	w.Started = time.Now()
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

func (w *responseLogger) MakePrefixFields() []string {
	t := time.Now()
	return []string{
		t.Format(w3cDate), t.Format(w3cTime), w.RemoteAddr, w.Method, w.URI,
		w.Protocol, w.UserAgent, w.Range,
	}
}

func (w *responseLogger) OnBeforeHandle() {
	w.WaitGroup.Add(1)
	w.partialLogTimer = time.AfterFunc(responseLogDelay,
		func() {
			defer w.WaitGroup.Done()
			w.Logger.WriteCommented(append(w.MakePrefixFields(), "->", fmt.Sprintf("0x%08x", w.RequestId)))
		})
}

func (w *responseLogger) OnAfterHandle() {
	suffix := []string{
		fmt.Sprintf("%d", w.Status),
		fmt.Sprintf("%d", w.Written),
		fmt.Sprintf("%f", time.Now().Sub(w.Started).Seconds()),
	}
	if w.WriteError != nil {
		suffix = append(suffix, w.WriteError.Error())
	} else {
		suffix = append(suffix, "")
	}
	if !w.partialLogTimer.Stop() {
		suffix = append(suffix, fmt.Sprintf("<- 0x%08x", w.RequestId))
	} else {
		suffix = append(suffix, "")
		w.WaitGroup.Done()
	}
	w.Logger.Write(append(w.MakePrefixFields(), suffix...))
}

type loggingHandler struct {
	Handler   http.Handler
	Logger    *w3cLogWriter
	WaitGroup *sync.WaitGroup
}

func (l loggingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	l.WaitGroup.Add(1)
	defer l.WaitGroup.Done()
	rww := &responseLogger{
		Writer:    w,
		Logger:    l.Logger,
		WaitGroup: l.WaitGroup,

		RequestId:  atomic.AddInt64(&requestCounter, 1),
		RemoteAddr: r.RemoteAddr,
		Method:     r.Method,
		URI:        r.URL.String(),
		Protocol:   r.Proto,
		UserAgent:  r.Header.Get("User-Agent"),
		Range:      r.Header.Get("Range"),
	}
	rww.Init()
	rww.OnBeforeHandle()
	defer rww.OnAfterHandle()
	l.Handler.ServeHTTP(rww, r)
}

func main() {
	flag.Parse()
	ctx, cancel := context.WithCancel(context.Background())

	var logWriter io.Writer = os.Stderr

	if *logToSyslog {
		syslogWriter, err := syslog.New(syslog.LOG_INFO, "gofileserver")
		defer syslogWriter.Close()
		if err != nil {
			log.Fatal(err)
		}
		logWriter = io.MultiWriter(logWriter, syslogWriter)
	}

	logger := newW3cLogWriter(logWriter)
	var wg sync.WaitGroup
	http.Handle("/", loggingHandler{
		Handler:   http.FileServer(http.Dir(*rootDirectory)),
		Logger:    logger,
		WaitGroup: &wg,
	})

	server := &http.Server{
		Addr: *bindAddress,
	}

	logger.WriteComment("Version: 1.0")
	logger.WriteComment(fmt.Sprintf("Date: %s %s", time.Now().Format(w3cDate), time.Now().Format(w3cTime)))
	logger.WriteComment("Fields: date time c-ip cs-method cs-uri x-cs-protocol cs(User-Agent) cs(Range) sc-status bytes time-taken x-write-error x-async-association")

	wg.Add(1)
	go func() {
		defer cancel()
		defer wg.Done()
		err := server.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			logger.WriteComment("Error: " + err.Error())
		}
	}()

	go func() {
		cancelCtx, _ := context.WithTimeout(ctx, time.Second)
		<-cancelCtx.Done()
		if cancelCtx.Err() == context.DeadlineExceeded {
			logger.WriteComment("Status: gofileserver started")
		}
	}()

	go func() {
		defer cancel()
		sigC := make(chan os.Signal, 2)
		signal.Notify(sigC, os.Interrupt, syscall.SIGTERM)

		<-sigC
	}()

	<-ctx.Done()
	logger.WriteComment("Status: Shutting down")
	server.Close()
	wg.Wait()
	logger.WriteComment("Status: gofileserver shutdown")
}
