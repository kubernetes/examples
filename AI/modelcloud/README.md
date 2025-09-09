# modelcloud example

## Goal: Production-grade inference on AI-conformant kubernetes clusters

This goal of this example is to build up production-grade inference
on AI-conformant kubernetes clusters.

We (aspirationally) aim to demonstrate the capabilities of the AI-conformance
profile.  Where we cannot achieve production-grade inference, we hope to
motivate discussion of extensions to the AI-conformance profile to plug those gaps.

## Walkthrough

### Deploying to a kubernetes cluster

Create a kubernetes cluster, we currently test with GKE and gcr.io but do not aim
to depend on non-conformant functionality; PRs to add support for deployment
to other conformant environments are very welcome.

1. From the modelcloud directory, run `dev/tools/push-images` to push to `gcr.io/$(gcloud config get project)/...`

1. Run `dev/tools/deploy-to-kube` to deploy.

We deploy two workloads:

1. `blob-server`, a statefulset with a persistent volume to hold the model blobs (files)

1. `gemma3`, a deployment running vLLM, with a frontend go process that will download the model from `blob-server`.

### Uploading a model

For now, we will likely be dealing with models from huggingface.

Begin by cloning the model locally (and note that currently only google/gemma-3-1b-it is supported):

```
git clone https://huggingface.co/google/gemma-3-1b-it
```

If you now run `go run ./cmd/model-hasher --src gemma-3-1b-it` you should see it print the hashes for each file:

```
spec:
  files:
  - hash: 1468e03275c15d522c721bd987f03c4ee21b8e246b901803c67448bbc25acf58
    path: .gitattributes
  - hash: 60be259533fe3acba21d109a51673815a4c29aefdb7769862695086fcedbeb7a
    path: README.md
  - hash: 50b2f405ba56a26d4913fd772089992252d7f942123cc0a034d96424221ba946
    path: added_tokens.json
  - hash: 19cb5d28c97778271ba2b3c3df47bf76bdd6706724777a2318b3522230afe91e
    path: config.json
  - hash: fd9324becc53c4be610db39e13a613006f09fd6ef71a95fb6320dc33157490a3
    path: generation_config.json
  - hash: 3d4ef8d71c14db7e448a09ebe891cfb6bf32c57a9b44499ae0d1c098e48516b6
    path: model.safetensors
  - hash: 2f7b0adf4fb469770bb1490e3e35df87b1dc578246c5e7e6fc76ecf33213a397
    path: special_tokens_map.json
  - hash: 4667f2089529e8e7657cfb6d1c19910ae71ff5f28aa7ab2ff2763330affad795
    path: tokenizer.json
  - hash: 1299c11d7cf632ef3b4e11937501358ada021bbdf7c47638d13c0ee982f2e79c
    path: tokenizer.model
  - hash: bfe25c2735e395407beb78456ea9a6984a1f00d8c16fa04a8b75f2a614cf53e1
    path: tokenizer_config.json
```

Inside the vllm-frontend, this [list of files is currently embedded](cmd/vllm-frontend/models/google/gemma-3-1b-it/model.yaml).
This is why we only support gemma-3-1b-it today, though we plan to relax this in future (e.g. a CRD?)

The blob-server accepts uploads, and we can upload the blobs using a port-forward:

```
kubectl port-forward blob-server-0 8081:8080 &
go run ./cmd/model-hasher/ --src gemma-3-1b-it/ --upload http://127.0.0.1:8081
```

This will then store the blobs on the persistent disk of blob-server, so they are now available in-cluster,
you can verify this with `kubectl debug`:

```
> kubectl debug blob-server-0 -it --image=debian:latest  --profile=general --share-processes --target blob-server
root@blob-server-0:/# cat /proc/1/mounts | grep blob
/dev/sdb /blobs ext4 rw,relatime 0 0
root@blob-server-0:/# ls -l /proc/1/root/blobs/
total 1991324
-rw------- 1 root root    4689074 Sep  8 21:40 1299c11d7cf632ef3b4e11937501358ada021bbdf7c47638d13c0ee982f2e79c
-rw------- 1 root root       1676 Sep  8 21:39 1468e03275c15d522c721bd987f03c4ee21b8e246b901803c67448bbc25acf58
-rw------- 1 root root        899 Sep  8 21:39 19cb5d28c97778271ba2b3c3df47bf76bdd6706724777a2318b3522230afe91e
-rw------- 1 root root        662 Sep  8 21:40 2f7b0adf4fb469770bb1490e3e35df87b1dc578246c5e7e6fc76ecf33213a397
-rw------- 1 root root 1999811208 Sep  8 21:40 3d4ef8d71c14db7e448a09ebe891cfb6bf32c57a9b44499ae0d1c098e48516b6
-rw------- 1 root root   33384568 Sep  8 21:40 4667f2089529e8e7657cfb6d1c19910ae71ff5f28aa7ab2ff2763330affad795
-rw------- 1 root root         35 Sep  8 21:39 50b2f405ba56a26d4913fd772089992252d7f942123cc0a034d96424221ba946
-rw------- 1 root root      24265 Sep  8 21:39 60be259533fe3acba21d109a51673815a4c29aefdb7769862695086fcedbeb7a
-rw------- 1 root root    1156999 Sep  8 21:40 bfe25c2735e395407beb78456ea9a6984a1f00d8c16fa04a8b75f2a614cf53e1
-rw------- 1 root root        215 Sep  8 21:39 fd9324becc53c4be610db39e13a613006f09fd6ef71a95fb6320dc33157490a3
drwx------ 2 root root      16384 Sep  8 21:38 lost+found
```

## Using the inference server

At this point the vLLM process should (hopefully) have downloaded the model and started.

```bash
kubectl wait --for=condition=Available --timeout=10s deployment/gemma3
kubectl get pods -l app=gemma3
```

To check logs (particularly if this is not already ready)
```bash
kubectl logs -f -l app=gemma3
```


## Verification / Seeing it Work

Forward local requests to vLLM service:

```bash
# Forward a local port (e.g., 8080) to the service port (e.g., 8080)
kubectl port-forward service/gemma3 8080:80 &
```

2. Send request to local forwarding port:

```bash
curl -X POST http://localhost:8080/v1/chat/completions \
-H "Content-Type: application/json" \
-d '{
  "model": "google/gemma-3-1b-it",
  "messages": [{"role": "user", "content": "Explain Quantum Computing in simple terms."}],
  "max_tokens": 100
}'
```

Expected output (or similar):

```json
{"id":"chatcmpl-462b3e153fd34e5ca7f5f02f3bcb6b0c","object":"chat.completion","created":1753164476,"model":"google/gemma-3-1b-it","choices":[{"index":0,"message":{"role":"assistant","reasoning_content":null,"content":"Okay, let’s break down quantum computing in a way that’s hopefully understandable without getting lost in too much jargon. Here's the gist:\n\n**1. Classical Computers vs. Quantum Computers:**\n\n* **Classical Computers:** These are the computers you use every day – laptops, phones, servers. They store information as *bits*. A bit is like a light switch: it's either on (1) or off (0). Everything a classical computer does – from playing games","tool_calls":[]},"logprobs":null,"finish_reason":"length","stop_reason":null}],"usage":{"prompt_tokens":16,"total_tokens":116,"completion_tokens":100,"prompt_tokens_details":null},"prompt_logprobs":null}
```

---

## Cleanup

```bash
kubectl delete deployment gemma3
kubectl delete statefulset blob-server
```
