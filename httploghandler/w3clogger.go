package httploghandler

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

const w3cDate = "2006-01-02"
const w3cTime = "15:04:05"

type w3cLogger struct {
	Writer http.ResponseWriter

	LogWriter *W3CFormatWriter
	WaitGroup *sync.WaitGroup

	RequestId  int64
	RemoteAddr string
	Method     string
	URI        string
	Protocol   string
	UserAgent  string
	Range      string

	ProvisionalLogDelay time.Duration

	Status     int
	Written    int64
	WriteError error

	started time.Time

	partialLogTimer *time.Timer
	onceAfterHandle sync.Once
}

func (w *w3cLogger) Init() {
	w.Status = http.StatusOK
	w.started = time.Now()
}

func (w *w3cLogger) Header() http.Header {
	return w.Writer.Header()
}

func (w *w3cLogger) Write(b []byte) (int, error) {
	n, err := w.Writer.Write(b)
	w.Written += int64(n)
	if err != nil {
		w.WriteError = err
	}
	return n, err
}

func (w *w3cLogger) WriteHeader(status int) {
	w.Status = status
	w.Writer.WriteHeader(status)
}

func (w *w3cLogger) MakePrefixFields() []string {
	t := time.Now()
	return []string{
		t.Format(w3cDate), t.Format(w3cTime), w.RemoteAddr, w.Method, w.URI,
		w.Protocol, w.UserAgent, w.Range,
	}
}

func (w *w3cLogger) OnBeforeHandle() {
	w.WaitGroup.Add(1)
	w.partialLogTimer = time.AfterFunc(w.ProvisionalLogDelay,
		func() {
			defer w.WaitGroup.Done()
			w.LogWriter.WriteCommented(append(w.MakePrefixFields(), "->", fmt.Sprintf("0x%08x", w.RequestId)))
		})
}

func (w *w3cLogger) OnAfterHandle() {
	suffix := []string{
		fmt.Sprintf("%d", w.Status),
		fmt.Sprintf("%d", w.Written),
		fmt.Sprintf("%f", time.Now().Sub(w.started).Seconds()),
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
	w.LogWriter.Write(append(w.MakePrefixFields(), suffix...))
}

type w3cHijackerLogger struct {
	*w3cLogger
	http.Hijacker

	hijacked bool
}

func (w *w3cHijackerLogger) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := w.Hijacker.Hijack()
	if err != nil {
		return conn, rw, err
	}
	w.hijacked = true
	w.Status = -1
	if c, ok := conn.(*net.TCPConn); ok {
		return &tcpConnWrap{TCPConn: c, l: w}, rw, err
	} else {
		return &connWrap{Conn: conn, l: w}, rw, err
	}
}

func (w *w3cHijackerLogger) OnAfterHandle() {
	if !w.hijacked {
		w.w3cLogger.OnAfterHandle()
	}
}

type connWrap struct {
	net.Conn
	l *w3cHijackerLogger

	bytesWritten int64
}

func (c *connWrap) Close() error {
	c.l.onceAfterHandle.Do(func() {
		c.l.w3cLogger.Written += c.bytesWritten
		c.l.w3cLogger.OnAfterHandle()
	})
	return c.Conn.Close()
}

func (c *connWrap) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	atomic.AddInt64(&c.bytesWritten, int64(n))
	return n, err
}

type tcpConnWrap struct {
	*net.TCPConn
	l *w3cHijackerLogger

	bytesWritten int64
}

func (c tcpConnWrap) Close() error {
	c.l.onceAfterHandle.Do(func() {
		c.l.w3cLogger.Written += c.bytesWritten
		c.l.w3cLogger.OnAfterHandle()
	})
	return c.TCPConn.Close()
}

func (c *tcpConnWrap) Write(b []byte) (int, error) {
	n, err := c.TCPConn.Write(b)
	atomic.AddInt64(&c.bytesWritten, int64(n))
	return n, err
}

func (c *tcpConnWrap) ReadFrom(r io.Reader) (int64, error) {
	n, err := c.TCPConn.ReadFrom(r)
	atomic.AddInt64(&c.bytesWritten, n)
	return n, err
}
