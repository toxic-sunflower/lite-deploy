package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

type inner struct {
	N int `json:"n" min:"1" max:"10"`
}

type sample struct {
	Name string            `json:"name" pattern:"^[a-z]+$" maxLen:"5"`
	Kind string            `json:"kind,omitempty" enum:"a,b"`
	Ptr  *inner            `json:"ptr,omitempty"`
	List []inner           `json:"list" minItems:"1" maxItems:"2"`
	Ints []int             `json:"ints,omitempty" min:"5" max:"6" maxItems:"2"`
	Map  map[string]string `json:"map,omitempty" keypattern:"^k[0-9]$" valpattern:"^v+$" maxProps:"2"`
	Skip string            `json:"-"`
	Raw  json.RawMessage   `json:"raw,omitempty"`
	Any  any               `json:"any,omitempty"`
	Flag bool              `json:"flag,omitempty"`
}

func paths(is []Issue) string {
	var out []string
	for _, i := range is {
		out = append(out, i.Path+"|"+i.Code)
	}
	return strings.Join(out, " ")
}

func TestCheck(t *testing.T) {
	ok := sample{Name: "abc", List: []inner{{N: 1}}}
	if is := Check(&ok); len(is) != 0 {
		t.Fatalf("%v", is)
	}
	bad := sample{
		Name: "ABCDEF", Kind: "z", Ptr: &inner{N: 11},
		List: []inner{{N: 0}, {N: 1}, {N: 2}},
		Ints: []int{4, 7, 5},
		Map:  map[string]string{"x": "w", "k1": "vv", "k2": "v", "k3": "v"},
	}
	got := paths(Check(bad))
	for _, want := range []string{
		"/name|pattern", "/name|maxLength", "/kind|enum", "/ptr/n|maximum", "/list|maxItems", "/list/0/n|minimum",
		"/ints|maxItems", "/ints/0|minimum", "/ints/1|maximum", "/map|maxProperties", "/map/x|pattern",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("нет %s в %s", want, got)
		}
	}
	if is := Check(sample{}); !strings.Contains(paths(is), "/name|required") || !strings.Contains(paths(is), "/list|minItems") {
		t.Errorf("пустое значение: %s", paths(is))
	}
}

func TestPointerEscaping(t *testing.T) {
	if ptr("/a", "x/y~z") != "/a/x~1y~0z" {
		t.Fatal(ptr("/a", "x/y~z"))
	}
}

func TestReflect(t *testing.T) {
	s := Reflect(sample{}, "id1", "Sample")
	b, _ := json.Marshal(s)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if m["$id"] != "id1" || m["additionalProperties"] != false || m["type"] != "object" {
		t.Fatalf("%s", b)
	}
	props := m["properties"].(map[string]any)
	if _, has := props["Skip"]; has || props["-"] != nil {
		t.Error("поле с json:\"-\" не должно попасть в схему")
	}
	req := m["required"].([]any)
	if len(req) != 2 { // name и list
		t.Errorf("required: %v", req)
	}
	name := props["name"].(map[string]any)
	if name["pattern"] != "^[a-z]+$" || name["maxLength"] != float64(5) {
		t.Errorf("name: %v", name)
	}
	mp := props["map"].(map[string]any)
	if mp["propertyNames"].(map[string]any)["pattern"] != "^k[0-9]$" || mp["maxProperties"] != float64(2) {
		t.Errorf("map: %v", mp)
	}
	if props["ints"].(map[string]any)["items"].(map[string]any)["minimum"] != float64(5) {
		t.Errorf("ints: %v", props["ints"])
	}
}
