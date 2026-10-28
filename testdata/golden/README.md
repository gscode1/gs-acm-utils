# Golden regression corpus

Backward-compatibility corpus ported from upstream `stolostron/go-template-utils` **v7.3.0**
(`cmd/template-resolver/testdata/test_*`, driven by `cmd/template-resolver/client_test.go`), plus a few
cases of our own (`own_*`).

The test is `TestGolden` in `internal/cli/golden_test.go`. For every case directory it runs
`acm-mock-resolver resolve` in-process and compares the result.

```
go test ./internal/cli -run TestGolden            # check
go test ./internal/cli -run TestGolden -update    # regenerate output.yaml / error.txt
```

(Review the diff after `-update`. A new case needs only `input.yaml`: the exit code decides whether
`output.yaml` or `error.txt` is written.)

## Case layout

| File | Meaning |
|---|---|
| `input.yaml` | Input passed to `resolve`. |
| `output.yaml` | Expected stdout, byte for byte (exit 0). Same file as upstream's `output.yaml`. |
| `error.txt` | Expected error (exit 1, empty stdout). See "Error comparison". |
| `mock/` | The case's own cohort (`objects/...`). |
| `cohort` | Alternative to `mock/`: one line, path of a cohort relative to the case dir. |
| `args` | Extra CLI flags, whitespace separated (e.g. `--skip-policy-validation`, `--strict=false`). |

A case with neither `mock/` nor `cohort` uses the shared cohort `_cohorts/upstream-setup`, which is
upstream's `testdata/setup/*` resources (the objects upstream applies to envtest before running the
`test_*` cases), converted with `hack/fixture2cohort`.
Every case runs with `--object-namespace my-obj-namespace --object-name my-obj-name`, the values
upstream's test uses.

`_local` cases carry their own `mock/` cohort, generated from upstream's `local_resources.yaml`:

```
go run ./hack/fixture2cohort -out testdata/golden/<case>/mock <local_resources.yaml>
go run ./hack/fixture2cohort -out testdata/golden/_cohorts/upstream-setup <upstream testdata/setup/*>
go run ./hack/fixture2cohort -hub -out <cohort> <file>      # write to hub/objects instead
```

`fixture2cohort` writes `objects/<apiVersion>/<Kind>/<ns>/<name>.yaml` (cluster-scoped objects, i.e.
those without `metadata.namespace`, go to `.../<Kind>/<name>.yaml`) and adds the annotation
`gs-acm-utils.io/data-encoding: base64` to every Secret that has `data`, because Secrets copied from
kubectl or upstream save-resources output carry base64 values already.

## Error comparison

stderr is `error: <text>`. The text after the prefix is compared with `error.txt`:

- If `error.txt` starts with `template: tmpl:` (upstream convention), only the part matching
  `template: tmpl:([0-9]+:){1,2} .*` of our error is compared, exactly as upstream's test does.
- Otherwise the full error text must equal `error.txt` (trimmed). All `invalid-*` cases and the
  `own_e*` catalog cases are in this class, so the text is exact.

## Status of the 39 upstream `test_*` cases

| Result | Count | Where |
|---|---|---|
| Ported here (output or error text equal to upstream) | 24 | `testdata/golden/<name>` |
| Ported here, resolver half only (lint output stripped) | 5 | see below |
| `*_hub` cases ported earlier (T6), same shared cohort converted | 10 | `internal/resolve/testdata/upstream/`, `TestUpstreamHubFixtures`, `TestUpstreamHubFixtureErrors` |
| Skipped entirely | 0 | |

Ported here (24, `output.yaml`/`error.txt` byte-identical to upstream): `config-policy-directly`,
`config-policy-directly_local`, `invalid-objdef`, `invalid-objdef-missing`, `invalid-policy-spec`,
`invalid-policy-spec-missing`, `invalid-policy-templates`, `invalid-policy-templates-bad-entry`,
`invalid-policy-templates-missing`, `label-configmaps`, `label-configmaps_local`,
`label-match-configmaps`, `label-match-configmaps_local`, `linting_quote_valid_raw`,
`obj_variable_config`, `obj_variable_operator`, `object-templates-raw`, `operator-policy`,
`operator-policy-directly`, `policy-policy-templates-int`, `skip-policy-validation`, `skipobject`,
`skipobject_multi_arg`, `skipobject_non_bool`.

### Resolver half only (5)

The linting cases set `lint: true` in upstream's `config.yaml`. Upstream then prefixes its expectation
with the linter report (and compares a `report.sarif`). `acm-mock-resolver` has no lint command, so
for these cases only the resolver part is asserted; the expectation is upstream's file with the
`Found linting issues: ...` block removed:

| Case | Asserted |
|---|---|
| `linting_trailing_whitespace` | resolved output (upstream output minus lint report) |
| `linting_invalid_var` | `template: tmpl:10: bad character U+0024 '$'` |
| `linting_quote_errors_raw` | `template: tmpl:1: unterminated quoted string (in policy-templates at index 0)` |
| `linting_multiple_errors` | `failed to parse input to YAML: error converting YAML to JSON: yaml: line 28: did not find expected key` |
| `linting_unquoted_values` | `failed to parse input to YAML: error converting YAML to JSON: yaml: line 24: did not find expected key` |

(`linting_quote_valid_raw` has no violations, so its `output.yaml` is upstream's unchanged and is
counted in the 24.)

### Behaviours upstream asserts that this tool does not have (not tested, not applicable)

- `save_resources.yaml` / `save_hub_resources.yaml` (`--save-resources` output listing the cluster
  objects the templates read). Present in many upstream cases; there is no equivalent flag.
- `report.sarif` and the textual lint report (`--lint`).
- Live-cluster behaviour: upstream's non-`_local` cases read from an envtest API server populated by
  `testdata/setup`. Here that data is the shared `_cohorts/upstream-setup` cohort, so those cases are
  fully ported; only the "was fetched from the cluster vs locally" comment in `save_resources.yaml`
  (`# Resource was locally fetched`) is lost with the save-resources feature.

## Differences from upstream

- The `_local` cases are equivalent but not identical: upstream's `--local-resources` replaces the
  whole cluster with the file, ours uses the cohort tree built from the same file. Outputs are equal.
- Strict mode (`--strict`, default) turns upstream's silent empty values into errors. None of the
  ported upstream cases relies on a missing object or key, so none differ; the `own_e*`,
  `own_lookup-missing-*` and `own_missing-key-basic` cases cover the strict behaviour and the
  `--strict=false` counterpart.

## Our own cases (`own_*`)

| Case | Covers |
|---|---|
| `own_basic-policy`, `own_basic-config-policy`, `own_basic-multi-doc` | `testdata/cohorts/basic` end to end: ConfigMap, Secret `base64dec`, lookup get/list, ClusterClaim, hub `.ManagedClusterName` |
| `own_secret-kubectl-base64` | kubectl-style Secret converted by `fixture2cohort` (annotation), `fromSecret` raw and `\| base64dec` |
| `own_e1-namespace-dir-missing`, `own_e2-object-missing`, `own_e3-key-missing`, `own_e4-lookup-unsupported` | catalog messages E1-E4 through the CLI |
| `own_lookup-missing-strict`, `own_lookup-missing-lenient` | `lookup` of a missing object: strict error vs `--strict=false` empty map |
| `own_missing-key-basic` | missing key against the basic cohort |
