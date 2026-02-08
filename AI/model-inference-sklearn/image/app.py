"""
FastAPI inference server for a scikit-learn Iris classification model.

This lightweight API loads a pre-trained model at startup and exposes
a /predict endpoint that accepts feature vectors and returns predicted
class labels with probabilities.

Endpoints:
    GET  /healthz   - Liveness probe (always returns 200).
    GET  /readyz    - Readiness probe (returns 200 once the model is loaded).
    POST /predict   - Accepts feature vectors and returns predictions.
"""

import logging
import os
from contextlib import asynccontextmanager
from typing import List

import joblib
import numpy as np
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------
MODEL_PATH = os.environ.get("MODEL_PATH", "/model/iris_model.joblib")
PORT = int(os.environ.get("PORT", "8080"))

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger("inference-server")

# ---------------------------------------------------------------------------
# Application state
# ---------------------------------------------------------------------------
model = None
class_names: List[str] = []


@asynccontextmanager
async def lifespan(application: FastAPI):
    """Load the model once at startup and release on shutdown."""
    global model, class_names
    logger.info("Loading model from %s ...", MODEL_PATH)
    try:
        model = joblib.load(MODEL_PATH)
        # Iris class names corresponding to label indices 0, 1, 2.
        class_names = ["setosa", "versicolor", "virginica"]
        logger.info("Model loaded successfully.")
    except Exception as exc:
        logger.error("Failed to load model: %s", exc)
        raise
    yield
    logger.info("Shutting down inference server.")


app = FastAPI(
    title="Scikit-Learn Inference Server",
    version="1.0.0",
    lifespan=lifespan,
)

# ---------------------------------------------------------------------------
# Request / response schemas
# ---------------------------------------------------------------------------


class PredictRequest(BaseModel):
    """Input payload for the /predict endpoint.

    Each entry in *instances* is a list of four numerical features
    corresponding to sepal length, sepal width, petal length, and petal width.
    """

    instances: List[List[float]] = Field(
        ...,
        min_length=1,
        json_schema_extra={
            "example": [[5.1, 3.5, 1.4, 0.2], [6.7, 3.0, 5.2, 2.3]]
        },
    )


class Prediction(BaseModel):
    label: str
    probability: float


class PredictResponse(BaseModel):
    predictions: List[Prediction]


# ---------------------------------------------------------------------------
# Health endpoints (used by Kubernetes probes)
# ---------------------------------------------------------------------------


@app.get("/healthz", status_code=200)
def liveness():
    """Liveness probe -- the process is alive."""
    return {"status": "alive"}


@app.get("/readyz", status_code=200)
def readiness():
    """Readiness probe -- the model is loaded and ready to serve."""
    if model is None:
        raise HTTPException(status_code=503, detail="Model not loaded yet")
    return {"status": "ready"}


# ---------------------------------------------------------------------------
# Prediction endpoint
# ---------------------------------------------------------------------------


@app.post("/predict", response_model=PredictResponse)
def predict(request: PredictRequest):
    """Return class predictions and confidence for each input instance."""
    if model is None:
        raise HTTPException(status_code=503, detail="Model not loaded yet")

    try:
        data = np.array(request.instances)
        if data.ndim != 2 or data.shape[1] != 4:
            raise HTTPException(
                status_code=422,
                detail="Each instance must have exactly 4 features.",
            )

        predictions = model.predict(data)
        probabilities = model.predict_proba(data)

        results = []
        for idx, label_idx in enumerate(predictions):
            results.append(
                Prediction(
                    label=class_names[label_idx],
                    probability=round(float(probabilities[idx][label_idx]), 4),
                )
            )
        return PredictResponse(predictions=results)
    except HTTPException:
        raise
    except Exception as exc:
        logger.exception("Prediction failed")
        raise HTTPException(status_code=500, detail=str(exc))


# ---------------------------------------------------------------------------
# Entrypoint (used by the container CMD)
# ---------------------------------------------------------------------------
if __name__ == "__main__":
    import uvicorn

    uvicorn.run(app, host="0.0.0.0", port=PORT)
