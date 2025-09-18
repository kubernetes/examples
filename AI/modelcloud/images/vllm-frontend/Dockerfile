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
ADD cmd/vllm-frontend /workspace/cmd/vllm-frontend/
ADD pkg /workspace/pkg/

WORKDIR /workspace

RUN CGO_ENABLED=0 go build -o /vllm-frontend ./cmd/vllm-frontend


FROM vllm/vllm-openai:v0.10.1.1
# gemma3-6cf4765df9-c4nmt gemma3 DEBUG 09-08 14:57:56 [__init__.py:58] Checking if CUDA platform is available.
# gemma3-6cf4765df9-c4nmt gemma3 DEBUG 09-08 14:57:56 [__init__.py:82] Exception happens when checking CUDA platform: NVML Shared Library Not Found
# gemma3-6cf4765df9-c4nmt gemma3 DEBUG 09-08 14:57:56 [__init__.py:99] CUDA platform is not available because: NVML Shared Library Not Found


# FROM vllm/vllm-openai:v0.10.0

COPY --from=builder /vllm-frontend /vllm-frontend

ENV LD_LIBRARY_PATH /usr/local/nvidia/lib64:/usr/local/lib/

ENTRYPOINT ["/vllm-frontend"]