# gs-acm-utils

Offline tooling for OCM/ACM policy templates.

`acm-mock-resolver` resolves and validates `{{ ... }}` and `{{hub ... hub}}` policy templates against a
directory of mock Kubernetes objects (a *cohort*), with **no kubeconfig and no cluster**. It uses
[stolostron/go-template-utils](https://github.com/stolostron/go-template-utils) v7.3.0 as a library, so
`fromConfigMap`, `fromSecret`, `lookup` and the other template functions run through the real library code
against fake clients seeded from your files. By default a missing object, key or namespace is an error
(a CI gate must not pass on empty values).

## Install

From a release (linux/amd64, linux/arm64, darwin/arm64), with checksums in `sha256sums.txt`:

```
gh release download v0.1.0 --repo gscode1/gs-acm-utils
tar -xzf acm-mock-resolver_v0.1.0_darwin_arm64.tar.gz
```

From source (the repository is private, so Go must not use the public proxy for it and git needs access):

```
export GOPRIVATE=github.com/gscode1/*
go install github.com/gscode1/gs-acm-utils/cmd/acm-mock-resolver@latest
```

Or clone and `make build` (produces `bin/acm-mock-resolver`, version injected from the git tag).

## Quick start

```
acm-mock-resolver version

# check a cohort
acm-mock-resolver mockdata check --mock-data-dir testdata/cohorts/basic

# resolve one policy and print the result
acm-mock-resolver resolve --mock-data-dir testdata/cohorts/basic testdata/policies/policy-basic.yaml

# validate many files/directories (CI gate; exit code 0/1/2/3)
acm-mock-resolver validate --mock-data-dir testdata/cohorts/basic testdata/policies/
```

A cohort is a directory:

```
cohort/
├── objects/v1/ConfigMap/ns-a/my-config.yaml      # managed-cluster view
├── hub/objects/...                               # hub view, only for {{hub ... hub}}
├── hub/context.yaml                              # clusterName, clusterLabels
└── params.yaml                                   # extra template context
```

## Commands

```
acm-mock-resolver resolve  --mock-data-dir DIR [flags] [FILE|-]   # print the resolved YAML to stdout
acm-mock-resolver validate --mock-data-dir DIR [flags] PATH...    # resolve many inputs, report failures
acm-mock-resolver mockdata check --mock-data-dir DIR              # load + validate the cohort only
acm-mock-resolver version
```

- `resolve` reads one input (stdin when `FILE` is `-` or absent). A multi-document input is resolved
  document by document and the results are joined with `---`. Nothing is printed to stdout if any
  document fails.
- `validate` takes files and directories (recursive, `.yaml`/`.yml`, dotfiles skipped). The mock data is
  loaded once before any input is read. Each failing document is printed to stderr as
  `<path>[#<doc>]: <error>`; the summary `N passed, M failed` (counted in documents) goes to stdout.
- `mockdata check` prints `ok: <n> managed objects, <m> hub objects` or the load errors.

Common flags (`resolve`, `validate`): `--mock-data-dir` (required), `--hub-mode`
(`resolve`|`passthrough`|`forbid`, default `resolve`), `--cluster-name`, `--hub-namespace`,
`--object-namespace`, `--object-name`, `--params FILE` (YAML/JSON; keys must be exported identifiers such
as `Env`), `--strict` (default `true`; `--strict=false` mimics a live cluster), `--skip-policy-validation`,
`--disable fn1,fn2`, and `-v N` for library log verbosity.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | All inputs resolved. |
| 1 | One or more inputs failed to resolve (template error, missing mock data, leftover `{{hub`). |
| 2 | The mock-data directory failed to load or validate. |
| 3 | Usage error: bad or missing flags, unreadable or missing input, no input files. |

## Documentation

- [docs/mock-data-contract.md](docs/mock-data-contract.md): cohort layout, validation rules, Secret
  encoding, template context, strict semantics, error catalog E1-E15 with fixes (platform team).
- [docs/ci-usage.md](docs/ci-usage.md): using `validate` in CI, cohort-per-matrix examples, exit codes,
  `--strict` / `--hub-mode` guidance.
- [docs/performance.md](docs/performance.md): benchmark results and the per-policy budget (NFR-2).
- [docs/upstream-sync.md](docs/upstream-sync.md): which upstream code is ported (v7.3.0) and how to
  re-sync; the `internal/spike` library-contract test.
- [docs/spike-T0.md](docs/spike-T0.md): the feasibility spike behind the fake-client approach.
- [testdata/golden/README.md](testdata/golden/README.md): golden regression corpus ported from upstream.

## Development

```
make build test lint     # must be green before every merge
make bench               # NFR-2 benchmarks
make release             # on a clean tagged checkout: dist/*.tar.gz + sha256sums.txt
```

There is no CI service for this repository; the checks above run locally. Release: tag `vX.Y.Z`, run
`make release`, then `gh release create vX.Y.Z dist/* --title vX.Y.Z --notes ...`.

Licensed under Apache-2.0; see `LICENSE` and `NOTICE`.
