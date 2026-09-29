package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type RuntimeRecord struct {
	Version   string `json:"version"`
	Directory string `json:"directory"`
	SHA512    string `json:"upstream_sha512"`
	Wrapper   string `json:"wrapper"`
}

// Unlike currentRuntime, this accepts a known older wrapper for local migration.
func installedRuntime(root string) (RuntimeRecord, error) {
	var r RuntimeRecord
	b, err := os.ReadFile(filepath.Join(root, "Runtime", "current.json"))
	if err != nil {
		return r, err
	}
	if len(b) > 16384 {
		return r, fmt.Errorf("运行时记录过大")
	}
	if err = json.Unmarshal(b, &r); err != nil {
		return r, err
	}
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?$`).MatchString(r.Version) {
		return r, fmt.Errorf("运行时版本记录无效")
	}
	if !strings.HasPrefix(r.Directory, "dsh-") || strings.ContainsAny(r.Directory, "/\\:") {
		return r, fmt.Errorf("运行时目录记录无效")
	}
	digest, err := base64.StdEncoding.DecodeString(r.SHA512)
	if err != nil || len(digest) != 64 {
		return r, fmt.Errorf("运行时来源校验记录无效")
	}
	dir, err := safeChild(filepath.Join(root, "Runtime"), r.Directory)
	if err != nil {
		return r, err
	}
	for _, rel := range []string{"DeepSeek Harness.exe", "resources/app.asar"} {
		st, e := os.Lstat(filepath.Join(dir, filepath.FromSlash(rel)))
		if e != nil {
			return r, e
		}
		if !st.Mode().IsRegular() || st.Size() == 0 {
			return r, fmt.Errorf("运行时文件缺失或不是普通文件")
		}
	}
	return r, nil
}
func currentRuntime(root string) (RuntimeRecord, error) {
	r, err := installedRuntime(root)
	if err == nil && r.Wrapper != wrapperVersion {
		err = fmt.Errorf("便携运行时版本不匹配")
	}
	return r, err
}
func sameOfficialBuild(r RuntimeRecord, f Feed) bool {
	return len(f.Files) == 1 && r.Version == f.Version && r.SHA512 == f.Files[0].SHA512
}
func supportedLocalWrapper(v string) bool { return v == "1.0.2" || v == "1.0.3" || v == wrapperVersion }

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(p)
}

// Reject filesystem links/reparse-like entries rather than copying outside the runtime.
func runtimeTreeSize(ctx context.Context, dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		st, e := d.Info()
		if e != nil {
			return e
		}
		if !st.IsDir() && !st.Mode().IsRegular() {
			return fmt.Errorf("运行时包含链接或特殊文件，拒绝自动复用")
		}
		if st.Mode().IsRegular() {
			total += st.Size()
			if total > 16<<30 {
				return fmt.Errorf("本地运行时异常过大")
			}
		}
		return nil
	})
	return total, err
}
func copyRuntimeTree(ctx context.Context, src, dst string, progress func(int64, int64)) error {
	total, err := runtimeTreeSize(ctx, src)
	if err != nil {
		return err
	}
	var copied int64
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		rel, e := filepath.Rel(src, p)
		if e != nil {
			return e
		}
		dest := filepath.Join(dst, rel)
		st, e := os.Lstat(p)
		if e != nil {
			return e
		}
		if st.IsDir() {
			return os.MkdirAll(dest, 0700)
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("复制期间发现链接或特殊文件")
		}
		in, e := os.Open(p)
		if e != nil {
			return e
		}
		defer in.Close()
		out, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, st.Mode().Perm())
		if e != nil {
			return e
		}
		n, e := io.Copy(out, contextReader{ctx, in})
		if e == nil && n != st.Size() {
			e = fmt.Errorf("复制期间运行时文件大小改变")
		}
		if e == nil {
			e = out.Sync()
		}
		ce := out.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
		copied += n
		if progress != nil {
			progress(copied, total)
		}
		return nil
	})
}

// A local-only transaction. It has no HTTP client and cannot download a payload.
// The active tree is never patched, renamed, or deleted. commit must atomically
// replace the pointer after preserving its previous bytes.
func migrateLocalRuntime(ctx context.Context, root string, r RuntimeRecord, commit func(string, RuntimeRecord) error, progress func(int64, int64)) (RuntimeRecord, error) {
	if !supportedLocalWrapper(r.Wrapper) {
		return r, fmt.Errorf("此便携版本暂不支持本地迁移")
	}
	runtimeDir := filepath.Join(root, "Runtime")
	st, err := os.Lstat(runtimeDir)
	if err != nil {
		return r, err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return r, fmt.Errorf("Runtime 目录不是普通目录")
	}
	src, err := safeChild(runtimeDir, r.Directory)
	if err != nil {
		return r, err
	}
	before, err := os.ReadFile(filepath.Join(runtimeDir, "current.json"))
	if err != nil {
		return r, err
	}
	onDisk, err := installedRuntime(root)
	if err != nil || onDisk != r {
		return r, fmt.Errorf("运行时指针已改变，请重新启动")
	}
	if _, err = runtimeTreeSize(ctx, src); err != nil {
		return r, err
	}
	if err = validatePortableAsar(ctx, filepath.Join(src, "resources", "app.asar"), r); err != nil {
		return r, err
	}
	stage, err := os.MkdirTemp(runtimeDir, ".local-upgrade-")
	if err != nil {
		return r, err
	}
	defer os.RemoveAll(stage)
	app := filepath.Join(stage, "app")
	if err = copyRuntimeTree(ctx, src, app, progress); err != nil {
		return r, err
	}
	if err = upgradePortableAsar(ctx, filepath.Join(app, "resources", "app.asar"), r); err != nil {
		return r, err
	}
	next := r
	next.Wrapper = wrapperVersion
	if err = validatePortableAsar(ctx, filepath.Join(app, "resources", "app.asar"), next); err != nil {
		return r, err
	}
	// Stable unique name without sharing mutable files through hard links.
	next.Directory = "dsh-" + r.Version + "-portable-" + wrapperVersion + "-" + strings.TrimPrefix(filepath.Base(stage), ".local-upgrade-")
	dest := filepath.Join(runtimeDir, next.Directory)
	if err = ctx.Err(); err != nil {
		return r, err
	}
	now, err := os.ReadFile(filepath.Join(runtimeDir, "current.json"))
	if err != nil || string(now) != string(before) {
		return r, fmt.Errorf("迁移期间运行时指针已改变，旧版本保持不动")
	}
	if err = os.Rename(app, dest); err != nil {
		return r, err
	}
	committed := false
	defer func() {
		if !committed {
			os.RemoveAll(dest)
		}
	}()
	if err = ctx.Err(); err != nil {
		return r, err
	}
	if err = commit(root, next); err != nil {
		return r, err
	}
	committed = true
	return next, nil
}

func fileSHA256(ctx context.Context, r io.Reader) (string, error) {
	h := sha256.New()
	_, e := io.Copy(h, contextReader{ctx, r})
	return hex.EncodeToString(h.Sum(nil)), e
}
