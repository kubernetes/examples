# Copyright 2025 The Kubernetes Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

FROM golang:1.25.1 AS builder

ADD go.mod /workspace/
ADD go.sum /workspace/
ADD cmd/blob-server /workspace/cmd/blob-server/
ADD pkg /workspace/pkg/

WORKDIR /workspace

RUN CGO_ENABLED=0 go build -o /blob-server ./cmd/blob-server

# TODO: Don't have permission to write to volumes if nonroot
#FROM gcr.io/distroless/static-debian12:nonroot
FROM gcr.io/distroless/static-debian12:latest

COPY --from=builder /blob-server /blob-server

ENTRYPOINT ["/blob-server"]