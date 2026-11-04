package config

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/renantarouco/solaria/internal/diag"
)

// toValue converts a YAML node into the JSON data model (map[string]any,
// []any, string, int64, float64, bool, nil), so it can be validated against
// the JSON Schema. It rejects YAML that has no JSON equivalent.
func toValue(n *yaml.Node, file string) (any, diag.List) {
	c := converter{file: file}
	v := c.convert(n)
	return v, c.diags
}

type converter struct {
	file  string
	diags diag.List
}

func (c *converter) errorf(n *yaml.Node, format string, args ...any) {
	c.diags = append(c.diags, diag.Errorf(c.file, pos(n), format, args...))
}

func (c *converter) convert(n *yaml.Node) any {
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil
		}
		return c.convert(n.Content[0])
	case yaml.AliasNode:
		return c.convert(n.Alias)
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, item := range n.Content {
			out = append(out, c.convert(item))
		}
		return out
	case yaml.MappingNode:
		return c.mapping(n)
	case yaml.ScalarNode:
		return c.scalar(n)
	}
	c.errorf(n, "unsupported YAML node")
	return nil
}

func (c *converter) mapping(n *yaml.Node) map[string]any {
	out := make(map[string]any, len(n.Content)/2)
	first := make(map[string]*yaml.Node, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := resolve(n.Content[i]), n.Content[i+1]
		if k.ShortTag() == "!!merge" {
			c.errorf(k, "merge keys (<<) aren't supported; repeat the fields instead")
			continue
		}
		if k.Kind != yaml.ScalarNode {
			c.errorf(k, "keys must be scalars")
			continue
		}
		// Any scalar key becomes its text, as JSON needs: a label key of
		// 1 or true means "1" or "true".
		if prev, ok := first[k.Value]; ok {
			c.errorf(k, "duplicate key %q (first at line %d)", k.Value, prev.Line)
			continue
		}
		first[k.Value] = k
		out[k.Value] = c.convert(v)
	}
	return out
}

func (c *converter) scalar(n *yaml.Node) any {
	switch n.ShortTag() {
	case "!!null":
		return nil
	case "!!bool":
		var b bool
		if err := n.Decode(&b); err != nil {
			c.errorf(n, "invalid boolean %q", n.Value)
		}
		return b
	case "!!int":
		var i int64
		if err := n.Decode(&i); err != nil {
			c.errorf(n, "integer %s is out of range", n.Value)
		}
		return i
	case "!!float":
		var f float64
		if err := n.Decode(&f); err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			c.errorf(n, "number %s can't be represented in JSON", n.Value)
			return nil
		}
		return f
	case "!!str", "!!timestamp", "!!binary":
		// Timestamps stay strings: no Solaria field is a date, and a
		// version such as 2026-01-01 must not change shape.
		return n.Value
	}
	c.errorf(n, "unsupported YAML tag %s", n.Tag)
	return nil
}

func resolve(n *yaml.Node) *yaml.Node {
	for n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	return n
}

func pos(n *yaml.Node) diag.Pos {
	if n == nil {
		return diag.Pos{}
	}
	return diag.Pos{Line: n.Line, Column: n.Column}
}

// locate follows a JSON pointer's tokens from n. It returns the value node
// at the deepest point reached, the key node that names it (nil for the root
// and for sequence items), and whether the whole path was found.
func locate(n *yaml.Node, tokens []string) (key, value *yaml.Node, found bool) {
	value = resolve(n)
	if value.Kind == yaml.DocumentNode && len(value.Content) > 0 {
		value = resolve(value.Content[0])
	}
	for _, tok := range tokens {
		switch value.Kind {
		case yaml.MappingNode:
			next := false
			for i := 0; i+1 < len(value.Content); i += 2 {
				if k := resolve(value.Content[i]); k.Value == tok {
					key, value, next = k, resolve(value.Content[i+1]), true
					break
				}
			}
			if !next {
				return key, value, false
			}
		case yaml.SequenceNode:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(value.Content) {
				return key, value, false
			}
			key, value = nil, resolve(value.Content[i])
		default:
			return key, value, false
		}
	}
	return key, value, true
}

// fieldPath renders JSON pointer tokens as spec.tools.mcp[0].ref, using the
// YAML tree to tell sequence indexes from keys.
func fieldPath(n *yaml.Node, tokens []string) string {
	var b strings.Builder
	cur := resolve(n)
	if cur.Kind == yaml.DocumentNode && len(cur.Content) > 0 {
		cur = resolve(cur.Content[0])
	}
	for _, tok := range tokens {
		if cur != nil && cur.Kind == yaml.SequenceNode {
			fmt.Fprintf(&b, "[%s]", tok)
		} else {
			if b.Len() > 0 {
				b.WriteByte('.')
			}
			b.WriteString(tok)
		}
		if cur != nil {
			_, next, ok := locate(cur, []string{tok})
			if ok {
				cur = next
			} else {
				cur = nil
			}
		}
	}
	return b.String()
}
