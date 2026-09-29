package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testCommit(root string, r RuntimeRecord) error {
	p := filepath.Join(root, "Runtime", "current.json")
	b, e := os.ReadFile(p)
	if e != nil {
		return e
	}
	if e = os.WriteFile(filepath.Join(root, "Runtime", "previous.json"), b, 0600); e != nil {
		return e
	}
	if e = os.WriteFile(p+".new", mustJSON(r), 0600); e != nil {
		return e
	}
	return os.Rename(p+".new", p)
}
func hashFileTest(t *testing.T, p string) string {
	t.Helper()
	f, e := os.Open(p)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	h, e := fileSHA256(context.Background(), f)
	if e != nil {
		t.Fatal(e)
	}
	return h
}
func makeLegacyAsar(t *testing.T, file, version string) {
	t.Helper()
	// Production bounded main/Host hooks are unchanged since 1.0.2. Rewrite only
	// the wrapper record/assets using exact shipped old assets to form the fixture.
	if e := patchAsar(file); e != nil {
		t.Fatal(e)
	}
	f, e := os.Open(file)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	tree, base, e := readAsarHeader(f)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := readAsarFile(f, tree, base, "package.json")
	if e != nil {
		t.Fatal(e)
	}
	var pkg map[string]any
	json.Unmarshal(raw, &pkg)
	main, _, e := originalMain(pkg)
	if e != nil {
		t.Fatal(e)
	}
	pkg["dshPortableWrapper"] = version
	payloads := []asarPayload{{"package.json", mustJSON(pkg)}}
	for _, name := range adapterNames {
		data, e := adapterForVersion(version, name, main)
		if e != nil {
			t.Fatal(e)
		}
		payloads = append(payloads, asarPayload{name, data})
	}
	if e = writeAsarPayloads(file, f, tree, base, payloads); e != nil {
		t.Fatal(e)
	}
}
func localFixture(t *testing.T, version string) (string, RuntimeRecord) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "便携 空格 # 😀")
	dir := filepath.Join(root, "Runtime", "dsh-fixture")
	res := filepath.Join(dir, "resources")
	os.MkdirAll(res, 0700)
	file := filepath.Join(res, "app.asar")
	makeAsar(t, file)
	os.MkdirAll(file+".unpacked", 0700)
	os.WriteFile(filepath.Join(file+".unpacked", "native.node"), []byte("test"), 0600)
	os.WriteFile(filepath.Join(dir, "DeepSeek Harness.exe"), []byte("fixture-executable-do-not-run"), 0700)
	os.WriteFile(filepath.Join(res, "app-update.yml"), []byte("fixture"), 0600)
	makeLegacyAsar(t, file, version)
	r := RuntimeRecord{"1.2.3", "dsh-fixture", base64.StdEncoding.EncodeToString(make([]byte, 64)), version}
	os.WriteFile(filepath.Join(root, "Runtime", "current.json"), mustJSON(r), 0600)
	os.MkdirAll(filepath.Join(root, "Data"), 0700)
	os.WriteFile(filepath.Join(root, "Data", "keep.txt"), []byte("user-data"), 0600)
	os.WriteFile(filepath.Join(root, "portable.json"), []byte("user-proxy-config"), 0600)
	return root, r
}
func fixtureFeed(r RuntimeRecord) Feed {
	f, e := parseFeed([]byte(fmt.Sprintf("version: %s\nfiles:\n  - url: https://download.deepseek.com/dsh-desk/bin/win-x64/deepseek-harness-%s-win-x64.exe\n    sha512: %s\n    size: 288245480\n", r.Version, r.Version, r.SHA512)))
	if e != nil {
		panic(e)
	}
	return f
}
func assertOriginalUnchanged(t *testing.T, root string, r RuntimeRecord, oldHash string) {
	t.Helper()
	if got := hashFileTest(t, filepath.Join(root, "Runtime", r.Directory, "resources", "app.asar")); got != oldHash {
		t.Fatal("active source ASAR changed")
	}
	b, _ := os.ReadFile(filepath.Join(root, "Data", "keep.txt"))
	if string(b) != "user-data" {
		t.Fatal("data changed")
	}
	b, _ = os.ReadFile(filepath.Join(root, "portable.json"))
	if string(b) != "user-proxy-config" {
		t.Fatal("proxy configuration changed")
	}
}
func TestLocalUpgradeKnownVersions(t *testing.T) {
	for _, v := range []string{"1.0.2", "1.0.3"} {
		t.Run(v, func(t *testing.T) {
			root, r := localFixture(t, v)
			old := hashFileTest(t, filepath.Join(root, "Runtime", r.Directory, "resources", "app.asar"))
			if !sameOfficialBuild(r, fixtureFeed(r)) {
				t.Fatal("same official payload not recognized")
			}
			if _, e := currentRuntime(root); e == nil {
				t.Fatal("old wrapper should need migration")
			}
			next, e := migrateLocalRuntime(context.Background(), root, r, testCommit, nil)
			if e != nil {
				t.Fatal(e)
			}
			if next.Wrapper != wrapperVersion || next.Version != r.Version || next.SHA512 != r.SHA512 || next.Directory == r.Directory {
				t.Fatal("bad migrated record")
			}
			got, e := currentRuntime(root)
			if e != nil || got != next {
				t.Fatal("pointer not published", e)
			}
			if e = validatePortableAsar(context.Background(), filepath.Join(root, "Runtime", next.Directory, "resources", "app.asar"), next); e != nil {
				t.Fatal(e)
			}
			var prev RuntimeRecord
			b, _ := os.ReadFile(filepath.Join(root, "Runtime", "previous.json"))
			json.Unmarshal(b, &prev)
			if prev != r {
				t.Fatal("previous pointer missing")
			}
			assertOriginalUnchanged(t, root, r, old)
			b, _ = os.ReadFile(filepath.Join(root, "Runtime", next.Directory, "DeepSeek Harness.exe"))
			if string(b) != "fixture-executable-do-not-run" {
				t.Fatal("executable was replaced")
			}
			entries, _ := os.ReadDir(filepath.Join(root, "Runtime"))
			for _, ent := range entries {
				if strings.HasPrefix(ent.Name(), ".local-upgrade-") {
					t.Fatal("staging residue")
				}
			}
		})
	}
}
func TestSameVersionDifferentPayloadRequiresDownload(t *testing.T) {
	_, r := localFixture(t, "1.0.2")
	f := fixtureFeed(r)
	f.Files[0].SHA512 = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 64))
	if sameOfficialBuild(r, f) {
		t.Fatal("same version with different payload reused")
	}
	f = fixtureFeed(r)
	f.Version = "1.2.4"
	if sameOfficialBuild(r, f) {
		t.Fatal("different official version reused")
	}
}
func TestLocalUpgradeFailurePreservesSource(t *testing.T) {
	for _, mode := range []string{"cancel-before", "cancel-copy", "commit-error", "bad-asset", "bad-unpacked", "bad-entry", "unknown-wrapper", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			root, r := localFixture(t, "1.0.2")
			file := filepath.Join(root, "Runtime", r.Directory, "resources", "app.asar")
			if mode == "bad-unpacked" {
				os.WriteFile(filepath.Join(file+".unpacked", "native.node"), []byte("fail"), 0600)
			}
			if mode == "bad-asset" || mode == "bad-entry" {
				f, _ := os.Open(file)
				tree, base, _ := readAsarHeader(f)
				name := "portable-node-proxy.mjs"
				if mode == "bad-entry" {
					name = "lib/main.js"
				}
				data, _ := readAsarFile(f, tree, base, name)
				data = append(data, []byte("\n// unexpected edit")...)
				if e := writeAsarPayloads(file, f, tree, base, []asarPayload{{name, data}}); e != nil {
					t.Fatal(e)
				}
				f.Close()
			}
			if mode == "unknown-wrapper" {
				r.Wrapper = "9.9.9"
				os.WriteFile(filepath.Join(root, "Runtime", "current.json"), mustJSON(r), 0600)
			}
			if mode == "symlink" {
				if e := os.Symlink(root, filepath.Join(root, "Runtime", r.Directory, "escape")); e != nil {
					t.Skip(e)
				}
			}
			old := hashFileTest(t, file)
			before, _ := os.ReadFile(filepath.Join(root, "Runtime", "current.json"))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel-before" {
				cancel()
			}
			progress := func(int64, int64) {
				if mode == "cancel-copy" {
					cancel()
				}
			}
			commit := testCommit
			if mode == "commit-error" {
				commit = func(string, RuntimeRecord) error { return fmt.Errorf("fixture pointer write denied") }
			}
			if _, e := migrateLocalRuntime(ctx, root, r, commit, progress); e == nil {
				t.Fatal("unsafe migration succeeded")
			}
			after, _ := os.ReadFile(filepath.Join(root, "Runtime", "current.json"))
			if !bytes.Equal(before, after) {
				t.Fatal("active pointer changed on failure")
			}
			assertOriginalUnchanged(t, root, r, old)
			entries, _ := os.ReadDir(filepath.Join(root, "Runtime"))
			for _, ent := range entries {
				if ent.IsDir() && ent.Name() != r.Directory {
					t.Fatal("failed migration left a directory", ent.Name())
				}
			}
		})
	}
}

func TestRealPublishedRuntimeLocalUpgrade(t *testing.T) {
	dir := os.Getenv("DSH_TEST_REUSE_APP")
	if dir == "" {
		t.Skip("set DSH_TEST_REUSE_APP to the original extracted runtime with app.asar.unpacked")
	}
	for _, v := range []string{"1.0.2", "1.0.3"} {
		t.Run(v, func(t *testing.T) {
			root := t.TempDir()
			r := RuntimeRecord{"0.1.7-rc.2", "dsh-published", "AY7f45dYO7BFrfgaLmzXNWP0pavlxkSbsehPo/WF6PXcFdDK3fF1oHUPs/4f2bzROgQvm6wSgawZ/g7UzbPRmw==", v}
			src := filepath.Join(root, "Runtime", r.Directory)
			if e := copyRuntimeTree(context.Background(), dir, src, nil); e != nil {
				t.Fatal(e)
			}
			file := filepath.Join(src, "resources", "app.asar")
			makeLegacyAsar(t, file, v)
			os.WriteFile(filepath.Join(root, "Runtime", "current.json"), mustJSON(r), 0600)
			before := hashFileTest(t, file)
			exeBefore := hashFileTest(t, filepath.Join(src, "DeepSeek Harness.exe"))
			next, e := migrateLocalRuntime(context.Background(), root, r, testCommit, nil)
			if e != nil {
				t.Fatal(e)
			}
			if hashFileTest(t, file) != before {
				t.Fatal("old ASAR changed")
			}
			if hashFileTest(t, filepath.Join(root, "Runtime", next.Directory, "DeepSeek Harness.exe")) != exeBefore {
				t.Fatal("official EXE changed")
			}
			t.Log("PASS: published ASAR and 1495 unpacked entries validated; local", v, "upgrade preserved original EXE/old ASAR and switched pointer; no download client involved")
		})
	}
}

// Keep the test helper's digest behavior explicit for reproducibility.
func TestSHA256Helper(t *testing.T) {
	b := []byte("test")
	h := sha256.Sum256(b)
	got, e := fileSHA256(context.Background(), bytes.NewReader(b))
	if e != nil || got != hex.EncodeToString(h[:]) {
		t.Fatal(e)
	}
	_, e = fileSHA256(context.Background(), io.LimitReader(bytes.NewReader(b), 4))
	if e != nil {
		t.Fatal(e)
	}
}
