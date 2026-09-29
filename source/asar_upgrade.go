package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Byte-exact known adapters, not executable fallback code.
//
//go:embed legacy/*/*.mjs
var legacyAdapters embed.FS

const hostEntry = "dsh/node_modules/@deepseek-ai/dsh-desktop-host/lib/index.js"
const hostPrelude = "import {installPortableNodeProxy} from \"../../../../../portable-node-proxy.mjs\";\nawait installPortableNodeProxy(\"host\");\n"
const desktopHook = "\n\t\tif (typeof globalThis.__dshPortableCheckUpdate === \"function\") return globalThis.__dshPortableCheckUpdate();"

var adapterNames = []string{"portable.mjs", "portable-node-proxy.mjs", "portable-network.mjs", "portable-update.mjs"}

func adapterForVersion(version, name, main string) ([]byte, error) {
	var b []byte
	var e error
	if version == wrapperVersion {
		b, e = assets.ReadFile("assets/" + name)
	} else if version == "1.0.2" || version == "1.0.3" {
		b, e = legacyAdapters.ReadFile("legacy/" + version + "/" + name)
	} else {
		return nil, fmt.Errorf("不支持的旧适配版本")
	}
	if name == "portable.mjs" {
		b = []byte(strings.ReplaceAll(string(b), "__ORIGINAL_MAIN__", main))
	}
	return b, e
}
func originalMain(pkg map[string]any) (string, map[string]string, error) {
	raw, ok := pkg["dshPortableOriginalSHA256"].(map[string]any)
	if !ok || len(raw) != 2 {
		return "", nil, fmt.Errorf("缺少原始入口校验记录")
	}
	hashes := map[string]string{}
	main := ""
	for name, v := range raw {
		h, ok := v.(string)
		if !ok || len(h) != 64 {
			return "", nil, fmt.Errorf("原始入口校验值无效")
		}
		if _, e := hex.DecodeString(h); e != nil {
			return "", nil, e
		}
		if _, e := safeChild("x", name); e != nil {
			return "", nil, e
		}
		hashes[name] = h
		if name != hostEntry {
			main = name
		}
	}
	if main == "" || hashes[hostEntry] == "" || strings.ContainsAny(main, "\"'\r\n") {
		return "", nil, fmt.Errorf("原始入口记录不完整")
	}
	return main, hashes, nil
}

// This detects corruption and unknown local modifications using retained ASAR
// digests. It is not independent publisher authentication of a local directory.
func validatePortableAsar(ctx context.Context, file string, r RuntimeRecord) error {
	if !supportedLocalWrapper(r.Wrapper) {
		return fmt.Errorf("此旧版适配不支持安全复用")
	}
	f, e := os.Open(file)
	if e != nil {
		return e
	}
	defer f.Close()
	tree, base, e := readAsarHeader(f)
	if e != nil {
		return e
	}
	raw, e := readAsarFile(f, tree, base, "package.json")
	if e != nil {
		return e
	}
	var pkg map[string]any
	if e = json.Unmarshal(raw, &pkg); e != nil {
		return e
	}
	if pkg["main"] != "portable.mjs" || pkg["version"] != r.Version || pkg["dshPortableWrapper"] != r.Wrapper {
		return fmt.Errorf("运行时版本与 ASAR 适配记录不匹配")
	}
	main, hashes, e := originalMain(pkg)
	if e != nil {
		return e
	}
	for _, name := range adapterNames {
		got, e := readAsarFile(f, tree, base, name)
		if e != nil {
			return e
		}
		want, e := adapterForVersion(r.Wrapper, name, main)
		if e != nil {
			return e
		}
		if !bytes.Equal(got, want) {
			return fmt.Errorf("旧便携适配模块与已知版本不一致: %s", name)
		}
	}
	for _, name := range []string{main, hostEntry} {
		got, e := readAsarFile(f, tree, base, name)
		if e != nil {
			return e
		}
		if name == hostEntry {
			if !bytes.HasPrefix(got, []byte(hostPrelude)) {
				return fmt.Errorf("Host 入口结构未知")
			}
			got = got[len(hostPrelude):]
		} else {
			if bytes.Count(got, []byte(desktopHook)) != 1 {
				return fmt.Errorf("桌面更新入口结构未知")
			}
			got = bytes.Replace(got, []byte(desktopHook), nil, 1)
		}
		sum := sha256.Sum256(got)
		if hex.EncodeToString(sum[:]) != hashes[name] {
			return fmt.Errorf("原始官方入口校验不一致")
		}
	}
	st, e := f.Stat()
	if e != nil {
		return e
	}
	bodySize := st.Size() - base
	if bodySize < 0 {
		return fmt.Errorf("ASAR 长度无效")
	}
	var walk func(*asarEntry, string) error
	walk = func(entry *asarEntry, name string) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if entry == nil {
			return fmt.Errorf("空 ASAR 条目")
		}
		if entry.Files != nil {
			for child, v := range entry.Files {
				if strings.ContainsAny(child, "/\\:") || child == "." || child == ".." || child == "" {
					return fmt.Errorf("ASAR 路径不安全")
				}
				rel := child
				if name != "" {
					rel = name + "/" + child
				}
				if e := walk(v, rel); e != nil {
					return e
				}
			}
			return nil
		}
		if entry.Link != "" {
			if _, e := safeChild("x", entry.Link); e != nil {
				return e
			}
			if asarFile(tree, entry.Link) == nil {
				return fmt.Errorf("ASAR 虚拟链接目标缺失")
			}
			return nil
		}
		if entry.Size < 0 || entry.Integrity == nil || entry.Integrity.Algorithm != "SHA256" || len(entry.Integrity.Hash) != 64 {
			return fmt.Errorf("ASAR 文件缺少有效完整性记录: %s", name)
		}
		var reader io.Reader
		var unpacked *os.File
		if entry.Unpacked {
			p, e := safeChild(file+".unpacked", name)
			if e != nil {
				return e
			}
			st, e := os.Lstat(p)
			if e != nil {
				return e
			}
			if !st.Mode().IsRegular() || st.Size() != entry.Size {
				return fmt.Errorf("ASAR 外置文件类型或大小不匹配")
			}
			unpacked, e = os.Open(p)
			if e != nil {
				return e
			}
			defer unpacked.Close()
			reader = unpacked
		} else {
			offset, e := strconv.ParseInt(entry.Offset, 10, 64)
			if e != nil || offset < 0 || offset > bodySize || entry.Size > bodySize-offset {
				return fmt.Errorf("ASAR 文件范围无效")
			}
			reader = io.NewSectionReader(f, base+offset, entry.Size)
		}
		sum, e := fileSHA256(ctx, reader)
		if e != nil {
			return e
		}
		if sum != entry.Integrity.Hash {
			return fmt.Errorf("ASAR 文件内容校验失败: %s", name)
		}
		return nil
	}
	return walk(tree, "")
}

func upgradePortableAsar(ctx context.Context, file string, r RuntimeRecord) error {
	if e := validatePortableAsar(ctx, file, r); e != nil {
		return e
	}
	f, e := os.Open(file)
	if e != nil {
		return e
	}
	defer f.Close()
	tree, base, e := readAsarHeader(f)
	if e != nil {
		return e
	}
	raw, e := readAsarFile(f, tree, base, "package.json")
	if e != nil {
		return e
	}
	var pkg map[string]any
	if e = json.Unmarshal(raw, &pkg); e != nil {
		return e
	}
	main, _, e := originalMain(pkg)
	if e != nil {
		return e
	}
	pkg["dshPortableWrapper"] = wrapperVersion
	raw, e = json.MarshalIndent(pkg, "", "  ")
	if e != nil {
		return e
	}
	payloads := []asarPayload{{"package.json", raw}}
	for _, name := range adapterNames {
		data, e := adapterForVersion(wrapperVersion, name, main)
		if e != nil {
			return e
		}
		payloads = append(payloads, asarPayload{name, data})
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if e = writeAsarPayloads(file, f, tree, base, payloads); e != nil {
		return e
	}
	cfg := filepath.Join(filepath.Dir(file), "app-update.yml")
	if _, e = os.Stat(cfg); e == nil {
		return os.Rename(cfg, cfg+".portable-disabled")
	}
	if !os.IsNotExist(e) {
		return e
	}
	return nil
}
