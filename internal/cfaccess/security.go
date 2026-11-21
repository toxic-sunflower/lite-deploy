package cfaccess

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

// UnderAttack is the Cloudflare security level "I'm Under Attack!": every visitor gets a short check page before the site.
const UnderAttack = "under_attack"

// NormalLevel is what "off" sets: the default security level of a zone.
const NormalLevel = "medium"

// SecurityLevel reads the security level of a zone (off, essentially_off, low, medium, high, under_attack).
func (c *Client) SecurityLevel(ctx context.Context, zone string) (string, error) {
	if !idRe.MatchString(zone) {
		return "", errors.New("invalid zone")
	}
	raw, err := c.do(ctx, http.MethodGet, "/zones/"+zone+"/settings/security_level", nil)
	if err != nil {
		return "", err
	}
	var s struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &s); err != nil || s.Value == "" {
		return "", errors.New("the answer of Cloudflare has no security level")
	}
	return s.Value, nil
}

// SetSecurityLevel sets the security level of a zone. The level applies to the whole domain, not to one address.
func (c *Client) SetSecurityLevel(ctx context.Context, zone, level string) error {
	if !idRe.MatchString(zone) {
		return errors.New("invalid zone")
	}
	switch level {
	case "off", "essentially_off", "low", "medium", "high", UnderAttack:
	default:
		return errors.New("unknown security level")
	}
	_, err := c.do(ctx, http.MethodPatch, "/zones/"+zone+"/settings/security_level", map[string]any{"value": level})
	return err
}
