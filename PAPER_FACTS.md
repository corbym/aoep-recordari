# AOEP-v0 Paper Facts

Canonical ground truth for arXiv:2606.30306 §9.2–9.6 (2026-08-28 verification).
The README's "How this differs from the paper's pilot" section links here.

## Systems in the paper's §9.4 pilot (Table 18)

Seven systems, in table order:
1. Governed reducer — upper bound — 15/15 obligation pass, 41/41 neg-inv, 92/92 all
2. No-memory floor — lower bound — 0/15 obligation pass, 41/41 neg-inv, 77/92 all
3. Naive append
4. Full context
5. Vector-RAG
6. Mem0-style reimplementation
7. Mem0 (actual mem0ai) — 3/15 obligation pass, 36/41 neg-inv, 73/92 all

**Recordari is not in the paper.** This repo is an independent application of the protocol.

## Reader

All SUTs in the paper answer probes through a **frozen Qwen2.5-7B reader (greedy decoding)**
sitting over the store. Table 18 scores measure store-plus-reader configurations.

This harness has no reader — probes hit store APIs directly. Numbers are therefore **not directly
comparable** to Table 18. Only the per-obligation failure pattern is comparable.

## Two-score design (§9.3)

The paper explicitly splits the scorecard: "the scorecard is deliberately split into two scores
rather than a single number" because "a system that stores nothing trivially passes every negative
invariant" and a pooled scalar "would reward amnesia."

Table 18 reports three columns: **Obligation | Neg.-invariant | All**.

## Additional evaluation (§9.5 and Table 19)

- 3 realistic multi-actor episodes (tau-bench-style refund, TheAgentCompany-style scheduling, one more)
- 3 real execution traces
- Reader-robustness sweep (Table 19): 3B–8B models, three families

This harness reproduces only the nine core fault patterns from §9.4.

## Spelling

Paper: `no_unauthorized_writes` (z). This repo: `no_unauthorised_writes` (s).
Search both spellings when grepping across the paper and this repo.
