package config

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"go.yaml.in/yaml/v3"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
	"github.com/solaria-sh/solaria/internal/diag"
	"github.com/solaria-sh/solaria/schemas"
)

type compiled struct {
	byKind map[string]*jsonschema.Schema
	docs   map[string]any // raw schema documents by URL, for suggestions
}

var loadSchemas = sync.OnceValues(func() (*compiled, error) {
	c := jsonschema.NewCompiler()
	out := &compiled{byKind: map[string]*jsonschema.Schema{}, docs: map[string]any{}}
	fsys := schemas.V1alpha1()
	names, err := fs.Glob(fsys, "*.json")
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		f, err := fsys.Open(name)
		if err != nil {
			return nil, err
		}
		doc, err := jsonschema.UnmarshalJSON(f)
		_ = f.Close()
		if err != nil {
			return nil, fmt.Errorf("schema %s: %w", name, err)
		}
		url := schemas.BaseURL + name
		if err := c.AddResource(url, doc); err != nil {
			return nil, fmt.Errorf("schema %s: %w", name, err)
		}
		out.docs[url] = doc
	}
	for _, k := range v1alpha1.Kinds {
		s, err := c.Compile(schemas.BaseURL + schemas.File(k))
		if err != nil {
			return nil, fmt.Errorf("compile schema for %s: %w", k, err)
		}
		out.byKind[k] = s
	}
	return out, nil
})

var printer = message.NewPrinter(language.English)

// messages replaces the validator's generic text for the combinators in our
// schemas, keyed by the end of the failing keyword's location.
var messages = map[string]string{
	"/$defs/KeyValue/oneOf":      "set exactly one of value and secretRef",
	"/$defs/McpServerSpec/anyOf": "set packages, remotes, or both",
	"/$defs/OutputSchema/oneOf":  "must be a path to a JSON or YAML file, or a JSON Schema object",
}

// patterns explains the patterns in our schemas in words, keyed like
// messages. The message is "<value> <explanation>".
var patterns = map[string]string{
	"/$defs/ObjectMeta/properties/name/pattern":              "is not a valid name: use lowercase letters, digits and hyphens, starting and ending with a letter or digit",
	"/$defs/ModelSpec/properties/preferred/pattern":          "is not provider:model, such as anthropic:claude-sonnet-5-5",
	"/$defs/ModelSpec/properties/fallback/items/pattern":     "is not provider:model, such as anthropic:claude-sonnet-5-5",
	"/$defs/ModelOverride/properties/preferred/pattern":      "is not provider:model, such as anthropic:claude-sonnet-5-5",
	"/$defs/ModelOverride/properties/fallback/items/pattern": "is not provider:model, such as anthropic:claude-sonnet-5-5",
	"/$defs/LimitsSpec/properties/timeout/pattern":           "is not a duration such as 30m or 1h30m",
	"/$defs/ResourcesSpec/properties/memory/pattern":         "is not a size such as 512Mi or 8Gi",
	"/$defs/ResourcesSpec/properties/disk/pattern":           "is not a size such as 512Mi or 8Gi",
	"/$defs/KeyValue/properties/secretRef/pattern":           "is not a secret name: use UPPER_SNAKE_CASE",
	"/$defs/NetworkSpec/properties/allow/items/pattern":      "is not a host name, such as example.com or *.example.com",
	"/$defs/EnvironmentSpec/properties/provider/pattern":     "is not a valid provider name",
	"/$defs/EnvironmentSpec/properties/placement/pattern":    "is not a valid placement name",
}

func keywordLocation(e *jsonschema.ValidationError) string {
	return e.SchemaURL + "/" + strings.Join(e.ErrorKind.KeywordPath(), "/")
}

// schemaDiags turns a validation error into diagnostics, one per root cause,
// placed on the YAML node at fault.
func (c *compiled) schemaDiags(err error, file string, root *yaml.Node) diag.List {
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return diag.List{diag.Errorf(file, pos(root), "%v", err)}
	}
	var out diag.List
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		switch e.ErrorKind.(type) {
		case *kind.OneOf, *kind.AnyOf:
			// Report the combinator itself: its branches' failures read as
			// contradictions ("missing value", "missing secretRef").
			out = append(out, c.leaf(e, file, root)...)
			return
		}
		if len(e.Causes) == 0 {
			out = append(out, c.leaf(e, file, root)...)
			return
		}
		for _, cause := range e.Causes {
			walk(cause)
		}
	}
	walk(ve)
	return out
}

func (c *compiled) leaf(e *jsonschema.ValidationError, file string, root *yaml.Node) diag.List {
	loc := e.InstanceLocation
	_, node, _ := locate(root, loc)
	where := fieldPath(root, loc)
	prefix := ""
	if where != "" {
		prefix = where + ": "
	}

	switch k := e.ErrorKind.(type) {
	case *kind.AdditionalProperties:
		allowed := c.allowedProperties(e.SchemaURL)
		var out diag.List
		for _, p := range k.Properties {
			key, _, _ := locate(root, append(slices.Clip(loc), p))
			msg := fmt.Sprintf("%sunknown field %q", prefix, p)
			if s := suggest(p, allowed); s != "" {
				msg += fmt.Sprintf("; did you mean %q?", s)
			}
			out = append(out, diag.Errorf(file, pos(key), "%s", msg))
		}
		return out
	case *kind.Required:
		var out diag.List
		for _, m := range k.Missing {
			out = append(out, diag.Errorf(file, pos(node), "%smissing required field %q", prefix, m))
		}
		return out
	}

	msg := e.ErrorKind.LocalizedString(printer)
	kwLoc := keywordLocation(e)
	if ty, ok := e.ErrorKind.(*kind.Type); ok && slices.Contains(ty.Want, "string") &&
		node != nil && node.Kind == yaml.ScalarNode && node.ShortTag() != "!!str" && node.Value != "" && node.Value != "~" {
		// YAML reads 1.10, true, 2 and null as numbers, booleans and
		// null, which is rarely what a version, label or name means.
		msg = fmt.Sprintf("%s is a %s in YAML; quote it (%q) to make it a string", node.Value, ty.Got, node.Value)
	}
	if m, ok := e.ErrorKind.(*kind.MinItems); ok {
		msg = "needs at least " + plural(m.Want, "item")
	}
	if p, ok := e.ErrorKind.(*kind.Pattern); ok {
		for suffix, text := range patterns {
			if strings.HasSuffix(kwLoc, suffix) {
				msg = fmt.Sprintf("%q %s", p.Got, text)
				break
			}
		}
	}
	for suffix, text := range messages {
		if strings.HasSuffix(kwLoc, suffix) {
			msg = text
			break
		}
	}
	return diag.List{diag.Errorf(file, pos(node), "%s%s", prefix, msg)}
}

// allowedProperties lists the properties of the schema object that owns a
// failing additionalProperties keyword.
func (c *compiled) allowedProperties(schemaURL string) []string {
	url, frag, _ := strings.Cut(schemaURL, "#")
	cur := c.docs[url]
	frag = strings.TrimSuffix(frag, "/additionalProperties")
	for _, tok := range strings.Split(strings.TrimPrefix(frag, "/"), "/") {
		if tok == "" {
			continue
		}
		tok = strings.NewReplacer("~1", "/", "~0", "~").Replace(tok)
		switch v := cur.(type) {
		case map[string]any:
			cur = v[tok]
		case []any:
			i, err := strconv.Atoi(tok)
			if err != nil || i >= len(v) {
				return nil
			}
			cur = v[i]
		default:
			return nil
		}
	}
	obj, _ := cur.(map[string]any)
	props, _ := obj["properties"].(map[string]any)
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// suggest returns the allowed name closest to got, if it is close enough to
// be a likely typo.
func suggest(got string, allowed []string) string {
	best, bestDist := "", 3
	for _, a := range allowed {
		if strings.EqualFold(a, got) {
			return a
		}
		if d := levenshtein(strings.ToLower(got), strings.ToLower(a)); d < bestDist {
			best, bestDist = a, d
		}
	}
	return best
}

func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}
