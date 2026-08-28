# Results

This directory holds committed, reproducible benchmark output.

The harness writes `<system>_<timestamp>.json` per run; those ad-hoc files are gitignored.
To publish a canonical result, run the full suite and commit a stable copy, e.g.:

```sh
go run ./cmd/harness -system all -out ./results
cp results/recordari_<timestamp>.json results/recordari_latest.json
cp results/mem0paper_<timestamp>.json  results/mem0paper_latest.json
git add results/*_latest.json
```

Each result JSON records, per episode, the scored obligations, the N/A (skipped) obligations
with reasons, and any `DeliveryErrors`. A run with a non-zero `TotalDeliveryErrors` is not
publishable — some writes never reached the system under test, so "resource absent" scores are
unreliable.
