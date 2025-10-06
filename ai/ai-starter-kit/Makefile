.PHONY: check_hf_token check_OCI_target package_helm lint dep_update install install_gke start uninstall push_helm

check_hf_token:
ifndef HF_TOKEN
	$(error HF_TOKEN is not set)
endif

check_OCI_target:
ifndef OCI_HELM_TARGET
	$(error OCI_HELM_TARGET is not set)
endif

package_helm:
	helm package helm-chart/ai-starter-kit/ --destination out/

push_helm: check_OCI_target
	helm push out/ai-starter-kit* oci://$$OCI_HELM_TARGET

lint:
	helm lint helm-chart/ai-starter-kit

dep_update:
	helm dependency update helm-chart/ai-starter-kit

install: check_hf_token
	helm upgrade --install ai-starter-kit helm-chart/ai-starter-kit --set huggingface.token="$$HF_TOKEN" --timeout 10m -f helm-chart/ai-starter-kit/values.yaml

start:
	mkdir -p /tmp/models-cache
	minikube start --cpus 4 --memory 15000 --mount --mount-string="$$HOME/models-cache:/tmp/models-cache"

start_gpu:
	mkdir -p $HOME/models-cache
	minikube start --driver krunkit --cpus 4 --memory 15000 --mount --mount-string="$HOME/models-cache:$HOME/models-cache"

uninstall:
	helm uninstall ai-starter-kit
	kubectl delete pod jupyter-user
	kubectl delete pvc ai-starter-kit-jupyterhub-hub-db-dir

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
