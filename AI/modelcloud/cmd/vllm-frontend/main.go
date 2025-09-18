// Copyright 2025 The Kubernetes Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law of agetd in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	models "k8s.io/examples/AI/modelcloud/cmd/vllm-frontend/models"

	"k8s.io/examples/AI/modelcloud/pkg/blobs"
	"k8s.io/klog/v2"
)

func main() {
	ctx := context.Background()
	if err := run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	log := klog.FromContext(ctx)

	llmModelID := os.Getenv("MODEL_NAME")
	flag.StringVar(&llmModelID, "model-name", llmModelID, "identifier of model.")

	blobserverFlag := os.Getenv("BLOBSERVER")
	if blobserverFlag == "" {
		blobserverFlag = "http://blob-server"
	}
	flag.StringVar(&blobserverFlag, "blobserver", blobserverFlag, "base url to blobserver")

	klog.InitFlags(nil)

	flag.Parse()

	llmModel, err := models.LoadModel(llmModelID)
	if err != nil {
		return fmt.Errorf("loading model %q: %w", llmModelID, err)
	}

	blobserverURL, err := url.Parse(blobserverFlag)
	if err != nil {
		return fmt.Errorf("parsing blobserver url %q: %w", blobserverFlag, err)
	}
	blobStore := &blobs.BlobServer{
		URL: blobserverURL,
	}

	loader := &ModelLoader{
		reader:              blobStore,
		maxDownloadAttempts: 5,
	}
	modelDir := filepath.Join(os.TempDir(), "model", llmModelID)
	if err := os.MkdirAll(modelDir, 0755); err != nil {
		return fmt.Errorf("creating temp directory %q: %w", modelDir, err)
	}
	log.Info("downloading model to", "dir", modelDir)

	for _, file := range llmModel.Spec.Files {
		localPath := filepath.Join(modelDir, file.Path)

		info := blobs.BlobInfo{
			Hash: file.Hash,
		}
		if err := loader.downloadToFile(ctx, info, localPath); err != nil {
			return fmt.Errorf("downloading model: %w", err)
		}
		log.Info("downloaded model file", "path", file.Path)
	}

	var vllmArgs []string

	baseArgs := []string{
		"python3", "-m", "vllm.entrypoints.openai.api_server",
		"--host=0.0.0.0",
		"--port=8080",
		"--model=" + modelDir,
		"--served_model_name=" + llmModelID,
	}

	vllmArgs = append(vllmArgs, baseArgs...)
	vllmArgs = append(vllmArgs, flag.Args()...)

	log.Info("starting vllm", "args", vllmArgs)

	cmd := exec.CommandContext(ctx, vllmArgs[0], vllmArgs[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	env := os.Environ()
	cmd.Env = env

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting vllm: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("vllm exited with error: %w", err)
	}
	return nil
}

type ModelLoader struct {
	// reader is the interface to fetch blobs
	reader blobs.BlobReader

	// maxDownloadAttempts is the number of times to attempt a download before failing
	maxDownloadAttempts int
}

func (l *ModelLoader) downloadToFile(ctx context.Context, info blobs.BlobInfo, destPath string) error {
	log := klog.FromContext(ctx)

	attempt := 0
	for {
		attempt++

		err := l.reader.Download(ctx, info, destPath)
		if err == nil {
			return nil
		}

		if attempt >= l.maxDownloadAttempts {
			return err
		}

		log.Error(err, "downloading blob, will retry", "info", info, "attempt", attempt)
		time.Sleep(5 * time.Second)
	}
}
