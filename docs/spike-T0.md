# Spike T0: fake clients + go-template-utils v7.3.0

Verdict: **hub-mode=resolve is feasible: YES.** The whole approach (fake dynamic + fake discovery
through `templates.NewResolverWithClients`) works. Tests: `internal/spike/spike_test.go`
(`go test -race ./internal/spike/` passes).

## What was proven
- Managed pass: `fromConfigMap`, `fromSecret | base64dec` (Secret stored base64, as on a cluster),
  `lookup` get / list / label selector (`"color=yellow"`), `copyConfigMapData`, `fromClusterClaim`,
  cluster-scoped `ManagedCluster` lookup, `GetUsedResources()`.
- Unknown GVK: `errors.Is(err, templates.ErrMissingAPIResource)` is true.
- `ResolveOptions.CustomFunctions["fromConfigMap"]` overrides the built-in; a typed error returned
  by a custom func is preserved (`errors.Is` works through the template exec error).
- `LookupNamespace` denies cluster-scoped lookups unless allow-listed via `ClusterScopedAllowList`;
  it relies on discovery reporting `Namespaced` correctly (it does with the recipe below).
- Two-pass: upstream fixtures `test_config-policy-directly_hub` (hub-only) and
  `test_label-configmaps_hub` (hub pass on the Policy, then managed pass on `object-templates-raw`)
  both reproduce upstream `output.yaml` exactly. Hub config: `AdditionalIndentation: 8`,
  `StartDelim "{{hub"`, `StopDelim "hub}}"`, ctx `{ManagedClusterName, ManagedClusterLabels,
  PolicyMetadata}`, options `LookupNamespace` + `ClusterScopedAllowList{cluster.open-cluster-management.io,
  ManagedCluster, <name>}`.
- 8 concurrent resolvers sharing one pair of fakes: race-free under `-race`.
- No `WithWatch` panic with client-go v0.36.5 (the fake dynamic client is used only for Get/List).

## Recipe (reuse in `internal/fakecluster`)
1. Discovery: `&discfake.FakeDiscovery{Fake: &clienttesting.Fake{}}`, then set `disco.Resources` to
   one `*metav1.APIResourceList` per GroupVersion, each entry
   `APIResource{Name: plural, Kind, Namespaced, Verbs}`. The ONLY discovery call the library makes is
   `ServerResourcesForGroupVersion(groupVersion)` (kubernetes-dependency-watches `client/cache.go:282`);
   an absent GroupVersion yields the "not installed" path -> `ErrMissingAPIResource`. Core is `"v1"`.
2. Dynamic: `dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToListKind, objs...)`
   with an **empty** scheme, `*unstructured.Unstructured` seed objects, and
   `gvrToListKind[gv.WithResource(plural)] = Kind+"List"` for **every** advertised resource
   (including ones with zero objects, else List panics).
3. `templates.NewResolverWithClients(dyn, disco, cfg)`. No scheme registration, no kubeconfig.

## Observations / deviations from plan section 3
- go.mod must pin `sigs.k8s.io/controller-runtime v0.24.1` (upstream's version). With only
  client-go v0.36.5 required, resolution picks controller-runtime v0.23.3, which does not compile.
- `ResolveTemplate` mutates the passed `*ResolveOptions` (appends to `DenylistFunctions`), which
  confirms the "fresh options per call" design rule.
- In JSON-input mode a template yielding a map (`copyConfigMapData`) is parsed back into a JSON map;
  a `lookup` of a missing object renders `.data` as `<no value>`.
- `lookup` swallows NotFound and a missing ConfigMap key gives `""` (as plan section 3 states), so
  strict wrappers are needed.
- A non-`Policy` kind (ConfigurationPolicy) in the hub pass needs an explicit hub namespace (upstream rule).
