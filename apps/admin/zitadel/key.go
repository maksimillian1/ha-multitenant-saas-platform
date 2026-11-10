package zitadel

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
)

// Key is a JWT profile key of a Zitadel machine user, in the JSON form
// Zitadel issues it ({"type":"serviceaccount","keyId":...,"key":...,"userId":...}).
// The private key signs the assertions the client exchanges for access
// tokens; it is never sent.
type Key struct {
	Type   string `json:"type"`
	KeyID  string `json:"keyId"`
	Key    string `json:"key"`
	UserID string `json:"userId"`
}

// LoadKey reads the key file at path. setting names where the path came
// from (ZITADEL_KEY_FILE) for the error. Errors never contain the key.
func LoadKey(setting, path string) (Key, error) {
	if path == "" {
		return Key{}, fmt.Errorf("%s is not set", setting)
	}
	data, err := os.ReadFile(path) //nolint:gosec // G304: the path is the operator's setting.
	if err != nil {
		var pathErr *os.PathError
		if errors.As(err, &pathErr) {
			err = pathErr.Err
		}
		return Key{}, fmt.Errorf("%s: reading the key file %s: %w", setting, path, err)
	}
	k, err := ParseKey(data)
	if err != nil {
		return Key{}, fmt.Errorf("%s: the key file %s: %w", setting, path, err)
	}
	return k, nil
}

// ParseKey parses and checks a key file. Errors never contain the key.
func ParseKey(data []byte) (Key, error) {
	var k Key
	if err := json.Unmarshal(data, &k); err != nil {
		// The JSON error may quote the file; say only where it failed.
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) {
			return Key{}, fmt.Errorf("not JSON (offset %d)", syntax.Offset)
		}
		return Key{}, errors.New("not a JWT profile key file")
	}
	switch {
	case k.Type != "serviceaccount":
		return Key{}, fmt.Errorf("type is %q, want serviceaccount", k.Type)
	case k.KeyID == "":
		return Key{}, errors.New("keyId is empty")
	case k.UserID == "":
		return Key{}, errors.New("userId is empty")
	case k.Key == "":
		return Key{}, errors.New("key is empty")
	}
	block, _ := pem.Decode([]byte(k.Key))
	if block == nil {
		return Key{}, errors.New("key is not PEM")
	}
	if _, err := x509.ParsePKCS1PrivateKey(block.Bytes); err != nil {
		if _, err8 := x509.ParsePKCS8PrivateKey(block.Bytes); err8 != nil {
			return Key{}, errors.New("key is not an RSA private key")
		}
	}
	return k, nil
}
