package plan

import "testing"

func TestCanonical(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"b":1,"a":2}`, `{"a":2,"b":1}`},
		{`{ "a" : [ 1 , 2 , { "z":null , "y":true } ] }`, `{"a":[1,2,{"y":true,"z":null}]}`},
		{`{"s":"a\u000ab\"c\\d/é<>&"}`, `{"s":"a\nb\"c\\d/é<>&"}`},
		{`{"\u20ac":1,"\r":2,"\ud83d\ude00":3,"a":4}`, "{\"\\r\":2,\"a\":4,\"€\":1,\"😀\":3}"},
		{`"\u0001"`, `"\u0001"`},
		{`[]`, `[]`},
		{`{}`, `{}`},
		{`-5`, `-5`},
	}
	for _, c := range cases {
		got, err := Canonical([]byte(c.in))
		if err != nil || string(got) != c.want {
			t.Errorf("%s\n got %s (%v)\nwant %s", c.in, got, err, c.want)
		}
	}
}

func TestCanonicalRejects(t *testing.T) {
	for _, in := range []string{`1.5`, `{"a":1e3}`, `{} {}`, `{`, ``} {
		if _, err := Canonical([]byte(in)); err == nil {
			t.Errorf("%q: expected an error", in)
		}
	}
}

func TestCanonicalKeyOrderUTF16(t *testing.T) {
	// the surrogate pair U+1F600 (0xD83D…) comes before U+FF5E (0xFF5E) by UTF-16 code units
	got, err := Canonical([]byte("{\"\uff5e\":1,\"\U0001F600\":2}"))
	if err != nil || string(got) != "{\"\U0001F600\":2,\"\uff5e\":1}" {
		t.Fatalf("%s %v", got, err)
	}
}

func TestHashChangesWithAnyEdit(t *testing.T) {
	pl, _ := goodPlan(t)
	base := pl.Hash
	pl.Steps[3].Summary += "."
	h, _ := pl.ComputeHash()
	if h == base {
		t.Fatal("the hash did not change after a step description edit")
	}
	pl.Steps[3].Summary = pl.Steps[3].Summary[:len(pl.Steps[3].Summary)-1]
	h, _ = pl.ComputeHash()
	if h != base {
		t.Fatal("the hash must return after the edit is reverted")
	}
	pl.Hash = "sha256:00"
	h, _ = pl.ComputeHash()
	if h != base {
		t.Fatal("the hash field must not be part of the hash")
	}
}

func TestHashStableUnderFieldReorder(t *testing.T) {
	a, _ := Canonical([]byte(`{"x":1,"y":{"b":2,"a":1}}`))
	b, _ := Canonical([]byte(`{"y":{"a":1,"b":2},"x":1}`))
	if HashBytes(a) != HashBytes(b) {
		t.Fatal("the hash depends on the field order")
	}
}
