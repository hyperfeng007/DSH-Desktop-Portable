package main

import (
	"encoding/base64"
	"fmt"
	"gopkg.in/yaml.v3"
	"net/url"
	"regexp"
)

const officialFeed = "https://download.deepseek.com/dsh-desk/feeds/win-x64/nightly.yml"

type Feed struct {
	Version string `yaml:"version"`
	Files   []struct {
		URL    string `yaml:"url"`
		SHA512 string `yaml:"sha512"`
		Size   int64  `yaml:"size"`
	} `yaml:"files"`
}

func parseFeed(raw []byte) (Feed, error) {
	var f Feed
	if e := yaml.Unmarshal(raw, &f); e != nil {
		return f, e
	}
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?$`).MatchString(f.Version) || len(f.Files) != 1 {
		return f, fmt.Errorf("官方更新元数据结构变化，需升级便携启动器")
	}
	a := f.Files[0]
	u, e := url.Parse(a.URL)
	if e != nil || u.Scheme != "https" || u.Host != "download.deepseek.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/dsh-desk/bin/win-x64/deepseek-harness-"+f.Version+"-win-x64.exe" {
		return f, fmt.Errorf("拒绝非预期官方安装包地址")
	}
	h, e := base64.StdEncoding.DecodeString(a.SHA512)
	if e != nil || len(h) != 64 || a.Size < 1<<20 || a.Size > 2<<30 {
		return f, fmt.Errorf("官方校验信息无效")
	}
	return f, nil
}
