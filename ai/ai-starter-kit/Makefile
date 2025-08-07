lint:
	helm lint helm-chart/ai-starter-kit

dep_update:
	helm dependency update helm-chart/ai-starter-kit

install:
	helm upgrade --install ai-starter-kit helm-chart/ai-starter-kit --set huggingface.token="your_hf_token" --timeout 10m

start:
	minikube start --cpus 4 --memory 15000 --mount --mount-string="/tmp/models-cache:/tmp/models-cache"

uninstall:
	helm uninstall ai-starter-kit

destroy:
	minikube delete
