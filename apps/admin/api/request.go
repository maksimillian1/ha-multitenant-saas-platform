package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/admin/zitadel"
)

const (
	maxBody      = 64 << 10
	defaultLimit = 20
	maxLimit     = 100
	maxRelated   = 100
)

// Unknown fields are an error, so a typo does not silently change nothing.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err == nil && dec.More() {
		err = errors.New("more than one JSON value")
	}
	if err != nil {
		core.WriteError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func badRequest(w http.ResponseWriter, err error) {
	core.WriteError(w, http.StatusBadRequest, err.Error())
}

func page(q url.Values) (zitadel.Page, error) {
	p := zitadel.Page{Limit: defaultLimit}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return p, errors.New("offset must be a whole number of at least 0")
		}
		p.Offset = n
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxLimit {
			return p, fmt.Errorf("limit must be a whole number from 1 to %d", maxLimit)
		}
		p.Limit = n
	}
	return p, nil
}

type fields struct{ first error }

func (f *fields) fail(format string, args ...any) {
	if f.first == nil {
		f.first = fmt.Errorf(format, args...)
	}
}

func (f *fields) text(name string, value *string, min, max int) {
	*value = strings.TrimSpace(*value)
	switch n := utf8.RuneCountInString(*value); {
	case n < min && min == 1:
		f.fail("%s is required", name)
	case n < min:
		f.fail("%s needs at least %d characters", name, min)
	case n > max:
		f.fail("%s has more than %d characters", name, max)
	}
}

// Empty values only with allowEmpty.
func (f *fields) optionalText(name string, value *string, max int, allowEmpty bool) {
	if value == nil {
		return
	}
	min := 1
	if allowEmpty {
		min = 0
	}
	f.text(name, value, min, max)
}

// Zitadel checks the details.
func (f *fields) email(name string, value *string) {
	f.text(name, value, 1, 200)
	if *value != "" && !strings.Contains(*value, "@") {
		f.fail("%s is not an email address", name)
	}
}

// Passwords are not trimmed.
func (f *fields) secret(name, value string) {
	switch n := utf8.RuneCountInString(value); {
	case n == 0:
		f.fail("%s is required", name)
	case n > 200:
		f.fail("%s has more than 200 characters", name)
	}
}

func (f *fields) roleKeys(keys *[]string) {
	if len(*keys) == 0 {
		f.fail("role_keys needs at least one role")
		return
	}
	out := make([]string, 0, len(*keys))
	for _, key := range *keys {
		key = strings.TrimSpace(key)
		if key == "" || utf8.RuneCountInString(key) > 200 {
			f.fail("role_keys has an empty or too long key")
			return
		}
		if !slices.Contains(out, key) {
			out = append(out, key)
		}
	}
	*keys = out
}

func (f *fields) err() error { return f.first }

func boolValue(b *bool) bool { return b != nil && *b }

func stringValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
