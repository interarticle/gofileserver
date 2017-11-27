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
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"log/syslog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/interarticle/gofileserver/httploghandler"
)

var (
	rootDirectory = flag.String("root_directory", "", "Path to the folder to be served")
	bindAddress   = flag.String("bind_address", "", "IP and port to bind the server to")
	logToSyslog   = flag.Bool("log_to_syslog", false, "Whether or not to log to Syslog")
)

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

	logger := httploghandler.NewW3CFormatWriter(logWriter)
	var wg sync.WaitGroup
	logHandler, err := httploghandler.NewHandler(
		newBetterHttpListingServer(http.Dir(*rootDirectory)), httploghandler.LogFormatExtendedLegacy,
		httploghandler.WithW3CFormatWriter(logger), httploghandler.WithWaitGroup(&wg))
	if err != nil {
		logger.WriteComment(fmt.Sprintf("Error initializing logging: %v", err))
		return
	}
	http.Handle("/", logHandler)

	server := &http.Server{
		Addr: *bindAddress,
	}

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
