# Copyright 2016 The Kubernetes Authors.
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

TAG = v1
PREFIX = gcr.io/google_containers
IMAGE_NAME = example-dns

ARCH ?= $(shell go env GOARCH)
ALL_ARCH = amd64 arm arm64 ppc64le s390x
ALL_IMAGE = frontend backend


MULTI_ARCH_IMG = $(PREFIX)/$(IMAGE_NAME)-$(IMAGE)-$(ARCH)

QEMUVERSION=v2.9.1

BASEIMAGE ?= python:2.7-slim
QEMUARCH ?= amd64

ifeq ($(ARCH),arm)
        BASEIMAGE = arm32v7/python:2.7-slim
	QEMUARCH = arm
endif
ifeq ($(ARCH),arm64)
        BASEIMAGE = arm64v8/python:2.7-slim
	QEMUARCH = aarch64
endif
ifeq ($(ARCH),ppc64le)
        BASEIMAGE = ppc64le/python:2.7-slim
	QEMUARCH = ppc64le
endif
ifeq ($(ARCH),s390x)
	BASEIMAGE = s390x/python:2.7-slim
	QEMUARCH =s390x
endif


TEMP_DIR := $(shell mktemp -d)

all: image

image: $(addprefix sub-image-,$(ALL_IMAGE))

sub-image-%:
	$(MAKE) --no-print-directory IMAGE=$* all-container

all-container: $(addprefix sub-container-,$(ALL_ARCH))

sub-container-%:
	$(MAKE) --no-print-directory ARCH=$* container

sub-push-image-%:
	$(MAKE) --no-print-directory IMAGE=$* all-image-push

all-image-push: $(addprefix sub-push-,$(ALL_ARCH))

sub-push-%:
	$(MAKE) --no-print-directory ARCH=$* push

all-push: $(addprefix sub-push-image-,$(ALL_IMAGE))

container: .container-$(ARCH)
.container-$(ARCH): 
	cp -r * $(TEMP_DIR)
	cd $(TEMP_DIR)/$(IMAGE)/ && sed -i 's|BASEIMAGE|$(BASEIMAGE)|g' Dockerfile
	cd $(TEMP_DIR)/$(IMAGE)/ && sed -i "s|ARCH|$(QEMUARCH)|g" Dockerfile

ifeq ($(ARCH),amd64)
	# When building "normally" for amd64, remove the whole line, it has no part in the amd64 image
	cd $(TEMP_DIR)/$(IMAGE)/ && sed -i "/CROSS_BUILD_/d" Dockerfile
else
	# When cross-building, only the placeholder "CROSS_BUILD_" should be removed
	# Register /usr/bin/qemu-ARCH-static as the handler for ARM binaries in the kernel
	docker run --rm --privileged multiarch/qemu-user-static:register --reset
	curl -sSL https://github.com/multiarch/qemu-user-static/releases/download/$(QEMUVERSION)/x86_64_qemu-$(QEMUARCH)-static.tar.gz | tar -xz -C $(TEMP_DIR)/$(IMAGE)
	cd $(TEMP_DIR)/$(IMAGE)/ && sed -i "s/CROSS_BUILD_//g" Dockerfile
endif
	docker build --pull -t ${MULTI_ARCH_IMG}:$(TAG) $(TEMP_DIR)/$(IMAGE)/
ifeq ($(ARCH), amd64)
	#Adding check for amd64
	docker tag $(MULTI_ARCH_IMG):$(TAG) $(PREFIX)/$(IMAGE_NAME)-$(IMAGE):$(TAG)
endif

push: .push-$(ARCH)
.push-$(ARCH): .container-$(ARCH)
	gcloud docker -- push $(MULTI_ARCH_IMG):$(TAG)
ifeq ($(ARCH), amd64)
	gcloud docker -- push $(PREFIX)/$(IMAGE_NAME):$(TAG)
endif

clean:
