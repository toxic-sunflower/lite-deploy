// Package schema validates Go structs and builds a JSON Schema from them by tags.
//
// The same tags (pattern, enum, min, max, ...) serve both for validation and for
// schemas, so the schema and Validate do not diverge (SPEC-v0.md, section 0).
package schema

import (
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Issue describes one error found (SPEC-v0.md, 2.2).
type Issue struct {
	Path    string `json:"path" maxLen:"512" doc:"JSON Pointer to the field; slash for the whole document"`
	Code    string `json:"code" maxLen:"32" doc:"error code"`
	Message string `json:"message" maxLen:"1024" doc:"human-readable description"`
}

type rules struct {
	pattern    string
	enum       []string
	min, max   *int
	maxLen     *int
	minItems   *int
	maxItems   *int
	minProps   *int
	maxProps   *int
	keyPattern string
	valPattern string
	valMaxLen  *int
	doc        string
}

func intTag(tag reflect.StructTag, name string) *int {
	s, ok := tag.Lookup(name)
	if !ok {
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		panic(fmt.Sprintf("schema: tag %s=%q is not a number", name, s))
	}
	return &n
}

func parseRules(tag reflect.StructTag) rules {
	r := rules{
		pattern:    tag.Get("pattern"),
		keyPattern: tag.Get("keypattern"),
		valPattern: tag.Get("valpattern"),
		doc:        tag.Get("doc"),
		min:        intTag(tag, "min"),
		max:        intTag(tag, "max"),
		maxLen:     intTag(tag, "maxLen"),
		minItems:   intTag(tag, "minItems"),
		maxItems:   intTag(tag, "maxItems"),
		minProps:   intTag(tag, "minProps"),
		maxProps:   intTag(tag, "maxProps"),
		valMaxLen:  intTag(tag, "valMaxLen"),
	}
	if e := tag.Get("enum"); e != "" {
		r.enum = strings.Split(e, ",")
	}
	return r
}

func jsonName(f reflect.StructField) (name string, required, skip bool) {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return "", false, true
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	if name == "" {
		name = f.Name
	}
	required = true
	for _, p := range parts[1:] {
		if p == "omitempty" || p == "omitzero" {
			required = false
		}
	}
	return name, required, false
}

var reCache = map[string]*regexp.Regexp{}

func re(p string) *regexp.Regexp {
	if r, ok := reCache[p]; ok {
		return r
	}
	r := regexp.MustCompile(p)
	reCache[p] = r
	return r
}

func ptr(parent, key string) string {
	key = strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
	return parent + "/" + key
}

// Check validates the value v (a struct or a pointer to a struct).
func Check(v any) []Issue {
	var out []Issue
	checkValue(reflect.ValueOf(v), rules{}, true, "", &out)
	return out
}

func add(out *[]Issue, path, code, format string, args ...any) {
	*out = append(*out, Issue{Path: path, Code: code, Message: fmt.Sprintf(format, args...)})
}

func checkValue(v reflect.Value, r rules, required bool, path string, out *[]Issue) {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			if required {
				add(out, path, "required", "the field is required")
			}
			return
		}
		checkValue(v.Elem(), r, required, path, out)
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name, req, skip := jsonName(f)
			if skip || !f.IsExported() {
				continue
			}
			checkValue(v.Field(i), parseRules(f.Tag), req, ptr(path, name), out)
		}
	case reflect.String:
		s := v.String()
		if s == "" {
			if required {
				add(out, path, "required", "the field is required")
			}
			return
		}
		checkString(s, r.pattern, r.enum, r.maxLen, path, out)
	case reflect.Int, reflect.Int64:
		n := int(v.Int())
		if n == 0 && !required {
			return
		}
		if r.min != nil && n < *r.min {
			add(out, path, "minimum", "value %d is less than %d", n, *r.min)
		}
		if r.max != nil && n > *r.max {
			add(out, path, "maximum", "value %d is greater than %d", n, *r.max)
		}
	case reflect.Slice:
		if v.Len() == 0 && !required {
			return
		}
		if r.minItems != nil && v.Len() < *r.minItems {
			add(out, path, "minItems", "%d elements, at least %d are required", v.Len(), *r.minItems)
		}
		if r.maxItems != nil && v.Len() > *r.maxItems {
			add(out, path, "maxItems", "%d elements, at most %d are allowed", v.Len(), *r.maxItems)
		}
		for i := 0; i < v.Len(); i++ {
			e := v.Index(i)
			ep := ptr(path, strconv.Itoa(i))
			if e.Kind() == reflect.String {
				checkString(e.String(), r.pattern, r.enum, r.maxLen, ep, out)
				continue
			}
			checkValue(e, r, true, ep, out)
		}
	case reflect.Map:
		if v.Len() == 0 && !required {
			return
		}
		if r.minProps != nil && v.Len() < *r.minProps {
			add(out, path, "minProperties", "%d entries, at least %d are required", v.Len(), *r.minProps)
		}
		if r.maxProps != nil && v.Len() > *r.maxProps {
			add(out, path, "maxProperties", "%d entries, at most %d are allowed", v.Len(), *r.maxProps)
		}
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		for _, k := range keys {
			kp := ptr(path, k.String())
			if r.keyPattern != "" && !re(r.keyPattern).MatchString(k.String()) {
				add(out, kp, "pattern", "the name %q does not match the pattern %s", k.String(), r.keyPattern)
			}
			e := v.MapIndex(k)
			if e.Kind() == reflect.String {
				if e.String() == "" {
					// an empty value is allowed only if the pattern lets it through
					checkStringAllowEmpty(e.String(), r.valPattern, r.valMaxLen, kp, out)
					continue
				}
				checkString(e.String(), r.valPattern, nil, r.valMaxLen, kp, out)
				continue
			}
			checkValue(e, rules{min: r.min, max: r.max, minItems: r.minItems, maxItems: r.maxItems}, true, kp, out)
		}
	}
}

func checkStringAllowEmpty(s, pattern string, maxLen *int, path string, out *[]Issue) {
	checkString(s, pattern, nil, maxLen, path, out)
}

func checkString(s, pattern string, enum []string, maxLen *int, path string, out *[]Issue) {
	if maxLen != nil && utf8.RuneCountInString(s) > *maxLen {
		add(out, path, "maxLength", "length is greater than %d", *maxLen)
	}
	if len(enum) > 0 {
		for _, e := range enum {
			if s == e {
				return
			}
		}
		add(out, path, "enum", "allowed: %s", strings.Join(enum, ", "))
		return
	}
	if pattern != "" && !re(pattern).MatchString(s) {
		add(out, path, "pattern", "the value does not match the pattern %s", pattern)
	}
}

// Reflect builds a JSON Schema (draft 2020-12) for the type of the value v.
func Reflect(v any, id, title string) map[string]any {
	s := reflectType(reflect.TypeOf(v), rules{})
	s["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	s["$id"] = id
	s["title"] = title
	return s
}

func reflectType(t reflect.Type, r rules) map[string]any {
	out := map[string]any{}
	if r.doc != "" {
		out["description"] = r.doc
	}
	switch t.Kind() {
	case reflect.Pointer:
		return reflectType(t.Elem(), r)
	case reflect.Interface:
		return out
	case reflect.Struct:
		out["type"] = "object"
		props := map[string]any{}
		var req []string
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name, required, skip := jsonName(f)
			if skip || !f.IsExported() {
				continue
			}
			props[name] = reflectType(f.Type, parseRules(f.Tag))
			if required {
				req = append(req, name)
			}
		}
		out["properties"] = props
		if len(req) > 0 {
			out["required"] = req
		}
		out["additionalProperties"] = false
	case reflect.String:
		out["type"] = "string"
		if r.pattern != "" {
			out["pattern"] = r.pattern
		}
		if len(r.enum) > 0 {
			out["enum"] = r.enum
		}
		if r.maxLen != nil {
			out["maxLength"] = *r.maxLen
		}
	case reflect.Int, reflect.Int64:
		out["type"] = "integer"
		if r.min != nil {
			out["minimum"] = *r.min
		}
		if r.max != nil {
			out["maximum"] = *r.max
		}
	case reflect.Bool:
		out["type"] = "boolean"
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 { // json.RawMessage
			return out
		}
		out["type"] = "array"
		out["items"] = reflectType(t.Elem(), rules{pattern: r.pattern, enum: r.enum, maxLen: r.maxLen, min: r.min, max: r.max})
		if r.minItems != nil {
			out["minItems"] = *r.minItems
		}
		if r.maxItems != nil {
			out["maxItems"] = *r.maxItems
		}
	case reflect.Map:
		out["type"] = "object"
		out["additionalProperties"] = reflectType(t.Elem(), rules{pattern: r.valPattern, maxLen: r.valMaxLen, min: r.min, max: r.max, minItems: r.minItems, maxItems: r.maxItems})
		if r.keyPattern != "" {
			out["propertyNames"] = map[string]any{"pattern": r.keyPattern}
		}
		if r.minProps != nil {
			out["minProperties"] = *r.minProps
		}
		if r.maxProps != nil {
			out["maxProperties"] = *r.maxProps
		}
	default:
		panic("schema: unsupported type " + t.String())
	}
	return out
}
