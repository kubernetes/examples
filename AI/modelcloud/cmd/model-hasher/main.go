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

package main

import (
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/examples/AI/modelcloud/pkg/api"
	"k8s.io/examples/AI/modelcloud/pkg/blobs"

	"sigs.k8s.io/yaml"

	"k8s.io/klog/v2"
)

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	log := klog.FromContext(ctx)

	srcDir := ""
	flag.StringVar(&srcDir, "src", srcDir, "Directory to scan for model files")

	upload := ""
	flag.StringVar(&upload, "upload", upload, "If set, the URL to upload to")

	flag.Parse()

	if srcDir == "" {
		return fmt.Errorf("--src is required")
	}

	model := &api.Model{}

	// Walk all the files in the directory, and print their SHA256 hashes
	err := filepath.WalkDir(srcDir, func(path string, dent fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relativePath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return fmt.Errorf("getting relative path for %q: %w", path, err)
		}
		if dent.IsDir() {
			if relativePath == ".git" {
				return filepath.SkipDir
			}
			return nil
		}

		hash, err := fileHash(path)
		if err != nil {
			log.Error(err, "error hashing file", "path", path)
			return nil
		}

		model.Spec.Files = append(model.Spec.Files, api.ModelFile{
			Path: relativePath,
			Hash: hash,
		})
		return nil
	})
	if err != nil {
		return fmt.Errorf("walking directory %q: %w", srcDir, err)
	}

	y, err := yaml.Marshal(model)
	if err != nil {
		return fmt.Errorf("marshalling model to YAML: %w", err)
	}
	fmt.Println(string(y))

	if upload != "" {
		var blobstore blobs.BlobStore

		if strings.HasPrefix(upload, "http://") || strings.HasPrefix(upload, "https://") {
			u, err := url.Parse(upload)
			if err != nil {
				return fmt.Errorf("parsing upload URL %q: %w", upload, err)
			}
			log.Info("using blob server", "url", u.String())
			blobstore = &blobs.BlobServer{
				URL: u,
			}
		} else {
			return fmt.Errorf("upload must be a URL (https://<host>/<path>), got %q", upload)
		}

		for _, file := range model.Spec.Files {
			info := blobs.BlobInfo{
				Hash: file.Hash,
			}
			srcPath := filepath.Join(srcDir, file.Path)
			r, err := os.Open(srcPath)
			if err != nil {
				return fmt.Errorf("opening file %q: %w", srcPath, err)
			}
			defer r.Close()

			if err := blobstore.Upload(ctx, r, info); err != nil {
				return fmt.Errorf("uploading file %q: %w", file.Path, err)
			}
			log.Info("uploaded file", "path", file.Path, "hash", file.Hash)
		}
	}

	return nil
}

func fileHash(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", fmt.Errorf("opening file %q: %w", p, err)
	}
	defer f.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return "", fmt.Errorf("hashing file %q: %w", p, err)
	}

	hash := fmt.Sprintf("%x", hasher.Sum(nil))
	return hash, nil
}
