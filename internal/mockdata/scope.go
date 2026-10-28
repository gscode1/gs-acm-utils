package mockdata

import "k8s.io/apimachinery/pkg/runtime/schema"

type scopeKey struct{ group, kind string }

// knownScopes maps (group, Kind) to whether the Kind is namespaced. Versions are
// ignored on purpose so every served version of a Kind shares one scope.
var knownScopes = buildScopes(
	// core v1
	map[string][]string{
		"": {
			"ConfigMap", "Secret", "ServiceAccount", "Service", "Pod", "Endpoints",
			"PersistentVolumeClaim", "ResourceQuota", "LimitRange", "ReplicationController",
			"Event", "PodTemplate",
		},
		"apps":                               {"Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "ControllerRevision"},
		"batch":                              {"Job", "CronJob"},
		"autoscaling":                        {"HorizontalPodAutoscaler"},
		"networking.k8s.io":                  {"Ingress", "NetworkPolicy"},
		"rbac.authorization.k8s.io":          {"Role", "RoleBinding"},
		"policy":                             {"PodDisruptionBudget"},
		"cluster.open-cluster-management.io": {"Placement", "PlacementDecision"},
		"policy.open-cluster-management.io": {
			"Policy", "PolicySet", "PlacementBinding", "ConfigurationPolicy", "CertificatePolicy",
			"OperatorPolicy",
		},
	},
	map[string][]string{
		"":                                   {"Namespace", "Node", "PersistentVolume", "ComponentStatus"},
		"rbac.authorization.k8s.io":          {"ClusterRole", "ClusterRoleBinding"},
		"storage.k8s.io":                     {"StorageClass", "VolumeAttachment", "CSIDriver", "CSINode"},
		"networking.k8s.io":                  {"IngressClass"},
		"apiextensions.k8s.io":               {"CustomResourceDefinition"},
		"cluster.open-cluster-management.io": {"ManagedCluster", "ClusterClaim", "ManagedClusterSet"},
	},
)

func buildScopes(namespaced, cluster map[string][]string) map[scopeKey]bool {
	m := map[scopeKey]bool{}

	for group, kinds := range namespaced {
		for _, k := range kinds {
			m[scopeKey{group, k}] = true
		}
	}

	for group, kinds := range cluster {
		for _, k := range kinds {
			m[scopeKey{group, k}] = false
		}
	}

	return m
}

// knownScope reports the scope of a built-in Kind. ok is false for unknown Kinds,
// whose scope is taken from the directory layout.
func knownScope(gv schema.GroupVersion, kind string) (namespaced, ok bool) {
	namespaced, ok = knownScopes[scopeKey{gv.Group, kind}]

	return namespaced, ok
}
