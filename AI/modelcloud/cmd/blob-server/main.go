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
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/examples/AI/modelcloud/pkg/blobs"
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

	listen := ":8080"
	cacheDir := os.Getenv("CACHE_DIR")
	if cacheDir == "" {
		// We expect CACHE_DIR to be set when running on kubernetes, but default sensibly for local dev
		cacheDir = "~/.cache/blob-server/blobs"
	}
	flag.StringVar(&listen, "listen", listen, "listen address")
	flag.StringVar(&cacheDir, "cache-dir", cacheDir, "cache directory")
	flag.Parse()

	if strings.HasPrefix(cacheDir, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("getting home directory: %w", err)
		}
		cacheDir = filepath.Join(homeDir, strings.TrimPrefix(cacheDir, "~/"))
	}

	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return fmt.Errorf("creating cache directory %q: %w", cacheDir, err)
	}

	blobStore := &blobs.LocalBlobStore{
		LocalDir: cacheDir,
	}

	blobCache := &blobCache{
		CacheDir:  cacheDir,
		blobStore: blobStore,
	}

	s := &httpServer{
		blobCache: blobCache,
		tmpDir:    filepath.Join(cacheDir, "tmp"),
	}

	log.Info("serving http", "endpoint", listen)
	if err := http.ListenAndServe(listen, s); err != nil {
		return fmt.Errorf("serving on %q: %w", listen, err)
	}

	return nil
}

type httpServer struct {
	blobCache *blobCache
	tmpDir    string
}

func (s *httpServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tokens := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(tokens) == 1 {
		if r.Method == "GET" {
			hash := tokens[0]
			s.serveGETBlob(w, r, hash)
			return
		}
		if r.Method == "PUT" {
			hash := tokens[0]
			s.servePUTBlob(w, r, hash)
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	http.Error(w, "not found", http.StatusNotFound)
}

func (s *httpServer) serveGETBlob(w http.ResponseWriter, r *http.Request, hash string) {
	ctx := r.Context()

	log := klog.FromContext(ctx)

	// TODO: Validate hash is hex, right length etc

	f, err := s.blobCache.GetBlob(ctx, hash)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			log.Info("blob not found", "hash", hash)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		log.Error(err, "error getting blob")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	p := f.Name()

	log.Info("serving blob", "hash", hash, "path", p)
	http.ServeFile(w, r, p)
}

func (s *httpServer) servePUTBlob(w http.ResponseWriter, r *http.Request, hash string) {
	ctx := r.Context()

	log := klog.FromContext(ctx)

	// TODO: Download to temp file first?

	if err := s.blobCache.PutBlob(ctx, hash, r.Body); err != nil {
		log.Error(err, "error stoing blob")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	log.Info("uploaded blob", "hash", hash)

	w.WriteHeader(http.StatusCreated)
}

type blobCache struct {
	CacheDir  string
	blobStore blobs.BlobStore
}

func (c *blobCache) GetBlob(ctx context.Context, hash string) (*os.File, error) {
	log := klog.FromContext(ctx)

	localPath := filepath.Join(c.CacheDir, hash)
	f, err := os.Open(localPath)
	if err == nil {
		return f, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("opening blob %q: %w", hash, err)
	}

	log.Info("blob not found in cache, downloading", "hash", hash)

	err = c.blobStore.Download(ctx, blobs.BlobInfo{Hash: hash}, localPath)
	if err == nil {
		f, err := os.Open(localPath)
		if err != nil {
			return nil, fmt.Errorf("opening blob %q after download: %w", hash, err)
		}
		return f, nil
	}

	return nil, err
}

func (c *blobCache) PutBlob(ctx context.Context, hash string, r io.Reader) error {
	log := klog.FromContext(ctx)

	if err := c.blobStore.Upload(ctx, r, blobs.BlobInfo{Hash: hash}); err != nil {
		log.Error(err, "error uploading blob")
		return fmt.Errorf("uploading blob %q: %w", hash, err)
	}

	// TODO: Side-load into local cache too?

	return nil
}
