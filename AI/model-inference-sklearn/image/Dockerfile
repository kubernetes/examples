# ---------------------------------------------------------
# Multi-stage Dockerfile for the scikit-learn inference API.
#
# Stage 1 -- "builder":  installs dependencies and trains
#                        the model so the artifact is baked
#                        into the image (no external storage
#                        required for this demo).
# Stage 2 -- "runtime":  copies only what is needed to serve.
# ---------------------------------------------------------

# ---------- Stage 1: build & train ----------
FROM python:3.12-slim AS builder

WORKDIR /build

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY train_model.py .
RUN python train_model.py

# ---------- Stage 2: runtime ----------
FROM python:3.12-slim

ARG APP_UID=10001
ARG APP_GID=10001

LABEL org.opencontainers.image.description="Minimal scikit-learn inference server (Iris model)" \
      org.opencontainers.image.source="https://github.com/kubernetes/examples/tree/master/AI/model-inference-sklearn"

# Run as non-root for security best practices.
RUN groupadd -g ${APP_GID} -r appuser && \
    useradd -u ${APP_UID} -r -g appuser -d /app -s /sbin/nologin appuser

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Copy application code and the pre-trained model from the builder stage.
COPY app.py .
COPY --from=builder /build/iris_model.joblib /model/iris_model.joblib

# Environment variables with sensible defaults.
ENV MODEL_PATH="/model/iris_model.joblib" \
    PORT="8080" \
    PYTHONDONTWRITEBYTECODE="1" \
    PYTHONUNBUFFERED="1" \
    TMPDIR="/tmp"

# Switch to non-root user.
USER appuser

EXPOSE 8080

# Start the FastAPI server via uvicorn.
CMD ["python", "app.py"]
