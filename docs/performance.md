# Performance (PRD NFR-2: < 1 s per policy)

## How it is measured

`internal/resolve/bench_test.go`, run with `make bench`
(`go test -run '^$' -bench . -benchmem -benchtime 200x ./internal/resolve`).

The corpus is generated into `b.TempDir()`: **500 files = 50 namespaces x 10 Kinds** (ConfigMap, Secret,
ServiceAccount, Service, Deployment, Role, RoleBinding, Ingress and two custom Kinds,
`example.com/v1` Widget and Gadget), one object per Kind and namespace.

| Benchmark | What one iteration does |
|---|---|
| `BenchmarkColdLoad` | `mockdata.Load` (read, parse, validate 500 files) + `fakecluster.New` + `resolve.NewEngine`: everything a CLI run does before its first document. |
| `BenchmarkResolvePolicy` | `Engine.ResolveDocument` of a `ConfigurationPolicy` with 20 template calls (`fromConfigMap`, `fromSecret \| base64dec`, `lookup` get, a `lookup` list, strict mode on) against the already-loaded cohort. |

Both report the Go default metrics plus `p50-ms` and `p95-ms`, computed from the per-iteration
durations of the 200 iterations.

## Results

Machine: Apple M5 Pro (18 cores), macOS 27.0 (darwin/arm64), Go 1.27.1, 2026-10-28.

```
BenchmarkColdLoad-18         200  22069913 ns/op   22.10 p50-ms  23.35 p95-ms  12269286 B/op  139187 allocs/op
BenchmarkResolvePolicy-18    200    367726 ns/op   0.2930 p50-ms 0.7122 p95-ms    625112 B/op    3635 allocs/op
```

| | p50 | p95 |
|---|---|---|
| Cold load of the 500-file cohort | 22 ms | 23 ms |
| Resolve one 20-call policy | 0.29 ms | 0.71 ms |

A whole process run on the small `testdata/cohorts/basic` cohort takes about 10 ms wall-clock including
process start.

## Budget

Per policy, in the worst case of a CI job that starts one process per policy against a 500-file cohort:

    process start (~10 ms) + cold load (p95 23 ms) + resolve (p95 0.71 ms)  ~=  35 ms

That is roughly 30x below the PRD NFR-2 limit of 1 s per policy. When many policies are validated with one
`validate` call, the cohort is loaded once and each additional policy costs well under 1 ms.

Budget adopted:

- **Per-policy budget: 1 s** (the PRD limit), measured headroom about 30x on a cohort of 500 files.
- **Regression guard**: `TestColdLoadCeiling` (skipped with `-short`) fails if loading the 500-file cohort
  exceeds **2 s**, ~90x the measured p95. It is deliberately coarse so that slow shared machines do not
  produce false failures; compare `make bench` runs for finer regressions.
- Cohorts are expected to be hundreds, not tens of thousands, of files. Cold load scales roughly linearly
  with file count (about 44 us per file here); a 10,000-file cohort would be expected to load in about a second.

To refresh the numbers, run `make bench` on the target machine and update the table above.
