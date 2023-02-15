package main

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/caarlos0/env/v7"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/pkg/errors"
)

type config struct {
	Port int `env:"PORT" envDefault:"3000"`
}

type key int

const (
	requestIDKey key = 0
)

var (
	healthy int32
	//go:embed index.html
	indexHTML string
)

func main() {
	logger := log.New(os.Stdout, "http: ", log.LstdFlags)
	logger.Println("Server is starting...")

	cfg := config{}
	if err := env.Parse(&cfg); err != nil {
		logger.Fatal(err)
	}

	r := mux.NewRouter()
	r.HandleFunc("/", getRoot)
	r.HandleFunc("/json/{code}", JSONHandler)
	r.HandleFunc("/plain/{code}", PlainHandler)
	r.HandleFunc("/healthz", healthz)

	nextRequestID := func() string {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}

	listenAddr := fmt.Sprintf(":%d", cfg.Port)
	server := &http.Server{
		Addr:         listenAddr,
		Handler:      handlers.RecoveryHandler()(tracing(nextRequestID)(logging(logger)(r))),
		ErrorLog:     logger,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  15 * time.Second,
	}

	done := make(chan bool)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt)

	go func() {
		<-quit
		logger.Println("Server is shutting down...")
		atomic.StoreInt32(&healthy, 0)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		server.SetKeepAlivesEnabled(false)
		if err := server.Shutdown(ctx); err != nil {
			logger.Fatalf("Could not gracefully shutdown the server: %v\n", err)
		}
		close(done)
	}()

	logger.Println("Server is ready to handle requests at", listenAddr)
	atomic.StoreInt32(&healthy, 1)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Fatalf("Could not listen on %s: %v\n", listenAddr, err)
	}

	<-done
	logger.Println("Server stopped")
}

func healthz(w http.ResponseWriter, r *http.Request) {
	if atomic.LoadInt32(&healthy) == 1 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
}

func logging(logger *log.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				requestID, ok := r.Context().Value(requestIDKey).(string)
				if !ok {
					requestID = "unknown"
				}
				logger.Println(requestID, r.Method, r.URL.Path, r.RemoteAddr, r.UserAgent())
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func tracing(nextRequestID func() string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := r.Header.Get("X-Request-Id")
			if requestID == "" {
				requestID = nextRequestID()
			}
			ctx := context.WithValue(r.Context(), requestIDKey, requestID)
			w.Header().Set("X-Request-Id", requestID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func JSONHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)

	code, err := strconv.ParseInt(vars["code"], 10, 0)
	if err != nil {
		panic(errors.Wrap(err, "Unable to process code"))
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(int(code))
	switch code {
	case http.StatusNoContent:
		return
	default:
		io.WriteString(w, "{}")
	}
}

func PlainHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)

	code, err := strconv.ParseInt(vars["code"], 10, 0)
	if err != nil {
		panic(errors.Wrap(err, "Unable to process code"))
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(int(code))
	switch code {
	case http.StatusNoContent:
		return
	default:
		io.WriteString(w, "")
	}
}

func getRoot(w http.ResponseWriter, r *http.Request) {
	io.WriteString(w, indexHTML)
}
