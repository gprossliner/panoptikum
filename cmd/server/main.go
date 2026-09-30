/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Command server is the portal-server (data plane) binary. Per
// docs/ARCHITECTURE.md Decision 4, it never talks to the Kubernetes API -
// it only reads its merged config from a mounted Secret. OIDC login and
// reverse-proxying are not implemented yet.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gprossliner/panoptikum/internal/portalconfig"
)

func main() {
	var configPath, addr, logLevel string
	flag.StringVar(&configPath, "config", "/etc/panoptikum/config.json",
		"Path to the merged portal config JSON file (see internal/portalconfig).")
	flag.StringVar(&addr, "address", ":8080", "The address the portal-server HTTP listener binds to.")
	flag.StringVar(&logLevel, "log-level", "info", "Status log level: debug, info, warn, or error.")
	flag.Parse()

	level, err := parseLogLevel(logLevel)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Status/application logs go to stderr; access logs (see withAccessLog)
	// go to stdout, so the two streams can be collected/filtered separately.
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	cfg, err := loadConfig(configPath)
	if err != nil {
		logger.Error("Failed to load config", "path", configPath, "error", err)
		os.Exit(1)
	}
	_ = cfg // TODO: wire into OIDC login + reverse proxy

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := &http.Server{Addr: addr, Handler: withAccessLog(mux)}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()

	logger.Info("starting portal-server", "address", addr, "config", configPath)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("portal-server exited", "error", err)
		os.Exit(1)
	}
}

func loadConfig(path string) (*portalconfig.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg portalconfig.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func parseLogLevel(s string) (slog.Level, error) {
	switch s {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("--log-level: unknown level %q (want debug, info, warn, or error)", s)
	}
}

// withAccessLog wraps h, writing one Apache Common Log Format line per
// request to stdout. Deliberately not configurable (see docs/ARCHITECTURE.md).
func withAccessLog(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(sw, r)

		host := r.RemoteAddr
		if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			host = h
		}

		_, _ = fmt.Fprintf(os.Stdout, "%s - - [%s] %q %d %d\n",
			host,
			start.Format("02/Jan/2006:15:04:05 -0700"),
			fmt.Sprintf("%s %s %s", r.Method, r.URL.RequestURI(), r.Proto),
			sw.status,
			sw.written,
		)
	})
}

// statusWriter records the status code and bytes written for the access log.
type statusWriter struct {
	http.ResponseWriter
	status  int
	written int64
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.written += int64(n)
	return n, err
}
