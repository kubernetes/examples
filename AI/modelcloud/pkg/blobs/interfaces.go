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
	"io"
)

type BlobReader interface {
	// If no such object exists, Download should return an error for which errors.Is(err, os.ErrNotExist) is true.
	Download(ctx context.Context, info BlobInfo, destPath string) error
}

type BlobStore interface {
	BlobReader

	// Upload uploads the blob to the blobstore, verifying the hash.
	// If an object with the same hash already exists, Upload should not modify the existing object.
	// On success, Upload returns (true, nil).
	// On failure, Upload returns (false, err).
	Upload(ctx context.Context, r io.Reader, info BlobInfo) error
}

type BlobInfo struct {
	Hash string
}
