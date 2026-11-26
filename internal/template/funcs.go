package template

import (
	"reflect"
	"strings"
)

// funcs sind die zusätzlichen Funktionen, die in Templates (Betreff und Body)
// zur Verfügung stehen.
//
//	default   {{default "Kunde" .user_name}}  Fallback, wenn der Wert leer ist
func funcs() map[string]any {
	return map[string]any{
		"default": defaultValue,
	}
}

// defaultValue gibt def zurück, wenn v "leer" ist, sonst v.
func defaultValue(def, v any) any {
	if isBlank(v) {
		return def
	}
	return v
}

// isBlank meldet, ob v als "nicht angegeben" gilt: nil, ein leerer oder nur aus
// Leerraum bestehender String, eine leere Liste oder Map. Zahlen und Booleans
// (auch 0 und false) gelten immer als angegeben.
func isBlank(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(t) == ""
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Map, reflect.Array:
		return rv.Len() == 0
	case reflect.Pointer, reflect.Interface:
		return rv.IsNil()
	}
	return false
}
