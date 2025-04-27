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

FROM nginx:latest


# Copy HTML and script files
COPY index2.html /usr/share/nginx/html/index2.html
COPY auto-reload-nginx.sh /home/auto-reload-nginx.sh

# Set permissions and install inotify-tools in one layer
RUN chmod 644 /usr/share/nginx/html/index2.html && \
    chmod +x /home/auto-reload-nginx.sh && \
    apt-get update && \
    apt-get install -y --no-install-recommends inotify-tools && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*
    
# Override at kubernetes deployment template 
CMD ["./auto-reload-nginx.sh"]
