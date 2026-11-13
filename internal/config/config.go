// Package config finds and loads Solaria resources from YAML files.
//
// Loading discovers YAML files, keeps the documents whose apiVersion group is
// solaria.sh, checks each against its kind's JSON Schema and decodes it into
// the v1alpha1 types. Every problem is reported as a diagnostic with a file
// and line. Checks that span resources, such as references, are in package
// validate.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/solaria-sh/solaria/internal/api/v1alpha1"
	"github.com/solaria-sh/solaria/internal/diag"
)

// Resource is one loaded document.
type Resource struct {
	Kind string
	Name string
	// File is the path the document was read from, as discovered, with
	// forward slashes on every OS.
	File string
	// Object is a *v1alpha1.Agent, *v1alpha1.Target,
	// *v1alpha1.Environment, *v1alpha1.McpServer or *v1alpha1.Vault.
	Object any

	node *yaml.Node
}

// Agent returns the resource as an Agent, or nil if it is another kind.
func (r *Resource) Agent() *v1alpha1.Agent { a, _ := r.Object.(*v1alpha1.Agent); return a }

// Target returns the resource as a Target, or nil.
func (r *Resource) Target() *v1alpha1.Target { t, _ := r.Object.(*v1alpha1.Target); return t }

// Environment returns the resource as an Environment, or nil.
func (r *Resource) Environment() *v1alpha1.Environment {
	e, _ := r.Object.(*v1alpha1.Environment)
	return e
}

// McpServer returns the resource as an McpServer, or nil.
func (r *Resource) McpServer() *v1alpha1.McpServer { m, _ := r.Object.(*v1alpha1.McpServer); return m }

// Vault returns the resource as a Vault, or nil.
func (r *Resource) Vault() *v1alpha1.Vault { v, _ := r.Object.(*v1alpha1.Vault); return v }

// ID is the resource's logical ID, such as Agent/triage.
func (r *Resource) ID() string { return r.Kind + "/" + r.Name }

// Dir is the directory relative paths in the resource are resolved from.
func (r *Resource) Dir() string { return filepath.Dir(r.File) }

// Pos returns the position of the value at path, given as field names and
// sequence indexes (for example "spec", "skills", "0"). If the path doesn't
// exist, it returns the position of the deepest part that does.
func (r *Resource) Pos(path ...string) diag.Pos {
	_, v, _ := locate(r.node, path)
	return pos(v)
}

// KeyPos is like Pos, but returns the position of the key that names the
// value, which reads better for errors about a map entry.
func (r *Resource) KeyPos(path ...string) diag.Pos {
	k, v, _ := locate(r.node, path)
	if k != nil {
		return pos(k)
	}
	return pos(v)
}

// Set is every resource loaded from a set of paths.
type Set struct {
	Resources []*Resource

	// invalid holds the IDs of documents that were found but failed to
	// load, so references to them aren't reported as missing too.
	invalid map[string]bool
}

// Exists reports whether a resource with the given kind and name was found,
// even if it failed to load.
func (s *Set) Exists(kind, name string) bool {
	return s.Lookup(kind, name) != nil || s.invalid[kind+"/"+name]
}

// Kind returns the resources of one kind, in load order.
func (s *Set) Kind(kind string) []*Resource {
	var out []*Resource
	for _, r := range s.Resources {
		if r.Kind == kind {
			out = append(out, r)
		}
	}
	return out
}

// Lookup returns the resource with the given kind and name, or nil.
func (s *Set) Lookup(kind, name string) *Resource {
	for _, r := range s.Resources {
		if r.Kind == kind && r.Name == name {
			return r
		}
	}
	return nil
}

// Load reads resources from files and directories. Directories are searched
// recursively for *.yaml and *.yml files, skipping hidden directories,
// node_modules, vendor and testdata. Discovered files that never mention
// solaria.sh/ are not parsed, so unrelated YAML can't fail a load; files
// named explicitly always are.
func Load(paths []string) (*Set, diag.List) {
	sch, err := loadSchemas()
	if err != nil {
		return nil, diag.List{diag.Errorf("", diag.Pos{}, "load schemas: %v", err)}
	}
	files, diags := discover(paths)
	set := &Set{invalid: map[string]bool{}}
	for _, f := range files {
		res, d := loadFile(sch, set, f.path, f.explicit)
		diags = append(diags, d...)
		set.Resources = append(set.Resources, res...)
	}
	diags = append(diags, duplicates(set)...)
	return set, diags
}

type found struct {
	path     string
	explicit bool
}

var skipDirs = map[string]bool{"node_modules": true, "vendor": true, "testdata": true}

func discover(paths []string) ([]found, diag.List) {
	var out []found
	var diags diag.List
	seen := map[string]bool{}
	add := func(p string, explicit bool) {
		if !seen[p] {
			seen[p] = true
			out = append(out, found{p, explicit})
		}
	}
	for _, root := range paths {
		info, err := os.Stat(root)
		if err != nil {
			diags = append(diags, diag.Errorf(root, diag.Pos{}, "%v", pathError(err)))
			continue
		}
		if !info.IsDir() {
			add(filepath.ToSlash(filepath.Clean(root)), true)
			continue
		}
		var batch []string
		err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if p != root && (strings.HasPrefix(d.Name(), ".") || skipDirs[d.Name()]) {
					return filepath.SkipDir
				}
				return nil
			}
			if ext := filepath.Ext(p); ext == ".yaml" || ext == ".yml" {
				batch = append(batch, p)
			}
			return nil
		})
		if err != nil {
			diags = append(diags, diag.Errorf(root, diag.Pos{}, "%v", pathError(err)))
		}
		slices.Sort(batch)
		for _, p := range batch {
			add(filepath.ToSlash(p), false)
		}
	}
	return out, diags
}

// pathError drops the path from an fs error, since diagnostics already
// show it, and uses one wording for a missing path on every OS.
func pathError(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return errors.New("not found")
	}
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

var marker = []byte(v1alpha1.Group + "/")

func loadFile(sch *compiled, set *Set, path string, explicit bool) ([]*Resource, diag.List) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, diag.List{diag.Errorf(path, diag.Pos{}, "%v", pathError(err))}
	}
	if !explicit && !bytes.Contains(data, marker) {
		return nil, nil
	}
	var out []*Resource
	var diags diag.List
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			diags = append(diags, syntaxError(path, err))
			break // the decoder can't recover within a file
		}
		r, d := loadDocument(sch, path, &doc)
		diags = append(diags, d...)
		if r != nil {
			out = append(out, r)
		} else if id := documentID(&doc); id != "" {
			set.invalid[id] = true
		}
	}
	return out, diags
}

var yamlLine = regexp.MustCompile(`^yaml: line (\d+): (.*)$`)

func syntaxError(path string, err error) diag.Diagnostic {
	msg := err.Error()
	if m := yamlLine.FindStringSubmatch(msg); m != nil {
		line, _ := strconv.Atoi(m[1])
		return diag.Errorf(path, diag.Pos{Line: line}, "invalid YAML: %s", m[2])
	}
	return diag.Errorf(path, diag.Pos{}, "invalid YAML: %s", strings.TrimPrefix(msg, "yaml: "))
}

func loadDocument(sch *compiled, path string, doc *yaml.Node) (*Resource, diag.List) {
	root := doc
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			return nil, nil
		}
		root = resolve(root.Content[0])
	}
	if root.Kind != yaml.MappingNode {
		return nil, nil
	}
	_, av, ok := locate(root, []string{"apiVersion"})
	if !ok || av.Kind != yaml.ScalarNode {
		return nil, nil // not a Kubernetes-style resource
	}
	group, version, _ := strings.Cut(av.Value, "/")
	if group != v1alpha1.Group {
		return nil, nil // someone else's resource
	}
	if version != v1alpha1.Version {
		return nil, diag.List{diag.Errorf(path, pos(av),
			"unsupported apiVersion %q; this version of solaria reads %s", av.Value, v1alpha1.APIVersion)}
	}
	_, kv, ok := locate(root, []string{"kind"})
	if !ok {
		return nil, diag.List{diag.Errorf(path, pos(root), "missing required field \"kind\"")}
	}
	if d := renamed(path, root, kv); d != nil {
		return nil, diag.List{*d}
	}
	if kv.Kind != yaml.ScalarNode || !slices.Contains(v1alpha1.Kinds, kv.Value) {
		return nil, diag.List{diag.Errorf(path, pos(kv),
			"unknown kind %q; %s has %s", kv.Value, v1alpha1.APIVersion, strings.Join(v1alpha1.Kinds, ", "))}
	}

	value, diags := toValue(root, path)
	if len(diags) > 0 {
		return nil, diags
	}
	if err := sch.byKind[kv.Value].Validate(value); err != nil {
		return nil, sch.schemaDiags(err, path, root)
	}

	obj := newObject(kv.Value)
	if err := decodeStrict(value, obj); err != nil {
		// The schema should have caught this; report it rather than panic.
		return nil, diag.List{diag.Errorf(path, pos(root), "decode %s: %v", kv.Value, err)}
	}
	r := &Resource{Kind: kv.Value, File: path, Object: obj, node: root}
	_, nv, _ := locate(root, []string{"metadata", "name"})
	r.Name = nv.Value
	return r, nil
}

// documentID returns Kind/name for a Solaria document that may not be valid,
// or "" if it doesn't have both.
func documentID(doc *yaml.Node) string {
	_, av, ok := locate(doc, []string{"apiVersion"})
	if !ok || !strings.HasPrefix(av.Value, v1alpha1.Group+"/") {
		return ""
	}
	_, kv, ok := locate(doc, []string{"kind"})
	if !ok || kv.Kind != yaml.ScalarNode {
		return ""
	}
	_, nv, ok := locate(doc, []string{"metadata", "name"})
	if !ok || nv.Kind != yaml.ScalarNode || nv.Value == "" {
		return ""
	}
	return kv.Value + "/" + nv.Value
}

// renamed reports a document written for the kinds before ADR 0020, when
// Environment was the target and held its sandbox, with how to fix it,
// rather than let schema validation call it unknown.
func renamed(path string, root, kind *yaml.Node) *diag.Diagnostic {
	switch kind.Value {
	case v1alpha1.KindEnvironment:
		if _, _, ok := locate(root, []string{"spec", "provider"}); ok {
			msg := "an Environment with spec.provider is now a Target (ADR 0020); change this document's kind to Target"
			if _, _, ok := locate(root, []string{"spec", "sandbox"}); ok {
				msg += ", and move spec.sandbox into an Environment that spec.environments references"
			}
			d := diag.Errorf(path, pos(kind), "%s", msg)
			return &d
		}
	case v1alpha1.KindTarget:
		if k, _, ok := locate(root, []string{"spec", "sandbox"}); ok {
			d := diag.Errorf(path, pos(k),
				"a Target has no spec.sandbox since ADR 0020; move it into the spec of an Environment, and reference that Environment from spec.environments")
			return &d
		}
	}
	return nil
}

func newObject(kind string) any {
	switch kind {
	case v1alpha1.KindAgent:
		return &v1alpha1.Agent{}
	case v1alpha1.KindTarget:
		return &v1alpha1.Target{}
	case v1alpha1.KindEnvironment:
		return &v1alpha1.Environment{}
	case v1alpha1.KindVault:
		return &v1alpha1.Vault{}
	default:
		return &v1alpha1.McpServer{}
	}
}

func decodeStrict(value, into any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	return dec.Decode(into)
}

func duplicates(set *Set) diag.List {
	var diags diag.List
	first := map[string]*Resource{}
	kept := set.Resources[:0]
	for _, r := range set.Resources {
		if prev, ok := first[r.ID()]; ok {
			p := prev.Pos("metadata", "name")
			diags = append(diags, diag.Errorf(r.File, r.Pos("metadata", "name"),
				"duplicate %s %q; first defined at %s:%d", r.Kind, r.Name, prev.File, p.Line))
			continue
		}
		first[r.ID()] = r
		kept = append(kept, r)
	}
	set.Resources = kept
	return diags
}
