package nilda

import (
	"encoding"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"unsafe"
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
	if !holdsSecret(rv.Type()) {
		return v
	}
	return redactValue(rv, map[uintptr]bool{}, 0)
}

// holdsSecret reports whether a value of t can carry a redacted type anywhere inside it. Only a COMPLETE answer is
// cached: one reached while t's own walk was still open (a recursive type met again) is provisional, and caching it
// told a later question "no" for a type that does hold a token (the separate review of 2026-11-11).
func holdsSecret(t reflect.Type) bool {
	if cached, ok := holdsCache.Load(t); ok {
		return cached.(bool)
	}
	holds, _ := holdsWalk(t, map[reflect.Type]bool{})
	return holds
}

// holdsWalk is holdsSecret's walk. complete is false when the answer leaned on a type still being walked.
func holdsWalk(t reflect.Type, open map[reflect.Type]bool) (holds, complete bool) {
	if cached, ok := holdsCache.Load(t); ok {
		return cached.(bool), true
	}
	if open[t] {
		return false, false // decided by the walk that opened it
	}
	open[t] = true
	defer delete(open, t)
	complete = true
	any := func(ts ...reflect.Type) bool {
		for _, x := range ts {
			h, c := holdsWalk(x, open)
			complete = complete && c
			if h {
				return true
			}
		}
		return false
	}
	switch {
	case t.Implements(redactedType) || reflect.PointerTo(t).Implements(redactedType):
		holds = true
	case t.Kind() != reflect.Interface && (t.Implements(marshalerType) || t.Implements(textType)):
		holds = false // its own MarshalJSON decides what it writes; none in this package writes a token
	default:
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			holds = any(t.Elem())
		case reflect.Map:
			holds = any(t.Key(), t.Elem())
		case reflect.Interface:
			holds = true // decided by what it holds, value by value
		case reflect.Struct:
			for i := 0; i < t.NumField() && !holds; i++ {
				// An embedded field counts exported or not: encoding/json promotes an unexported embedded struct's
				// exported fields, and so writes a token inside one.
				if f := t.Field(i); f.IsExported() || f.Anonymous {
					holds = any(f.Type)
				}
			}
		}
	}
	if holds {
		complete = true // a token found is found, whatever is still open
	}
	if complete && t.Kind() != reflect.Interface {
		holdsCache.Store(t, holds)
	}
	return holds, complete
}

// maxRedactDepth bounds the walk: a value nested deeper is written as a marker, never walked into a stack overflow.
const maxRedactDepth = 64

// redactValue is v as the logger writes it, with every redacted type replaced by its String. A map or pointer met again
// on its own path is a cycle, written "[cycle]": json.Marshal answered one with an error the handler reported, and a
// walk without this check overflowed the stack — a crash recover cannot catch (the review of 2026-11-11).
func redactValue(v reflect.Value, path map[uintptr]bool, depth int) any {
	if !v.IsValid() {
		return nil
	}
	if depth > maxRedactDepth {
		return "[too deep]"
	}
	if v.Type().Implements(redactedType) {
		if v.Kind() == reflect.Pointer && v.IsNil() {
			return nil
		}
		return stringOf(v.Interface())
	}
	if v.Kind() != reflect.Interface && !holdsSecret(v.Type()) {
		return v.Interface()
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Map:
		if v.IsNil() {
			return nil
		}
		at := v.Pointer()
		if path[at] {
			return "[cycle]"
		}
		path[at] = true
		defer delete(path, at)
	}
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return redactValue(v.Elem(), path, depth+1)
	case reflect.Pointer:
		return redactValue(v.Elem(), path, depth+1)
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			return nil
		}
		out := make([]any, v.Len())
		for i := range out {
			out[i] = redactValue(v.Index(i), path, depth+1)
		}
		return out
	case reflect.Map:
		out := make(map[string]any, v.Len())
		it := v.MapRange()
		for it.Next() {
			out[mapKey(it.Key())] = redactValue(it.Value(), path, depth+1)
		}
		return out
	case reflect.Struct:
		// Addressable, so an unexported embedded struct inside it can be read (redactStruct): a struct held in a map or
		// an interface is not, and is copied first.
		if !v.CanAddr() {
			c := reflect.New(v.Type()).Elem()
			c.Set(v)
			v = c
		}
		out := map[string]any{}
		redactStruct(v, out, path, depth)
		return out
	}
	return v.Interface()
}

// redactStruct writes v's exported fields into out under their JSON names, embedded structs inlined as
// encoding/json inlines them.
func redactStruct(v reflect.Value, out map[string]any, path map[uintptr]bool, depth int) {
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
				if !fv.CanInterface() {
					// An unexported embedded struct, whose exported fields encoding/json promotes: reflect hands it
					// out read-only, so it is read through its address — the parent is addressable (redactValue).
					if !fv.CanAddr() {
						continue
					}
					fv = reflect.NewAt(ft, unsafe.Pointer(fv.UnsafeAddr())).Elem()
				}
				redactStruct(fv, out, path, depth+1)
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
		out[name] = redactValue(fv, path, depth+1)
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
