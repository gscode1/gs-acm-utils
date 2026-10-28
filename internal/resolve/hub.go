// The hub pass is derived from the hub section of ProcessTemplate in
// github.com/stolostron/go-template-utils, file
// cmd/template-resolver/utils/resolver_utils.go at tag v7.3.0
// (Apache-2.0, Copyright Contributors to the Open Cluster Management project),
// with the hub cluster replaced by the cohort's hub/objects/ mock data.

package resolve

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/stolostron/go-template-utils/v7/pkg/templates"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// HubMode selects how {{hub ... hub}} templates are handled.
type HubMode string

const (
	// HubModeResolve (the default) resolves hub templates against hub/objects/.
	HubModeResolve HubMode = "resolve"
	// HubModePassthrough replaces each hub template with its expression text
	// (FR-7.1 stub, not a simulation) and warns.
	HubModePassthrough HubMode = "passthrough"
	// HubModeForbid fails when the input contains a hub template.
	HubModeForbid HubMode = "forbid"
)

// ParseHubMode validates a --hub-mode value; "" means the default (resolve).
func ParseHubMode(s string) (HubMode, error) {
	switch m := HubMode(s); m {
	case "":
		return HubModeResolve, nil
	case HubModeResolve, HubModePassthrough, HubModeForbid:
		return m, nil
	}

	return "", fmt.Errorf("invalid hub mode %q: must be one of resolve, passthrough, forbid", s)
}

// HubForbiddenError is returned in HubModeForbid when the input has hub templates.
type HubForbiddenError struct{}

func (*HubForbiddenError) Error() string {
	return "input contains {{hub templates but hub templates are forbidden by --hub-mode=forbid"
}

const (
	ocmGroup          = "cluster.open-cluster-management.io"
	managedClusterAPI = ocmGroup + "/v1"
)

// hubPass resolves the hub templates of policy against the hub mock data and
// returns the resolved object. It ports the hub section of upstream
// ProcessTemplate: namespace rules (same error texts), the custom service
// account branch (no LookupNamespace / allow-list), PolicyMetadata for Policy
// kinds, ManagedClusterName / ManagedClusterLabels, and the ManagedCluster
// cluster-scoped allow-list.
//
// ManagedClusterName is DocOptions.ClusterName, else hub/context.yaml
// "clusterName". ManagedClusterLabels come from the ManagedCluster object of that
// name in hub/objects, else hub/context.yaml "clusterLabels", else an empty map
// (a missing ManagedCluster is not an error; templates reading a label get "").
func (e *Engine) hubPass(s *Session, policy *unstructured.Unstructured, o *DocOptions) (map[string]any, error) {
	// E12 first: without hub data nothing below can run.
	if _, err := s.Hub(); err != nil {
		return nil, err
	}

	hubNamespace := o.HubNamespace

	customSA, _, _ := unstructured.NestedString(policy.Object, "spec", "hubTemplateOptions", "serviceAccountName")

	if customSA == "" {
		if policy.GetKind() == "Policy" {
			// neither specified
			if hubNamespace == "" && policy.GetNamespace() == "" {
				return nil, errors.New("a namespace must be specified for hub templates, " +
					"either in the input Policy or as an argument if spec.hubTemplateOptions.serviceAccountName " +
					"is not specified")
			}

			// both specified and don't match
			if hubNamespace != "" && policy.GetNamespace() != "" && hubNamespace != policy.GetNamespace() {
				return nil, errors.New("the namespace specified in the Policy and the " +
					"hub-namespace argument must match")
			}

			// either hubNamespace is already specified, or we'll use the one in the policy
			if policy.GetNamespace() != "" {
				hubNamespace = policy.GetNamespace()
			}
		} else if hubNamespace == "" {
			// Non-Policy types just always require the argument
			return nil, errors.New("a hub namespace must be provided when a hub kubeconfig is provided " +
				"and spec.hubTemplateOptions.serviceAccountName is not specified")
		}
	}

	clusterName := o.ClusterName
	if clusterName == "" {
		clusterName, _ = e.hubContext["clusterName"].(string)
	}

	values := mergeParams(e.params, o.Params)
	values[keyManagedClusterName] = clusterName
	values[keyManagedClusterLabels] = e.clusterLabels(clusterName)

	var policyMetadata map[string]any

	if policy.GetKind() == "Policy" {
		policyMetadata = map[string]any{
			"annotations": policy.GetAnnotations(),
			"labels":      policy.GetLabels(),
			"name":        policy.GetName(),
			"namespace":   policy.GetNamespace(),
		}
	}

	values[keyPolicyMetadata] = policyMetadata

	ctx, err := buildContext(values)
	if err != nil {
		return nil, err
	}

	po := PassOptions{}

	// If a custom service account is provided, assume the hub access is that
	// service account's, so no namespace restriction applies.
	if customSA == "" {
		po.LookupNamespace = hubNamespace

		if clusterName != "" {
			po.ClusterScopedAllowList = []templates.ClusterScopedObjectIdentifier{{
				Group: ocmGroup,
				Kind:  "ManagedCluster",
				Name:  clusterName,
			}}
		}
	}

	objectDefinitionJSON, err := json.Marshal(policy.Object)
	if err != nil {
		return nil, fmt.Errorf("invalid object: %w", err)
	}

	res, err := s.Resolve(HubPass, objectDefinitionJSON, ctx, po)
	if err != nil {
		return nil, wrapHub(err)
	}

	var resolved map[string]any

	if err := json.Unmarshal(res.ResolvedJSON, &resolved); err != nil {
		return nil, fmt.Errorf("invalid object after resolving hub templates: %w", err)
	}

	return resolved, nil
}

// wrapHub keeps catalog errors verbatim and gives other errors upstream's
// "invalid object" prefix.
func wrapHub(err error) error {
	if isCatalogError(err) {
		return err
	}

	return fmt.Errorf("invalid object: %w", err)
}

// clusterLabels returns the labels for the ManagedCluster named name.
func (e *Engine) clusterLabels(name string) map[string]string {
	labels := map[string]string{}

	if e.hub != nil && name != "" {
		if mc, err := e.hub.ix.Get(managedClusterAPI, "ManagedCluster", "", name); err == nil {
			for k, v := range mc.GetLabels() {
				labels[k] = v
			}

			return labels
		}
	}

	if raw, ok := e.hubContext["clusterLabels"].(map[string]any); ok {
		for k, v := range raw {
			labels[k] = fmt.Sprint(v)
		}
	}

	return labels
}

// hubTemplateRE matches {{hub EXPR hub}} including the {{hub- / -hub}} trim forms.
var hubTemplateRE = regexp.MustCompile(`(?s)\{\{hub-?\s*(.*?)\s*-?hub\}\}`)

// passthroughHub replaces each hub template in every string (keys and values)
// of v with its trimmed expression text.
func passthroughHub(v any) any {
	switch t := v.(type) {
	case string:
		return hubTemplateRE.ReplaceAllString(t, "${1}")
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[passthroughHub(k).(string)] = passthroughHub(e)
		}

		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = passthroughHub(e)
		}

		return out
	default:
		return v
	}
}

func containsHub(b []byte) bool { return bytes.Contains(b, []byte(hubStartDelim)) }
