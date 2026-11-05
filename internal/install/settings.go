package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"

	"github.com/lllypuk/plait/internal/agent"
	"github.com/lllypuk/plait/internal/output"
)

// PermissionWriter turns the abstract deny commands of a descriptor into operations on its settings file.
type PermissionWriter interface {
	Plan(path string, cmds []string) ([]Op, error)
	Remove(path string, cmds []string) ([]Op, error)
}

// Writer returns the permission writer registered under name.
func Writer(name string) (PermissionWriter, bool) {
	w, ok := writers()[name]
	return w, ok
}

func writers() map[string]PermissionWriter {
	return map[string]PermissionWriter{
		agent.WriterClaudeSettings: settingsWriter{rule: func(cmd string) string { return "Bash(" + cmd + ":*)" }},
		agent.WriterQwenSettings:   settingsWriter{rule: func(cmd string) string { return "Bash(" + cmd + " *)" }},
		agent.WriterNone:           noneWriter{},
	}
}

type noneWriter struct{}

func (noneWriter) Plan(string, []string) ([]Op, error) {
	return []Op{{Action: ActionWarn, Detail: "the agent has no setting to deny commands; " +
		"plait approve and plait reject still require a terminal"}}, nil
}

func (noneWriter) Remove(string, []string) ([]Op, error) { return nil, nil }

// settingsWriter keeps rules in permissions.deny of a JSON settings file and leaves every other key as it was.
type settingsWriter struct {
	rule func(cmd string) string
}

func (w settingsWriter) rules(cmds []string) []string {
	out := make([]string, len(cmds))
	for i, c := range cmds {
		out[i] = w.rule(c)
	}
	return out
}

func (w settingsWriter) Plan(path string, cmds []string) ([]Op, error) {
	rules := w.rules(cmds)
	doc, mode, err := readSettings(path)
	if err != nil {
		return nil, err
	}
	op := Op{Action: ActionUpdate, Path: path, mode: mode}
	if doc == nil {
		doc, op.Action, op.mode = &object{}, ActionCreate, fileMode
	}
	perms, err := child(doc, "permissions", path)
	if err != nil {
		return nil, err
	}
	deny, err := denyList(perms, path)
	if err != nil {
		return nil, err
	}
	var added []string
	for _, r := range rules {
		// slices.Contains would panic comparing a nested array of the user's settings.
		if !slices.ContainsFunc(deny, func(v any) bool { s, ok := v.(string); return ok && s == r }) {
			deny = append(deny, r)
			added = append(added, r)
		}
	}
	if len(added) == 0 {
		return []Op{{Action: ActionSkip, Path: path}}, nil
	}
	perms.set("deny", deny)
	op.Detail = "deny " + strings.Join(added, ", ")
	if op.content, err = encode(doc); err != nil {
		return nil, output.Internal(err)
	}
	return []Op{op}, nil
}

func (w settingsWriter) Remove(path string, cmds []string) ([]Op, error) {
	rules := w.rules(cmds)
	doc, mode, err := readSettings(path)
	if err != nil {
		return nil, err
	}
	skip := []Op{{Action: ActionSkip, Path: path}}
	if doc == nil {
		return skip, nil
	}
	perms, ok := doc.get("permissions").(*object)
	if !ok {
		return skip, nil
	}
	deny, ok := perms.get("deny").([]any)
	if !ok {
		return skip, nil
	}
	var removed []string
	kept := slices.DeleteFunc(slices.Clone(deny), func(v any) bool {
		s, isStr := v.(string)
		if isStr && slices.Contains(rules, s) {
			removed = append(removed, s)
			return true
		}
		return false
	})
	if len(removed) == 0 {
		return skip, nil
	}
	if len(kept) > 0 {
		perms.set("deny", kept)
	} else {
		perms.del("deny")
	}
	if len(perms.keys) == 0 {
		doc.del("permissions")
	}
	op := Op{Action: ActionUpdate, Path: path, mode: mode, Detail: "drop " + strings.Join(removed, ", ")}
	if op.content, err = encode(doc); err != nil {
		return nil, output.Internal(err)
	}
	return []Op{op}, nil
}

// readSettings returns nil for a missing file.
func readSettings(path string) (*object, fs.FileMode, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, output.Internal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, output.Internal(err)
	}
	v, err := decode(b)
	if err != nil {
		return nil, 0, settingsError(path, "is not valid JSON: "+err.Error())
	}
	o, ok := v.(*object)
	if !ok {
		return nil, 0, settingsError(path, "is not a JSON object")
	}
	return o, info.Mode().Perm(), nil
}

func child(o *object, key, path string) (*object, error) {
	switch v := o.get(key).(type) {
	case nil:
		c := &object{}
		o.set(key, c)
		return c, nil
	case *object:
		return v, nil
	default:
		return nil, settingsError(path, key+" is not a JSON object")
	}
}

func denyList(perms *object, path string) ([]any, error) {
	switch v := perms.get("deny").(type) {
	case nil:
		return []any{}, nil
	case []any:
		return v, nil
	default:
		return nil, settingsError(path, "permissions.deny is not a JSON array")
	}
}

func settingsError(path, problem string) error {
	return output.Invalid("settings_invalid", fmt.Sprintf("%s %s; plait left it unchanged", path, problem),
		fmt.Sprintf("fix %s by hand or move it away, then rerun the command", path))
}

// object is a JSON object that keeps its key order.
type object struct {
	keys []string
	vals map[string]any
}

func (o *object) get(k string) any { return o.vals[k] }

func (o *object) set(k string, v any) {
	if o.vals == nil {
		o.vals = map[string]any{}
	}
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *object) del(k string) {
	delete(o.vals, k)
	o.keys = slices.DeleteFunc(o.keys, func(s string) bool { return s == k })
}

// decode parses JSON into *object, []any, string, json.Number, bool and nil.
func decode(b []byte) (any, error) {
	if !json.Valid(b) {
		var v any
		return nil, json.Unmarshal(b, &v)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	return decodeValue(dec)
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch tok {
	case json.Delim('{'):
		o := &object{}
		for dec.More() {
			kt, kerr := dec.Token()
			if kerr != nil {
				return nil, kerr
			}
			v, verr := decodeValue(dec)
			if verr != nil {
				return nil, verr
			}
			k, _ := kt.(string) // json.Valid passed, so an object key is a string
			o.set(k, v)
		}
		_, err = dec.Token()
		return o, err
	case json.Delim('['):
		arr := []any{}
		for dec.More() {
			v, verr := decodeValue(dec)
			if verr != nil {
				return nil, verr
			}
			arr = append(arr, v)
		}
		_, err = dec.Token()
		return arr, err
	}
	return tok, nil
}

// encode writes v indented by two spaces with a final newline.
func encode(v any) ([]byte, error) {
	var b bytes.Buffer
	if err := encodeValue(&b, v, ""); err != nil {
		return nil, err
	}
	b.WriteByte('\n')
	return b.Bytes(), nil
}

func encodeValue(b *bytes.Buffer, v any, indent string) error {
	inner := indent + "  "
	switch t := v.(type) {
	case *object:
		if len(t.keys) == 0 {
			b.WriteString("{}")
			return nil
		}
		b.WriteString("{\n")
		for i, k := range t.keys {
			b.WriteString(inner)
			if err := scalar(b, k); err != nil {
				return err
			}
			b.WriteString(": ")
			if err := encodeValue(b, t.vals[k], inner); err != nil {
				return err
			}
			separator(b, i, len(t.keys))
		}
		b.WriteString(indent + "}")
	case []any:
		if len(t) == 0 {
			b.WriteString("[]")
			return nil
		}
		b.WriteString("[\n")
		for i, e := range t {
			b.WriteString(inner)
			if err := encodeValue(b, e, inner); err != nil {
				return err
			}
			separator(b, i, len(t))
		}
		b.WriteString(indent + "]")
	default:
		return scalar(b, v)
	}
	return nil
}

func separator(b *bytes.Buffer, i, n int) {
	if i < n-1 {
		b.WriteByte(',')
	}
	b.WriteByte('\n')
}

// scalar keeps <, > and & as they are: settings hold shell commands.
func scalar(b *bytes.Buffer, v any) error {
	var s bytes.Buffer
	enc := json.NewEncoder(&s)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	b.Write(bytes.TrimSuffix(s.Bytes(), []byte("\n")))
	return nil
}
