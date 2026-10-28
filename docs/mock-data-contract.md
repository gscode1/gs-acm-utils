# Mock-data contract (for the platform team)

A **cohort** is a directory of Kubernetes objects that stands in for a cluster (or a group of
clusters that look alike). `acm-mock-resolver` reads it with `--mock-data-dir DIR` and answers
`fromConfigMap`, `fromSecret`, `lookup`, `copyConfigMapData`, `copySecretData`, `fromClusterClaim`,
node functions and label-selector lists from it. No kubeconfig and no network are used.

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
2. `apiVersion`, `kind` and `metadata.name` are present.
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

## 5. Template context

| Key | Pass | Source |
|---|---|---|
| `.ObjectNamespace`, `.ObjectName` | managed | `--object-namespace`, `--object-name` (empty by default) |
| `.ManagedClusterName` | hub | `--cluster-name`, else `hub/context.yaml` `clusterName` |
| `.ManagedClusterLabels` | hub | labels of the `ManagedCluster` named `.ManagedClusterName` in `hub/objects/.../ManagedCluster/`, else `hub/context.yaml` `clusterLabels`, else empty |
| `.PolicyMetadata` | hub | derived from the input Policy (`name`, `namespace`, `labels`, `annotations`), as upstream; only set when the input kind is `Policy` |
| extra keys, e.g. `.Env` | both | `params.yaml` in the cohort, overridden by `--params FILE`; flag values win over file values |

Extra keys must be exported identifiers (start with an uppercase letter). The hub pass additionally
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
(also `missing apiVersion`, `missing kind`). *Fix:* add the field to the file body.

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
Other reasons: `dotfile`, `broken symlink`, `symlink escapes root`, `symlink to directory is not supported`,
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
