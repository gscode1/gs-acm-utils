# Mock-data contract (for the platform team)

A **cohort** is a directory of Kubernetes objects that stands in for a cluster (or a group of
clusters that look alike). `acm-mock-resolver` reads it with `--mock-data-dir DIR` and answers
`fromConfigMap`, `fromSecret`, `lookup`, `copyConfigMapData`, `copySecretData`, `fromClusterClaim`,
node functions and label-selector lists from it. No kubeconfig and no network are used.

The same context can instead be written as one file and passed with `--mock-data-file FILE`; see
[section 7](#7-consolidated-file-format). Everything below describes the directory format unless stated.

Check a cohort at any time (this is what you run before committing changes to it):

```
acm-mock-resolver mockdata check --mock-data-dir path/to/cohort
# ok: 4 managed objects, 2 hub objects
```

## 1. Layout

```
<cohort>/                                   # passed as --mock-data-dir
├── objects/                                # managed-cluster view: answers {{ ... }} templates
│   └── <apiVersion path>/<Kind>/<namespace>/<name>.yaml     # namespaced Kind
│   └── <apiVersion path>/<Kind>/<name>.yaml                 # cluster-scoped Kind
├── hub/                                    # optional; needed only if policies contain {{hub ... hub}}
│   ├── objects/...                         # hub-cluster view, same layout: answers {{hub ... hub}}
│   └── context.yaml                        # optional hub defaults (clusterName, clusterLabels)
└── params.yaml                             # optional default values for extra template context keys
```

`<apiVersion path>` is `v1` for the core group and `<group>/<version>` (two directory levels) for
everything else:

```
objects/v1/ConfigMap/ns-a/my-config.yaml
objects/v1/Namespace/ns-a.yaml                                          # cluster-scoped: no namespace dir
objects/apps/v1/Deployment/ns-a/web.yaml
objects/cluster.open-cluster-management.io/v1alpha1/ClusterClaim/id.k8s.io.yaml
hub/objects/cluster.open-cluster-management.io/v1/ManagedCluster/local-cluster.yaml
```

Rules of thumb:

- `objects/` and `hub/objects/` are two independent views of the same layout. A `{{ ... }}` template
  only sees `objects/`; a `{{hub ... hub}}` template only sees `hub/objects/`.
- One object per file. The file name (without extension) is `metadata.name`.
- For each namespace directory the tool also synthesizes a `v1/Namespace` object, so
  `lookup "v1" "Namespace" "" "ns-a"` works without a file. An explicit file wins.
- `hub/context.yaml` (optional):

  ```yaml
  clusterName: local-cluster        # default for .ManagedClusterName (flag --cluster-name wins)
  clusterLabels: {env: prod}        # used only if hub/objects has no ManagedCluster named clusterName
  ```
- `params.yaml` (optional): a map of extra template context keys, see section 5.

## 2. File rules and validation (checked at load time)

Every file under `objects/` and `hub/objects/` must satisfy all of these, otherwise the whole cohort is
rejected (exit code 2). **All** problems are collected and printed together, not just the first.

1. The file parses as YAML (`.yaml`, `.yml`) or JSON (`.json`), parsed with `sigs.k8s.io/yaml`, and holds
   exactly one object.
2. `apiVersion`, `kind` and `metadata.name` are present. `metadata.name` and `metadata.namespace` must be
   strings (see the YAML tip below).
3. Path and body agree: the `apiVersion` path, the `Kind` directory, the namespace directory (or its
   absence for cluster-scoped Kinds) and the file name stem equal `apiVersion`, `kind`,
   `metadata.namespace` and `metadata.name`.
4. Scope is consistent. A Kind is either namespaced or cluster-scoped. For well-known Kinds the tool
   knows the real scope (ConfigMap, Secret, ServiceAccount, Service, Pod, Deployment, StatefulSet,
   DaemonSet, Job, CronJob, Ingress, NetworkPolicy, Role, RoleBinding, Policy, ConfigurationPolicy,
   OperatorPolicy, Placement ... are namespaced; Namespace, Node, PersistentVolume, ClusterRole,
   ClusterRoleBinding, StorageClass, CustomResourceDefinition, ManagedCluster, ClusterClaim ...
   are cluster-scoped). For any other Kind, the scope is taken from the layout and **all** files of that
   Kind must agree.
5. No two files define the same object (same apiVersion, Kind, namespace and name), e.g. `x.yaml` and
   `x.json`.
6. No dotfiles or dot-directories, no broken symlinks, no symlinks that leave the cohort root, no
   other file extensions.
7. `objects/` exists and contains at least one file.
8. Secrets obey the encoding rules of section 3.
9. Objects of three well-known kinds may only have their standard top-level fields, so a typo such as
   `dta:` is an error instead of being silently ignored: `v1/ConfigMap` allows `apiVersion`, `kind`,
   `metadata`, `data`, `binaryData`, `immutable`; `v1/Secret` allows `apiVersion`, `kind`, `metadata`,
   `data`, `stringData`, `type`, `immutable`; `v1/Namespace` allows `apiVersion`, `kind`, `metadata`,
   `spec`, `status`. Every other kind is free-form.
10. Keys of `params.yaml` start with an uppercase letter (they become `.Key` in templates).

**YAML tip:** YAML 1.1 reads unquoted `no`, `yes`, `on`, `off` and plain numbers as booleans and numbers, not
strings. Quote such values: `namespace: "no"`, `name: "123"`. An unquoted one fails with
`metadata.namespace must be a string, got bool false; quote the value`.

## 3. Secrets

Templates must behave exactly as on a live cluster: `{{ fromSecret "ns" "name" "key" }}` returns the
**base64** value and `| base64dec` gives the plaintext. To make mock files easy to write, plaintext is
the default:

| In the file | What the tool does |
|---|---|
| `stringData:` (plaintext) | base64-encodes into `data`, drops `stringData` |
| `data:` (plaintext, **default**) | base64-encodes at load |
| `data:` + annotation `gs-acm-utils.io/data-encoding: base64` | validates each value as strict base64 and keeps it as is |
| key in both `data` and `stringData` | `stringData` wins (Kubernetes semantics) |

```yaml
# objects/v1/Secret/ns-a/db-creds.yaml
apiVersion: v1
kind: Secret
metadata: {name: db-creds, namespace: ns-a}
stringData:
  password: s3cr3t          # fromSecret -> czNjcjN0 ; fromSecret | base64dec -> s3cr3t
```

**Files copied from `kubectl get secret -o yaml`** already hold base64 values. Add the annotation:

```yaml
metadata:
  name: db-creds
  namespace: ns-a
  annotations:
    gs-acm-utils.io/data-encoding: base64
data:
  password: czNjcjN0
```

(`go run ./hack/fixture2cohort` does this when it converts a multi-document resources file.)

**kubectl guard.** If a Secret has no annotation, has `metadata.resourceVersion`, `metadata.uid` or
`metadata.managedFields` (it looks like `kubectl get` output) *and* every `data` value is valid base64,
the load fails with E10 rather than silently double-encoding it.

## 4. Strict semantics (default `--strict=true`)

The library returns an empty string or an empty object where a live cluster has no data. A CI gate
must not pass on that, so by default the tool fails instead. `--strict=false` reproduces the lenient
live-cluster behaviour.

| Function | Live cluster | Strict (default) |
|---|---|---|
| `fromConfigMap ns name key` | missing object: error; missing key: `""` | missing namespace directory, object or key: error (E1, E2, E3) |
| `fromSecret ns name key` | same; returns base64 | same errors; returns base64 |
| `lookup av kind ns name` with a name | not found: empty map | not found: error (E1/E2) |
| `lookup av kind ns ""` (list) or with a label selector | list | list; an **empty list is not an error** |
| `copyConfigMapData`, `copySecretData`, `fromClusterClaim`, `lookupClusterClaim`, node functions | native | native (a missing object already errors) |
| any function on an apiVersion/Kind with no files | error from discovery | E4 |
| `protect` | encrypts | always disabled, E14 |

Strict functions honour `LookupNamespace` in the hub pass exactly like the library.

### Disabling managed templates

A `ConfigurationPolicy` annotated `policy.open-cluster-management.io/disable-templates: "true"`
(`metadata.annotations`) is not evaluated by the config-policy controller, so `{{ ... }}` in it is literal
(for example a `go-template` argument inside a `script`). The tool mirrors that, standalone or nested in a
`Policy`'s `spec.policy-templates[].objectDefinition`: the object is returned unchanged, with no managed
template evaluation and no strict mock-data lookups. Only the exact string `"true"` counts (`"True"`,
`"yes"`, `"false"` or a missing annotation evaluate as usual). Hub templates (`{{hub ... hub}}`) are
still resolved, as the hub propagator does that regardless of the annotation.

## 5. Template context

| Key | Pass | Source |
|---|---|---|
| `.ObjectNamespace`, `.ObjectName` | managed | `--object-namespace`, `--object-name` (empty by default) |
| `.ManagedClusterName` | hub | `--cluster-name`, else `hub/context.yaml` `clusterName` |
| `.ManagedClusterLabels` | hub | labels of the `ManagedCluster` named `.ManagedClusterName` in `hub/objects/.../ManagedCluster/`, else `hub/context.yaml` `clusterLabels`, else empty |
| `.PolicyMetadata` | hub | derived from the input Policy (`name`, `namespace`, `labels`, `annotations`), as upstream; only set when the input kind is `Policy` |
| extra keys, e.g. `.Env` | both | `params.yaml` in the cohort, overridden by `--params FILE`; flag values win over file values |

Extra keys must be exported identifiers (start with an uppercase letter); keys of `params.yaml` and
`spec.params` are checked at load time (exit 2), keys of `--params FILE` when the flag is read (exit 3). The hub pass additionally
needs a hub namespace: the Policy's `metadata.namespace`, or `--hub-namespace` (they must match if both
are given), or `spec.hubTemplateOptions.serviceAccountName`.

`--hub-mode`: `resolve` (default) resolves hub templates against `hub/objects/`; `passthrough` replaces
each hub template by its inner expression without simulating anything (for cohorts without hub data; a
warning is printed); `forbid` fails when a hub template is present.

## 6. Error catalog

Mock-data problems found at load time exit with code 2; problems found while resolving exit with code 1.
The text below is what the tool prints.

### Load-time (exit 2)

**E5 missing identity field**
`mock data: file objects/v1/ConfigMap/ns-a/my-config.yaml: missing metadata.name`
(also `missing apiVersion`, `missing kind`). *Fix:* add the field to the file body. A `metadata.name` or
`metadata.namespace` that is present but not a string (`namespace: no`, `name: 123`) is reported as
`metadata.namespace must be a string, got bool false; quote the value`; quote the value.

**E6 path and body disagree**
`mock data: file objects/v1/ConfigMap/ns-a/my-config.yaml: metadata.name is "other-name" but directory says "my-config"`
(also for `metadata.namespace`, `kind` and `apiVersion`). *Fix:* rename or move the file, or correct the body;
the path is the contract.

**E7 scope mismatch**
`mock data: file objects/v1/ConfigMap/my-config.yaml: Kind ConfigMap is namespaced but file is not under a namespace directory`
`mock data: file objects/v1/Namespace/ns-a/ns-a.yaml: Kind Namespace is cluster-scoped but file is under a namespace directory`
`mock data: file objects/example.com/v1/Widget/ns-a/b-ns.yaml: Kind Widget is cluster-scoped but file is under a namespace directory (scope set by objects/example.com/v1/Widget/a-cluster.yaml)`
*Fix:* put namespaced objects in `<Kind>/<namespace>/` and cluster-scoped ones directly in `<Kind>/`.
For a custom Kind, make all its files use the same layout.

**E8 duplicate object**
`mock data: duplicate object v1/ConfigMap ns-a/x in objects/v1/ConfigMap/ns-a/x.json and objects/v1/ConfigMap/ns-a/x.yaml`
*Fix:* delete one of the two files.

**E9 invalid file**
`mock data: file objects/v1/ConfigMap/ns-a/my-config.yaml: parse error: yaml: line 3: did not find expected ',' or ']'`
Other reasons: `unknown field "dta" for v1 ConfigMap (allowed: ...)` (a misspelled top-level field of a
ConfigMap, Secret or Namespace), `params key "env" must be an exported identifier ...` (in `params.yaml`), `dotfile`, `broken symlink`, `symlink escapes root`, `symlink to directory is not supported`,
`unsupported extension`, `not a regular file`. *Fix:* repair or remove the file; keep notes in a
`README.md` outside the cohort (a `.md` inside it is an unsupported extension).

**E10 Secret looks like kubectl output**
`mock data: file objects/v1/Secret/ns-a/db-creds.yaml: Secret data looks base64-encoded (kubectl output?); decode values or add annotation gs-acm-utils.io/data-encoding: base64`
*Fix:* add the annotation `gs-acm-utils.io/data-encoding: base64` (values stay base64), or replace
`data` by plaintext `stringData`.

**E11 invalid base64 in an annotated Secret**
`mock data: file objects/v1/Secret/ns-a/db-creds.yaml: data.password is not valid base64 (annotation data-encoding: base64 is set)`
*Fix:* correct the value, or remove the annotation and write plaintext.

**E15 empty cohort**
`mock data: no objects found under <cohort>/objects/`
*Fix:* check `--mock-data-dir` points at the cohort root (the directory *containing* `objects/`) and that
`objects/` has files. A cohort that does not exist reads `mock data: cohort directory <dir>: does not exist`.
For a `--mock-data-file` the message is
`mock data: <file>: spec: no objects found: neither spec.managed nor spec.hub contains objects`.

### Resolve-time (exit 1)

**E1 namespace directory missing**
`mock data: namespace directory missing: objects/v1/ConfigMap/no-such-ns/ (referenced by fromConfigMap "no-such-ns" "cool-car" "model")`
*Fix:* the policy names a namespace the cohort lacks: add the object (which creates the directory), or
fix the policy.

**E2 object missing**
`mock data: object not found: objects/v1/ConfigMap/default/no-such-map.yaml (referenced by fromConfigMap "default" "no-such-map" "model")`
*Fix:* add the file at exactly that path, or fix the name in the policy.

**E3 key missing**
`mock data: key "nope" not found in objects/v1/ConfigMap/ns-a/my-config.yaml (data has: color, size)`
*Fix:* add the key to the mock object (same name as on the real cluster), or fix the key in the policy.

**E4 lookup unsupported**
`mock data: lookup not supported for apiVersion=apps/v1, Kind=Deployment (no files under objects/apps/v1/Deployment/)`
*Fix:* the cohort has no object of that apiVersion/Kind at all. Add at least one file under that
directory, or check the apiVersion/Kind spelling in the policy.

**E12 hub data missing**
`mock data: input contains {{hub templates but <cohort>/hub/objects/ does not exist (use --hub-mode=passthrough to skip)`
*Fix:* add `hub/objects/` (and `hub/context.yaml`) to the cohort, or run with `--hub-mode=passthrough`
if hub values are not needed.

**E13 unresolved hub template**
`unresolved hub template remains after hub pass` (with `--hub-mode=forbid`:
`input contains {{hub templates but hub templates are forbidden by --hub-mode=forbid`).
*Fix:* a `{{hub` survived resolution, typically a hub delimiter inside a value that a template produced
or a mismatched `hub}}`. Fix the delimiters; use `--hub-mode=resolve` if you used `forbid` by mistake.

**E14 encryption unsupported**
`function "protect" is not available in mock mode: encryption is not supported`
*Fix:* the `protect` function needs real hub encryption keys. Remove it from the policy under test, or
validate that policy in a real environment.

Template syntax and execution errors that are not mock-data problems (for example
`template: tmpl:19: unexpected "{" in operand`) are passed through from the library unchanged and also
exit with code 1.

One of them gets an extra `hint:` line: `unexpected "\\" in operand`. The library converts each object to
YAML before parsing its `{{ }}` templates, and it writes a string double-quoted (turning `"` into `\"`)
when a line of the string ends in a space. A common cause is a template that resolved to empty, such as
`retention: {{hub ... hub}}` in a `|` block with a missing hub value under `--strict=false`. The cluster
controllers use the same library, so a live cluster likely fails the same way. *Fix:* remove the space before the
line break, or quote the template or give it a default (`| default "7d"`).

## 7. Consolidated file format

`--mock-data-file FILE` loads one YAML or JSON document holding one resolver context. It produces the same
mock data as the equivalent cohort directory; the directory format is unchanged. Pass exactly one of
`--mock-data-dir` and `--mock-data-file`, and not as an empty string (a usage error, exit 3, otherwise). The conventional file name is
`fixture.yaml`; any path is accepted. `testdata/fixtures/basic.yaml` is the file form of
`testdata/cohorts/basic` (a test keeps the two equivalent).

```yaml
apiVersion: acm-utils.gskode.com/v1alpha1
kind: MockData
metadata:
  name: basic                 # optional label; only `name` is allowed
spec:
  managed:                    # required, non-empty list  -> objects/ (managed view)
    - apiVersion: v1
      kind: ConfigMap
      metadata: {name: my-config, namespace: ns-a, labels: {color: blue}}
      data: {color: blue, size: "3"}
    - apiVersion: v1
      kind: Secret
      metadata: {name: db-creds, namespace: ns-a}
      data: {password: s3cr3t-plaintext}      # plaintext, as in section 3
      stringData: {username: admin}
    - apiVersion: cluster.open-cluster-management.io/v1alpha1
      kind: ClusterClaim
      metadata: {name: id.k8s.io}
      spec: {value: 7b1c0f4e-8f1d-4f0a-9a57-000000000001}
  hub:                        # optional list -> hub/objects/ (hub view); `hub: []` = hub data present
    - apiVersion: v1
      kind: ConfigMap
      metadata: {name: hub-config, namespace: policies}
      data: {logLevel: debug}
    - apiVersion: cluster.open-cluster-management.io/v1
      kind: ManagedCluster
      metadata: {name: local-cluster, labels: {environment: dev}}
  hubContext:                 # optional map -> hub/context.yaml
    clusterName: local-cluster
    clusterLabels: {environment: dev, region: na}
  params:                     # optional map -> params.yaml
    Env: dev
```

```
acm-mock-resolver mockdata check --mock-data-file fixture.yaml
# ok: 3 managed objects, 2 hub objects
acm-mock-resolver resolve --mock-data-file fixture.yaml --params extra.yaml policy.yaml
```

### Rules

1. The file holds exactly one YAML/JSON mapping document (no `---` multi-document; `.json` files skip that
   check as in the directory format). Unknown fields at the top level, in `metadata` and in `spec` are
   rejected, and so is a duplicate mapping key anywhere in the file (the directory format does not detect
   duplicate keys; this is the one parsing divergence). `apiVersion` must be
   `acm-utils.gskode.com/v1alpha1` and `kind` `MockData`; `spec` is required.
2. `spec.managed` and `spec.hub` are lists of objects, each a mapping. Per object the rules of section 2
   that do not involve a path apply: `apiVersion`, `kind` and `metadata.name` are required, scope is
   consistent, no duplicates (an identity may appear once in `managed` and once in `hub`; the two views are
   independent), and Secrets follow section 3, including `stringData` and the `gs-acm-utils.io/data-encoding`
   annotation. The type and unknown-field rules (2 and 9 of section 2) apply as well. There is no path/body
   agreement check.
3. Scope: a known namespaced Kind needs `metadata.namespace`; a known cluster-scoped Kind must not have it.
   For an unknown Kind the first object of that Kind in the list sets the scope (namespaced when
   `metadata.namespace` is set) and the rest must agree.
4. At least one of `spec.managed` and `spec.hub` must hold an object, else E15. A hub-only file is valid:
   `spec.managed` may be absent, `null` or `[]` when `spec.hub` has objects (managed `{{ }}` lookups then
   fail with the usual E1/E2/E4; plain managed content passes through). A `spec.managed` or `spec.hub` that
   is not a list is its own error, without an extra E15. `spec.hub` present, even as
   `[]` (a `null` value counts as absent), means hub data is present; absent means `--hub-mode=resolve`
   fails with E12 on a hub template, as for a cohort without `hub/objects/`.
5. `spec.hubContext` and `spec.params` are free-form maps like `hub/context.yaml` and `params.yaml`: absent or
   `null` is none, `{}` is an empty map. `spec.params` keys must start with an uppercase letter (a load error
   otherwise); `hubContext` is allowed without `hub`.
6. Nothing is synthesized at load time. As with directories, a Namespace object is derived for every
   namespace that holds objects, unless an explicit `v1/Namespace` object exists.

### Precedence

`--params FILE` keys are merged over `spec.params`; `--cluster-name` wins over `spec.hubContext.clusterName`,
which wins over the `ManagedCluster` / default lookup (section 5).

### Errors

Every load failure, including an unreadable file, exits with code 2 and lists all problems, sorted by
position (`spec.managed[2]` before `spec.managed[10]`), each prefixed with the path as given and the YAML
path, plus `(apiVersion/Kind namespace/name)` when known:

```
fixture.yaml: spec.managed[0]: missing metadata.name
fixture.yaml: spec.managed[3] (v1/ConfigMap ns-a/x): Kind ConfigMap is namespaced but metadata.namespace is not set
fixture.yaml: spec.managed[4]: duplicate object v1/ConfigMap ns-a/x (first defined at spec.managed[1])
fixture.yaml: spec.managed[5] (v1/Secret ns-a/s): data.k is not valid base64 (annotation data-encoding: base64 is set)
fixture.yaml: spec.managed[6] (v1/ConfigMap ns-a/y): unknown field "dta" for v1 ConfigMap (allowed: apiVersion, kind, metadata, data, binaryData, immutable)
fixture.yaml: spec.params: params key "env" must be an exported identifier (start with an uppercase letter)
fixture.yaml: unknown field "managd"
fixture.yaml: spec.managed: no objects found
fixture.yaml: does not exist
```

Resolve-time errors keep their catalog entry (E1-E4, E12) with file wording:

```
mock data: no v1/ConfigMap objects in namespace no-such-ns under spec.managed (fixture.yaml) (referenced by fromConfigMap "no-such-ns" "cm" "k")
mock data: object not found: v1/ConfigMap ns-a/zzz in spec.managed (fixture.yaml)
mock data: key "nope" not found in fixture.yaml: spec.managed[0] (v1/ConfigMap ns-a/my-config) (data has: color, size)
mock data: lookup not supported for apiVersion=apps/v1, Kind=Deployment (no objects of that Kind in spec.managed (fixture.yaml))
mock data: input contains {{hub templates but spec.hub (fixture.yaml) is absent (use --hub-mode=passthrough to skip)
```

### Boundary

One file is one cluster identity (one resolver context). Grouped fixtures such as gitops-pr-review
`PolicyFixture` groups (`targetFixtures` plus a central catalog) are not supported and are not merged by the
tool: the caller selects one context and emits one file for it. A directory/file converter and a published
JSON Schema are out of scope.
