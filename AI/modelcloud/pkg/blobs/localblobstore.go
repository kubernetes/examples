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
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"k8s.io/klog/v2"
)

type LocalBlobStore struct {
	LocalDir string // Directory to store blobs
}

var _ BlobStore = (*LocalBlobStore)(nil)

func (j *LocalBlobStore) Upload(ctx context.Context, r io.Reader, info BlobInfo) error {
	log := klog.FromContext(ctx)

	localPath := filepath.Join(j.LocalDir, info.Hash)
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return fmt.Errorf("creating parent directories for %q: %w", localPath, err)
	}

	stat, err := os.Stat(localPath)
	if err == nil {
		log.Info("file already exists, skipping upload", "path", localPath, "size", stat.Size(), "modTime", stat.ModTime())
		return nil
	}

	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("checking for destination file %q: %w", localPath, err)
	}

	// TODO: Try to optimize case where already exists?
	if _, err := writeToFile(ctx, r, localPath, info); err != nil {
		return fmt.Errorf("writing file %q: %w", localPath, err)
	}

	log.Info("added blob to local store", "path", localPath)

	return nil
}

func (j *LocalBlobStore) Download(ctx context.Context, info BlobInfo, destinationPath string) error {
	log := klog.FromContext(ctx)

	localPath := filepath.Join(j.LocalDir, info.Hash)

	startedAt := time.Now()
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("opening local blob %q: %w", localPath, err)
	}
	defer f.Close()

	n, err := writeToFile(ctx, f, destinationPath, info)
	if err != nil {
		return fmt.Errorf("copying file %q to %q: %w", localPath, destinationPath, err)
	}

	log.Info("downloaded blob from local store", "source", localPath, "destination", destinationPath, "bytes", n, "duration", time.Since(startedAt))

	return nil
}

func writeToFile(ctx context.Context, src io.Reader, destinationPath string, info BlobInfo) (int64, error) {
	log := klog.FromContext(ctx)

	dir := filepath.Dir(destinationPath)
	tempFile, err := os.CreateTemp(dir, "download")
	if err != nil {
		return 0, fmt.Errorf("creating temp file: %w", err)
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

	hasher := sha256.New()
	mw := io.MultiWriter(tempFile, hasher)

	n, err := io.Copy(mw, src)
	if err != nil {
		return n, fmt.Errorf("downloading from upstream source: %w", err)
	}

	calculatedHash := fmt.Sprintf("%x", hasher.Sum(nil))
	if info.Hash != "" && calculatedHash != info.Hash {
		return n, fmt.Errorf("hash mismatch: expected %q, got %q", info.Hash, calculatedHash)
	}

	if err := tempFile.Close(); err != nil {
		return n, fmt.Errorf("closing temp file: %w", err)
	}
	shouldCloseTempFile = false

	if err := os.Rename(tempFile.Name(), destinationPath); err != nil {
		return n, fmt.Errorf("renaming temp file: %w", err)
	}
	shouldDeleteTempFile = false

	return n, nil
}
