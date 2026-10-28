// Portions of this file are derived from
// github.com/stolostron/go-template-utils, file
// cmd/template-resolver/utils/resolver_utils.go at tag v7.3.0,
// Copyright Contributors to the Open Cluster Management project,
// licensed under the Apache License, Version 2.0 (see LICENSE and NOTICE).
//
// Ported functions: ProcessTemplate (kind dispatch), processPolicyTemplate,
// processConfigPolicyTemplate, processObjectTemplates, processOperatorPolicyTemplates,
// processObjTemplatesRaw, processRawGoTemplate, resolveManagedTemplate.
//
// Changes from upstream: every ResolveTemplate call goes through a Session and
// gets freshly built options (including skipObject, which is passed per call);
// the context is the map-built struct from context.go; the unresolved-hub error
// is the catalog error E13; template/mock-data errors are returned unwrapped when
// they already are catalog errors. Upstream HEAD's fix for skip-policy-validation
// (returning the real error from processRawGoTemplate) is included.

package resolve

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/gscode1/gs-acm-utils/internal/mockdata"
)

// walker resolves the managed-cluster templates of one document.
type walker struct {
	s   *Session
	ctx any
}

// dispatch resolves policy in place according to its kind (Policy,
// ConfigurationPolicy, OperatorPolicy, object-templates-raw, or - with
// skipPolicyValidation - any raw Go template). rawInput is the raw text used for
// the skipPolicyValidation fallback.
func (w *walker) dispatch(policy *unstructured.Unstructured, rawInput []byte, skipPolicyValidation bool) error {
	var err error

	switch policy.GetKind() {
	case "Policy":
		err = w.processPolicyTemplate(policy)
	case "ConfigurationPolicy":
		err = w.processConfigPolicyTemplate(policy)
	case "OperatorPolicy":
		_, err = w.processOperatorPolicyTemplates(policy.Object)
	default:
		if skipPolicyValidation {
			var resolvedRaw any

			resolvedRaw, err = w.processRawGoTemplate(string(rawInput))
			if err != nil {
				return err
			}

			resolved, ok := resolvedRaw.(map[string]any)
			if !ok {
				return errors.New("failed to cast returned object to map[string]interface{}")
			}

			policy.Object = resolved

			return nil
		}

		if _, ok := policy.Object["object-templates-raw"]; !ok {
			return errors.New("invalid YAML. Supported types: Policy, " +
				"ConfigurationPolicy, OperatorPolicy, object-templates-raw")
		}

		err = w.processObjTemplatesRaw(policy)
	}

	return err
}

// processPolicyTemplate resolves all valid ConfigurationPolicy / OperatorPolicy
// templates in a Policy's spec.policy-templates.
func (w *walker) processPolicyTemplate(policy *unstructured.Unstructured) error {
	policyTemplates, found, err := unstructured.NestedSlice(policy.Object, "spec", "policy-templates")
	if err != nil {
		return fmt.Errorf("invalid policy-templates array was provided: %w", err)
	} else if !found {
		return errors.New("invalid policy-templates array was provided: spec.policy-templates keys not found")
	}

	for i := range policyTemplates {
		policyTemplate, ok := policyTemplates[i].(map[string]any)
		if !ok {
			return fmt.Errorf("invalid policy-templates entry was provided at index %d: "+
				"could not parse to map[string]interface{}", i)
		}

		objectDefinition, found, err := unstructured.NestedMap(policyTemplate, "objectDefinition")
		if err != nil {
			return fmt.Errorf("invalid policy-templates entry was provided at index %d: %w", i, err)
		} else if !found {
			return fmt.Errorf("invalid policy-templates entry was provided at index %d: "+
				"objectDefinition key not found", i)
		}

		templateObj := unstructured.Unstructured{Object: objectDefinition}

		switch templateObj.GetAPIVersion() {
		case "policy.open-cluster-management.io/v1":
			if templateObj.GetKind() != "ConfigurationPolicy" {
				continue
			}

			objectDefinition, err = w.processObjectTemplates(objectDefinition)
			if err != nil {
				return fmt.Errorf("%w (in policy-templates at index %d)", err, i)
			}
		case "policy.open-cluster-management.io/v1beta1":
			if templateObj.GetKind() != "OperatorPolicy" {
				continue
			}

			objectDefinition, err = w.processOperatorPolicyTemplates(objectDefinition)
			if err != nil {
				return fmt.Errorf("%w (in policy-templates at index %d)", err, i)
			}
		default:
			continue
		}

		err = unstructured.SetNestedField(policyTemplate, objectDefinition, "objectDefinition")
		if err != nil {
			return fmt.Errorf(
				"invalid policy-templates entry at index %d after resolving templates: %w",
				i,
				err,
			)
		}
	}

	err = unstructured.SetNestedSlice(policy.Object, policyTemplates, "spec", "policy-templates")
	if err != nil {
		return fmt.Errorf("invalid policy-templates after resolving templates: %w", err)
	}

	return nil
}

// processConfigPolicyTemplate resolves a bare ConfigurationPolicy.
func (w *walker) processConfigPolicyTemplate(policy *unstructured.Unstructured) error {
	resolvedPolicy, err := w.processObjectTemplates(policy.Object)
	if err != nil {
		return err
	}

	policy.Object = resolvedPolicy

	return nil
}

// processRawGoTemplate resolves a raw YAML Go template string.
func (w *walker) processRawGoTemplate(input string) (resolved any, err error) {
	if strings.Contains(input, "{{hub") {
		return nil, &mockdata.ErrUnresolvedHub{}
	}

	tmplResult, err := w.s.Resolve(ManagedPass, []byte(input), w.ctx, PassOptions{InputIsYAML: true})
	if err != nil {
		return nil, wrapProcess(err)
	}

	err = json.Unmarshal(tmplResult.ResolvedJSON, &resolved)
	if err != nil {
		return nil, fmt.Errorf("failed to process the templates: %w", err)
	}

	return resolved, nil
}

// processObjTemplatesRaw resolves the object-templates-raw string of raw into
// object-templates.
func (w *walker) processObjTemplatesRaw(raw *unstructured.Unstructured) error {
	oTRaw, _, _ := unstructured.NestedString(raw.Object, "object-templates-raw")
	if oTRaw == "" {
		return errors.New("invalid object-templates-raw after resolving hub templates")
	}

	resolved, err := w.processRawGoTemplate(oTRaw)
	if err != nil {
		return err
	}

	var objectTemplates []any

	switch v := resolved.(type) {
	case []any:
		objectTemplates = v
	case nil:
		objectTemplates = []any{}
	default:
		return errors.New("object-templates-raw was not an array after templates were resolved")
	}

	unstructured.RemoveNestedField(raw.Object, "object-templates-raw")

	err = unstructured.SetNestedSlice(raw.Object, objectTemplates, "object-templates")
	if err != nil {
		return fmt.Errorf("failed to process the object-templates-raw: %w", err)
	}

	return nil
}

// skipObjectFunc returns the skipObject template function bound to *skip.
func skipObjectFunc(skip *bool) func(...any) (string, error) {
	return func(skips ...any) (empty string, err error) {
		switch len(skips) {
		case 0:
			*skip = true
		case 1:
			if !*skip {
				if v, ok := skips[0].(bool); ok {
					*skip = v
				} else {
					err = fmt.Errorf("expected boolean but received '%v'", skips[0])
				}
			}
		default:
			err = fmt.Errorf("expected one optional boolean argument but received %d arguments", len(skips))
		}

		return empty, err
	}
}

// processObjectTemplates resolves the managed templates of a ConfigurationPolicy
// (or any object with spec.object-templates / spec.object-templates-raw), one
// object-template at a time with fresh options and a fresh skipObject.
func (w *walker) processObjectTemplates(objectDefinition map[string]any) (map[string]any, error) {
	_, oTRawFound, _ := unstructured.NestedString(objectDefinition, "spec", "object-templates-raw")
	if oTRawFound {
		policy := unstructured.Unstructured{Object: objectDefinition["spec"].(map[string]any)}

		err := w.processObjTemplatesRaw(&policy)
		if err != nil {
			return nil, err
		}

		objectDefinition["spec"] = policy.Object

		return objectDefinition, nil
	}

	objTemplates, _, err := unstructured.NestedSlice(objectDefinition, "spec", "object-templates")
	if err != nil {
		return nil, fmt.Errorf("invalid object-templates array in Configuration Policy: %w", err)
	}

	resolvedTemplates := []any{}

	for i, objTemplate := range objTemplates {
		fieldName := fmt.Sprintf("object-templates[%v]", i)
		skipObject := false

		resolved, err := w.resolveManagedTemplate(objTemplate, fieldName, PassOptions{
			CustomFunctions: map[string]any{"skipObject": skipObjectFunc(&skipObject)},
		})
		if err != nil {
			return nil, err
		}

		if !skipObject {
			resolvedTemplates = append(resolvedTemplates, resolved)
		}
	}

	err = unstructured.SetNestedSlice(objectDefinition, resolvedTemplates, "spec", "object-templates")
	if err != nil {
		return nil, fmt.Errorf("failed to process the templates: %w", err)
	}

	return objectDefinition, nil
}

// processOperatorPolicyTemplates resolves an OperatorPolicy's operatorGroup,
// subscription and versions.
func (w *walker) processOperatorPolicyTemplates(operatorPolicy map[string]any) (map[string]any, error) {
	opGroup, found, err := unstructured.NestedMap(operatorPolicy, "spec", "operatorGroup")
	if err != nil {
		return nil, fmt.Errorf("invalid operatorGroup: %w", err)
	}

	if found {
		resolved, err := w.resolveManagedTemplate(opGroup, "operatorGroup", PassOptions{})
		if err != nil {
			return nil, err
		}

		err = unstructured.SetNestedField(operatorPolicy, resolved, "spec", "operatorGroup")
		if err != nil {
			return nil, err
		}
	}

	sub, found, err := unstructured.NestedMap(operatorPolicy, "spec", "subscription")
	if err != nil {
		return nil, fmt.Errorf("invalid subscription: %w", err)
	}

	if found {
		resolved, err := w.resolveManagedTemplate(sub, "subscription", PassOptions{})
		if err != nil {
			return nil, err
		}

		err = unstructured.SetNestedField(operatorPolicy, resolved, "spec", "subscription")
		if err != nil {
			return nil, err
		}
	} else {
		return nil, errors.New("spec.subscription must be set in OperatorPolicies")
	}

	versions, found, err := unstructured.NestedStringSlice(operatorPolicy, "spec", "versions")
	if err != nil {
		return nil, fmt.Errorf("invalid versions: %w", err)
	}

	if found {
		resolved, err := w.resolveManagedTemplate(versions, "versions", PassOptions{})
		if err != nil {
			return nil, err
		}

		resolvedList, _ := resolved.([]any)
		resolvedVersions := make([]any, 0, len(resolvedList))

		for _, version := range resolvedList {
			trimmedVersion := strings.TrimSpace(fmt.Sprint(version))

			if trimmedVersion != "" {
				resolvedVersions = append(resolvedVersions, trimmedVersion)
			}
		}

		err = unstructured.SetNestedField(operatorPolicy, resolvedVersions, "spec", "versions")
		if err != nil {
			return nil, err
		}
	}

	return operatorPolicy, nil
}

// resolveManagedTemplate resolves a template, and emits E13 if any hub
// templates are still in the object. po supplies per-call extras (skipObject).
func (w *walker) resolveManagedTemplate(field any, fieldName string, po PassOptions) (any, error) {
	rawData, err := json.Marshal(field)
	if err != nil {
		return nil, fmt.Errorf("invalid %v: %w", fieldName, err)
	}

	if bytes.Contains(rawData, []byte(hubStartDelim)) {
		return nil, &mockdata.ErrUnresolvedHub{}
	}

	tmplResult, err := w.s.Resolve(ManagedPass, rawData, w.ctx, po)
	if err != nil {
		return nil, wrapProcess(err)
	}

	var resolved any

	err = json.Unmarshal(tmplResult.ResolvedJSON, &resolved)
	if err != nil {
		return nil, fmt.Errorf("failed to process the templates: %w", err)
	}

	return resolved, nil
}

// wrapProcess adds upstream's "failed to process the templates" prefix to library
// errors; catalog errors (E1-E4, E12-E14) are returned as they are so their
// message keeps the exact "mock data: ..." shape.
func wrapProcess(err error) error {
	if isCatalogError(err) {
		return err
	}

	return fmt.Errorf("failed to process the templates: %w", err)
}

func isCatalogError(err error) bool {
	var (
		e1  *mockdata.ErrNamespaceDirMissing
		e2  *mockdata.ErrObjectMissing
		e3  *mockdata.ErrKeyMissing
		e4  *mockdata.ErrLookupUnsupported
		e12 *mockdata.ErrHubDataMissing
		e13 *mockdata.ErrUnresolvedHub
		e14 *mockdata.ErrEncryptionUnsupported
		uns *UnsupportedResourceError
	)

	return errors.As(err, &e1) || errors.As(err, &e2) || errors.As(err, &e3) || errors.As(err, &e4) ||
		errors.As(err, &e12) || errors.As(err, &e13) || errors.As(err, &e14) || errors.As(err, &uns)
}
