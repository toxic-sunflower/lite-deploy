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
			t.Errorf("%q: ожидали ошибку", in)
		}
	}
}

func TestCanonicalKeyOrderUTF16(t *testing.T) {
	// суррогатная пара U+1F600 (0xD83D…) идёт раньше U+FF5E (0xFF5E) по кодовым единицам UTF-16
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
		t.Fatal("хэш не изменился после правки описания шага")
	}
	pl.Steps[3].Summary = pl.Steps[3].Summary[:len(pl.Steps[3].Summary)-1]
	h, _ = pl.ComputeHash()
	if h != base {
		t.Fatal("хэш должен вернуться после отката правки")
	}
	pl.Hash = "sha256:00"
	h, _ = pl.ComputeHash()
	if h != base {
		t.Fatal("поле hash не должно входить в хэш")
	}
}

func TestHashStableUnderFieldReorder(t *testing.T) {
	a, _ := Canonical([]byte(`{"x":1,"y":{"b":2,"a":1}}`))
	b, _ := Canonical([]byte(`{"y":{"a":1,"b":2},"x":1}`))
	if HashBytes(a) != HashBytes(b) {
		t.Fatal("хэш зависит от порядка полей")
	}
}
