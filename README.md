# gs-acm-utils

Offline tooling for OCM/ACM policy templates.

`acm-mock-resolver` resolves and validates `{{ ... }}` and `{{hub ... hub}}` policy
templates against a directory of mock Kubernetes objects, with no kubeconfig or
cluster required. It consumes
[stolostron/go-template-utils](https://github.com/stolostron/go-template-utils)
as a library.

Status: under construction. See `docs/` as it grows.

```
make build
bin/acm-mock-resolver version
```

## Usage

```
acm-mock-resolver resolve  --mock-data-dir DIR [flags] [FILE|-]   # print the resolved YAML to stdout
acm-mock-resolver validate --mock-data-dir DIR [flags] PATH...    # resolve many inputs, report failures
acm-mock-resolver mockdata check --mock-data-dir DIR              # load + validate the cohort only
```

- `resolve` reads one input (stdin when `FILE` is `-` or absent). A multi-document input is
  resolved document by document and the results are joined with `---`. Nothing is printed to
  stdout if any document fails.
- `validate` takes files and directories (recursive, `.yaml`/`.yml`, dotfiles skipped). The mock
  data is loaded once before any input is read. Each failing document is printed to stderr as
  `<path>[#<doc>]: <error>` (`#<doc>` is 1-based and only shown for multi-document files); the
  summary `N passed, M failed` (counted in documents) goes to stdout.
- `mockdata check` prints `ok: <n> managed objects, <m> hub objects` or the load errors.

Common flags (`resolve`, `validate`): `--mock-data-dir` (required), `--hub-mode`
(`resolve`|`passthrough`|`forbid`, default `resolve`), `--cluster-name`, `--hub-namespace`,
`--object-namespace`, `--object-name`, `--params FILE` (YAML/JSON; keys must be exported
identifiers such as `Env`), `--strict` (default `true`; `--strict=false` mimics a live cluster),
`--skip-policy-validation`, `--disable fn1,fn2`, and `-v N` for library log verbosity.

```
acm-mock-resolver validate --mock-data-dir testdata/cohorts/basic testdata/policies/
```

### Exit codes

| Code | Meaning |
|---|---|
| 0 | All inputs resolved. |
| 1 | One or more inputs failed to resolve (template error, missing mock data, leftover `{{hub`). |
| 2 | The mock-data directory failed to load or validate. |
| 3 | Usage error: bad or missing flags, unreadable or missing input, no input files. |

Licensed under Apache-2.0; see `LICENSE` and `NOTICE`.
