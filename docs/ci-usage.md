# Using acm-mock-resolver in CI

This page is for **teams that consume** the tool (policy repositories). This repository itself has no
CI; see `CLAUDE.md`.

`acm-mock-resolver validate` needs no kubeconfig, no network and no credentials, so it runs anywhere
a binary runs. Install it as described in the README (release tarball or `go install`).

## Basic gate

```
acm-mock-resolver validate --mock-data-dir cohorts/prod-eu policies/
```

Prints nothing but `N passed, M failed` on success; every failure is printed to stderr as
`<path>[#<doc>]: <error>`. The cohort is loaded once, then every document is resolved.

Check the cohorts themselves in the platform repository's gate:

```
acm-mock-resolver mockdata check --mock-data-dir cohorts/prod-eu
```

## Exit codes

| Code | Meaning | Typical cause |
|---|---|---|
| 0 | All inputs resolved | |
| 1 | One or more documents failed to resolve | template syntax error, missing mock object / key (E1-E4), leftover `{{hub` (E12-E14) |
| 2 | The cohort failed to load or validate | broken mock file (E5-E11, E15); nothing was processed |
| 3 | Usage error | missing `--mock-data-dir`, bad flag value, unreadable or missing input path, no input files |

Treat any non-zero code as a failed job. Code 2 means the policy under test was not judged at all (fix the
cohort); code 1 means the policy is wrong, or the cohort lacks data the policy needs. The catalog with fixes
is in `docs/mock-data-contract.md`.

## One cohort per cluster flavour (matrix)

A cohort describes one kind of target cluster (for example per environment, region or OpenShift minor
version). Validate the same policies against every cohort that they will be applied to:

```
cohorts/
  prod-eu/    objects/ hub/ params.yaml
  prod-us/    objects/ hub/ params.yaml
  dev/        objects/ hub/ params.yaml
policies/
```

### Any shell

```sh
set -eu
rc=0
for c in cohorts/*/; do
  echo "== $c"
  acm-mock-resolver validate --mock-data-dir "$c" policies/ || rc=1
done
exit $rc
```

### GitHub Actions (example for a consumer repository)

```yaml
jobs:
  validate:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        cohort: [prod-eu, prod-us, dev]
    steps:
      - uses: actions/checkout@v4
      - run: |
          gh release download v0.1.0 --repo gscode1/gs-acm-utils \
            --pattern 'acm-mock-resolver_v0.1.0_linux_amd64.tar.gz'
          tar -xzf acm-mock-resolver_*.tar.gz --strip-components=1 --wildcards '*/acm-mock-resolver'
        env:
          GH_TOKEN: ${{ secrets.GS_ACM_UTILS_TOKEN }}   # the repository is private
      - run: ./acm-mock-resolver validate --mock-data-dir cohorts/${{ matrix.cohort }} policies/
```

### GitLab CI

```yaml
validate:
  parallel:
    matrix:
      - COHORT: [prod-eu, prod-us, dev]
  script:
    - acm-mock-resolver validate --mock-data-dir cohorts/$COHORT policies/
```

(Provide the binary through your own image or a download step.)

## Flags worth knowing in CI

- `--strict` (default `true`): keep it on in gates. A missing ConfigMap, Secret, key or namespace directory
  fails instead of silently resolving to an empty value, which is what a real cluster would give you only
  after the policy was already applied. `--strict=false` mimics live-cluster leniency; use it only to
  reproduce a cluster's behaviour while debugging.
- `--hub-mode`:
  - `resolve` (default): two-pass resolution against `hub/objects/`. Use when the cohort has a hub tree.
    If a policy contains `{{hub` and the cohort has no `hub/objects/`, you get E12.
  - `passthrough`: hub templates are replaced by their inner expression text (a stub, not a simulation) with
    a warning on stderr. Use it for cohorts without hub data when you only want to check the managed-side
    templates and the syntax.
  - `forbid`: fail if any hub template is present (for repositories that must not use them).
- `--cluster-name`, `--hub-namespace`, `--object-namespace`, `--object-name`, `--params FILE`: supply the
  template context (see section 5 of the contract). Policies in a `validate` run share the values, so pass
  the ones your policies need.
- `--skip-policy-validation`: treat inputs that are not Policy / ConfigurationPolicy / OperatorPolicy as raw
  Go templates.
- `--disable fn1,fn2`: disable template functions.
- Multi-document files are validated document by document; failures are numbered `file.yaml#2`.

## Tips

- Run `mockdata check` first (exit code 2 is then reported once, with every cohort problem).
- Keep cohorts in version control next to the platform's cluster baselines; the paths in error messages
  (`objects/v1/ConfigMap/ns-a/my-config.yaml`) are exactly the files to fix.
- Do not put secrets in cohorts. Use made-up values; templates only need the shape of the data.
