package eventbus

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"unicode/utf8"
)

const maxSafeInteger = 1<<53 - 1

var ErrUnsupportedValue = errors.New("value cannot be canonicalised")

// CanonicalJSON must produce the same bytes as canonicalJson in the TypeScript
// eventbus library. The value is first reduced to what json.Marshal puts on
// the wire, so typed Go values and decoded payloads sign identically.
func CanonicalJSON(v any) ([]byte, error) {
	wire, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupportedValue, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(wire))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupportedValue, err)
	}
	var out bytes.Buffer
	if err := writeCanonical(&out, decoded); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func writeCanonical(out *bytes.Buffer, value any) error {
	switch typed := value.(type) {
	case nil:
		out.WriteString("null")
	case bool:
		out.WriteString(strconv.FormatBool(typed))
	case string:
		return writeCanonicalString(out, typed)
	case json.Number:
		return writeCanonicalNumber(out, typed)
	case []any:
		return writeCanonicalArray(out, typed)
	case map[string]any:
		return writeCanonicalObject(out, typed)
	default:
		return fmt.Errorf("%w: %T", ErrUnsupportedValue, value)
	}
	return nil
}

func writeCanonicalString(out *bytes.Buffer, s string) error {
	if !utf8.ValidString(s) {
		return fmt.Errorf("%w: invalid utf-8", ErrUnsupportedValue)
	}
	out.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			out.WriteString(`\"`)
		case r == '\\':
			out.WriteString(`\\`)
		case r < 0x20:
			fmt.Fprintf(out, `\u%04x`, r)
		default:
			out.WriteRune(r)
		}
	}
	out.WriteByte('"')
	return nil
}

func writeCanonicalNumber(out *bytes.Buffer, n json.Number) error {
	f, err := strconv.ParseFloat(n.String(), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return fmt.Errorf("%w: number %s", ErrUnsupportedValue, n)
	}
	if f == math.Trunc(f) {
		if math.Abs(f) > maxSafeInteger {
			return fmt.Errorf("%w: integer %s out of range", ErrUnsupportedValue, n)
		}
		out.WriteString(strconv.FormatInt(int64(f), 10))
		return nil
	}
	if magnitude := math.Abs(f); magnitude < 1e-6 || magnitude >= 1e21 {
		return fmt.Errorf("%w: number %s out of range", ErrUnsupportedValue, n)
	}
	out.WriteString(strconv.FormatFloat(f, 'f', -1, 64))
	return nil
}

func writeCanonicalArray(out *bytes.Buffer, items []any) error {
	out.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			out.WriteByte(',')
		}
		if err := writeCanonical(out, item); err != nil {
			return err
		}
	}
	out.WriteByte(']')
	return nil
}

func writeCanonicalObject(out *bytes.Buffer, object map[string]any) error {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			out.WriteByte(',')
		}
		if err := writeCanonicalString(out, key); err != nil {
			return err
		}
		out.WriteByte(':')
		if err := writeCanonical(out, object[key]); err != nil {
			return err
		}
	}
	out.WriteByte('}')
	return nil
}
