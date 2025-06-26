lint:
	helm lint helm-chart/ai-starter-kit

dep_update:
	helm dependency update helm-chart/ai-starter-kit

install:
	helm upgrade --install ai-starter-kit helm-chart/ai-starter-kit --set huggingface.token="your_hf_token" --timeout 10m -f helm-chart/ai-starter-kit/values.yaml

install_gke:
	helm upgrade --install ai-starter-kit helm-chart/ai-starter-kit --set huggingface.token="your_hf_token" --timeout 10m -f helm-chart/ai-starter-kit/values-gke.yaml

start:
	mkdir -p /tmp/models-cache
	minikube start --cpus 4 --memory 15000 --mount --mount-string="/tmp/models-cache:/tmp/models-cache"

uninstall:
	helm uninstall ai-starter-kit

destroy:
	minikube delete

validate_jupyterhub:
	kubectl get pods; \
    kubectl wait --for=condition=Ready pods -l 'component!=continuous-image-puller' --timeout=1800s; \
    kubectl get pods; \
    kubectl get services; \
    kubectl port-forward service/ai-starter-kit-jupyterhub-proxy-public 8081:80 & \
    PID=$$!; \
    echo "Port-forward PID=$${PID}"; \
    sleep 5s; \
    python3 ./ci/test_hub.py "127.0.0.1:8081"; \
    kill $$PID

validate_ray:
	kubectl wait --for=condition=Ready pods -l 'app.kubernetes.io/created-by=kuberay-operator' --timeout=1800s; \
	kubectl get pods; \
	kubectl get services; \
	kubectl port-forward service/ai-starter-kit-kuberay-head-svc 8265:8265 & \
	PID=$$!; \
	sleep 10s; \
	ray job submit --address=http://127.0.0.1:8265 -- python -c "import ray; ray.init(); print(ray.cluster_resources())"; \
	kill $$PID
