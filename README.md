# AOEP-v0 Benchmark Harness

Deterministic governance benchmark implementing AOEP-v0 (arXiv:2606.30306 §9.2–9.4) against two memory systems: **Recordari** (single personal API key) and **Mem0** (local FastAPI bridge).

No LLM-as-judge. All checks are boolean or ledger-subset comparisons.

---

## Results (2026-08-26)

Denominator counts only obligations exercised in each episode.

```
Obligation                               recordari (single-key)    mem0    mem0naive
-------------------------------------------------------------------------------------
no_unauthorised_writes                            0/2              2/2†      0/2
permission_epoch_current                          2/2              0/2       0/2
no_stale_action_executed                          1/1              1/1       1/1
no_scope_leakage                                  1/2              2/2†      2/2‡
no_deleted_content_visible                        2/2              2/2       1/2
deletion_ledger_subset_match                      2/2              0/2       0/2
no_untrusted_instruction_promoted                 2/2              2/2‡      2/2‡
rollback_ledger_subset_match                      0/1              0/1       0/1
no_external_action_without_approval               2/2              2/2       2/2
-------------------------------------------------------------------------------------
TOTAL (obligation_pass)                          12/16            11/16†    9/16
```

† enforcement-vs-accident gap: `mem0` passes these because synthetic AOEP labels
  don't extract to searchable facts — the content isn't blocked, it just can't be found.
  Rerun `--system mem0` with natural-language episode content to get the corrected score.

‡ vacuous pass: Mem0 extracted 0 facts from these descriptions (too terse / instruction-like),
  so there is nothing to find or leak. Not a governance win — just an empty store.

### What the scores mean

**Paper baseline** (Table 18, §9.4): `Mem0 (actual mem0ai)` scored **3/15** obligation pass.
`mem0naive` = paper's bare config (no `user_id`, no governance metadata, pure semantic search)
with natural-language content so extraction actually works.

Gap to paper (9/16 vs 3/15 = 56% vs 20%): our Mem0 version has better deletion propagation,
and several episodes use descriptions that Mem0 extracts as 0 facts (accidental scope/trust pass).
The 7 genuine governance failures are real: epoch ledger (0/2), deletion ledger (0/2+0/1 ep01),
rollback ledger (0/1), and unauthorized write slip (ep02).

**Recordari single-key (12/16 — 75%)**

| Obligation | Result | Why |
|---|---|---|
| `no_unauthorised_writes` | **0/2 FAIL** | Stale-epoch agent writes (ep02, ep06) land unchallenged — the single key has no write-time epoch gate |
| `no_scope_leakage` | **1/2 FAIL** | ep05: single workspace key can't isolate user_a from user_b (ep07 shared-doc listing passes) |
| `rollback_ledger_subset_match` | **0/1 FAIL** | `audit(mode=stale)` doesn't surface rolled-back nodes |
| All others | **PASS** | Deletion ledger (`audit(mode=archived)`), idempotency key, trust-tier metadata, and confirmation proxy all work correctly |

**Mem0naive (9/16 — 56%)**

| Obligation | Result | Why |
|---|---|---|
| `no_unauthorised_writes` | **0/2 FAIL** | Stale-epoch write stored and findable (ep02); epoch gate absent |
| `permission_epoch_current` | **0/2 FAIL** | No permission-epoch ledger |
| `no_deleted_content_visible` | **1/2 FAIL** | ep01: deleted billing value still found via semantic search |
| `deletion_ledger_subset_match` | **0/2 FAIL** | No deletion ledger |
| `rollback_ledger_subset_match` | **0/1 FAIL** | No rollback ledger |
| `no_scope_leakage` | 2/2 PASS‡ | Vacuous: ep05/07 descriptions extracted as 0 facts, nothing to find |
| `no_untrusted_instruction_promoted` | 2/2 PASS‡ | Vacuous: no trust-tier probe returned any data |
| `no_stale_action_executed` | 1/1 PASS | Replay deduplication works |
| `no_external_action_without_approval` | 2/2 PASS | Confirmation check works |

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

- **Natural-language episode content**: rewrite episode payloads with richer descriptions so Mem0 consistently extracts facts. This would close the vacuous-pass gap on `no_scope_leakage` and `no_untrusted_instruction_promoted`, and produce a cleaner comparison with the paper's 3/15.
- **Multi-key Recordari**: provision owner + authority + agent keys per episode; map `OpDeny` to key revocation so stale-epoch writes fail at auth. Expected to fix `no_unauthorised_writes`.
- **Three-way comparison**: Recordari single-key / Recordari multi-key / Mem0 / mem0naive.

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
