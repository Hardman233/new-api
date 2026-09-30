package youchuancredential

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

type Credential struct {
	AppID  string `json:"app_id"`
	Secret string `json:"secret"`
}

func Parse(raw string) (Credential, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Credential{}, errors.New("empty Youchuan credential")
	}

	var values map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return Credential{}, errors.New("Youchuan credential must be a JSON object")
	}
	credential := Credential{
		AppID:  firstValue(values, "app_id", "appId", "appID", "app"),
		Secret: firstValue(values, "secret", "secret_key", "secretKey", "app_secret", "appSecret"),
	}
	if credential.AppID == "" || credential.Secret == "" {
		return Credential{}, errors.New("Youchuan credential requires app_id and secret")
	}
	return credential, nil
}

func Fingerprint(credential Credential) string {
	sum := sha256.Sum256([]byte(credential.AppID + "\x00" + credential.Secret))
	return hex.EncodeToString(sum[:8])
}

func firstValue(values map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		raw, ok := values[key]
		if !ok {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) == nil && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
