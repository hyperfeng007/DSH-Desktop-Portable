package main

import (
	"bytes"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestProxyValidation(t *testing.T) {
	for _, s := range []string{"http://127.0.0.1:7890", "socks5://localhost:1080", "https://u:p@proxy.example:443"} {
		if _, e := validateProxy(s); e != nil {
			t.Fatal(e)
		}
	}
	for _, s := range []string{"file:///etc/passwd", "ftp://localhost:1", "http://", "http://a/b", "http://a?x=1"} {
		if _, e := validateProxy(s); e == nil {
			t.Fatal("accepted", s)
		}
	}
}
func TestSafePaths(t *testing.T) {
	for _, s := range []string{"../bad", "a/../../b", "a/../b", `C:\bad`, `\bad`, `a\..\bad`} {
		if _, e := safeChild("root", s); e == nil {
			t.Fatal("accepted", s)
		}
	}
	if _, e := safeChild("root", "lib/main.js"); e != nil {
		t.Fatal(e)
	}
}
func TestFeed(t *testing.T) {
	hash := sha512.Sum512([]byte("a"))
	raw := "version: 0.1.7-rc.2\nfiles:\n  - url: >-\n      https://download.deepseek.com/dsh-desk/bin/win-x64/deepseek-harness-0.1.7-rc.2-win-x64.exe\n    sha512: " + base64.StdEncoding.EncodeToString(hash[:]) + "\n    size: 288245480\n"
	if _, e := parseFeed([]byte(raw)); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{strings.ReplaceAll(raw, "https://download.deepseek.com/", "https://evil.example/"), strings.ReplaceAll(raw, "size: 288245480", "size: 9"), strings.ReplaceAll(raw, "sha512:", "shaWrong:")} {
		if _, e := parseFeed([]byte(bad)); e == nil {
			t.Fatal("invalid feed accepted")
		}
	}
}
func makeAsar(t *testing.T, p string) {
	t.Helper()
	pkg := []byte(`{"name":"fixture","version":"1.2.3","type":"module","main":"lib/main.js","dshMandatoryUpdatePolicy":{"origin":"https://harness.deepseek.com"}}`)
	js := []byte("export const untouched = 42;\nconst openUpdatePrompt = (manual = false) => {return Promise.resolve()};\n")
	host := []byte("export const hostOriginal = true;\n")
	tree := asarEntry{Files: map[string]*asarEntry{"native.node": {Size: 4, Unpacked: true, Integrity: checksumEntry([]byte("test"))}}}
	body := new(bytes.Buffer)
	for _, v := range []struct {
		name string
		data []byte
	}{{"package.json", pkg}, {"lib/main.js", js}, {"dsh/node_modules/@deepseek-ai/dsh-desktop-host/lib/index.js", host}} {
		parts := strings.Split(v.name, "/")
		parent := &tree
		for _, part := range parts[:len(parts)-1] {
			if parent.Files[part] == nil {
				parent.Files[part] = &asarEntry{Files: map[string]*asarEntry{}}
			}
			parent = parent.Files[part]
		}
		parent.Files[parts[len(parts)-1]] = &asarEntry{Size: int64(len(v.data)), Offset: strconv.Itoa(body.Len()), Integrity: checksumEntry(v.data)}
		body.Write(v.data)
	}
	h, _ := json.Marshal(tree)
	pad := (len(h) + 3) &^ 3
	b := new(bytes.Buffer)
	binary.Write(b, binary.LittleEndian, []uint32{4, uint32(8 + pad), uint32(4 + pad), uint32(len(h))})
	b.Write(h)
	b.Write(make([]byte, pad-len(h)))
	b.Write(body.Bytes())
	if e := os.WriteFile(p, b.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
func TestAsarPatch(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "app.asar")
	makeAsar(t, p)
	os.WriteFile(filepath.Join(d, "app-update.yml"), []byte("provider: generic"), 0600)
	if e := patchAsar(p); e != nil {
		t.Fatal(e)
	}
	f, _ := os.Open(p)
	defer f.Close()
	tree, base, e := readAsarHeader(f)
	if e != nil {
		t.Fatal(e)
	}
	pkg, e := readAsarFile(f, tree, base, "package.json")
	if e != nil || !bytes.Contains(pkg, []byte(`"main": "portable.mjs"`)) {
		t.Fatal("bad package", string(pkg), e)
	}
	if !bytes.Contains(pkg, []byte("dshMandatoryUpdatePolicy")) {
		t.Fatal("upstream mandatory policy must stay")
	}
	js, _ := readAsarFile(f, tree, base, "lib/main.js")
	if !bytes.Contains(js, []byte("__dshPortableCheckUpdate")) || !bytes.Contains(js, []byte("export const untouched = 42")) {
		t.Fatal("desktop update hook missing")
	}
	w, _ := readAsarFile(f, tree, base, "portable.mjs")
	if bytes.Contains(w, []byte("__ORIGINAL_MAIN__")) || !bytes.Contains(w, []byte("lib/main.js")) {
		t.Fatal("wrapper failed")
	}
	if !tree.Files["native.node"].Unpacked {
		t.Fatal("lost unpacked native")
	}
	if _, e = os.Stat(filepath.Join(d, "app-update.yml")); !os.IsNotExist(e) {
		t.Fatal("installer updater enabled")
	}
	if e = patchAsar(p); e == nil {
		t.Fatal("allowed double patch")
	}
}
func TestBridgeHTTPAndAuth(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("credential leak")
		}
		w.Write([]byte("hello portable"))
	}))
	defer origin.Close()
	b, e := startBridge(func(r *http.Request) (*url.URL, error) { return nil, nil }, "test-token")
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	p, _ := url.Parse(b.URL())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(p)}}
	res, e := client.Get(origin.URL)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 407 {
		t.Fatal(res.StatusCode)
	}
	p.User = url.UserPassword("dsh", "test-token")
	tr := &http.Transport{Proxy: http.ProxyURL(p)}
	defer tr.CloseIdleConnections()
	client.Transport = tr
	res, e = client.Get(origin.URL)
	if e != nil {
		t.Fatal(e)
	}
	buf, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(buf) != "hello portable" {
		t.Fatal(string(buf))
	}
}
func TestBridgeHTTPSConnect(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("secure response")) }))
	defer origin.Close()
	b, e := startBridge(func(r *http.Request) (*url.URL, error) { return nil, nil }, "secret")
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	p, _ := url.Parse(b.URL())
	p.User = url.UserPassword("dsh", "secret")
	tr := origin.Client().Transport.(*http.Transport).Clone()
	tr.Proxy = http.ProxyURL(p)
	defer tr.CloseIdleConnections()
	c := &http.Client{Transport: tr}
	res, e := c.Get(origin.URL)
	if e != nil {
		t.Fatal(e)
	}
	data, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(data) != "secure response" {
		t.Fatal(string(data))
	}
}
func TestBridgeChainedHTTPProxy(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("chained")) }))
	defer origin.Close()
	up, e := startBridge(func(r *http.Request) (*url.URL, error) { return nil, nil }, "up-token")
	if e != nil {
		t.Fatal(e)
	}
	defer up.Close()
	upURL, _ := url.Parse(up.URL())
	upURL.User = url.UserPassword("dsh", "up-token")
	b, e := startBridge(func(r *http.Request) (*url.URL, error) { return upURL, nil }, "local-token")
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	p, _ := url.Parse(b.URL())
	p.User = url.UserPassword("dsh", "local-token")
	tr := origin.Client().Transport.(*http.Transport).Clone()
	tr.Proxy = http.ProxyURL(p)
	defer tr.CloseIdleConnections()
	res, e := (&http.Client{Transport: tr}).Get(origin.URL)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(raw) != "chained" {
		t.Fatal(string(raw))
	}
}
func TestOfficialAsar(t *testing.T) {
	p := os.Getenv("DSH_TEST_OFFICIAL_ASAR")
	if p == "" {
		t.Skip("set DSH_TEST_OFFICIAL_ASAR to verify a real upstream payload")
	}
	d := t.TempDir()
	dest := filepath.Join(d, "app.asar")
	in, e := os.Open(p)
	if e != nil {
		t.Fatal(e)
	}
	originalTree, originalBase, e := readAsarHeader(in)
	if e != nil {
		t.Fatal(e)
	}
	originalMain, e := readAsarFile(in, originalTree, originalBase, "lib/main.js")
	if e != nil {
		t.Fatal(e)
	}
	in.Seek(0, 0)
	out, _ := os.Create(dest)
	io.Copy(out, in)
	out.Close()
	in.Close()
	if e = patchAsar(dest); e != nil {
		t.Fatal(e)
	}
	f, _ := os.Open(dest)
	defer f.Close()
	tree, base, e := readAsarHeader(f)
	if e != nil {
		t.Fatal(e)
	}
	main, e := readAsarFile(f, tree, base, "lib/main.js")
	injection := []byte("\n\t\tif (typeof globalThis.__dshPortableCheckUpdate === \"function\") return globalThis.__dshPortableCheckUpdate();")
	if e != nil || !bytes.Equal(bytes.Replace(main, injection, nil, 1), originalMain) {
		t.Fatal("unexpected desktop changes")
	}
	host, e := readAsarFile(f, tree, base, "dsh/node_modules/@deepseek-ai/dsh-desktop-host/lib/index.js")
	if e != nil || !bytes.HasPrefix(host, []byte("import {installPortableNodeProxy}")) {
		t.Fatal("host proxy prelude missing")
	}
	for _, p := range []string{"portable-node-proxy.mjs", "portable-network.mjs", "portable-update.mjs"} {
		if asarFile(tree, p) == nil {
			t.Fatal("missing adapter", p)
		}
	}
	t.Log("Official ASAR: bounded desktop hook, Host prelude and three network/update modules verified")
}

func TestAsarEmptyEntries(t *testing.T) {
	for _, e := range []asarEntry{{Files: map[string]*asarEntry{}}, {Size: 0, Offset: "0"}, {Size: 0, Unpacked: true}} {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		if e.Files != nil && !bytes.Contains(b, []byte(`"files":{}`)) {
			t.Fatal(string(b))
		}
		if e.Files == nil && !bytes.Contains(b, []byte(`"size":0`)) {
			t.Fatal(string(b))
		}
	}
}
func TestProxyFailureDoesNotGoDirect(t *testing.T) {
	called := false
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.Write([]byte("must not connect")) }))
	defer origin.Close()
	b, e := startBridge(func(r *http.Request) (*url.URL, error) { return nil, io.ErrUnexpectedEOF }, "test-token")
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	p, _ := url.Parse(b.URL())
	p.User = url.UserPassword("dsh", "test-token")
	tr := &http.Transport{Proxy: http.ProxyURL(p)}
	defer tr.CloseIdleConnections()
	res, e := (&http.Client{Transport: tr}).Get(origin.URL)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 502 || called {
		t.Fatal("proxy failure fell back to direct")
	}
}
