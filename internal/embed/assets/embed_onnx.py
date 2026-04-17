#!/usr/bin/env python3
import contextlib
import io
import json
import sys
import time
from pathlib import Path

import numpy as np
import onnxruntime as ort
from tokenizers import Tokenizer


def load_request() -> dict:
    return json.load(sys.stdin)


def preload_cuda_dependencies() -> None:
    sink = io.StringIO()
    with contextlib.redirect_stdout(sink), contextlib.redirect_stderr(sink):
        ort.preload_dlls(cuda=True, cudnn=True, msvc=True, directory="")


def select_execution_providers(device: str = "") -> list[str]:
    if device == "cpu":
        return ["CPUExecutionProvider"]
    if device in ("cuda", "gpu"):
        available = set(ort.get_available_providers())
        if "CUDAExecutionProvider" not in available:
            if "CPUExecutionProvider" in available:
                return ["CPUExecutionProvider"]
            raise RuntimeError(
                f"CUDAExecutionProvider requested but not available; available={sorted(available)}"
            )
        return ["CUDAExecutionProvider", "CPUExecutionProvider"]
    available = set(ort.get_available_providers())
    providers: list[str] = []
    for name in (
        "CUDAExecutionProvider",
        "ROCMExecutionProvider",
        "DmlExecutionProvider",
        "CoreMLExecutionProvider",
    ):
        if name in available:
            providers.append(name)
    if "CPUExecutionProvider" in available:
        providers.append("CPUExecutionProvider")
    if not providers:
        raise RuntimeError(
            f"onnxruntime has no supported execution providers; available={sorted(available)}"
        )
    return providers


def resolve_batch_size(requested: int, providers: list[str]) -> int:
    if requested > 0:
        return requested
    if providers and providers[0] != "CPUExecutionProvider":
        return 192
    return 32


def is_retryable_oom(exc: Exception) -> bool:
    text = str(exc)
    return any(
        marker in text
        for marker in (
            "Failed to allocate memory",
            "CUDA out of memory",
            "BFCArena",
            "MemcpyToHost",
        )
    )


def emit_batch_stats(
    batch_index: int,
    batch_size: int,
    processed: int,
    tokenize_ms: float,
    infer_ms: float,
    normalize_ms: float,
    retry_count: int,
    settled_batch: int,
) -> None:
    payload = {
        "index": batch_index,
        "size": batch_size,
        "processed": processed,
        "tokenize_ms": tokenize_ms,
        "infer_ms": infer_ms,
        "normalize_ms": normalize_ms,
        "retry_count": retry_count,
        "settled_batch": settled_batch,
    }
    sys.stderr.write("WAVE_EMBED_BATCH " + json.dumps(payload) + "\n")
    sys.stderr.flush()


def resolve_model_path(model_dir: Path) -> Path:
    root_model = model_dir / "model.onnx"
    if root_model.exists():
        return root_model
    nested_model = model_dir / "onnx" / "model.onnx"
    if nested_model.exists():
        return nested_model
    raise FileNotFoundError(f"model.onnx not found in {model_dir}")


def build_input_feed(session: ort.InferenceSession, encoded: list) -> dict[str, np.ndarray]:
    input_ids = np.array([item.ids for item in encoded], dtype=np.int64)
    attention_mask = np.array([item.attention_mask for item in encoded], dtype=np.int64)
    feed = {
        "input_ids": input_ids,
        "attention_mask": attention_mask,
    }
    input_names = {item.name for item in session.get_inputs()}
    if "token_type_ids" in input_names:
        feed["token_type_ids"] = np.array([item.type_ids for item in encoded], dtype=np.int64)
    return feed


def select_embeddings(outputs: list, attention_mask: np.ndarray) -> np.ndarray:
    if len(outputs) > 0 and getattr(outputs[0], "ndim", 0) == 3:
        token_embeddings = outputs[0].astype(np.float32, copy=False)
        expanded_mask = attention_mask[..., np.newaxis].astype(np.float32, copy=False)
        masked = token_embeddings * expanded_mask
        counts = np.clip(expanded_mask.sum(axis=1), 1e-9, None)
        return masked.sum(axis=1) / counts
    if len(outputs) > 1 and getattr(outputs[1], "ndim", 0) == 2:
        return outputs[1].astype(np.float32, copy=False)
    if len(outputs) > 0 and getattr(outputs[0], "ndim", 0) == 2:
        return outputs[0].astype(np.float32, copy=False)
    raise RuntimeError("unable to determine embedding tensor from model outputs")


def main() -> int:
    total_started_at = time.perf_counter()
    request_started_at = time.perf_counter()
    request = load_request()
    request_ms = (time.perf_counter() - request_started_at) * 1000.0
    model_dir = Path(request["model_dir"])
    texts = request.get("texts", [])
    requested_batch_size = int(request.get("batch_size", 0))
    device = request.get("device", "")

    providers = select_execution_providers(device)
    preload_started_at = time.perf_counter()
    if providers and providers[0] != "CPUExecutionProvider":
        preload_cuda_dependencies()
    preload_ms = (time.perf_counter() - preload_started_at) * 1000.0
    batch_size = resolve_batch_size(requested_batch_size, providers)

    tokenizer = Tokenizer.from_file(str(model_dir / "tokenizer.json"))
    tokenizer.enable_truncation(max_length=256)
    tokenizer.enable_padding(pad_id=0, pad_token="[PAD]")

    session_started_at = time.perf_counter()
    session = ort.InferenceSession(
        str(resolve_model_path(model_dir)),
        providers=providers,
    )
    session_ms = (time.perf_counter() - session_started_at) * 1000.0

    all_embeddings: list[np.ndarray] = []
    tokenize_ms = 0.0
    infer_ms = 0.0
    normalize_ms = 0.0
    batch_count = 0
    oom_retries = 0
    processed = 0
    settled_batch_size = batch_size
    start = 0
    # Sanitize texts: encode_batch requires non-empty strings.
    texts = [t if isinstance(t, str) and t else " " for t in texts]

    while start < len(texts):
        current_batch_size = min(batch_size, len(texts) - start)
        batch_retry_count = 0
        while True:
            batch = texts[start : start + current_batch_size]
            tokenize_started_at = time.perf_counter()
            encoded = tokenizer.encode_batch(batch)
            feed = build_input_feed(session, encoded)
            batch_tokenize_ms = (time.perf_counter() - tokenize_started_at) * 1000.0
            tokenize_ms += batch_tokenize_ms

            try:
                infer_started_at = time.perf_counter()
                outputs = session.run(None, feed)
                batch_infer_ms = (time.perf_counter() - infer_started_at) * 1000.0
                infer_ms += batch_infer_ms
                break
            except Exception as exc:
                if current_batch_size == 1 or not is_retryable_oom(exc):
                    raise
                oom_retries += 1
                batch_retry_count += 1
                current_batch_size = max(1, current_batch_size // 2)

        normalize_started_at = time.perf_counter()
        embeddings = select_embeddings(outputs, feed["attention_mask"])
        embeddings = embeddings / np.linalg.norm(embeddings, axis=1, keepdims=True)
        all_embeddings.append(embeddings.astype(np.float32, copy=False))
        batch_normalize_ms = (time.perf_counter() - normalize_started_at) * 1000.0
        normalize_ms += batch_normalize_ms
        batch_count += 1
        processed += len(batch)
        settled_batch_size = max(settled_batch_size, current_batch_size)
        emit_batch_stats(
            batch_count,
            len(batch),
            processed,
            batch_tokenize_ms,
            batch_infer_ms,
            batch_normalize_ms,
            batch_retry_count,
            current_batch_size,
        )
        batch_size = current_batch_size
        start += len(batch)

    serialize_started_at = time.perf_counter()
    if all_embeddings:
        flat = np.concatenate(all_embeddings, axis=0)
        dim = int(flat.shape[1])
    else:
        flat = np.empty((0, 0), dtype=np.float32)
        dim = 0

    serialize_ms = (time.perf_counter() - serialize_started_at) * 1000.0
    header = json.dumps(
        {
            "count": len(texts),
            "dim": dim,
            "provider": providers[0],
            "requested_batch": requested_batch_size,
            "selected_batch": settled_batch_size,
            "batch_count": batch_count,
            "oom_retries": oom_retries,
            "request_ms": request_ms,
            "preload_ms": preload_ms,
            "session_ms": session_ms,
            "tokenize_ms": tokenize_ms,
            "infer_ms": infer_ms,
            "normalize_ms": normalize_ms,
            "serialize_ms": serialize_ms,
            "total_ms": (time.perf_counter() - total_started_at) * 1000.0,
        }
    ).encode("utf-8") + b"\n"
    sys.stdout.buffer.write(header)
    sys.stdout.buffer.write(flat.astype(np.float32, copy=False).tobytes())
    sys.stdout.buffer.flush()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
