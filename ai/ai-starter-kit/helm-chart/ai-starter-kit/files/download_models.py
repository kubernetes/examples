from huggingface_hub import snapshot_download

# --- Model Download ---
# List your desired Hugging Face model names here
model_names = [
    "Qwen/Qwen3-Embedding-0.6B",
]

for model_name in model_names:
    print(f"--- Downloading {model_name} ---")
    try:
        snapshot_download(repo_id=model_name)
        print(f"Successfully cached {model_name}")
    except Exception as e:
        print(f"Failed to download {model_name}. Error: {e}")

print("--- Model download process finished. ---")
