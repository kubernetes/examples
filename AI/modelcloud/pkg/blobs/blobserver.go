// Copyright 2025 The Kubernetes Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package blobs

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"k8s.io/klog/v2"
)

type BlobServer struct {
	// URL is the base URL to the blob-server, typically http://blob-store
	URL *url.URL
}

var _ BlobStore = &BlobServer{}

func (l *BlobServer) Upload(ctx context.Context, r io.Reader, info BlobInfo) error {
	url := l.URL.JoinPath(info.Hash)
	return l.uploadFile(ctx, url.String(), r)
}

func (l *BlobServer) Download(ctx context.Context, info BlobInfo, destPath string) error {
	url := l.URL.JoinPath(info.Hash)
	return l.downloadToFile(ctx, url.String(), destPath)
}

func (l *BlobServer) downloadToFile(ctx context.Context, url string, destPath string) error {
	log := klog.FromContext(ctx)

	dir := filepath.Dir(destPath)
	tempFile, err := os.CreateTemp(dir, "model")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}

	shouldDeleteTempFile := true
	defer func() {
		if shouldDeleteTempFile {
			if err := os.Remove(tempFile.Name()); err != nil {
				log.Error(err, "removing temp file", "path", tempFile.Name)
			}
		}
	}()

	shouldCloseTempFile := true
	defer func() {
		if shouldCloseTempFile {
			if err := tempFile.Close(); err != nil {
				log.Error(err, "closing temp file", "path", tempFile.Name)
			}
		}
	}()

	if err := l.downloadToWriter(ctx, url, tempFile); err != nil {
		return fmt.Errorf("downloading from %q: %w", url, err)
	}

	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}
	shouldCloseTempFile = false

	if err := os.Rename(tempFile.Name(), destPath); err != nil {
		return fmt.Errorf("renaming temp file: %w", err)
	}
	shouldDeleteTempFile = false

	return nil
}

func (l *BlobServer) downloadToWriter(ctx context.Context, url string, w io.Writer) error {
	log := klog.FromContext(ctx)

	log.Info("downloading from url", "url", url)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	startedAt := time.Now()

	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("doing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		if resp.StatusCode == 404 {
			return fmt.Errorf("blob not found: %w", os.ErrNotExist)
		}
		return fmt.Errorf("unexpected status downloading from upstream source: %v", resp.Status)
	}

	n, err := io.Copy(w, resp.Body)
	if err != nil {
		return fmt.Errorf("downloading from upstream source: %w", err)
	}

	log.Info("downloaded blob", "url", url, "bytes", n, "duration", time.Since(startedAt))

	return nil
}

func (l *BlobServer) uploadFile(ctx context.Context, url string, r io.Reader) error {
	log := klog.FromContext(ctx)

	log.Info("uploading to url", "url", url)

	req, err := http.NewRequestWithContext(ctx, "PUT", url, r)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	startedAt := time.Now()

	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("doing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 201 {
		return fmt.Errorf("unexpected status uploading to %q: %v", url, resp.Status)
	}

	log.Info("uploaded blob", "url", url, "duration", time.Since(startedAt))

	return nil
}
