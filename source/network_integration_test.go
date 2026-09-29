package main

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublishedModelsUseLauncherProxy(t *testing.T) {
	runPublishedModelsUseLauncherProxy(t, false, false, []string{"1", "0"})
}

func TestPublishedModelsCompressedHTTP2Endpoint(t *testing.T) {
	runPublishedModelsUseLauncherProxy(t, true, true, []string{"0"})
}

func runPublishedModelsUseLauncherProxy(t *testing.T, h2, compressed bool, baselines []string) {
	node := os.Getenv("DSH_TEST_NODE")
	rt := os.Getenv("DSH_TEST_RUNTIME")
	if node == "" || rt == "" {
		t.Skip("set DSH_TEST_NODE and DSH_TEST_RUNTIME to test the published adapter")
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Portable test CA"}, DNSNames: []string{"models.portable.invalid"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	pemCert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyBytes, _ := x509.MarshalECPrivateKey(key)
	pair, e := tls.X509KeyPair(pemCert, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes}))
	if e != nil {
		t.Fatal(e)
	}
	var modelHits, tunnelHits atomic.Int32
	model := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelHits.Add(1)
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer fixture-model-key" {
			t.Errorf("bad model request %s", r.URL.Path)
		}
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy secret reached model server")
		}
		w.Header().Set("Content-Type", "application/json")
		if compressed {
			if r.ProtoMajor != 1 {
				t.Errorf("portable fetch negotiated HTTP/%d; expected HTTP/1.1", r.ProtoMajor)
			}
			w.Header().Set("Content-Encoding", "gzip")
			z := gzip.NewWriter(w)
			io.WriteString(z, `{"data":[{"id":"fixture-model"}]}`)
			z.Close()
		} else {
			io.WriteString(w, `{"data":[{"id":"fixture-model"}]}`)
		}
	}))
	model.TLS = &tls.Config{Certificates: []tls.Certificate{pair}}
	model.EnableHTTP2 = h2
	model.StartTLS()
	defer model.Close()
	loop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "local-ok") }))
	defer loop.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Host != "models.portable.invalid:443" {
			t.Errorf("unexpected upstream request %s %s", r.Method, r.Host)
			http.Error(w, "bad", 400)
			return
		}
		tunnelHits.Add(1)
		remote, e := net.Dial("tcp", model.Listener.Addr().String())
		if e != nil {
			t.Error(e)
			return
		}
		defer remote.Close()
		c, rw, e := w.(http.Hijacker).Hijack()
		if e != nil {
			t.Error(e)
			return
		}
		defer c.Close()
		rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		rw.Flush()
		done := make(chan struct{})
		go func() { io.Copy(remote, rw); remote.Close(); close(done) }()
		io.Copy(c, remote)
		c.Close()
		<-done
	}))
	defer upstream.Close()
	proxyURL, _ := url.Parse(upstream.URL)
	var bridgeHits atomic.Int32
	bridge, e := startBridgeObserved(func(r *http.Request) (*url.URL, error) { return proxyURL, nil }, "bridge-secret", func(event, host, detail string) {
		if event == "tunnel-connected" {
			bridgeHits.Add(1)
		}
	})
	if e != nil {
		t.Fatal(e)
	}
	defer bridge.Close()
	for _, baseline := range baselines {
		t.Run("baseline-"+baseline, func(t *testing.T) {
			root := t.TempDir()
			ca := filepath.Join(root, "fixture-ca.pem")
			os.WriteFile(ca, pemCert, 0600)
			ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, node, "tests/published-model-probe.mjs")
			cmd.Env = append(os.Environ(), "DSH_TEST_BASELINE="+baseline, "DSH_PORTABLE_ROOT="+root, "DSH_PORTABLE_PROXY="+bridge.URL(), "DSH_PORTABLE_PROXY_TOKEN=bridge-secret", "DSH_TEST_ORIGIN=https://models.portable.invalid", "DSH_TEST_LOOPBACK="+loop.URL, "NODE_EXTRA_CA_CERTS="+ca)
			out, e := cmd.CombinedOutput()
			t.Log(string(out))
			if e != nil {
				t.Fatal(e)
			}
			log, _ := os.ReadFile(filepath.Join(root, "Data", "Logs", "network.log"))
			if strings.Contains(string(log), "fixture-model-key") || strings.Contains(string(log), "bridge-secret") {
				t.Fatal("network log contains credentials")
			}
		})
	}
	if modelHits.Load() != int32(len(baselines)) || bridgeHits.Load() != int32(len(baselines)) || tunnelHits.Load() != int32(len(baselines)) {
		t.Fatalf("model %d bridge %d upstream %d", modelHits.Load(), bridgeHits.Load(), tunnelHits.Load())
	}
}

// A fake transport proves the desktop check uses the provided launcher client,
// accepts only the fixed official feed, and binds responses to this launch nonce.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestDesktopUpdateCheckUsesLauncherClient(t *testing.T) {
	dir := t.TempDir()
	nonce := "test-nonce"
	id := strings.Repeat("a", 24)
	raw, _ := json.Marshal(updateCheckRequest{nonce, id})
	os.WriteFile(filepath.Join(dir, "update-check.request.json"), raw, 0600)
	called := false
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		if r.URL.String() != officialFeed {
			t.Fatal("unexpected update URL")
		}
		body := "version: 0.1.7-rc.2\nfiles:\n  - url: https://download.deepseek.com/dsh-desk/bin/win-x64/deepseek-harness-0.1.7-rc.2-win-x64.exe\n    sha512: AY7f45dYO7BFrfgaLmzXNWP0pavlxkSbsehPo/WF6PXcFdDK3fF1oHUPs/4f2bzROgQvm6wSgawZ/g7UzbPRmw==\n    size: 288245480\n"
		return http.ReadResponse(bufio.NewReader(strings.NewReader("HTTP/1.1 200 OK\r\nContent-Type: text/yaml\r\nConnection: close\r\n\r\n"+body)), r)
	})}
	if e := serviceUpdateCheck(context.Background(), client, dir, nonce); e != nil {
		t.Fatal(e)
	}
	if !called {
		t.Fatal("launcher client not used")
	}
	b, e := os.ReadFile(filepath.Join(dir, "update-check.response.json"))
	if e != nil {
		t.Fatal(e)
	}
	var response updateCheckResponse
	if e = json.Unmarshal(b, &response); e != nil {
		t.Fatal(e)
	}
	if response.Nonce != nonce || response.ID != id || response.Version != "0.1.7-rc.2" || response.Error != "" {
		t.Fatalf("bad response %#v", response)
	}
}

// The HTTP/2-capable fixture must fall back to HTTP/1.1 without losing HTTPS,
// response headers, JSON decoding, or the authenticated CONNECT proxy route.
func TestPortableCompressionMatrix(t *testing.T) {
	node, rt := os.Getenv("DSH_TEST_NODE"), os.Getenv("DSH_TEST_RUNTIME")
	if node == "" || rt == "" {
		t.Skip("set DSH_TEST_NODE and DSH_TEST_RUNTIME")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "Portable compression test"}, DNSNames: []string{"models.portable.invalid"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ca, keyFile := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "key.pem")
	if err = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "tests/compression-probe.mjs")
	cmd.Env = append(os.Environ(), "DSH_TEST_TLS_CERT="+ca, "DSH_TEST_TLS_KEY="+keyFile, "NODE_EXTRA_CA_CERTS="+ca, "DSH_PORTABLE_ROOT="+dir)
	output, err := cmd.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
	log, _ := os.ReadFile(filepath.Join(dir, "Data", "Logs", "network.log"))
	if strings.Contains(string(log), "local-test-key") || strings.Contains(string(log), "fixture-token") {
		t.Fatal("credentials leaked into network log")
	}
}
