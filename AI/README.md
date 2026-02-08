# AI/ML Examples on Kubernetes

Welcome to the AI/ML examples section! Our goal is to provide a collection of
community-curated, open-source reference manifests for deploying and managing
AI/ML workloads, MLOps toolchains, and end-to-end platforms on Kubernetes.

This area is under active development as part of a broader initiative to enhance
the `kubernetes/examples` repository. We aim to simplify the developer and operator
experience for AI applications on Kubernetes, promoting best practices and interoperability.

## Vision for AI/ML Examples

We envision this section housing examples such as:
* Setups for distributed training frameworks.
* Configurations for model serving solutions.
* Blueprints for data versioning and experiment tracking integrations.
* End-to-end MLOps platform examples.
* "AI Kits" designed to help AI/ML experts quickly get started on Kubernetes.

## Call for Contributions

The success of this initiative depends on community contributions! If you have expertise
in running AI/ML workloads on Kubernetes or ideas for valuable examples, we strongly
encourage you to contribute.

We are particularly interested in examples that are:
* Educational and provide an easy start for AI/ML practitioners new to Kubernetes.
* Modular and showcase best practices.
* Cover a diverse range of tools and MLOps stages.

## Available Examples

| Example | Description | GPU Required |
|---|---|---|
| [Model Inference with Scikit-Learn](model-inference-sklearn/) | Minimal inference API (FastAPI + scikit-learn) with Kubernetes best practices (probes, resource limits, security context) | No |
| [TensorFlow Model Serving](model-serving-tensorflow/) | Deploy TensorFlow Serving with PersistentVolumes and Ingress | No (CPU mode) |
| [vLLM Inference Server](vllm-deployment/) | Serve large language models (Gemma) with vLLM and optional HPA | Yes |
