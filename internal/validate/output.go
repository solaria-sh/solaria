package validate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/solaria-sh/solaria/internal/config"
	"github.com/solaria-sh/solaria/internal/diag"
	"github.com/solaria-sh/solaria/internal/resolve"
)

// outputURL names the agent's output schema inside the compiler. It's never
// fetched.
const outputURL = "solaria:///output.json"

// outputSchema checks an agent's spec.output.schema (ADR 0019): the file
// exists and parses, the schema is valid JSON Schema (draft 2020-12 unless
// it says otherwise), it's self-contained, and its root is an object
// schema.
func outputSchema(r *config.Resource) diag.List {
	o := r.Agent().Spec.Output
	if o == nil {
		return nil
	}
	p := r.Pos("spec", "output", "schema")
	fail := func(format string, args ...any) diag.List {
		return diag.List{diag.Errorf(r.File, p, "spec.output.schema: %s", fmt.Sprintf(format, args...))}
	}

	if f := o.Schema.File; f != "" {
		if err := regularFile(r.Dir(), f); err != nil {
			return fail("%v", err)
		}
		// Problems with the schema itself name the file it's in.
		inner := fail
		fail = func(format string, args ...any) diag.List {
			return inner("%s: %s", f, fmt.Sprintf(format, args...))
		}
	}
	doc, err := resolve.OutputSchema(r.Dir(), o.Schema)
	if err != nil {
		return fail("%v", err)
	}
	if err := compileOutputSchema(doc); err != nil {
		return fail("%v", err)
	}
	m, ok := doc.(map[string]any)
	if !ok {
		return fail("must be a JSON Schema object")
	}
	if t, _ := m["type"].(string); t != "object" {
		return fail("the root must be an object schema (type: object), since the answer is one JSON object")
	}
	return nil
}

// compileOutputSchema compiles doc as a JSON Schema. It never loads
// anything: a $ref to another document is an error, since no host would
// resolve it.
func compileOutputSchema(doc any) error {
	b, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	// The compiler wants numbers as json.Number, which its own decoder gives.
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return err
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.UseLoader(noLoader{})
	if err := c.AddResource(outputURL, v); err != nil {
		return err
	}
	if _, err := c.Compile(outputURL); err != nil {
		var load *jsonschema.LoadURLError
		if errors.As(err, &load) {
			return fmt.Errorf("$ref %s points outside the schema; output schemas must be self-contained", load.URL)
		}
		return fmt.Errorf("not a valid JSON Schema: %s", metaschemaError(err))
	}
	return nil
}

// noLoader refuses every URL the compiler asks for.
type noLoader struct{}

func (noLoader) Load(string) (any, error) {
	return nil, fs.ErrNotExist
}

// metaschemaError reports the first root cause of a schema that fails its
// metaschema, rather than the validator's whole tree.
func metaschemaError(err error) string {
	var se *jsonschema.SchemaValidationError
	if errors.As(err, &se) {
		err = se.Err
	}
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return strings.Join(strings.Fields(err.Error()), " ")
	}
	for len(ve.Causes) > 0 {
		ve = ve.Causes[0]
	}
	msg := ve.ErrorKind.LocalizedString(message.NewPrinter(language.English))
	if len(ve.InstanceLocation) == 0 {
		return msg
	}
	return fmt.Sprintf("at %s: %s", strings.Join(ve.InstanceLocation, "."), msg)
}
