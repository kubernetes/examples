from huggingface_hub import login
from pathlib import Path
from transformers import AutoModel, AutoTokenizer

TOKEN_PATH = Path("/etc/secrets/huggingface/token")
if TOKEN_PATH.is_file():
    print("Hugging Face token file found.")
    try:
        token = TOKEN_PATH.read_text().strip()
        if token:
            print("Logging into Hugging Face Hub...")
            login(token=token)
            print("Login successful.")
        else:
            print("Token file is empty. Proceeding without login.")
    except Exception as e:
        print(f"Failed to read token or login: {e}")
else:
    print("Hugging Face token not found. Proceeding without login.")
    print("Downloads for private or gated models may fail.")


# --- Model Download ---
# List your desired Hugging Face model names here
model_names = [
    "Qwen/Qwen3-Embedding-0.6B",
]

# The cache directory is mounted from a PersistentVolumeClaim
save_base_dir = "/tmp/models-cache"

for model_name in model_names:
    print(f"--- Downloading {model_name} ---")
    try:
         model = AutoModel.from_pretrained(model_name)
         tokenizer = AutoTokenizer.from_pretrained(model_name)
         save_dir = f"{save_base_dir}/{model_name}"
         model.save_pretrained(save_dir)
         tokenizer.save_pretrained(save_dir)
         print(f"Successfully cached {model_name} in {save_base_dir}")
    except Exception as e:
        print(f"Failed to download {model_name}. Error: {e}")

print("--- Model download process finished. ---")