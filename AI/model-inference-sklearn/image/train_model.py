"""
Train a simple scikit-learn Random Forest classifier on the Iris dataset
and persist it as a joblib file.

The resulting model file (iris_model.joblib) is embedded in the container
image so the inference server can load it at startup without any external
storage dependency.

Usage:
    python train_model.py
"""

import joblib
from sklearn.datasets import load_iris
from sklearn.ensemble import RandomForestClassifier
from sklearn.model_selection import train_test_split

# ------------------------------------------------------------------
# 1. Load the built-in Iris dataset
# ------------------------------------------------------------------
iris = load_iris()
X, y = iris.data, iris.target  # 4 features, 3 classes

# ------------------------------------------------------------------
# 2. Split into training and test sets
# ------------------------------------------------------------------
X_train, X_test, y_train, y_test = train_test_split(
    X, y, test_size=0.2, random_state=42
)

# ------------------------------------------------------------------
# 3. Train a lightweight Random Forest classifier
# ------------------------------------------------------------------
clf = RandomForestClassifier(n_estimators=50, random_state=42)
clf.fit(X_train, y_train)

accuracy = clf.score(X_test, y_test)
print(f"Test accuracy: {accuracy:.4f}")

# ------------------------------------------------------------------
# 4. Persist the trained model
# ------------------------------------------------------------------
model_path = "iris_model.joblib"
joblib.dump(clf, model_path)
print(f"Model saved to {model_path}")
