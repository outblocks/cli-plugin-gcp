package fileutil

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
)

func DownloadFile(ctx context.Context, url, target string) error {
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
		},
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, http.NoBody)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("failed to fetch %s : %s", url, resp.Status)
	}

	f, err := os.Create(target)
	if err != nil {
		return err
	}

	_, err = io.Copy(f, resp.Body)
	resp.Body.Close()

	return err
}
