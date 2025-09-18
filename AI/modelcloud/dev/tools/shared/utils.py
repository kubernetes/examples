#!/usr/bin/env python3
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


import os
import subprocess

def find_srcdir():
    """Finds the project root directory by looking for go.mod."""
    p = os.path.dirname(os.path.abspath(__file__))
    while True:
        # We go up two levels to get out of dev/tools/shared
        p = os.path.dirname(os.path.dirname(os.path.dirname(p)))
        if os.path.exists(os.path.join(p, "go.mod")):
            return p
        parent = os.path.dirname(p)
        if parent == p:
            raise Exception("could not find go.mod in any parent directory")
        p = parent

def get_gcp_project():
    """Gets the GCP project ID from gcloud."""
    return subprocess.check_output(["gcloud", "config", "get-value", "project"], text=True).strip()

def get_git_commit_short():
    """Gets the short git commit hash for HEAD."""
    return subprocess.check_output(["git", "rev-parse", "--short", "HEAD"], text=True).strip()

def get_image_tag():
    """Gets the image tag based on the git commit."""
    return f"git-{get_git_commit_short()}"

def get_image_prefix():
    """Constructs the image prefix for a container image."""
    project_id = get_gcp_project()
    return f"gcr.io/{project_id}/"

def get_full_image_name(service_name):
    """Constructs the full GCR image name for a service."""
    image_prefix = get_image_prefix()
    tag = get_image_tag()
    return f"{image_prefix}{service_name}:{tag}"
