# Performance Benchmarks and Memory Stability

This document defines the benchmark corpus and reporting procedure for RFC M5 Track A (`TW-M5-PERF-*`).

Goals (RFC beta targets, warm runs):

- Parse + diagnostics (typical files): p95 `< 50 ms`
- Full document format (typical files): p95 `< 100 ms`
- No unbounded memory growth across repeated LSP `open/change/close` cycles

## Benchmark Runner

Use the checked-in benchmark tool:

```bash
go run ./scripts/perf-report
```

It reports:

- parse + diagnostics latency (`syntax.Parse`) with p50/p95
- full document format latency (`format.Document`) with p50/p95
- LSP snapshot-store memory loop (`open/change/close`) with heap growth samples

The formatter benchmark is **format-only** on pre-parsed trees (warm), which is the closest match to the LSP formatting path.

## Corpus Sets (Required by RFC)

The benchmark runner always includes repository fixtures (`testdata/format/input`) so all sets exist even without an external corpus:

- `small`:
  - `001_top_level_grouping.thrift`
  - `002_comment_preservation.thrift`
  - `003_legacy_spellings_and_literals.thrift`
- `typical`:
  - `004_apache_thrifttest.thrift`
  - `005_apache_fb303.thrift`
- `large`:
  - `006_apache_cassandra.thrift`
- `malformed`:
  - fallback to a repo fixture if no external corpus is provided

When an external Thrift checkout is provided, the runner expands the sets:

```bash
go run ./scripts/perf-report --external-thrift-root /path/to/thrift
```

External corpus rules:

- Non-malformed files are sampled into:
  - `small`: files `< 4 KiB`
  - `typical`: files `>= 4 KiB` and `< 32 KiB`
  - `large`: files `>= 32 KiB`
- `malformed` is sourced from `test/audit/`:
  - `break*.thrift`
  - `warning.thrift`
  - `test.thrift`

The runner uses deterministic sorting and capped sample counts for reproducibility.

## Recommended Commands

Quick smoke (local development):

```bash
go run ./scripts/perf-report \
  --iterations 3 \
  --warmup 1 \
  --memory-iterations 50 \
  --memory-sample-every 10
```

Beta-signoff style run (local baseline + JSON artifact):

```bash
go run ./scripts/perf-report \
  --external-thrift-root /path/to/thrift \
  --iterations 20 \
  --warmup 3 \
  --memory-iterations 500 \
  --memory-sample-every 25 \
  --json .tmp/perf-report.json
```

Optional lower-noise memory samples (slower):

```bash
go run ./scripts/perf-report \
  --memory-free-os \
  --memory-iterations 300
```

## Measurement Rules (RFC Alignment)

When reporting numbers (release notes / beta sign-off):

- Record hardware + OS baseline:
  - CPU model (or machine type)
  - RAM size
  - OS version
  - Go version
- Report p50/p95 for:
  - parse + diagnostics
  - full document format
- Record memory loop growth metrics:
  - `heap_alloc` growth
  - `heap_inuse` growth
  - whether the run flagged `unbounded_growth_hint`
- Include corpus source path (if external corpus used)

## Notes on Interpretation

- `small`, `typical`, and `large` are benchmark buckets, not product limits.
- `malformed` parse timings help monitor recovery-path performance regressions.
- The memory loop uses full-document replacement changes in v1 to avoid brittle offset assumptions while still exercising parser/store churn.
