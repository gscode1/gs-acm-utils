# Upstream sync

`acm-mock-resolver` uses `github.com/stolostron/go-template-utils/v7` **as a library, pinned to v7.3.0**
(`go.mod`) and never modifies it. A small part of the upstream CLI is ported because it is `package main`
code that cannot be imported.

## What is ported

All from `cmd/template-resolver/utils/resolver_utils.go` at tag **v7.3.0** (Apache-2.0; attribution
headers are in the Go files and `NOTICE`).

| Upstream function | Our location |
|---|---|
| `ProcessTemplate` (kind dispatch Policy / ConfigurationPolicy / OperatorPolicy / raw) | `internal/resolve/policy.go`, `document.go` |
| `processPolicyTemplate`, `processConfigPolicyTemplate`, `processObjectTemplates` | `internal/resolve/policy.go` |
| `processOperatorPolicyTemplates` | `internal/resolve/policy.go` |
| `processObjTemplatesRaw`, `processRawGoTemplate`, `resolveManagedTemplate` | `internal/resolve/policy.go` |
| Hub section of `ProcessTemplate` (namespace rules, `customSA`, `PolicyMetadata`, `ManagedClusterName`/`Labels`, `ClusterScopedAllowList`) | `internal/resolve/hub.go` |
| `getNamespace`, `getOrList`, `lookup`, `fromConfigMap`/`fromSecret` semantics (`generic_lookup_func.go`, `k8sresource_funcs.go`) re-implemented strictly | `internal/resolve/strictfuncs.go` |

Deliberate differences: every `ResolveTemplate` call gets freshly built `ResolveOptions` (the library
mutates them), clients are fake dynamic + discovery clients seeded from the cohort
(`internal/fakecluster`, recipe in `docs/spike-T0.md`), the missing-object/key behaviour is strict, `protect`
is disabled, and upstream HEAD's fix for `--skip-policy-validation` (return the real error from
`processRawGoTemplate`) is included.

The library itself (`pkg/templates`) is used unmodified through `templates.NewResolverWithClients`.

## Library-contract test

`internal/spike` (kept on purpose, originally the T0 feasibility spike) is a contract test of the
library API we rely on: fake dynamic/discovery clients, `ErrMissingAPIResource` for unknown GVKs,
`CustomFunctions` overriding built-ins, `LookupNamespace`/`ClusterScopedAllowList`, two-pass hub resolution
and concurrent resolvers over shared fakes. When bumping the pinned version, run it first
(`go test ./internal/spike/`): a failure there means the library behaviour changed under us. Findings are
written up in `docs/spike-T0.md`.

## How to re-sync with a new upstream release

1. Read the upstream diff between v7.3.0 and the new tag for the files above:
   `git diff v7.3.0..vX.Y.Z -- cmd/template-resolver/utils/resolver_utils.go pkg/templates/`
   (in a clone of `stolostron/go-template-utils`).
2. Bump the dependency: `go get github.com/stolostron/go-template-utils/v7@vX.Y.Z && make tidy`.
   Check `go.mod` still pins the `sigs.k8s.io/controller-runtime` version upstream uses (an older
   resolution does not compile).
3. Run `go test ./internal/spike/` and `make test`; fix library-contract breakages first.
4. Port changed upstream logic into `internal/resolve/policy.go` / `hub.go` / `strictfuncs.go` and update
   the "at tag" line in each file header, `NOTICE` and this document.
5. Port new or changed upstream fixtures (`cmd/template-resolver/testdata/test_*`):
   - hub cases: add to `internal/resolve/testdata/upstream/` and the list in `TestUpstreamHubFixtures`;
   - other cases: add a directory to `testdata/golden/` (see `testdata/golden/README.md`; use
     `go run ./hack/fixture2cohort` for `local_resources.yaml` or changed `testdata/setup` files), then
     `go test ./internal/cli -run TestGolden`.
   Outputs should equal upstream's byte for byte; document intentional differences in the golden README.
6. `make build test lint bench`; update the pinned version in README and CLAUDE.md.
