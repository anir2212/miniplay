package main

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNewHTTPClientTrustsOnlyTheGivenCA(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer server.Close()
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	trusted, err := newHTTPClient(caFile)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := trusted.Get(server.URL)
	if err != nil {
		t.Fatalf("client with the server's CA: %v", err)
	}
	resp.Body.Close()

	plain, err := newHTTPClient("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plain.Get(server.URL); err == nil {
		t.Fatal("client without the CA trusted a self-signed server")
	}

	empty := filepath.Join(t.TempDir(), "empty.pem")
	if err := os.WriteFile(empty, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newHTTPClient(empty); err == nil {
		t.Fatal("a file without a certificate was accepted")
	}
}
