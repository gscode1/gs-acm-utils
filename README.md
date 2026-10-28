# gs-acm-utils

[![License](https://img.shields.io/github/license/gscode1/gs-acm-utils)](LICENSE)
[![Go version](https://img.shields.io/github/go-mod/go-version/gscode1/gs-acm-utils)](go.mod)
[![Go Reference](https://pkg.go.dev/badge/github.com/gscode1/gs-acm-utils.svg)](https://pkg.go.dev/github.com/gscode1/gs-acm-utils)
[![Release](https://img.shields.io/github/v/release/gscode1/gs-acm-utils)](https://github.com/gscode1/gs-acm-utils/releases)

`acm-mock-resolver` validates and renders Open Cluster Management / Red Hat ACM policy templates
(`{{ ... }}` and `{{hub ... hub}}`) **offline**, against a directory of mock Kubernetes objects instead
of a live cluster. It needs no kubeconfig and no network, so policy authors can check templates on a
laptop and in CI before anything is applied. It is built on
[stolostron/go-template-utils](https://github.com/stolostron/go-template-utils) v7 and runs the real
library code against fake clients seeded from your files.

This is an independent project. It is not affiliated with, endorsed by, or supported by Red Hat or the
stolostron / Open Cluster Management projects.

## Features

- Cluster template functions answered from mock data: `fromConfigMap`, `fromSecret`, `lookup` (get,
  list and label selectors), `copyConfigMapData`, `copySecretData`, `fromClusterClaim`, node functions.
- Two-pass hub resolution: `{{hub ... hub}}` against a hub view, then `{{ ... }}` against the managed view.
- Strict mode (default): a missing object, key or namespace fails instead of resolving to an empty value.
- Structured, actionable errors (catalog E1-E15 in the [mock-data contract](docs/mock-data-contract.md)).
- CI-friendly exit codes and a `validate` command for many files and directories.
- Fast: about 10 ms for a whole run on a small cohort; a 500-file cohort loads in about 22 ms and a
  20-call policy resolves in about 0.3 ms (see [docs/performance.md](docs/performance.md)).

## Install

Go (requires the Go version in `go.mod`):

```
go install github.com/gscode1/gs-acm-utils/cmd/acm-mock-resolver@latest
```

Release binaries (linux/amd64, linux/arm64, darwin/arm64) are on the
[Releases](https://github.com/gscode1/gs-acm-utils/releases) page. Verify the download against
`sha256sums.txt` from the same release:

```
sha256sum -c --ignore-missing sha256sums.txt      # macOS: shasum -a 256 -c --ignore-missing sha256sums.txt
tar -xzf acm-mock-resolver_v0.1.0_linux_amd64.tar.gz
```

From source:

```
git clone https://github.com/gscode1/gs-acm-utils.git && cd gs-acm-utils
make build        # produces bin/acm-mock-resolver
```

## Quick start

A cohort is a directory of mock objects. The one used here is `testdata/cohorts/basic`:

```
testdata/cohorts/basic/
├── objects/
│   ├── v1/ConfigMap/ns-a/my-config.yaml
│   ├── v1/ConfigMap/ns-b/other-config.yaml
│   ├── v1/Secret/ns-a/db-creds.yaml
│   └── cluster.open-cluster-management.io/v1alpha1/ClusterClaim/id.k8s.io.yaml
└── hub/
    ├── context.yaml                                  # clusterName: local-cluster
    └── objects/...                                   # ConfigMap and ManagedCluster for {{hub ... hub}}
```

The policy `testdata/policies/policy-basic.yaml` contains these templates:

```yaml
data:
  color: '{{ fromConfigMap "ns-a" "my-config" "color" }}'
  password: '{{ fromSecret "ns-a" "db-creds" "password" | base64dec }}'
  size: '{{ (lookup "v1" "ConfigMap" "ns-a" "my-config").data.size }}'
  cluster: '{{hub .ManagedClusterName hub}}'
```

Check the cohort, then resolve the policy (run with `KUBECONFIG=/nonexistent` to see that no cluster is used):

```
$ export KUBECONFIG=/nonexistent
$ acm-mock-resolver mockdata check --mock-data-dir testdata/cohorts/basic
ok: 4 managed objects, 2 hub objects

$ acm-mock-resolver resolve --mock-data-dir testdata/cohorts/basic testdata/policies/policy-basic.yaml
apiVersion: policy.open-cluster-management.io/v1
kind: Policy
metadata:
  name: policy-basic
  namespace: policies
spec:
  disabled: false
  policy-templates:
    - objectDefinition:
        apiVersion: policy.open-cluster-management.io/v1
        kind: ConfigurationPolicy
        metadata:
          name: basic-config
        spec:
          object-templates:
            - complianceType: musthave
              objectDefinition:
                apiVersion: v1
                data:
                  cluster: local-cluster
                  color: blue
                  password: s3cr3t-plaintext
                  size: "3"
                kind: ConfigMap
                metadata:
                  name: app-config
                  namespace: ns-a
          remediationAction: inform
          severity: low
  remediationAction: inform
```

Validate whole directories (exit code 0, 1, 2 or 3):

```
$ acm-mock-resolver validate --mock-data-dir testdata/cohorts/basic testdata/policies/
4 passed, 0 failed

$ acm-mock-resolver validate --mock-data-dir testdata/cohorts/basic testdata/policies-failing/
testdata/policies-failing/missing-key.yaml: mock data: key "nope" not found in objects/v1/ConfigMap/ns-a/my-config.yaml (data has: color, size)
testdata/policies-failing/multi-doc-mixed.yaml#2: mock data: namespace directory missing: objects/v1/ConfigMap/ns-missing/ (referenced by fromConfigMap "ns-missing" "cm" "k")
2 passed, 2 failed
```

## Mock data layout

```
<cohort>/                                   # --mock-data-dir
├── objects/<apiVersion path>/<Kind>/<namespace>/<name>.yaml   # managed-cluster view ({{ ... }})
├── objects/<apiVersion path>/<Kind>/<name>.yaml               # cluster-scoped Kind
├── hub/objects/...                         # optional hub view ({{hub ... hub}})
├── hub/context.yaml                        # optional: clusterName, clusterLabels
└── params.yaml                             # optional: extra template context keys
```

`<apiVersion path>` is `v1` for the core group and `<group>/<version>` otherwise. One object per file;
the path must match the object's `apiVersion`, `kind`, namespace and name. Full rules, Secret encoding,
the template context and the error catalog are in [docs/mock-data-contract.md](docs/mock-data-contract.md).

## Commands and flags

| Command | Purpose |
|---|---|
| `resolve [FILE\|-]` | Resolve one input (stdin when absent) and print the YAML. Multi-document input is joined with `---`; nothing is printed if any document fails. |
| `validate PATH...` | Resolve files and directories (recursive, `.yaml`/`.yml`); failures go to stderr, `N passed, M failed` to stdout. |
| `mockdata check` | Load and validate the cohort only. |
| `version` | Print the version. |

Flags of `resolve` and `validate` (`mockdata check` takes `--mock-data-dir`):

| Flag | Default | Description |
|---|---|---|
| `--mock-data-dir DIR` | required | Cohort directory. |
| `--hub-mode` | `resolve` | `resolve`, `passthrough` (replace hub templates by their inner text, with a warning) or `forbid`. |
| `--cluster-name` | `hub/context.yaml` | Value of `.ManagedClusterName`. |
| `--hub-namespace` | the Policy's namespace | Namespace for hub template lookups. |
| `--object-namespace` | empty | Value of `.ObjectNamespace`. |
| `--object-name` | empty | Value of `.ObjectName`. |
| `--params FILE` | none | YAML/JSON with extra template context keys. |
| `--strict` | `true` | Fail on missing objects and keys; `--strict=false` mimics a live cluster. |
| `--skip-policy-validation` | `false` | Treat input that is not a policy kind as a raw Go template. |
| `--disable fn1,fn2` | none | Template functions to disable. |
| `-v N` | 0 | Library (klog) verbosity on stderr. |

## Exit codes

| Code | Meaning |
|---|---|
| 0 | All inputs resolved. |
| 1 | One or more inputs failed to resolve (template error, missing mock data, leftover `{{hub`). |
| 2 | The mock-data directory failed to load or validate. |
| 3 | Usage error: bad or missing flags, unreadable or missing input, no input files. |

## CI

```yaml
- run: acm-mock-resolver validate --mock-data-dir cohorts/prod-eu policies/
```

Any non-zero exit fails the job. A matrix over cohorts, GitLab CI and shell examples are in
[docs/ci-usage.md](docs/ci-usage.md).

## Limitations

- No `protect`/encryption: `protect` is always disabled (error E14) because it needs real hub keys.
- Not a linter: it checks that templates resolve against the mock data, not policy best practices.
- No live-cluster mode: results are only as accurate as your mock data. `--strict=false` only mimics
  the lenient behaviour of a live cluster.
- `--params` and `params.yaml` keys must be exported identifiers (start with an uppercase letter, e.g. `Env`).
- Strict `lookup`: with a name, a missing object is an error; a list or label-selector lookup that
  matches nothing returns an empty list and is not an error.

## Development

```
make build     # bin/acm-mock-resolver
make test      # go test -race ./...
make lint      # golangci-lint run ./...
make bench     # benchmarks
make release   # on a clean tagged checkout: dist/*.tar.gz + sha256sums.txt
```

There is no CI service for this repository: tests and lint run locally, and `make build test lint`
must be green before every merge.

Layout (everything is under `internal/`; there is no public Go API):

- `internal/mockdata`: loads and validates cohorts (layout, scope, Secret encoding).
- `internal/fakecluster`: fake Kubernetes clients and discovery seeded from a cohort.
- `internal/resolve`: resolution engine, policy walkers, hub pass, strict functions.
- `internal/input`: expands input paths and splits multi-document YAML.
- `internal/cli`: cobra commands and exit codes.
- `internal/spike`: library-contract test, run first when bumping go-template-utils.

See [CLAUDE.md](CLAUDE.md) for conventions and [docs/upstream-sync.md](docs/upstream-sync.md) for how
the pinned upstream version (v7.3.0) is kept in sync. Other docs:
[performance](docs/performance.md), [spike notes](docs/spike-T0.md).

## License

Apache-2.0, see [LICENSE](LICENSE). [NOTICE](NOTICE) records that parts of the code are ported from
[stolostron/go-template-utils](https://github.com/stolostron/go-template-utils) (Apache-2.0).
