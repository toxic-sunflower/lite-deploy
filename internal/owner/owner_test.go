package owner

import (
	"strings"
	"testing"
	"time"
)

func TestSignedRequestsAreAcceptedOnceAndOnlyForTheSameRequest(t *testing.T) {
	pub, priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	keys := ParseKeys(Line(pub, "laptop"))
	if len(keys) != 1 || keys[0].Label != "laptop" {
		t.Fatalf("keys: %+v", keys)
	}
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	body := []byte(`{"project":"shop"}`)
	h, err := Sign(priv, "srv1", "POST", "/v1/rollback", body, now)
	if err != nil {
		t.Fatal(err)
	}
	var v Verifier
	if err := v.Verify(keys, "srv1", "POST", "/v1/rollback", body, h, now); err != nil {
		t.Fatalf("a valid signature must pass: %v", err)
	}
	if err := v.Verify(keys, "srv1", "POST", "/v1/rollback", body, h, now); err == nil {
		t.Error("the same signature must not work twice")
	}
	h2, _ := Sign(priv, "srv1", "POST", "/v1/rollback", body, now)
	for name, c := range map[string]struct {
		server, path string
		body         []byte
		at           time.Time
	}{
		"another server": {"srv2", "/v1/rollback", body, now},
		"another path":   {"srv1", "/v1/deploy", body, now},
		"another body":   {"srv1", "/v1/rollback", []byte(`{"project":"other"}`), now},
		"too late":       {"srv1", "/v1/rollback", body, now.Add(6 * time.Minute)},
	} {
		var vv Verifier
		if err := vv.Verify(keys, c.server, "POST", c.path, c.body, h2, c.at); err == nil {
			t.Errorf("%s: must be refused", name)
		}
	}
	other, otherPriv, _ := GenerateKey()
	_ = other
	h3, _ := Sign(otherPriv, "srv1", "POST", "/v1/rollback", body, now)
	var v3 Verifier
	if err := v3.Verify(keys, "srv1", "POST", "/v1/rollback", body, h3, now); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Errorf("an unknown key must be refused: %v", err)
	}
	if err := v3.Verify(keys, "srv1", "POST", "/v1/rollback", body, "", now); err == nil {
		t.Error("a request without a signature must be refused")
	}
}
