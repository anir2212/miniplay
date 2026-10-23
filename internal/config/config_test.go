package config

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestTLSFilesRequiresBothOrNeither(t *testing.T) {
	cases := []struct {
		cert, key string
		wantErr   bool
	}{
		{"", "", false},
		{"cert.pem", "key.pem", false},
		{"cert.pem", "", true},
		{"", "key.pem", true},
	}
	for _, tc := range cases {
		t.Setenv("MINIPLAY_TLS_CERT", tc.cert)
		t.Setenv("MINIPLAY_TLS_KEY", tc.key)
		cert, key, err := TLSFiles()
		if (err != nil) != tc.wantErr {
			t.Fatalf("cert=%q key=%q: err=%v, want error %v", tc.cert, tc.key, err, tc.wantErr)
		}
		if err == nil && (cert != tc.cert || key != tc.key) {
			t.Fatalf("got %q %q, want %q %q", cert, key, tc.cert, tc.key)
		}
	}
}

func TestTLSConfigRejectsMissingFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := TLSConfig(filepath.Join(dir, "none.pem"), filepath.Join(dir, "none.key")); err == nil {
		t.Fatal("missing certificate files were accepted")
	}
}

// writeTestPKI creates a throwaway CA and a server certificate for 127.0.0.1
// signed by it, and returns the server's PEM paths and a pool holding the CA.
func writeTestPKI(t *testing.T) (certFile, keyFile string, roots *x509.CertPool) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "127.0.0.1"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, ca, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(serverKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile = filepath.Join(dir, "server.pem"), filepath.Join(dir, "server.key")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	roots = x509.NewCertPool()
	roots.AddCert(ca)
	return certFile, keyFile, roots
}

// startTLS serves handler over TLS on a loopback port until the test ends.
func startTLS(t *testing.T, handler http.Handler) (addr string, roots *x509.CertPool) {
	t.Helper()
	certFile, keyFile, roots := writeTestPKI(t)
	tlsConfig, err := TLSConfig(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- serve(ctx, newServer(listener.Addr().String(), handler), listener, tlsConfig) }()
	t.Cleanup(func() {
		cancel()
		if err := <-result; err != nil {
			t.Errorf("serve returned %v", err)
		}
	})
	return listener.Addr().String(), roots
}

func TestServeTLSRequiresTrustedCertificateAndRefusesPlainHTTP(t *testing.T) {
	addr, roots := startTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.Proto)
	}))

	trusted := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots}, ForceAttemptHTTP2: true,
	}}
	resp, err := trusted.Get("https://" + addr + "/api/health")
	if err != nil {
		t.Fatalf("client trusting the CA: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.TLS == nil || resp.TLS.Version < tls.VersionTLS12 {
		t.Fatalf("status %d, TLS state %+v", resp.StatusCode, resp.TLS)
	}
	if string(body) != "HTTP/2.0" {
		t.Fatalf("protocol %q, want HTTP/2.0 through ALPN", body)
	}

	untrusted := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: x509.NewCertPool()},
	}}
	if _, err := untrusted.Get("https://" + addr + "/api/health"); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("client without the CA: err=%v, want a certificate error", err)
	}

	oldTLS := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, MaxVersion: tls.VersionTLS11},
	}}
	if _, err := oldTLS.Get("https://" + addr + "/api/health"); err == nil {
		t.Fatal("a TLS 1.1 client was accepted")
	}

	plain := &http.Client{Timeout: 5 * time.Second}
	resp, err = plain.Get("http://" + addr + "/api/health")
	if err != nil {
		t.Fatalf("plain HTTP request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("plain HTTP got %d, want 400 from the TLS listener", resp.StatusCode)
	}
}

func TestServeTLSCarriesWebSocketUpgrade(t *testing.T) {
	addr, roots := startTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		_ = conn.Write(r.Context(), websocket.MessageText, []byte("board"))
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}}
	conn, _, err := websocket.Dial(ctx, "wss://"+addr+"/api/boards/live", &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		t.Fatalf("wss dial: %v", err)
	}
	defer conn.CloseNow()
	_, payload, err := conn.Read(ctx)
	if err != nil || string(payload) != "board" {
		t.Fatalf("read %q, %v", payload, err)
	}
}
