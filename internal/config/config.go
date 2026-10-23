package config

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func Env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func Required(key string) (string, error) {
	value := os.Getenv(key)
	if value == "" {
		return "", errors.New(key + " is required")
	}
	return value, nil
}

// TLSFiles reads MINIPLAY_TLS_CERT and MINIPLAY_TLS_KEY, the PEM certificate
// chain and private key a service should serve HTTPS with. Both unset means
// plain HTTP. Setting only one is an error, so a half-finished TLS setup never
// falls back to plain HTTP without notice.
func TLSFiles() (certFile, keyFile string, err error) {
	certFile, keyFile = os.Getenv("MINIPLAY_TLS_CERT"), os.Getenv("MINIPLAY_TLS_KEY")
	if (certFile == "") != (keyFile == "") {
		return "", "", errors.New("set both MINIPLAY_TLS_CERT and MINIPLAY_TLS_KEY, or neither")
	}
	return certFile, keyFile, nil
}

// TLSConfig loads a certificate and key pair and returns the server TLS
// settings: TLS 1.2 or later. net/http offers HTTP/2 and HTTP/1.1 through
// ALPN; WebSocket upgrades still use HTTP/1.1, because Go's HTTP/2 server does
// not advertise extended CONNECT by default.
func TLSConfig(certFile, keyFile string) (*tls.Config, error) {
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load TLS certificate: %w", err)
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}, nil
}

func newServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
}

// Serve listens on addr with plain HTTP until SIGINT or SIGTERM.
func Serve(addr string, handler http.Handler) error {
	return ServeTLS(addr, handler, nil)
}

// ServeTLS listens on addr until SIGINT or SIGTERM. A nil tlsConfig serves
// plain HTTP; otherwise every connection must complete a TLS handshake.
func ServeTLS(addr string, handler http.Handler, tlsConfig *tls.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return serve(ctx, newServer(addr, handler), listener, tlsConfig)
}

func serve(ctx context.Context, server *http.Server, listener net.Listener, tlsConfig *tls.Config) error {
	done := make(chan struct{})
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
		close(done)
	}()
	var err error
	if tlsConfig != nil {
		server.TLSConfig = tlsConfig
		slog.Info("listening", "address", listener.Addr().String(), "tls", true)
		err = server.ServeTLS(listener, "", "")
	} else {
		slog.Info("listening", "address", listener.Addr().String(), "tls", false)
		err = server.Serve(listener)
	}
	if !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-done
	return nil
}

func LocalAddr(port string) string {
	return Env("MINIPLAY_HTTP_ADDR", "127.0.0.1:"+strings.TrimPrefix(port, ":"))
}
