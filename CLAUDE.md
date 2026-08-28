# AOEP-Recordari Benchmark Harness

Implements the AOEP-v0 governance benchmark (arXiv:2606.30306 §9.2–9.4) against
Recordari (api.recordar.io MCP) and Mem0 (local paper-baseline replication).

## Key constraints

- Validator is fully deterministic — no LLM-as-judge.
- Harness strips outcome-revealing fields before delivering events to the system under test.
- Probes are neutral: they name only actor + target resource, never expected values or invariant names.
- Comparator is `mem0paper`: the paper's local mem0ai config (§9.6) — single shared namespace, no governance envelope. Paper baseline: 3/15. The LoCoMo "Memora only" publication rule does NOT apply here.
- Use a dedicated benchmark workspace/org in Recordari — never production data.

## Env vars

```
RECORDARI_API_KEY=...          # personal key with read+write scopes
RECORDARI_MCP_URL=https://api.recordar.io/mcp
RECORDARI_WORKSPACE_ID=...     # dedicated benchmark workspace

OPENAI_API_KEY=...             # for Mem0 local embedder
```

## Run

```
go run ./cmd/harness --episodes ./episodes --system recordari  --out ./results
go run ./cmd/harness --episodes ./episodes --system mem0paper  --out ./results
go run ./cmd/harness --episodes ./episodes --system all        --out ./results
```
