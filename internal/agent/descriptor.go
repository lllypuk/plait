package agent

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/lllypuk/plait/internal/output"
)

// Schema names the descriptor format.
const Schema = "plait-agent/v1"

// Writer names of the permission settings formats.
const (
	WriterNone           = "none"
	WriterClaudeSettings = "claude-settings"
)

const (
	schemaID         = "https://github.com/lllypuk/plait/schemas/plait-agent.v1.json"
	maxSchemaReports = 3
)

//go:embed schemas/plait-agent.v1.json
var schemaDoc []byte

var compiledSchema = sync.OnceValues(func() (*jsonschema.Schema, error) { //nolint:gochecknoglobals // compiled once
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaDoc))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	if err = c.AddResource(schemaID, doc); err != nil {
		return nil, err
	}
	return c.Compile(schemaID)
})

// Writers lists the permission formats plait can write; each needs a writer in the installer.
func Writers() []string { return []string{WriterClaudeSettings, WriterNone} }

// merge returns base with over applied: nested maps merge, any other value replaces.
func merge(base, over map[string]any) map[string]any {
	out := maps.Clone(base)
	if out == nil {
		out = map[string]any{}
	}
	for k, v := range over {
		if vm, ok := v.(map[string]any); ok {
			if bm, isMap := out[k].(map[string]any); isMap {
				out[k] = merge(bm, vm)
				continue
			}
			v = merge(nil, vm)
		}
		out[k] = v
	}
	return out
}

// Merge applies over to base as config layers do for agents.<name>.
func Merge(base, over map[string]map[string]any) map[string]map[string]any {
	out := maps.Clone(base)
	if out == nil {
		out = map[string]map[string]any{}
	}
	for name, m := range over {
		out[name] = merge(out[name], m)
	}
	return out
}

// decode validates a resolved raw descriptor against the schema and fills defaults.
func decode(raw map[string]any) (Descriptor, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return Descriptor{}, err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return Descriptor{}, err
	}
	sch, err := compiledSchema()
	if err != nil {
		return Descriptor{}, fmt.Errorf("compile %s: %w", Schema, err)
	}
	if err = sch.Validate(doc); err != nil {
		if ve, ok := errors.AsType[*jsonschema.ValidationError](err); ok {
			return Descriptor{}, errors.New(schemaMessage(ve))
		}
		return Descriptor{}, err
	}
	var d Descriptor
	if err = json.Unmarshal(b, &d); err != nil {
		return Descriptor{}, err
	}
	if d.Permissions.Writer == "" {
		d.Permissions.Writer = WriterNone
	}
	if !slices.Contains(Writers(), d.Permissions.Writer) {
		return Descriptor{}, fmt.Errorf("unknown writer %q, known: %s",
			d.Permissions.Writer, strings.Join(Writers(), ", "))
	}
	if d.VersionArgs == nil {
		d.VersionArgs = []string{"--version"}
	}
	return d, nil
}

func schemaMessage(ve *jsonschema.ValidationError) string {
	p := message.NewPrinter(language.English)
	var msgs []string
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			msgs = append(msgs, "at /"+strings.Join(e.InstanceLocation, "/")+": "+e.ErrorKind.LocalizedString(p))
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	// Causes follow map order inside the validator; sorting keeps the message stable between runs.
	slices.Sort(msgs)
	return "does not match " + Schema + ": " + strings.Join(msgs[:min(len(msgs), maxSchemaReports)], "; ")
}

func configError(name string, err error) *output.Error {
	e := output.Invalid("config_invalid", fmt.Sprintf("agents.%s: %v", name, err),
		fmt.Sprintf("fix agents.%s in the plait config (fields of %s), then rerun the command", name, Schema))
	e.Err = err
	return e
}
