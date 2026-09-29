package main

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"golang.org/x/sys/windows"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func checkTools(root string) error {
	checks := map[string]string{
		"7z.exe": "4a41aa37786c7eae7451e81c2c97458d5d1ae5a3a8154637a0d5f77adc05e619",
		"7z.dll": "bbd705e3b58ca7677c1e9e67473f166a6712da034dcb567d571fbb67507a443f",
	}
	for name, want := range checks {
		f, e := os.Open(filepath.Join(root, "tools", name))
		if e != nil {
			return fmt.Errorf("缺少 tools/%s，请完整解压 ZIP", name)
		}
		h := sha256.New()
		_, e = io.Copy(h, f)
		f.Close()
		if e != nil {
			return e
		}
		if hex.EncodeToString(h.Sum(nil)) != want {
			return fmt.Errorf("tools/%s 校验失败，请重新解压原始便携包", name)
		}
	}
	return nil
}
func obtainRuntime(ctx context.Context, root string, client *http.Client, j *Job, env []string, force bool) (RuntimeRecord, error) {
	current, oldErr := installedRuntime(root)
	if oldErr == nil && current.Wrapper == wrapperVersion && !force {
		return current, nil
	}
	status("01 / 05 · 检查官方桌面版本", "正在访问 DeepSeek 官方更新源；不会运行安装程序。", -1)
	req, _ := http.NewRequestWithContext(ctx, "GET", officialFeed, nil)
	resp, e := client.Do(req)
	if e != nil {
		return current, fmt.Errorf("读取官方更新源失败，请检查网络或代理: %w", e)
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if e != nil {
		return current, e
	}
	if resp.StatusCode != 200 {
		return current, fmt.Errorf("官方更新源 HTTP %d", resp.StatusCode)
	}
	feed, e := parseFeed(raw)
	if e != nil {
		return current, e
	}
	if oldErr == nil && sameOfficialBuild(current, feed) {
		if current.Wrapper == wrapperVersion {
			status("已是当前官方版本", current.Version+" · 复用本地运行时，不下载", 100)
			return current, nil
		}
		status("校验本地运行时", "官方版本未变化；正在检查是否可仅更新便携适配模块，不下载桌面安装包。", -1)
		src := filepath.Join(root, "Runtime", current.Directory)
		var localErr error
		var size int64
		if !supportedLocalWrapper(current.Wrapper) {
			localErr = fmt.Errorf("旧适配版本 %s 暂不支持本地迁移", current.Wrapper)
		} else {
			size, localErr = runtimeTreeSize(ctx, src)
			if localErr == nil {
				localErr = validatePortableAsar(ctx, filepath.Join(src, "resources", "app.asar"), current)
			}
		}
		if ctx.Err() != nil {
			return current, ctx.Err()
		}
		if localErr == nil {
			asarInfo, e := os.Stat(filepath.Join(src, "resources", "app.asar"))
			if e != nil {
				return current, e
			}
			var free uint64
			if e = windows.GetDiskFreeSpaceEx(wstr(root), &free, nil, nil); e != nil {
				return current, e
			}
			needed := uint64(size + asarInfo.Size() + 64*1024*1024)
			if free < needed {
				return current, fmt.Errorf("本地安全升级需要 %.1f GB 空间（现有 %.1f GB），旧运行时保留，未下载", float64(needed)/(1<<30), float64(free)/(1<<30))
			}
			status("本地升级 · 不下载 DSH Desktop", "复制已验证的运行时并更新适配模块；旧版本保留，数据不动。", 0)
			last := time.Time{}
			next, e := migrateLocalRuntime(ctx, root, current, commitRuntimePointer, func(done, total int64) {
				if time.Since(last) > 250*time.Millisecond || done == total {
					percent := 0
					if total > 0 {
						percent = int(90 * done / total)
					}
					status("本地升级 · 不下载 DSH Desktop", fmt.Sprintf("复制 %.0f / %.0f MB；完成后校验并切换，旧版本保留。", float64(done)/1e6, float64(total)/1e6), percent)
					last = time.Now()
				}
			})
			if e != nil {
				return current, fmt.Errorf("本地升级未完成，原运行时和数据保留（未改为下载）: %w", e)
			}
			status("本地适配升级完成", "复用官方版本 "+next.Version+"，未下载桌面安装包。", 100)
			return next, nil
		}
		if !confirm("本地运行时未通过复用检查", localErr.Error()+"\n\n旧运行时和 Data 保留。是否重新下载官方安装包来修复？取消则不下载。") {
			return current, fmt.Errorf("已取消下载；原运行时和数据保留")
		}
	} else if oldErr == nil && !confirm("官方构建与本地不同", "当前版本："+current.Version+"\n更新源版本："+feed.Version+"\n\n将下载并准备官方构建；现有 Data 数据不删除。升级前请备份 Data。\n\n是否继续？") {
		if current.Wrapper == wrapperVersion {
			return current, nil
		}
		return current, fmt.Errorf("已取消更新；保留原运行时，请使用原启动器")
	}
	if oldErr != nil {
		if _, statErr := os.Stat(filepath.Join(root, "Runtime", "current.json")); statErr == nil {
			if !confirm("本地运行时记录不完整", "现有运行时无法直接复用："+oldErr.Error()+"\n\n是否重新下载官方安装包？原有 Data 保留，取消则不下载。") {
				return current, fmt.Errorf("已取消下载；原目录和数据保留")
			}
		}
	}
	if e := checkTools(root); e != nil {
		return current, e
	}
	var free uint64
	if e = windows.GetDiskFreeSpaceEx(wstr(root), &free, nil, nil); e != nil {
		return current, e
	}
	if free < 4<<30 {
		return current, fmt.Errorf("准备运行时需要至少 4 GB 可用空间（当前 %.1f GB）", float64(free)/(1<<30))
	}
	work := filepath.Join(root, "Runtime", ".staging")
	if e = os.RemoveAll(work); e != nil {
		return current, e
	}
	if e = os.MkdirAll(work, 0700); e != nil {
		return current, e
	}
	archive := filepath.Join(work, "official.exe")
	a := feed.Files[0]
	status("02 / 05 · 下载官方桌面程序", fmt.Sprintf("版本 %s · %.0f MB · 文件保存在 U 盘", feed.Version, float64(a.Size)/1e6), 0)
	if e = download(ctx, client, a.URL, archive, a.Size, a.SHA512); e != nil {
		return current, e
	}
	if e = ctx.Err(); e != nil {
		return current, e
	}
	status("03 / 05 · 校验完成，解包运行时", "解压两层官方安装包；U 盘较慢时可能需要几分钟。", -1)
	extractor := filepath.Join(root, "tools", "7z.exe")
	log := filepath.Join(root, "Data", "Logs", "extract.log")
	run := func(args ...string) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		c, e := j.Start(extractor, args, root, env, log)
		if e != nil {
			return e
		}
		return c.Wait()
	}
	installer := filepath.Join(work, "installer")
	if e = run("x", archive, "-o"+installer, "-y", "-bd", "-bso0", "-bsp0", "$PLUGINSDIR/app-64.7z"); e != nil {
		return current, fmt.Errorf("安装包解压失败: %w", e)
	}
	packed := filepath.Join(installer, "$PLUGINSDIR", "app-64.7z")
	if _, e = os.Stat(packed); e != nil {
		return current, fmt.Errorf("官方安装包结构变化，未找到 app-64.7z")
	}
	appDir := filepath.Join(work, "app")
	if e = run("x", packed, "-o"+appDir, "-y", "-bd", "-bso0", "-bsp0"); e != nil {
		return current, fmt.Errorf("桌面运行时解压失败: %w", e)
	}
	if e = ctx.Err(); e != nil {
		return current, e
	}
	if _, e = os.Stat(filepath.Join(appDir, "DeepSeek Harness.exe")); e != nil {
		return current, fmt.Errorf("官方桌面可执行文件缺失")
	}
	status("04 / 05 · 应用便携隔离设置", "配置、缓存、日志和临时目录重定向到 U 盘；关闭安装式自动更新。", -1)
	if e = patchAsar(filepath.Join(appDir, "resources", "app.asar")); e != nil {
		return current, fmt.Errorf("便携适配失败: %w", e)
	}
	dir := "dsh-" + feed.Version + "-" + time.Now().UTC().Format("20060102T150405")
	dest := filepath.Join(root, "Runtime", dir)
	if e = os.Rename(appDir, dest); e != nil {
		return current, e
	}
	record := RuntimeRecord{feed.Version, dir, a.SHA512, wrapperVersion}
	if e = commitRuntimePointer(root, record); e != nil {
		return current, e
	}
	os.WriteFile(filepath.Join(dest, "upstream-feed.yml"), raw, 0600)
	os.RemoveAll(work)
	// Old runtime is kept for manual rollback; data migrations cannot safely be auto-reversed.
	return record, nil
}
func download(ctx context.Context, c *http.Client, u, dest string, size int64, want string) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	resp, e := c.Do(req)
	if e != nil {
		return fmt.Errorf("下载失败: %w", e)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("下载返回 HTTP %d", resp.StatusCode)
	}
	f, e := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	h := sha512.New()
	reader := io.LimitReader(resp.Body, size+1)
	buf := make([]byte, 256*1024)
	var n int64
	last := time.Time{}
	for {
		if e = ctx.Err(); e != nil {
			return e
		}
		m, er := reader.Read(buf)
		if m > 0 {
			if _, e = f.Write(buf[:m]); e != nil {
				return e
			}
			h.Write(buf[:m])
			n += int64(m)
			if time.Since(last) > 200*time.Millisecond {
				status("02 / 05 · 下载官方桌面程序", fmt.Sprintf("%.1f / %.1f MB · %.0f%%", float64(n)/1e6, float64(size)/1e6, 100*float64(n)/float64(size)), int(100*n/size))
				last = time.Now()
			}
		}
		if er == io.EOF {
			break
		}
		if er != nil {
			return er
		}
	}
	if n != size || base64.StdEncoding.EncodeToString(h.Sum(nil)) != strings.TrimSpace(want) {
		return fmt.Errorf("下载大小或 SHA-512 校验失败；未执行或解压此文件。请重试")
	}
	return f.Sync()
}

// Preserve the old pointer, then atomically publish the new validated directory.
// A pre-publication failure never changes current.json.
func commitRuntimePointer(root string, record RuntimeRecord) error {
	dir := filepath.Join(root, "Runtime")
	current := filepath.Join(dir, "current.json")
	old, err := os.ReadFile(current)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		if err = atomicRuntimeFile(filepath.Join(dir, "previous.json"), old); err != nil {
			return err
		}
	}
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return atomicRuntimeFile(current, raw)
}
func atomicRuntimeFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".runtime-pointer-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err != nil {
		return err
	}
	if ce != nil {
		return ce
	}
	return windows.MoveFileEx(wstr(name), wstr(path), windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
