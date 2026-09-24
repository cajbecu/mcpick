package spec

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// TopLevelOrder reads the key order of a JSON object without decoding its
// values. Go maps are unordered, so rewriting ~/.claude.json through one would
// reshuffle a file that is mostly somebody else's state.
func TopLevelOrder(data []byte) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("not a JSON object")
	}
	var order []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("unexpected key token %v", tok)
		}
		order = append(order, key)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// MarshalOrdered writes top as an indented JSON object, keys in order first and
// any others after them sorted. Values are re-indented, not re-encoded, so their
// content — escaping, number formatting — is exactly what was read.
func MarshalOrdered(top map[string]json.RawMessage, order []string) ([]byte, error) {
	if len(top) == 0 {
		return []byte("{}\n"), nil
	}
	var b bytes.Buffer
	b.WriteString("{\n")
	written := 0
	emit := func(k string) error {
		raw, ok := top[k]
		if !ok {
			return nil
		}
		// A value produced by MarshalOrdered itself ends in a newline, which
		// json.Indent would keep and leave a lone comma on the next line.
		raw = bytes.TrimSpace(raw)
		if written > 0 {
			b.WriteString(",\n")
		}
		key, err := marshalString(k)
		if err != nil {
			return err
		}
		b.WriteString("  ")
		b.Write(key)
		b.WriteString(": ")
		var indented bytes.Buffer
		if err := json.Indent(&indented, raw, "  ", "  "); err != nil {
			b.Write(raw)
		} else {
			b.Write(indented.Bytes())
		}
		written++
		return nil
	}
	seen := map[string]bool{}
	for _, k := range order {
		seen[k] = true
		if err := emit(k); err != nil {
			return nil, err
		}
	}
	for _, k := range SortedKeys(top) {
		if seen[k] {
			continue
		}
		if err := emit(k); err != nil {
			return nil, err
		}
	}
	b.WriteString("\n}\n")
	return b.Bytes(), nil
}

func marshalString(s string) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}
