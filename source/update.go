package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type updateCheckRequest struct {
	Nonce string `json:"nonce"`
	ID    string `json:"id"`
}
type updateCheckResponse struct {
	Nonce   string `json:"nonce"`
	ID      string `json:"id"`
	Version string `json:"version,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Error   string `json:"error,omitempty"`
}

func fetchUpdate(ctx context.Context, client *http.Client) (Feed, error) {
	ctx, cancel := context.WithTimeout(ctx, 85*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", officialFeed, nil)
	resp, e := client.Do(req)
	if e != nil {
		return Feed{}, fmt.Errorf("通过启动器网络检查更新失败，请查看 network.log")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return Feed{}, fmt.Errorf("官方更新源返回 HTTP %d", resp.StatusCode)
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if e != nil {
		return Feed{}, fmt.Errorf("官方更新元数据读取失败")
	}
	return parseFeed(raw)
}

// Called only by the launcher that owns this run directory. Never accept URLs from the renderer.
func serviceUpdateCheck(ctx context.Context, client *http.Client, run, nonce string) error {
	request := filepath.Join(run, "update-check.request.json")
	raw, e := os.ReadFile(request)
	if e != nil {
		return e
	}
	var input updateCheckRequest
	if e = json.Unmarshal(raw, &input); e != nil {
		return e
	}
	if input.Nonce != nonce || len(input.ID) != 24 {
		return fmt.Errorf("invalid update-check request")
	}
	os.Remove(request)
	output := updateCheckResponse{Nonce: nonce, ID: input.ID}
	feed, e := fetchUpdate(ctx, client)
	if e != nil {
		output.Error = e.Error()
	} else {
		output.Version = feed.Version
		output.Size = feed.Files[0].Size
	}
	data, _ := json.Marshal(output)
	// The desktop retries partially written JSON; the nonce and request ID must both match.
	return os.WriteFile(filepath.Join(run, "update-check.response.json"), data, 0600)
}
