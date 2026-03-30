#!/usr/bin/env python3
import json
import sys
from pathlib import Path

import numpy as np
import onnxruntime as ort
from tokenizers import Tokenizer


def load_request() -> dict:
    return json.load(sys.stdin)


def main() -> int:
    request = load_request()
    model_dir = Path(request["model_dir"])
    texts = request.get("texts", [])

    tokenizer = Tokenizer.from_file(str(model_dir / "tokenizer.json"))
    tokenizer.enable_truncation(max_length=512)
    tokenizer.enable_padding(pad_id=0, pad_token="[PAD]")

    session = ort.InferenceSession(
        str(model_dir / "onnx" / "model.onnx"),
        providers=["CPUExecutionProvider"],
    )

    encoded = tokenizer.encode_batch(texts)
    input_ids = np.array([item.ids for item in encoded], dtype=np.int64)
    attention_mask = np.array([item.attention_mask for item in encoded], dtype=np.int64)

    outputs = session.run(None, {"input_ids": input_ids, "attention_mask": attention_mask})
    embeddings = outputs[1]
    embeddings = embeddings / np.linalg.norm(embeddings, axis=1, keepdims=True)

    json.dump({"embeddings": embeddings.tolist()}, sys.stdout)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
