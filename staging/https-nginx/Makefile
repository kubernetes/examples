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

TAG = 1.0
PREFIX = gcr.io/google_samples
IMAGE = httpsnginx
KEY = /tmp/nginx.key
CERT = /tmp/nginx.crt
SECRET = /tmp/secret.json

ARCH ?= $(shell go env GOARCH)
ALL_ARCH = amd64 arm arm64 ppc64le s390x

MULTI_ARCH_IMG = $(PREFIX)/$(IMAGE)-$(ARCH)

QEMUVERSION=v2.9.1

BASEIMAGE ?= nginx
QEMUARCH ?= amd64

# Set the baseimage for each arch
ifeq ($(ARCH), arm)
	BASEIMAGE = arm32v7/nginx
	QEMUARCH = arm
endif
ifeq ($(ARCH), arm64)
	BASEIMAGE = arm64v8/nginx
	QEMUARCH = aarch64
endif
ifeq ($(ARCH), ppc64le)
	BASEIMAGE = ppc64le/nginx
	QEMUARCH = ppc64le
endif
ifeq ($(ARCH), s390x)
	BASEIMAGE = s390x/nginx
	QEMUARCH = s390x
endif
	
TEMP_DIR := $(shell mktemp -d)

keys:
        # The CName used here is specific to the service specified in nginx-app.yaml.
        openssl req -x509 -nodes -days 365 -newkey rsa:2048 -keyout $(KEY) -out $(CERT) -subj "/CN=nginxsvc/O=nginxsvc"

secret:
	go run make_secret.go -crt $(CERT) -key $(KEY) > $(SECRET)

all: all-container

sub-container-%:
	$(MAKE) --no-print-directory ARCH=$* container

sub-push-%:
	$(MAKE) --no-print-directory ARCH=$* push

all-container: $(addprefix sub-container-,$(ALL_ARCH))

all-push: $(addprefix sub-push-,$(ALL_ARCH))

container: .container-$(ARCH)
.container-$(ARCH): 
	cp -r * $(TEMP_DIR)
	cd $(TEMP_DIR) && sed -i 's|BASEIMAGE|$(BASEIMAGE)|g' Dockerfile
	cd $(TEMP_DIR) && sed -i "s|ARCH|$(QEMUARCH)|g" Dockerfile

ifeq ($(ARCH),amd64)
	# When building "normally" for amd64, remove the whole line, it has no part in the amd64 image
	cd $(TEMP_DIR) && sed -i "/CROSS_BUILD_/d" Dockerfile
else
	# When cross-building, only the placeholder "CROSS_BUILD_" should be removed
	# Register /usr/bin/qemu-ARCH-static as the handler for ARM binaries in the kernel
	docker run --rm --privileged multiarch/qemu-user-static:register --reset
	curl -sSL https://github.com/multiarch/qemu-user-static/releases/download/$(QEMUVERSION)/x86_64_qemu-$(QEMUARCH)-static.tar.gz | tar -xz -C $(TEMP_DIR)
	cd $(TEMP_DIR) && sed -i "s/CROSS_BUILD_//g" Dockerfile
endif
	docker build --pull -t ${MULTI_ARCH_IMG}:$(TAG) $(TEMP_DIR)
ifeq ($(ARCH), amd64)
	# Adding check for amd64
	docker tag $(MULTI_ARCH_IMG):$(TAG) $(PREFIX)/$(IMAGE):$(TAG)
endif


push: .push-$(ARCH)
.push-$(ARCH): .container-$(ARCH)
	gcloud docker -- push $(MULTI_ARCH_IMG):$(TAG)
ifeq ($(ARCH), amd64)
	gcloud docker -- push $(IMAGE):$(TAG)
endif

clean:
	rm $(KEY)
	rm $(CERT)
