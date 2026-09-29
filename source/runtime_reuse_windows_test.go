package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These require native Windows execution; cross-compilation is not execution.
func TestWindowsExistingLatestOnlyFetchesFeed(t *testing.T) {
	for _, v := range []string{"1.0.2", "1.0.3"} {
		t.Run(v, func(t *testing.T) {
			root, r := localFixture(t, v)
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.URL.String() != officialFeed {
					return nil, fmt.Errorf("unexpected download request")
				}
				raw := fmt.Sprintf("version: %s\nfiles:\n  - url: https://download.deepseek.com/dsh-desk/bin/win-x64/deepseek-harness-%s-win-x64.exe\n    sha512: %s\n    size: 288245480\n", r.Version, r.Version, r.SHA512)
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(raw)), Header: make(http.Header)}, nil
			})}
			next, e := obtainRuntime(context.Background(), root, client, nil, nil, false)
			if e != nil {
				t.Fatal(e)
			}
			if calls != 1 || next.Wrapper != wrapperVersion {
				t.Fatal("did not use feed-only local migration")
			}
			if _, e = os.Stat(filepath.Join(root, "Runtime", "previous.json")); e != nil {
				t.Fatal("old pointer not preserved", e)
			}
			again, e := obtainRuntime(context.Background(), root, client, nil, nil, false)
			if e != nil || again != next || calls != 1 {
				t.Fatal("subsequent launch used network or recopied runtime", e)
			}
		})
	}
}
