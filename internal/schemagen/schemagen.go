// Package schemagen generates the JSON Schema for the v1alpha1 resources from
// their Go types. Only the generator and its drift test use it; the CLI
// validates against the committed copy in package schemas.
package schemagen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/invopop/jsonschema"

	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
	"github.com/solaria-sh/solaria/schemas"
)

const (
	modulePath = "github.com/solaria-sh/solaria"
	apiDir     = "internal/api/v1alpha1"

	// OutDir is where the schema files are written, relative to the module
	// root. Package schemas embeds them from there.
	OutDir = "schemas/" + v1alpha1.Version
)

const (
	namePattern  = `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	modelPattern = `^[a-z][a-z0-9-]*:[^ ]+$`
	labelKey     = `^([a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*/)?[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$`
)

type kindSchema struct {
	kind string
	typ  any
}

var kinds = []kindSchema{
	{v1alpha1.KindAgent, &v1alpha1.Agent{}},
	{v1alpha1.KindEnvironment, &v1alpha1.Environment{}},
	{v1alpha1.KindMcpServer, &v1alpha1.McpServer{}},
	{v1alpha1.KindVault, &v1alpha1.Vault{}},
}

// Generate returns the schema files by name. It reads doc comments from the
// Go sources, so it must run with the module root as working directory.
func Generate() (map[string][]byte, error) {
	out, _, err := generate()
	return out, err
}

// generate returns the schema files and the combined schema they end with.
func generate() (map[string][]byte, *jsonschema.Schema, error) {
	out := make(map[string][]byte, len(kinds)+1)
	perKind := make(map[string]*jsonschema.Schema, len(kinds))
	for _, k := range kinds {
		r, err := newReflector()
		if err != nil {
			return nil, nil, err
		}
		file := schemas.File(k.kind)
		s := r.Reflect(k.typ)
		s.ID = jsonschema.ID(schemas.BaseURL + file)
		s.Title = "Solaria " + k.kind
		extend(s, k.kind)
		b, err := encode(s)
		if err != nil {
			return nil, nil, fmt.Errorf("encode %s: %w", file, err)
		}
		out[file] = b
		perKind[k.kind] = s
	}
	c, err := combined(perKind)
	if err != nil {
		return nil, nil, err
	}
	b, err := encode(c)
	if err != nil {
		return nil, nil, fmt.Errorf("encode %s: %w", schemas.CombinedFile, err)
	}
	out[schemas.CombinedFile] = b
	return out, c, nil
}

// tidyComment unwraps a doc comment onto one line and, for type comments,
// drops the leading Go type name ("AgentSpec describes ..." becomes
// "Describes ...", "KeyValue is a ..." becomes "A ..."), since schema
// readers never see Go names.
func tidyComment(key, text string) string {
	text = strings.Join(strings.Fields(text), " ")
	name := strings.TrimPrefix(key, modulePath+"/"+apiDir+".")
	if strings.Contains(name, ".") {
		return text // a field comment
	}
	rest, ok := strings.CutPrefix(text, name+" ")
	if !ok || rest == "" {
		return text
	}
	if r, ok := strings.CutPrefix(rest, "is "); ok && r != "" {
		rest = r
	}
	return strings.ToUpper(rest[:1]) + rest[1:]
}

func newReflector() (*jsonschema.Reflector, error) {
	r := &jsonschema.Reflector{
		RequiredFromJSONSchemaTags: true,
		ExpandedStruct:             true,
		Mapper:                     mapNamedTypes,
	}
	if err := r.AddGoComments(modulePath, apiDir, jsonschema.WithFullComment()); err != nil {
		return nil, fmt.Errorf("read doc comments from %s: %w", apiDir, err)
	}
	// Keys embed the walked directory, which uses backslashes on Windows;
	// reflection looks them up with import-path slashes.
	tidy := make(map[string]string, len(r.CommentMap))
	for k, v := range r.CommentMap {
		k = strings.ReplaceAll(k, `\`, "/")
		tidy[k] = tidyComment(k, v)
	}
	r.CommentMap = tidy
	return r, nil
}

// mapNamedTypes gives the named string types their enum or pattern.
func mapNamedTypes(t reflect.Type) *jsonschema.Schema {
	switch t {
	case reflect.TypeFor[v1alpha1.ModelRef]():
		return &jsonschema.Schema{Type: "string", Pattern: modelPattern}
	case reflect.TypeFor[v1alpha1.Effort]():
		return enum(v1alpha1.EffortLow, v1alpha1.EffortMedium, v1alpha1.EffortHigh, v1alpha1.EffortXHigh, v1alpha1.EffortMax)
	case reflect.TypeFor[v1alpha1.BuiltinTool]():
		return enum(v1alpha1.BuiltinTools...)
	case reflect.TypeFor[v1alpha1.NetworkMode]():
		return enum(v1alpha1.NetworkLimited, v1alpha1.NetworkUnrestricted)
	case reflect.TypeFor[v1alpha1.SecretSource]():
		return enum(v1alpha1.SecretsEnv, v1alpha1.SecretsDotenv)
	case reflect.TypeFor[v1alpha1.OTLPProtocol]():
		return enum(v1alpha1.OTLPGRPC, v1alpha1.OTLPHTTPProtobuf)
	case reflect.TypeFor[v1alpha1.RegistryType]():
		return enum(v1alpha1.RegistryNPM, v1alpha1.RegistryPyPI)
	case reflect.TypeFor[v1alpha1.ArgumentType]():
		return enum(v1alpha1.ArgumentPositional, v1alpha1.ArgumentNamed)
	case reflect.TypeFor[v1alpha1.RemoteType]():
		return enum(v1alpha1.RemoteStreamableHTTP, v1alpha1.RemoteSSE)
	}
	return nil
}

func enum[T ~string](values ...T) *jsonschema.Schema {
	s := &jsonschema.Schema{Type: "string"}
	for _, v := range values {
		s.Enum = append(s.Enum, string(v))
	}
	return s
}

// extend adds the constraints struct tags can't express.
func extend(s *jsonschema.Schema, kind string) {
	setProp(s, "apiVersion", &jsonschema.Schema{Const: v1alpha1.APIVersion, Description: "API version of this resource."})
	setProp(s, "kind", &jsonschema.Schema{Const: kind, Description: "Kind of this resource."})

	if d := s.Definitions["ObjectMeta"]; d != nil {
		if labels, ok := d.Properties.Get("labels"); ok {
			labels.PropertyNames = &jsonschema.Schema{Pattern: labelKey, MaxLength: ptr(uint64(253))}
			labels.AdditionalProperties = &jsonschema.Schema{Type: "string", MaxLength: ptr(uint64(63))}
		}
	}
	for _, name := range []string{"AgentSpec", "AgentOverride"} {
		if d := s.Definitions[name]; d != nil {
			if p, ok := d.Properties.Get("providers"); ok {
				p.PropertyNames = &jsonschema.Schema{Pattern: namePattern}
			}
		}
	}
	if d := s.Definitions["EnvironmentSpec"]; d != nil {
		if p, ok := d.Properties.Get("overrides"); ok {
			p.PropertyNames = &jsonschema.Schema{Pattern: namePattern}
		}
	}
	if d := s.Definitions["NetworkSpec"]; d != nil {
		if p, ok := d.Properties.Get("allow"); ok {
			p.Items = &jsonschema.Schema{Type: "string", Pattern: `^(\*\.)?[A-Za-z0-9]([-A-Za-z0-9]*[A-Za-z0-9])?(\.[A-Za-z0-9]([-A-Za-z0-9]*[A-Za-z0-9])?)*$`}
		}
	}
	if d := s.Definitions["McpServerSpec"]; d != nil {
		d.AnyOf = []*jsonschema.Schema{{Required: []string{"packages"}}, {Required: []string{"remotes"}}}
	}
	if d := s.Definitions["CredentialTarget"]; d != nil {
		d.OneOf = []*jsonschema.Schema{{Required: []string{"mcpServer"}}, {Required: []string{"env"}}}
	}
	if d := s.Definitions["KeyValue"]; d != nil {
		d.OneOf = []*jsonschema.Schema{{Required: []string{"value"}}, {Required: []string{"secretRef"}}}
	}
	// OutputSchema decodes from a string or an object, which reflection
	// can't see.
	if _, ok := s.Definitions["OutputSchema"]; ok {
		s.Definitions["OutputSchema"] = &jsonschema.Schema{OneOf: []*jsonschema.Schema{
			{Type: "string", MinLength: ptr(uint64(1)), Description: "Path to a JSON or YAML file holding the schema."},
			{Type: "object", Description: "The JSON Schema itself."},
		}}
	}
	if d := s.Definitions["MCPArgument"]; d != nil {
		d.If = props("type", &jsonschema.Schema{Const: string(v1alpha1.ArgumentNamed)})
		d.Then = &jsonschema.Schema{Required: []string{"name"}}
	}
}

// combined builds the schema for any resource. It is self-contained: each
// kind's schema becomes a definition next to the definitions they share, so
// an editor can use the file offline, before the schema URLs are served.
func combined(perKind map[string]*jsonschema.Schema) (*jsonschema.Schema, error) {
	s := &jsonschema.Schema{
		Version:     jsonschema.Version,
		ID:          jsonschema.ID(schemas.BaseURL + schemas.CombinedFile),
		Title:       "Solaria resource",
		Description: "Any " + v1alpha1.APIVersion + " resource. The kind field selects the schema.",
		Type:        "object",
		Required:    []string{"apiVersion", "kind"},
		Definitions: jsonschema.Definitions{},
	}
	kindEnum := &jsonschema.Schema{Description: "Kind of this resource."}
	for _, k := range kinds {
		ks := *perKind[k.kind]
		for name, def := range ks.Definitions {
			if prev, ok := s.Definitions[name]; ok {
				a, _ := json.Marshal(prev)
				b, _ := json.Marshal(def)
				if !bytes.Equal(a, b) {
					return nil, fmt.Errorf("definition %s differs between kinds", name)
				}
			}
			s.Definitions[name] = def
		}
		if _, clash := s.Definitions[k.kind]; clash {
			return nil, fmt.Errorf("kind %s clashes with a definition of the same name", k.kind)
		}
		ks.Version, ks.ID, ks.Definitions = "", "", nil
		s.Definitions[k.kind] = &ks

		kindEnum.Enum = append(kindEnum.Enum, k.kind)
		s.AllOf = append(s.AllOf, &jsonschema.Schema{
			If:   withRequired(props("kind", &jsonschema.Schema{Const: k.kind}), "kind"),
			Then: &jsonschema.Schema{Ref: "#/$defs/" + k.kind},
		})
	}
	s.Properties = props(
		"apiVersion", &jsonschema.Schema{Const: v1alpha1.APIVersion, Description: "API version of this resource."},
		"kind", kindEnum,
	).Properties
	return s, nil
}

func setProp(s *jsonschema.Schema, name string, v *jsonschema.Schema) {
	s.Properties.Set(name, v)
}

// props returns a schema with the given name, schema pairs as properties.
func props(kv ...any) *jsonschema.Schema {
	s := &jsonschema.Schema{Properties: jsonschema.NewProperties()}
	for i := 0; i+1 < len(kv); i += 2 {
		s.Properties.Set(kv[i].(string), kv[i+1].(*jsonschema.Schema))
	}
	return s
}

func withRequired(s *jsonschema.Schema, names ...string) *jsonschema.Schema {
	s.Required = names
	return s
}

func encode(s *jsonschema.Schema) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		return nil, err
	}
	return []byte(strings.ReplaceAll(buf.String(), "\r\n", "\n")), nil
}

func ptr[T any](v T) *T { return &v }
