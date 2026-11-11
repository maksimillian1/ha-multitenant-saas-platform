package zitadel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Verifies: tenant-management AC-16
func TestLoadKeyRejectsMissingOrInvalid(t *testing.T) {
	good, _ := testKey(t)
	dir := t.TempDir()
	write := func(name string, v any) string {
		t.Helper()
		var raw []byte
		if s, ok := v.(string); ok {
			raw = []byte(s)
		} else {
			raw, _ = json.Marshal(v)
		}
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	with := func(f func(*Key)) Key { k := good; f(&k); return k }

	if k, err := LoadKey("ZITADEL_KEY_FILE", write("good.json", good)); err != nil || k.UserID != good.UserID || k.KeyID != good.KeyID {
		t.Fatalf("a good key: %+v, %v", k.UserID, err)
	}
	keyBody := strings.Split(good.Key, "\n")[1]
	for _, tc := range []struct {
		name, path, want string
	}{
		{"no setting", "", "ZITADEL_KEY_FILE is not set"},
		{"no file", filepath.Join(dir, "missing.json"), "no such file"},
		{"not JSON", write("broken.json", good.Key), "not JSON"},
		{"wrong type", write("type.json", with(func(k *Key) { k.Type = "application" })), "want serviceaccount"},
		{"no key ID", write("kid.json", with(func(k *Key) { k.KeyID = "" })), "keyId is empty"},
		{"no user", write("user.json", with(func(k *Key) { k.UserID = "" })), "userId is empty"},
		{"no key", write("key.json", with(func(k *Key) { k.Key = "" })), "key is empty"},
		{"not PEM", write("pem.json", with(func(k *Key) { k.Key = keyBody })), "not PEM"},
		{"not RSA", write("rsa.json", with(func(k *Key) {
			k.Key = "-----BEGIN RSA PRIVATE KEY-----\n" + keyBody + "\n-----END RSA PRIVATE KEY-----\n"
		})), "not an RSA private key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadKey("ZITADEL_KEY_FILE", tc.path)
			if err == nil {
				t.Fatal("no error")
			}
			msg := err.Error()
			if !strings.Contains(msg, "ZITADEL_KEY_FILE") || !strings.Contains(msg, tc.want) {
				t.Errorf("error %q, want the setting and %q", msg, tc.want)
			}
			if strings.Contains(msg, keyBody) || strings.Contains(msg, "PRIVATE KEY") {
				t.Errorf("error %q contains the key", msg)
			}
		})
	}
}
