# === Install runtime deps (in the notebook) ===
!pip -q install "ray[serve]" fastapi "pydantic<2" huggingface_hub mlflow

import os, time, glob
import ray
from ray import serve
from fastapi import FastAPI, Request
from huggingface_hub import snapshot_download

# ---- Ray / Serve / Model config ----
# If your head service name differs, change it here:
RAY_ADDRESS = os.getenv("RAY_ADDRESS", "ray://ai-starter-kit-kuberay-head-svc:10001")
SERVE_PORT  = int(os.getenv("SERVE_PORT", "8000"))
SERVE_ROUTE = "/v1"   # We'll expose /v1/chat/completions for OpenAI-compat
MODEL_REPO  = "lmstudio-community/Llama-3.2-1B-Instruct-GGUF"
GGUF_PREF   = "Q4_K_M"      # small/CPU-friendly
CTX_LEN     = int(os.getenv("LLM_CONTEXT", "4096"))
MAX_TOKENS  = int(os.getenv("LLM_MAX_TOKENS", "512"))

# Optional MLflow URI (not strictly needed by server):
os.environ.setdefault("MLFLOW_TRACKING_URI", os.getenv("MLFLOW_TRACKING_URI", "http://ai-starter-kit-mlflow-tracking"))

# Optional HF token if you mounted one at this path
HF_TOKEN_PATH = "/etc/secrets/huggingface/token"
HF_TOKEN = None
if os.path.exists(HF_TOKEN_PATH):
    HF_TOKEN = open(HF_TOKEN_PATH).read().strip()

# Connect to Ray
ray.init(address=RAY_ADDRESS, namespace="llama32", ignore_reinit_error=True)

# Start Serve (idempotent)
try:
    serve.start(detached=True, http_options={"host": "0.0.0.0", "port": SERVE_PORT})
except RuntimeError:
    pass

# Workers will pip-install these in their own env:
RUNTIME_PIP = [
    "fastapi",
    "pydantic<2",
    "huggingface_hub",
    "llama-cpp-python==0.2.90",
]

app = FastAPI()

@serve.deployment(
    name="llama32_1b_openai_compat",
    num_replicas=1,
    ray_actor_options={
        "num_cpus": 2,  # tweak for your node size (2 is fine for 1B on CPU)
        "runtime_env": {
            "pip": RUNTIME_PIP,
            "env_vars": {"HUGGINGFACE_HUB_TOKEN": HF_TOKEN or ""},
        },
    },
)
@serve.ingress(app)
class OpenAICompatLlama:
    def __init__(self,
                 repo_id: str = MODEL_REPO,
                 gguf_pref: str = GGUF_PREF,
                 ctx_len: int = CTX_LEN):
        # Download model (cached by HF hub)
        local_dir = snapshot_download(
            repo_id=repo_id,
            allow_patterns=["*.gguf"],
            token=os.getenv("HUGGINGFACE_HUB_TOKEN") or None,
        )
        ggufs = sorted(glob.glob(os.path.join(local_dir, "*.gguf")))
        picked = next((p for p in ggufs if gguf_pref.lower() in os.path.basename(p).lower()), ggufs[0])

        # Load llama.cpp
        from llama_cpp import Llama
        n_threads = max(2, (os.cpu_count() or 4) // 2)
        self.llm = Llama(
            model_path=picked,
            n_ctx=ctx_len,
            n_threads=n_threads,
            logits_all=False,
            vocab_only=False,
            verbose=False,
        )

    @app.get("/healthz")
    async def health(self):
        return {"status": "ok"}

    @app.post("/v1/chat/completions")
    async def chat(self, request: Request):
        body = await request.json()
        messages    = body.get("messages", [])
        temperature = float(body.get("temperature", 0.2))
        max_tokens  = int(body.get("max_tokens", MAX_TOKENS))
        stream      = bool(body.get("stream", False))

        t0 = time.time()
        out = self.llm.create_chat_completion(
            messages=messages,
            temperature=temperature,
            max_tokens=max_tokens,
            stream=stream,
        )
        latency = time.time() - t0

        if stream:
            content = "".join(chunk["choices"][0]["delta"].get("content","") for chunk in out)
            usage = {}
        else:
            content = out["choices"][0]["message"]["content"]
            usage = out.get("usage", {})

        return {
            "id": "chatcmpl-rayserve",
            "object": "chat.completion",
            "model": "llama-3.2-1b-instruct-gguf",
            "created": int(time.time()),
            "choices": [{"index": 0, "finish_reason": "stop",
                         "message": {"role": "assistant", "content": content}}],
            "usage": usage,
            "ray_latency_s": latency,
        }

# Deploy or update; route is /v1
serve.run(OpenAICompatLlama.bind(), route_prefix=SERVE_ROUTE)

print("✅ Ray Serve is up.")
print("Inside cluster:")
print("  Health: http://ai-starter-kit-kuberay-head-svc:8000/healthz")
print("  Chat:   http://ai-starter-kit-kuberay-head-svc:8000/v1/chat/completions")


# Install just what the client needs here
!pip -q install pyautogen~=0.2.35 mlflow

import os, time, json
import mlflow
import autogen
from autogen import AssistantAgent, UserProxyAgent

# === Endpoint & logging ===
OPENAI_BASE = os.getenv("RAY_SERVE_BASE", "http://ai-starter-kit-kuberay-head-svc:8000") + "/v1"
MODEL_NAME  = "llama-3.2-1b-instruct-gguf"

mlflow.set_tracking_uri(os.getenv("MLFLOW_TRACKING_URI", "http://ai-starter-kit-mlflow-tracking"))
mlflow.set_experiment("multiagent-llama32-cpu")

config_list = [{
    "model": MODEL_NAME,
    "base_url": OPENAI_BASE,  # <- Ray Serve URL
    "api_key": "dummy",
    "temperature": 0.2,
}]

# quick probe
llm = autogen.OpenAIWrapper(config_list=config_list)
print("Probe:", llm.create(messages=[{"role": "user", "content": "Reply 'ok' only."}]).choices[0].message.content)

# === Agents (same style you already use) ===
user_proxy = UserProxyAgent(
    name="UserProxy",
    system_message="You are the human admin. Initiate the task.",
    code_execution_config=False,
    human_input_mode="NEVER",
)

researcher = AssistantAgent(
    name="Researcher",
    system_message=(
        "You are a researcher. Gather concise, verified facts on the topic. "
        "Return 6–8 bullet points with inline source domains (e.g., nature.com, ibm.com). "
        "Keep under ~180 words. Do not fabricate sources."
    ),
    llm_config={"config_list": config_list},
)

writer = AssistantAgent(
    name="Writer",
    system_message=(
        "You are a writer. Using the Researcher’s notes, produce a clear 200–300 word report. "
        "Avoid speculation. Keep it structured and readable."
    ),
    llm_config={"config_list": config_list},
)

critic = AssistantAgent(
    name="Critic",
    system_message=(
        "You are a critic. Review the Writer’s report for accuracy, clarity, and flow. "
        "Present the tightened final text. On a new last line output exactly: <|END|>"
    ),
    llm_config={"config_list": config_list},
)

task = "Research the latest advancements in quantum computing as of 2025. Gather key facts, then write a short report (200–300 words). Have the Critic review and finalize."

def run_sequential_with_mlflow(task_text: str):
    t0 = time.time()
    with mlflow.start_run(run_name="multiagent-inference"):
        mlflow.log_params({
            "model": MODEL_NAME,
            "endpoint": OPENAI_BASE,
            "temperature": 0.2,
            "pattern": "Researcher→Writer→Critic",
        })

        r = researcher.generate_reply(messages=[{"role":"user","content":task_text}])
        research_notes = r if isinstance(r, str) else r.get("content", "")
        mlflow.log_text(research_notes or "", "artifacts/researcher.txt")

        w_prompt = f"Using these research notes, write the report:\n{research_notes}"
        w = writer.generate_reply(messages=[{"role":"user","content":w_prompt}])
        report = w if isinstance(w, str) else w.get("content", "")
        mlflow.log_text(report or "", "artifacts/writer.txt")

        c_prompt = f"Review this report and tighten it. End with <|END|>:\n{report}"
        c = critic.generate_reply(messages=[{"role":"user","content":c_prompt}])
        final_text = c if isinstance(c, str) else c.get("content", "")
        mlflow.log_text(final_text or "", "artifacts/critic.txt")

        mlflow.log_metric("total_secs", time.time()-t0)
        mlflow.log_text(json.dumps({
            "task": task_text,
            "researcher": research_notes,
            "writer": report,
            "critic": final_text,
        }, ensure_ascii=False, indent=2), "artifacts/transcript.json")

    return final_text

final_output = run_sequential_with_mlflow(task)
print("\n=== FINAL ===\n", final_output)
