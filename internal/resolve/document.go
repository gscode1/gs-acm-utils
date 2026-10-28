package resolve

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/stolostron/go-template-utils/v7/pkg/templates"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8syaml "sigs.k8s.io/yaml"

	"github.com/gscode1/gs-acm-utils/internal/mockdata"
)

// DocOptions are the per-document knobs of ResolveDocument; they map to the CLI
// flags --hub-mode, --cluster-name, --hub-namespace, --object-namespace,
// --object-name, --params and --skip-policy-validation.
type DocOptions struct {
	// HubMode defaults to HubModeResolve.
	HubMode HubMode
	// ClusterName is .ManagedClusterName for the hub pass (default: hub/context.yaml clusterName).
	ClusterName string
	// HubNamespace is the hub-pass LookupNamespace (default: the Policy's namespace).
	HubNamespace string
	// ObjectNamespace and ObjectName are .ObjectNamespace / .ObjectName in the managed pass.
	ObjectNamespace string
	ObjectName      string
	// Params are extra context keys, merged over the cohort's params.yaml. Keys
	// must be exported identifiers (uppercase first letter); flag values
	// (ObjectNamespace, ObjectName, ManagedClusterName, ...) win over Params.
	Params map[string]any
	// SkipPolicyValidation treats an input that is not a Policy,
	// ConfigurationPolicy or OperatorPolicy as a raw Go template.
	SkipPolicyValidation bool
}

// ResolveDocument resolves one YAML document (Policy, ConfigurationPolicy,
// OperatorPolicy, an object with object-templates-raw, or - with
// SkipPolicyValidation - a raw Go template): hub pass first (when the input
// contains {{hub), then the managed pass. It uses a new Session per call and
// returns the resolved document as YAML.
func (e *Engine) ResolveDocument(yamlBytes []byte, o DocOptions) ([]byte, error) {
	policy := unstructured.Unstructured{}

	if err := k8syaml.Unmarshal(yamlBytes, &policy.Object); err != nil {
		return nil, fmt.Errorf("failed to parse input to YAML: %w", err)
	}

	s, err := e.NewSession()
	if err != nil {
		return nil, err
	}

	mode, err := ParseHubMode(string(o.HubMode))
	if err != nil {
		return nil, err
	}

	rawInput := yamlBytes

	if containsHub(yamlBytes) {
		if err := e.handleHub(s, &policy, mode, &o); err != nil {
			return nil, err
		}

		// The raw (skip-policy-validation) path re-reads the text, so give it the
		// hub-processed document instead of the original.
		if rawInput, err = k8syaml.Marshal(policy.Object); err != nil {
			return nil, fmt.Errorf("invalid object after resolving hub templates: %w", err)
		}
	}

	ctx, err := e.managedContext(&o)
	if err != nil {
		return nil, err
	}

	w := &walker{s: s, ctx: ctx}
	if err := w.dispatch(&policy, rawInput, o.SkipPolicyValidation); err != nil {
		return nil, err
	}

	resolvedJSON, err := json.Marshal(policy.Object)
	if err != nil {
		return nil, fmt.Errorf("invalid JSON resulted after resolving templates: %w", err)
	}

	resolvedYAML, err := templates.JSONToYAML(resolvedJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to convert the processed object back to YAML: %w", err)
	}

	return resolvedYAML, nil
}

// handleHub applies the hub mode to a document that contains {{hub.
func (e *Engine) handleHub(s *Session, policy *unstructured.Unstructured, mode HubMode, o *DocOptions) error {
	switch mode {
	case HubModeForbid:
		return &HubForbiddenError{}
	case HubModePassthrough:
		if e.opts.Warn != nil {
			_, _ = io.WriteString(e.opts.Warn, "warning: --hub-mode=passthrough: hub templates are not resolved; "+
				"each {{hub ... hub}} is replaced by its expression text (stub, not a simulation)\n")
		}

		policy.Object, _ = passthroughHub(policy.Object).(map[string]any)
	default:
		resolved, err := e.hubPass(s, policy, o)
		if err != nil {
			return err
		}

		policy.Object = resolved
	}

	// Anything still looking like a hub template (unbalanced delimiters, output
	// that re-creates one) cannot be resolved by the managed pass: E13.
	remaining, err := json.Marshal(policy.Object)
	if err != nil {
		return fmt.Errorf("invalid object after resolving hub templates: %w", err)
	}

	if bytes.Contains(remaining, []byte(hubStartDelim)) {
		return &mockdata.ErrUnresolvedHub{}
	}

	return nil
}
