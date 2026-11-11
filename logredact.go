package nilda

import (
	"encoding"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
)

// WHAT THE LOGGER NEVER WRITES. A payment's tokens — a kept card's (PaymentSavedToken, PaymentSaved,
// PaymentForgetRequest), a delegated credential (PaymentCredential), a client secret for the payer's browser
// (ClientAction) — each print as "[redacted]" through fmt. But the logger writes JSON, and json.Marshal reads the
// fields, not String: `core.Log().Debug("start", "req", in)` with a PaymentStartRequest put the saved card's token
// and the client secret in the operator's log in clear (the 2026-11-11 review of the SDK, finding 3).
//
// So an attribute whose type holds one of them, at any depth, is rewritten before it is marshalled: each such value
// becomes its String, and everything around it keeps its JSON shape (field names by their json tags, omitempty and
// "-" honoured). An attribute whose type holds none is written exactly as before.

// logRedacted marks a type the logger writes as its String.
type logRedacted interface{ logRedacted() }

func (PaymentCredential) logRedacted()    {}
func (PaymentSaved) logRedacted()         {}
func (PaymentSavedToken) logRedacted()    {}
func (PaymentForgetRequest) logRedacted() {}
func (ClientAction) logRedacted()         {}

var (
	redactedType  = reflect.TypeFor[logRedacted]()
	marshalerType = reflect.TypeFor[json.Marshaler]()
	textType      = reflect.TypeFor[encoding.TextMarshaler]()
	holdsCache    sync.Map // reflect.Type -> bool
)

// redactForLog returns v, or a copy of it in which every secret-bearing value is its String.
func redactForLog(v any) any {
	if v == nil {
		return nil
	}
	rv := reflect.ValueOf(v)
	if !holdsSecret(rv.Type(), map[reflect.Type]bool{}) {
		return v
	}
	return redactValue(rv)
}

// holdsSecret reports whether a value of t can carry a redacted type anywhere inside it.
func holdsSecret(t reflect.Type, seen map[reflect.Type]bool) bool {
	if cached, ok := holdsCache.Load(t); ok {
		return cached.(bool)
	}
	if seen[t] {
		return false // a recursive type: its other paths decide
	}
	seen[t] = true
	holds := false
	switch {
	case t.Implements(redactedType) || reflect.PointerTo(t).Implements(redactedType):
		holds = true
	case t.Kind() != reflect.Interface && (t.Implements(marshalerType) || t.Implements(textType)):
		holds = false // its own MarshalJSON decides what it writes; none in this package writes a token
	default:
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			holds = holdsSecret(t.Elem(), seen)
		case reflect.Map:
			holds = holdsSecret(t.Key(), seen) || holdsSecret(t.Elem(), seen)
		case reflect.Interface:
			holds = true // decided by what it holds, value by value
		case reflect.Struct:
			for i := 0; i < t.NumField(); i++ {
				if f := t.Field(i); f.IsExported() && holdsSecret(f.Type, seen) {
					holds = true
					break
				}
			}
		}
	}
	if t.Kind() != reflect.Interface {
		holdsCache.Store(t, holds)
	}
	return holds
}

// redactValue is v as the logger writes it, with every redacted type replaced by its String.
func redactValue(v reflect.Value) any {
	if !v.IsValid() {
		return nil
	}
	if v.Type().Implements(redactedType) {
		if v.Kind() == reflect.Pointer && v.IsNil() {
			return nil
		}
		return stringOf(v.Interface())
	}
	if v.Kind() != reflect.Interface && !holdsSecret(v.Type(), map[reflect.Type]bool{}) {
		return v.Interface()
	}
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			return nil
		}
		return redactValue(v.Elem())
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			return nil
		}
		out := make([]any, v.Len())
		for i := range out {
			out[i] = redactValue(v.Index(i))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return nil
		}
		out := make(map[string]any, v.Len())
		it := v.MapRange()
		for it.Next() {
			out[mapKey(it.Key())] = redactValue(it.Value())
		}
		return out
	case reflect.Struct:
		out := map[string]any{}
		redactStruct(v, out)
		return out
	}
	return v.Interface()
}

// redactStruct writes v's exported fields into out under their JSON names, embedded structs inlined as
// encoding/json inlines them.
func redactStruct(v reflect.Value, out map[string]any) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		fv := v.Field(i)
		if f.Anonymous && name == "" {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				if fv.IsNil() {
					continue
				}
				fv, ft = fv.Elem(), ft.Elem()
			}
			if ft.Kind() == reflect.Struct && !ft.Implements(marshalerType) && !ft.Implements(redactedType) {
				redactStruct(fv, out)
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		if strings.Contains(","+opts+",", ",omitempty,") && emptyValue(fv) {
			continue
		}
		out[name] = redactValue(fv)
	}
}

func mapKey(k reflect.Value) string {
	if k.Kind() == reflect.String {
		return k.String()
	}
	if k.Type().Implements(textType) {
		if b, err := k.Interface().(encoding.TextMarshaler).MarshalText(); err == nil {
			return string(b)
		}
	}
	b, _ := json.Marshal(k.Interface())
	return strings.Trim(string(b), `"`)
}

func emptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Interface, reflect.Pointer:
		return v.IsNil()
	}
	return v.IsZero() && v.Kind() != reflect.Struct
}

func stringOf(v any) string {
	if s, ok := v.(interface{ String() string }); ok {
		return s.String()
	}
	return "[redacted]"
}
