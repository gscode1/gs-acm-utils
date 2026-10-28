// Package resolve is the template resolution engine: it wires mock data, the
// fake cluster clients and go-template-utils together, and layers the strict
// "hard-fail on missing mock data" template functions on top.
package resolve

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/stolostron/go-template-utils/v7/pkg/templates"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"

	"github.com/gscode1/gs-acm-utils/internal/fakecluster"
	"github.com/gscode1/gs-acm-utils/internal/mockdata"
)

// Pass selects which cluster view a resolution runs against.
type Pass int

const (
	// ManagedPass resolves {{ ... }} against the managed-cluster view (objects/).
	ManagedPass Pass = iota
	// HubPass resolves {{hub ... hub}} against the hub view (hub/objects/).
	HubPass
)

const (
	hubStartDelim = "{{hub"
	hubStopDelim  = "hub}}"
	// hubIndentation matches upstream's hub resolver (template-resolver CLI).
	hubIndentation = 8
)

// Options configures an Engine.
type Options struct {
	// Strict enables the strict fromConfigMap / fromSecret / lookup functions
	// (missing objects, namespaces and keys are errors). The CLI defaults it to true.
	Strict bool
	// Disabled lists extra template function names to disable. "protect" is always disabled.
	Disabled []string
	// Cohort is the cohort directory, used only in error messages (E12).
	Cohort string
	// Warn receives non-fatal warnings (the --hub-mode=passthrough stub warning).
	// Nil discards them; the CLI passes stderr.
	Warn io.Writer
}

// PassOptions are the per-call, per-pass knobs that feed a fresh
// templates.ResolveOptions.
type PassOptions struct {
	// LookupNamespace restricts lookups like the library (hub pass).
	LookupNamespace string
	// ClusterScopedAllowList is honored together with LookupNamespace.
	ClusterScopedAllowList []templates.ClusterScopedObjectIdentifier
	// InputIsYAML marks the input as raw YAML instead of JSON.
	InputIsYAML bool
	// CustomFunctions are extra template functions for this one call (for
	// example skipObject). They are copied into the fresh options and override
	// the engine's functions of the same name.
	CustomFunctions map[string]any
}

type clients struct {
	dyn  dynamic.Interface
	disc discovery.DiscoveryInterface
	ix   *mockdata.Index
}

// Engine is built once per run. It owns the shared fake clients; per-document
// state lives in a Session. An Engine is safe for concurrent use.
type Engine struct {
	opts     Options
	disabled []string
	managed  clients
	hub      *clients // nil when the cohort has no hub/objects

	// params and hubContext are the cohort's params.yaml and hub/context.yaml.
	params     map[string]any
	hubContext map[string]any
}

// NewEngine builds the fake clients for the managed view and, when the cohort
// has hub data, for the hub view.
func NewEngine(set *mockdata.Set, opts Options) (*Engine, error) {
	if set == nil || set.Managed == nil {
		return nil, errors.New("resolve: nil mock data set")
	}

	disabled := []string{"protect"}

	for _, d := range opts.Disabled {
		if !slices.Contains(disabled, d) {
			disabled = append(disabled, d)
		}
	}

	e := &Engine{opts: opts, disabled: disabled, params: set.Params, hubContext: set.HubContext}

	dyn, disc, err := fakecluster.New(set.Managed)
	if err != nil {
		return nil, fmt.Errorf("resolve: managed fake cluster: %w", err)
	}

	e.managed = clients{dyn: dyn, disc: disc, ix: set.Managed}

	if set.HubPresent && set.Hub != nil {
		hdyn, hdisc, err := fakecluster.New(set.Hub)
		if err != nil {
			return nil, fmt.Errorf("resolve: hub fake cluster: %w", err)
		}

		e.hub = &clients{dyn: hdyn, disc: hdisc, ix: set.Hub}
	}

	return e, nil
}

// HubAvailable reports whether the cohort has hub data.
func (e *Engine) HubAvailable() bool { return e.hub != nil }

// Strict reports whether strict functions are enabled.
func (e *Engine) Strict() bool { return e.opts.Strict }

// DisabledFunctions returns the effective disabled function names (always containing "protect").
func (e *Engine) DisabledFunctions() []string { return slices.Clone(e.disabled) }

// resolveOptions returns a NEW *templates.ResolveOptions on every call, because
// ResolveTemplate mutates the options it is given. Never cache or share the result.
func (e *Engine) resolveOptions(pass Pass, po PassOptions) *templates.ResolveOptions {
	cl := &e.managed
	if pass == HubPass && e.hub != nil {
		cl = e.hub
	}

	opts := &templates.ResolveOptions{
		InputIsYAML:            po.InputIsYAML,
		LookupNamespace:        po.LookupNamespace,
		ClusterScopedAllowList: slices.Clone(po.ClusterScopedAllowList),
	}

	opts.CustomFunctions = map[string]any{
		// protect is disabled in mock mode; give a catalog error (E14) rather than "not defined".
		"protect": func(_ string) (string, error) { return "", &mockdata.ErrEncryptionUnsupported{} },
	}

	if e.opts.Strict {
		s := &strictFuncs{ix: cl.ix, lookupNamespace: po.LookupNamespace, allowList: opts.ClusterScopedAllowList}

		// A function the user disabled must stay disabled: custom functions
		// would otherwise re-add it.
		for name, fn := range map[string]any{
			"fromConfigMap": s.fromConfigMap,
			"fromSecret":    s.fromSecret,
			"lookup":        s.lookup,
		} {
			if !slices.Contains(e.disabled, name) {
				opts.CustomFunctions[name] = fn
			}
		}
	}

	for name, fn := range po.CustomFunctions {
		opts.CustomFunctions[name] = fn
	}

	return opts
}

// Session holds the per-document resolvers: fresh template resolvers (and so a
// fresh temporary object cache) over the Engine's shared fake clients.
type Session struct {
	e       *Engine
	managed *templates.TemplateResolver
	hub     *templates.TemplateResolver
}

// NewSession returns a fresh Session; create one per input document.
func (e *Engine) NewSession() (*Session, error) {
	r, err := templates.NewResolverWithClients(e.managed.dyn, e.managed.disc, templates.Config{
		DisabledFunctions: slices.Clone(e.disabled),
	})
	if err != nil {
		return nil, fmt.Errorf("resolve: managed resolver: %w", err)
	}

	return &Session{e: e, managed: r}, nil
}

// Managed returns the session's managed-pass resolver.
func (s *Session) Managed() *templates.TemplateResolver { return s.managed }

// Hub returns the session's hub-pass resolver, creating it on first use. It
// returns *mockdata.ErrHubDataMissing (E12) when the cohort has no hub data.
func (s *Session) Hub() (*templates.TemplateResolver, error) {
	if s.hub != nil {
		return s.hub, nil
	}

	if s.e.hub == nil {
		return nil, &mockdata.ErrHubDataMissing{Cohort: s.e.opts.Cohort}
	}

	r, err := templates.NewResolverWithClients(s.e.hub.dyn, s.e.hub.disc, templates.Config{
		AdditionalIndentation: hubIndentation,
		DisabledFunctions:     slices.Clone(s.e.disabled),
		StartDelim:            hubStartDelim,
		StopDelim:             hubStopDelim,
	})
	if err != nil {
		return nil, fmt.Errorf("resolve: hub resolver: %w", err)
	}

	s.hub = r

	return r, nil
}

// Resolve runs one ResolveTemplate call for the given pass with freshly built
// options, translating library errors into mock-data errors (see translateError).
func (s *Session) Resolve(pass Pass, input []byte, ctx any, po PassOptions) (templates.TemplateResult, error) {
	r := s.managed

	if pass == HubPass {
		var err error
		if r, err = s.Hub(); err != nil {
			return templates.TemplateResult{}, err
		}
	}

	res, err := r.ResolveTemplate(input, ctx, s.e.resolveOptions(pass, po))
	if err != nil {
		return res, translateError(err)
	}

	return res, nil
}

// ResolveRaw resolves a raw YAML template with the managed pass and returns the
// result as YAML. ctx must be nil or a struct (library rule).
func (s *Session) ResolveRaw(input []byte, ctx any) ([]byte, error) {
	res, err := s.Resolve(ManagedPass, input, ctx, PassOptions{InputIsYAML: true})
	if err != nil {
		return nil, err
	}

	return yaml.JSONToYAML(res.ResolvedJSON)
}

// ResolveRaw is a convenience that resolves with a throwaway Session.
func (e *Engine) ResolveRaw(input []byte, ctx any) ([]byte, error) {
	s, err := e.NewSession()
	if err != nil {
		return nil, err
	}

	return s.ResolveRaw(input, ctx)
}

// UnsupportedResourceError reports that a function referenced an apiVersion/Kind
// that has no mock data (the library's ErrMissingAPIResource). The library does
// not say which Kind, so the message is generic; strict lookups return the
// precise *mockdata.ErrLookupUnsupported (E4) instead.
type UnsupportedResourceError struct{ Err error }

func (e *UnsupportedResourceError) Error() string {
	return "mock data: lookup not supported: the requested apiVersion/Kind has no files under objects/ (" +
		e.Err.Error() + ")"
}

func (e *UnsupportedResourceError) Unwrap() error { return e.Err }

// translateError maps a ResolveTemplate error to a catalog error. Mock-data
// errors raised inside template functions (E1-E4, E14) are returned directly
// (they already carry a "referenced by" call); ErrMissingAPIResource becomes an
// *UnsupportedResourceError; the library's "function protect not defined" parse
// error becomes E14. Anything else is returned unchanged.
func translateError(err error) error {
	var (
		e1  *mockdata.ErrNamespaceDirMissing
		e2  *mockdata.ErrObjectMissing
		e3  *mockdata.ErrKeyMissing
		e4  *mockdata.ErrLookupUnsupported
		e14 *mockdata.ErrEncryptionUnsupported
	)

	switch {
	case errors.As(err, &e1):
		return e1
	case errors.As(err, &e2):
		return e2
	case errors.As(err, &e3):
		return e3
	case errors.As(err, &e4):
		return e4
	case errors.As(err, &e14):
		return e14
	case errors.Is(err, templates.ErrMissingAPIResource):
		return &UnsupportedResourceError{Err: templates.ErrMissingAPIResource}
	case strings.Contains(err.Error(), `function "protect" not defined`):
		return &mockdata.ErrEncryptionUnsupported{}
	}

	return err
}
