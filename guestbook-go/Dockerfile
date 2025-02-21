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
# Use Go 1.18 base image
FROM golang:1.18 AS builder

# Set the working directory
WORKDIR /app

# Copy Go module files and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code and build the application
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -o main .

# Use a minimal base image to keep the final image small
FROM scratch

# Set the working directory
WORKDIR /app

# Copy the built binary from the builder stage
COPY --from=builder /app/main .

# Copy static files
COPY ./public/index.html public/index.html
COPY ./public/script.js public/script.js
COPY ./public/style.css public/style.css

# Define the command to run the application
CMD ["/app/main"]

# Expose the port on which the application will run
EXPOSE 3000
