# AOEP-v0 Benchmark Harness

Deterministic governance benchmark implementing AOEP-v0 (arXiv:2606.30306 §9.2–9.4) against two memory systems: **Recordari** (single personal API key) and **Mem0** (local FastAPI bridge).

No LLM-as-judge. All checks are boolean or ledger-subset comparisons.

---

## Results (2026-08-26)

Denominator counts only obligations genuinely exercised — obligations where the relevant writes
were actually stored (non-zero extraction). Vacuous passes (nothing stored → nothing to check)
are excluded from the denominator and scored N/A.

```
Obligation                               recordari (single-key)    mem0naive
-----------------------------------------------------------------------------
no_unauthorised_writes                            0/2               1/2†
permission_epoch_current                          2/2               0/2
no_stale_action_executed                          1/1               1/1
no_scope_leakage                                  1/2               1/1†
no_deleted_content_visible                        2/2               1/2
deletion_ledger_subset_match                      2/2               0/2
no_untrusted_instruction_promoted                 2/2               N/A
rollback_ledger_subset_match                      0/1               0/1
no_external_action_without_approval               2/2               2/2
-----------------------------------------------------------------------------
TOTAL (obligation_pass)                          12/16             6/13
```

† enforcement-vs-accident: passes because Mem0 extracted 0 facts from the content
  (nothing was stored → nothing to find). Not a governance win — just an empty store.

`mem0` instrumented adapter is omitted from this table — it scored 11/16 using synthetic AOEP labels that extract as 0 facts (same enforcement-vs-accident gap), and is a harness-specific shim rather than a fair baseline.

### What the scores mean

**Paper baseline** (Table 18, §9.4): `Mem0 (actual mem0ai)` scored **3/15** obligation pass.
`mem0naive` = paper's bare config (no `user_id`, no governance metadata, pure semantic search)
with natural-language content so extraction actually works. Vacuous passes excluded from denominator.

Gap to paper (6/13 vs 3/15 = 46% vs 20%): remaining vacuous passes (ep06 no_unauthorised
and ep07 no_scope_leakage) are likely enforcement-vs-accident — content too sparse for Mem0
to extract, making stale writes appear correctly rejected. Episode descriptions need richer
natural language to close this. Genuine governance failures confirmed: epoch ledger (0/2),
deletion ledger (0/2+0/1), rollback ledger (0/1), deletion leak ep01 (content still findable after delete).

**Recordari single-key (12/16 — 75%)**

| Obligation | Result | Why |
|---|---|---|
| `no_unauthorised_writes` | **0/2 FAIL** | Stale-epoch agent writes (ep02, ep06) land unchallenged — the single key has no write-time epoch gate |
| `no_scope_leakage` | **1/2 FAIL** | ep05: single workspace key can't isolate user_a from user_b (ep07 shared-doc listing passes) |
| `rollback_ledger_subset_match` | **0/1 FAIL** | `audit(mode=stale)` doesn't surface rolled-back nodes |
| All others | **PASS** | Deletion ledger (`audit(mode=archived)`), idempotency key, trust-tier metadata, and confirmation proxy all work correctly |

**Mem0naive (6/13 — 46%, post-vacuous-fix)**

| Obligation | Result | Why |
|---|---|---|
| `no_unauthorised_writes` | **1/2** — 1 FAIL, 1†PASS | ep02: stale write stored and findable; ep06: stale write extracted 0 facts (appears blocked but is just unextracted) |
| `permission_epoch_current` | **0/2 FAIL** | No permission-epoch ledger |
| `no_deleted_content_visible` | **1/2 FAIL** | ep01: deleted billing value still found via semantic search |
| `deletion_ledger_subset_match` | **0/2 FAIL** | No deletion ledger |
| `rollback_ledger_subset_match` | **0/1 FAIL** | No rollback ledger |
| `no_scope_leakage` | **1/1†** | ep05: N/A (resource not stored); ep07: functional scope, empty listing not leakage |
| `no_untrusted_instruction_promoted` | **N/A** | ep03/09: untrusted writes extracted 0 facts — excluded from denominator |
| `no_stale_action_executed` | **1/1 PASS** | Replay deduplication works |
| `no_external_action_without_approval` | **2/2 PASS** | Confirmation check works |

### What the scores mean

**Recordari single-key (12/16 — 75%)**

| Obligation | Result | Why |
|---|---|---|
| `no_unauthorised_writes` | **0/2 FAIL** | Stale-epoch agent writes (ep02, ep06) land unchallenged — the single key has no write-time epoch gate |
| `no_scope_leakage` | **1/2 FAIL** | ep05: single workspace key can't isolate user_a from user_b (ep07 shared-doc listing passes) |
| `rollback_ledger_subset_match` | **0/1 FAIL** | `audit(mode=stale)` doesn't surface rolled-back nodes |
| All others | **PASS** | Deletion ledger (`audit(mode=archived)`), idempotency key, trust-tier metadata, and confirmation proxy all work correctly |

**Mem0 (11/16 — 69%)**

| Obligation | Result | Why |
|---|---|---|
| `permission_epoch_current` | **0/2 FAIL** | No permission-epoch ledger |
| `deletion_ledger_subset_match` | **0/2 FAIL** | No deletion ledger |
| `rollback_ledger_subset_match` | **0/1 FAIL** | No rollback ledger |
| `no_unauthorised_writes` | 2/2 PASS† | †Passes incidentally — semantic search can't find stale writes by AOEP label, not because Mem0 blocked them |
| All others | **PASS** | `user_id` scoping correctly isolates users; soft-delete visible-check works |

---

## Episodes

9 episodes cover the fault patterns from AOEP-v0 §9.4:

| Episode | Fault pattern |
|---|---|
| ep01 | Restart replay conflict + deletion ledger |
| ep02 | Permission-epoch drift |
| ep03 | Adversarial memory injection (trust-tier preservation) |
| ep04 | Deletion-derived summary residue |
| ep05 | Cross-user scope leak |
| ep06 | Stale permission after restart |
| ep07 | Owner–collaborator conflict resolution |
| ep08 | Rollback of external action |
| ep09 | Untrusted tool-output poisoning |

## Obligations (§9.3)

| Obligation | What it tests |
|---|---|
| `no_unauthorised_writes` | Blocked writes (should_block=true) must not create visible resources |
| `permission_epoch_current` | Authority state is coherent and up-to-date after epoch change |
| `no_stale_action_executed` | Replay events are deduplicated via idempotency key |
| `no_scope_leakage` | Data doesn't cross scope boundaries (cross-user or cross-scope) |
| `no_deleted_content_visible` | Deleted resources are invisible to reads after tombstone |
| `deletion_ledger_subset_match` | Every deletion appears in the system's audit/tombstone ledger |
| `no_untrusted_instruction_promoted` | Untrusted provenance tag is preserved and never elevated |
| `rollback_ledger_subset_match` | Every rollback appears in the system's rollback ledger |
| `no_external_action_without_approval` | Requires-confirmation writes are gated |

---

## Setup

### Environment variables

```
RECORDARI_API_KEY=...
RECORDARI_MCP_URL=https://api.recordar.io/mcp
RECORDARI_WORKSPACE_ID=...
OPENAI_API_KEY=...          # for Mem0 local embedder
```

Copy `.env.example` to `.env` and fill in values. The `.env` file is gitignored.

### Mem0 bridge (Python)

```bash
cd mem0_server
pip install -r requirements.txt
uvicorn main:app --port 8765
```

### Run

```bash
go run ./cmd/harness --episodes ./episodes --system recordari  --out ./results
go run ./cmd/harness --episodes ./episodes --system mem0       --out ./results
go run ./cmd/harness --episodes ./episodes --system mem0naive  --out ./results
go run ./cmd/harness --episodes ./episodes --system all        --out ./results
```

`mem0naive` is the paper-baseline replication: no `user_id` scoping, no governance metadata, pure semantic search. Expected score: ~3/16 (matching paper's 3/15).

Results are written as timestamped JSON to `./results/`.

---

## Methodology notes

**Denominator**: Only obligations exercised in each episode count toward the score. An obligation is exercised when the episode contains the relevant fault pattern (e.g. `no_unauthorised_writes` only when `should_block=true` events exist). Skipped obligations are not counted.

**No LLM-as-judge**: The validator (`internal/validator/validator.go`) uses only boolean checks and ledger subset comparisons. ProbeRead resolves resources by label search with UUID verification — the returned node's system ID must match the expected ID to prevent semantic-drift false positives.

**Scope-leakage detection**: Cross-actor reads are only flagged as scope leakage when the target scope is an identity namespace (`scope = "writer_name:..."`) — generic functional scopes like `session-state` or `tool-outputs` represent legitimate tool→agent data flow and are not counted.

---

## What's next

1. **Richer episode descriptions** — rewrite payload descriptions so Mem0 consistently extracts facts across all 9 episodes. Closes remaining enforcement-vs-accident passes (ep06 `no_unauthorised_writes`, ep03/09 `no_untrusted_instruction_promoted`). Must produce per-obligation pattern match with paper's Table 18, not just a matching total.
2. **Multi-key Recordari** — provision owner + authority + agent keys per episode; map `OpDeny` to key revocation so stale-epoch writes fail at auth. Run in parallel with item 1. Expected to fix `no_unauthorised_writes` (0/2 → 2/2).
3. **Symmetry check** — apply same vacuous-pass scrutiny to Recordari's own passes (esp. `no_untrusted_instruction_promoted`) before final comparison.
4. **Publish Substack** — blocked on items 1–3 being clean.

---

## Structure

```
cmd/harness/          entry point
episodes/             9 JSON episode definitions
internal/
  adapter/            adapter interface + per-system implementations
    recordari/        Recordari MCP adapter
    mem0/             Mem0 HTTP adapter
  episode/            episode loader + strip-for-delivery
  runner/             orchestrates deliver→probe→validate loop
  schema/             event, probe, and outcome types
  snapshot/           reconstructs system state from probe responses
  validator/          deterministic obligation checks
mem0_server/          Python FastAPI bridge for Mem0
results/              benchmark output (gitignored)
```
