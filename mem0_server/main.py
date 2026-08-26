"""
Local mem0 HTTP bridge for the AOEP-v0 benchmark harness.

Wraps mem0.Memory with a minimal REST API so the Go harness can drive it.
Uses Chroma (in-process, no external server) + OpenAI embedder.

Run:
    cd mem0_server
    pip install -r requirements.txt
    uvicorn main:app --port 8888

Env vars:
    OPENAI_API_KEY  — required (embeddings + LLM fact extraction)
    CHROMA_PATH     — optional, defaults to ./chroma_data
"""

import os
import uuid

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
from mem0 import Memory

CHROMA_PATH = os.environ.get("CHROMA_PATH", "./chroma_data")

config = {
    "llm": {
        "provider": "openai",
        "config": {"model": "gpt-4o-mini"},
    },
    "embedder": {
        "provider": "openai",
        "config": {"model": "text-embedding-3-small"},
    },
    "vector_store": {
        "provider": "chroma",
        "config": {
            "collection_name": "aoep_benchmark",
            "path": CHROMA_PATH,
        },
    },
}

m = Memory.from_config(config)
app = FastAPI(title="mem0 AOEP bridge", version="0.2.0")


# --- Request models ---

class AddRequest(BaseModel):
    content: str
    user_id: str = "aoep-benchmark"
    metadata: dict = {}

class SearchRequest(BaseModel):
    query: str
    user_id: str = "aoep-benchmark"
    limit: int = 10


# --- Helpers ---

def _items(response) -> list:
    """Normalise mem0 response to a plain list.

    mem0 >= 0.1 returns {"results": [...], "relations": [...]}.
    Older builds returned a bare list. Handle both.
    """
    if isinstance(response, dict):
        return response.get("results", [])
    if isinstance(response, list):
        return response
    return []


# --- Endpoints ---

@app.post("/add")
def add_memory(req: AddRequest):
    """Write content to mem0. Returns the primary memory ID assigned."""
    response = m.add(
        messages=[{"role": "user", "content": req.content}],
        user_id=req.user_id,
        metadata=req.metadata,
    )
    items = _items(response)
    ids = [r["id"] for r in items if isinstance(r, dict) and r.get("id")]
    primary_id = ids[0] if ids else str(uuid.uuid4())
    return {"id": primary_id, "all_ids": ids, "count": len(ids)}


@app.get("/get/{memory_id}")
def get_memory(memory_id: str):
    """Retrieve one memory by ID; 404 if deleted or not found."""
    try:
        result = m.get(memory_id)
    except Exception as exc:
        raise HTTPException(status_code=404, detail=str(exc))
    if not result:
        raise HTTPException(status_code=404, detail="memory not found")
    return result


@app.post("/search")
def search_memory(req: SearchRequest):
    """Semantic search scoped by user_id."""
    response = m.search(
        req.query,
        filters={"user_id": req.user_id},
        limit=req.limit,
    )
    return {"memories": _items(response)}


@app.get("/list")
def list_memories(user_id: str = "aoep-benchmark"):
    """List all memories for an actor."""
    response = m.get_all(filters={"user_id": user_id})
    return {"memories": _items(response)}


@app.delete("/delete/{memory_id}")
def delete_memory(memory_id: str):
    """Hard-delete one memory. No-ops if already absent."""
    try:
        m.delete(memory_id)
    except Exception:
        pass  # not found is idempotent
    return {"deleted": memory_id}


@app.delete("/reset")
def reset_user(user_id: str = "aoep-benchmark"):
    """Delete all memories for one user."""
    try:
        m.delete_all(filters={"user_id": user_id})
    except Exception:
        pass
    return {"reset": True, "user_id": user_id}


@app.delete("/reset_all")
def reset_all():
    """Delete ALL memories across all users (between episodes)."""
    try:
        m.reset()
    except Exception:
        pass
    return {"reset": True, "scope": "all"}


@app.get("/health")
def health():
    return {"status": "ok"}
