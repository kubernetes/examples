#!/bin/bash

# This script sends a POST request to a local LLM endpoint every second
# without waiting for a response. It runs until manually stopped by pressing Ctrl+C.
#
# Usage: ./request-looper.sh [PORT] [MODEL_NAME] ["MESSAGE CONTENT"]
#
# Examples:
#   ./request-looper.sh
#   ./request-looper.sh 8082
#   ./request-looper.sh 8082 "google/gemma-2b" "What is the capital of France?"

# --- Configuration (with defaults) ---
# Use command-line arguments if provided, otherwise use defaults.
PORT=${1:-"8081"}
MODEL=${2:-"google/gemma-3-1b-it"}
CONTENT=${3:-"Explain Quantum Computing in simple terms."}

# The URL of the LLM API endpoint.
URL="http://localhost:${PORT}/v1/chat/completions"

# The JSON payload for the request.
JSON_PAYLOAD=$(printf '{
  "model": "%s",
  "messages": [{"role": "user", "content": "%s"}]
}' "$MODEL" "$CONTENT")

# --- Script Logic ---
echo "Starting request loop..."
echo "  PORT: $PORT"
echo "  MODEL: $MODEL"
echo "  CONTENT: $CONTENT"
echo "Press Ctrl+C to stop."

# Infinite loop to send requests.
while true
do
  echo "----------------------------------------"
  echo "Sending request at $(date)"

  # Send the POST request using curl and run it in the background (&).
  # The output and errors are redirected to /dev/null to keep the console clean.
  curl -X POST "$URL" \
       -H "Content-Type: application/json" \
       -d "$JSON_PAYLOAD" \
       --silent \
       -o /dev/null \
       -w "HTTP Status: %{http_code}\n" &

  # Wait for 1 second before the next request.
  sleep 1
done
