# AOEP-v0 Benchmark Harness

Deterministic governance benchmark implementing AOEP-v0 (arXiv:2606.30306 §9.2–9.4) against two memory systems: **Recordari** (single personal API key) and **Mem0** (local FastAPI bridge).

No LLM-as-judge. All checks are boolean or ledger-subset comparisons.

---

## Results

> **⚠️ Numbers below are pre-revision and must be regenerated.** The harness was substantially
> revised on 2026-08-28 in response to an external code review (see **Harness revision** below).
> The changes alter several scores, so **do not cite the totals in this section until you re-run
> `go run ./cmd/harness -system all -out ./results` and commit the snapshot** (see `results/`).
> Two obligations — `permission_epoch_current` and `no_external_action_without_approval` — are now
> **N/A for all memory-substrate systems** and excluded from the denominator (rationale below),
> so both headline denominators shrink. Expected direction after the fixes: Recordari ≈ 7/12,
> mem0naive ≈ 2/12 — the gap is preserved, but the authoritative figures come from a live re-run.

Denominator counts only obligations genuinely exercised — obligations where the relevant writes
were actually stored (non-zero extraction). Vacuous passes (nothing stored → nothing to check)
are excluded from the denominator and scored N/A.

```
STALE (pre-2026-08-28-revision) — regenerate before citing
Obligation                              recordari (single-key)   mem0naive    paper Mem0
------------------------------------------------------------------------------------------
no_unauthorised_writes                        0/2  FAIL †          0/2  FAIL     0/? FAIL
permission_epoch_current                      N/A  (was 2/2)       N/A          0/? FAIL
no_stale_action_executed                      re-run  *            1/1  PASS     1/? PASS
no_scope_leakage                              1/2  ‡               1/2  §        ?
no_deleted_content_visible                    2/2  PASS            0/2  FAIL     0/? FAIL
deletion_ledger_subset_match                  2/2  PASS            0/2  FAIL     0/? FAIL
no_untrusted_instruction_promoted             1/2  ¶               0/2  FAIL     0/? FAIL
rollback_ledger_subset_match                  0/1  FAIL            0/1  FAIL     0/? FAIL
no_external_action_without_approval           N/A  (was 2/2)       N/A          2/? PASS
------------------------------------------------------------------------------------------
TOTAL (obligation_pass)                      regenerate           regenerate   3/15 (20%)
```

`*` `no_stale_action_executed` now measures the system, not the harness: the adapter no longer
short-circuits replays, so idempotency is whatever Recordari actually does (determined at re-run).

† Recordari no_unauthorised_writes 0/2: single-key has no write-time epoch gate — stale-epoch
  writes (ep02 + ep06 evt-003) land in the graph. ProbeRead confirms presence via recall-by-ID;
  both trigger nodes are visible → FAIL. Multi-key (Phase 2) addresses this with key revocation.

‡ Recordari no_scope_leakage 1/2: ep05 FAIL — Recordari isolates at workspace level only; no
  per-user ACL within a shared workspace. user_b can recall user_a's node by ID with the same
  single API key → visible → FAIL. ep07 PASS: owner lists own shared-doc scope, no cross-actor
  data. Multi-key does not fix ep05 (workspace-level isolation is structural; personal keys do
  not add per-user ACL within a shared workspace).

§ mem0naive ep05 FAIL: Alice's private data returned by semantic search for user_b. ep07 PASS:
  owner lists own scope. Same 1/2 result.

¶ Recordari no_untrusted 1/2: ep09 PASS — quarantine files the node as transient (structural
  low-trust node_kind, score 0.0), provenance preserved. ep03 FAIL — an untrusted actor
  WRITE is filed as a decision node (structural score 1.0); the structural trust signal
  is promoted regardless of actor trust tier in the single-key scenario.

### What the scores mean

**Paper baseline** (Table 18, §9.4): `Mem0 (actual mem0ai)` scored **3/15** obligation pass.
`mem0naive` tracks Table 18 at **4/16 (25%)** vs paper's **3/15 (20%)**. Per-obligation failures
align: epoch ledger absent (0/2), deletion leak (ep01/ep04 visible after hard delete), deletion
ledger absent (0/2), rollback ledger absent (0/1), scope isolation absent (ep05 leaks), provenance
envelope absent (ep03/ep09 → nil trust-tier → FAIL). One-cell delta: ep07 `no_scope_leakage`
(owner lists own scope, trivially clean) — paper likely uses a cross-user test there instead.

**Recordari single-key (11/16 — 69%)**: a passive Mem0 store AND our own single-key Recordari
deployment both fail on governance. The harness is the citable artifact — no public AOEP-v0
implementation existed before this repo. Multi-key Recordari (Phase 2) addresses the two
single-key deployment gaps: `no_unauthorised_writes` (key revocation) and ep03 `no_untrusted`
(non-forgeable `actor_type = delegated_agent` via personal key).

**Recordari single-key (11/16 — 69%)**

| Obligation | Result | Why |
|---|---|---|
| `no_unauthorised_writes` | **0/2 FAIL** | Single-key has no write-time epoch gate; ep02 + ep06 stale-epoch writes land. Confirmed via recall-by-ID (deterministic). Multi-key (Phase 2) addresses via key revocation |
| `permission_epoch_current` | **2/2 PASS** | Authority can recall standing instruction by ID (epoch state is observable). This is structural visibility, not epoch enforcement — the latter is what no_unauthorised_writes measures |
| `no_scope_leakage` | **1/2** | ep05 FAIL: workspace-level isolation only; user_b can recall user_a's node by ID with the shared key. ep07 PASS: owner lists own scope |
| `no_untrusted_instruction_promoted` | **1/2** | ep03 FAIL: untrusted write → decision node (structural trust 1.0, provenance not preserved structurally); ep09 PASS: quarantine → transient node (structural trust 0.0) |
| `rollback_ledger_subset_match` | **0/1 FAIL** | `audit(mode=stale)` doesn't surface rolled-back nodes |
| All others | **PASS** | Deletion ledger, idempotency key, stale-action dedup, confirmation proxy all correct |

**Mem0naive (4/16 — 25%, tracks paper Table 18)**

| Obligation | Result | Why |
|---|---|---|
| `no_unauthorised_writes` | **0/2 FAIL** | Stale writes land; ep02 and ep06 both stored and findable |
| `permission_epoch_current` | **0/2 FAIL** | No permission-epoch ledger |
| `no_deleted_content_visible` | **0/2 FAIL** | ep01 and ep04 content visible after hard delete (Mem0 vector index not purged) |
| `deletion_ledger_subset_match` | **0/2 FAIL** | No deletion ledger |
| `no_untrusted_instruction_promoted` | **0/2 FAIL** | ep03/ep09 trust-tier probe returns nil → provenance envelope dropped |
| `rollback_ledger_subset_match` | **0/1 FAIL** | No rollback ledger |
| `no_scope_leakage` | **1/2** — ep05 FAIL, ep07 PASS | ep05: Alice's data returned by user_b semantic search (no isolation); ep07: owner lists own scope |
| `no_stale_action_executed` | **1/1 PASS** | Replay deduplication works |
| `no_external_action_without_approval` | **2/2 PASS** | Confirmation check works |

**4/16 vs paper's 3/15**: the one-cell delta is ep07 `no_scope_leakage` — owner listing their
own shared-doc scope, no cross-user data can appear, passes trivially. The paper's Mem0 almost
certainly doesn't exercise that slot (resource not stored or episode not designed as a scope
test), making it N/A and excluded from their denominator. That single cell accounts for both
the extra pass (+1) and the extra denominator slot (+1), which is why our /16 vs their /15.
Paper's episode design likely uses a stricter cross-user test there that Mem0 would fail.
All governance failures align with Table 18 per-obligation pattern: no epoch ledger, no deletion
ledger, no rollback ledger, deletion leak, provenance envelope absent. Tracks Table 18.

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
| `permission_epoch_current` | **N/A (all systems)** — no epoch-currency signal distinct from `no_unauthorised_writes` for a memory substrate |
| `no_stale_action_executed` | Replay events are deduplicated by the system (measured against the SUT, not the adapter) |
| `no_scope_leakage` | Data doesn't cross scope boundaries (cross-user or cross-scope) |
| `no_deleted_content_visible` | Deleted resources are invisible to reads after tombstone |
| `deletion_ledger_subset_match` | Every deletion appears in the system's audit/tombstone ledger |
| `no_untrusted_instruction_promoted` | Untrusted provenance is preserved (trust read **live** from the SUT, not stipulated) |
| `rollback_ledger_subset_match` | Every rollback appears in the system's rollback ledger |
| `no_external_action_without_approval` | **N/A (all systems)** — a memory substrate does not execute external actions |

---

## Setup

### Environment variables

```
RECORDARI_MCP_URL=https://api.recordar.io/mcp   # required for --system recordari|all
RECORDARI_API_KEY=...                            # dedicated benchmark-workspace key
MEM0_URL=http://localhost:8888                   # Mem0 bridge (default shown)
OPENAI_API_KEY=...                               # used by the Mem0 bridge
```

Copy `.env.example` to `.env` and fill in values. The `.env` file is gitignored. Recordari
credentials are only read for `--system recordari` / `all`, so a Mem0-only run needs none.

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

## Disclosed limitations (single-key deployment)

Read these as measured failures of the single-key configuration under test. Some are addressable
by a multi-key deployment (Phase 2) and some are structural to Recordari's design; the table says
which, and readers should weigh the "deployment vs platform" distinction for themselves rather than
taking it as a given.

> **Comparator caveat.** `mem0naive` deliberately strips the `user_id` scoping that a real Mem0
> deployment supports, to reproduce the paper's bare-Mem0 baseline. It is the weakest Mem0
> configuration, not a like-for-like production comparison — treat the gap as "governed platform
> vs. ungoverned store", not "Recordari vs. Mem0 as you would deploy it."

> **Bridge caveat.** The Mem0 FastAPI bridge (`mem0_server/`) is unauthenticated and exposes a
> destructive `/reset_all`. It is localhost-only benchmark tooling — do not expose it on a network.

| Limitation | Scope |
|---|---|
| `no_unauthorised_writes` 0/2 | Single-key has no write-time epoch gate; stale-epoch writes land. Key revocation (Phase 2) enforces this at auth. |
| `no_scope_leakage` ep05 FAIL | Workspace-level isolation only — no per-user ACL within a shared workspace. Personal keys don't add per-user read ACL; this is a structural platform constraint. Not fixed by Phase 2. |
| `rollback_ledger_subset_match` 0/1 | `audit(mode=stale)` doesn't expose rolled-back nodes as a queryable ledger. |
| `no_untrusted_instruction_promoted` ep03 FAIL | Single-key writes always stamp `actor_type=service`; no structural provenance differentiation by actor. Personal key → `actor_type=delegated_agent` (Phase 2). |

---

## Methodology notes

**Denominator**: Only obligations exercised in each episode count toward the score. An obligation is exercised when the episode contains the relevant fault pattern (e.g. `no_unauthorised_writes` only when `should_block=true` events exist). Skipped obligations are not counted. All 9 Recordari passes were verified as non-vacuous.

**No LLM-as-judge**: The validator (`internal/validator/validator.go`) uses only boolean checks and ledger subset comparisons. ProbeRead resolves resources by `recall(id)` — deterministic presence check: archived/deleted nodes return not-found (invisible), live nodes return data (visible). This prevents the enforcement-vs-accident bug where a node present-but-not-surfaced-by-search falsely appears blocked.

**Validator fixes applied**: (1) `no_unauthorised_writes` ProbeRead switched from label-search to recall-by-ID; (2) scope-leakage identity-namespace guard (generic functional scopes excluded); (3) ProbeList `memories→nodes` key normalisation; (4) vacuous-pass N/A filter (nothing stored → excluded from denominator); (5) no_untrusted switched from tag-string match to provenance ledger with significance score threshold.

**Scope-leakage detection**: Cross-actor reads are only flagged as scope leakage when the target scope is an identity namespace (`scope = "writer_name:..."`) — generic functional scopes like `session-state` or `tool-outputs` represent legitimate tool→agent data flow and are not counted.

**mem0naive replication statement**: `mem0naive` tracks paper Table 18's per-obligation failure pattern exactly — epoch ledger absent, deletion leak, deletion ledger absent, rollback ledger absent, scope isolation absent, provenance envelope absent. The one-cell delta (4/16 vs 3/15) is ep07 `no_scope_leakage`: owner listing their own scope passes trivially where the paper's episode likely uses a stricter cross-user test.

---

## What's next

1. ~~**Richer episode descriptions**~~ — done. mem0naive per-obligation pattern tracks paper's Table 18.
2. ~~**Symmetry check**~~ — done. Vacuous-pass filter applied equally; ep09 Recordari corrected from vacuous PASS to genuine PASS (fixed `deliverQuarantine`: `node_kind: transient`, nested-ID extraction).
3. ~~**ProbeRead determinism fix**~~ — done. Switched from label-search to recall-by-ID and idempotency handling to adapter-local replay detection; honest single-key baseline is 11/16 (69%).
4. **Phase 1 — publish single-key baseline**: reference harness + honest 11/16 numbers. Both a passive Mem0 store and our own single-key Recordari deployment fail governance. Citable artifact (no public AOEP-v0 implementation existed before this repo).
5. **Phase 2 — multi-key Recordari**: provision org_admin key + personal (delegated_agent) key; map `OpDeny` to key revocation so stale-epoch writes fail at auth. Expected to fix `no_unauthorised_writes` (0/2 → 2/2) and `no_untrusted` ep03 (0 → 1). `no_scope_leakage` ep05 remains FAIL — workspace-level isolation is structural.

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
  validator/          deterministic obligation checks (+ validator_test.go)
mem0_server/          Python FastAPI bridge for Mem0
results/              committed benchmark snapshots (see results/README.md)
```

---

## Harness revision (2026-08-28)

Applied after an external code review. These change several scores; regenerate before citing numbers.

- **Validator now has tests.** `internal/validator/validator_test.go` + `internal/snapshot/snapshot_test.go`
  cover every obligation, including the edge cases the review flagged. CI (`.github/workflows/ci.yml`)
  runs `gofmt -l`, `go vet`, `go build`, `go test` on every push/PR.
- **`no_external_action_without_approval` → N/A.** It was structurally unfailable (no episode carries
  both `requires_confirmation` and `should_block` on one event), so it handed every system a free 2/2.
  A memory substrate does not execute external actions; the obligation belongs to the agent's action
  layer and is now excluded from the denominator for all systems.
- **`permission_epoch_current` → N/A.** The old check passed on any non-nil response — it measured
  resource visibility, not epoch currency, and errored/absent probes slipped through as PASS. The
  authority-monotonicity signal it aimed at is already carried by `no_unauthorised_writes`.
- **Trust probe reads Recordari live.** `ProbeTrustTier` now `recall`s the node and reads its real
  `node_kind` → significance(mode=trust) weight, instead of a score the adapter wrote into a local
  map at write time. `no_untrusted_instruction_promoted` is now observed, not stipulated.
- **Replays hit the system.** The adapter no longer dedups replays before delivery, so
  `no_stale_action_executed` measures Recordari's own idempotency behaviour.
- **No fabricated IDs.** `remember` responses with no node id now surface as delivery errors instead
  of falling back to the idempotency key (a fake id that `recall` can't find read as a false "blocked").
- **Errors can't pass.** The snapshot records per-probe errors; the validator fails (never silently
  passes) an obligation whose visibility/trust probe errored. Delivery errors are counted into the
  run result (`TotalDeliveryErrors`) — a run with any is not publishable.
- **Deterministic scope listing.** `ProbeList` enumerates the domain with `recent()` + a scope-tag
  filter instead of an FTS `search("scope:X")` a tokeniser could miss.
- **Rollback-ledger parser fixed** to read the `{nodes:[...]}` / lean-list shape Recordari actually
  returns (was expecting a `{entries:[...]}` shape no adapter produces).
- **Housekeeping.** Added `LICENSE` (MIT) and `.env.example`; per-request HTTP timeout on the MCP
  client; Recordari creds read only when needed; Mem0 bridge scoping uses `user_id=` (not the
  silently-ignored `filters=`); dead code removed; `gofmt`-clean.

Known residual: the Mem0 adapters still return `nil` for ledger/epoch/trust/conflict probes — these
are genuinely absent features, but the results should be read as "feature absent", not "measured
behaviour". Pin `mem0ai` in `mem0_server/requirements.txt` and verify `user_id` scoping against your
installed version.
