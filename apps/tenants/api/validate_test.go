package api

import (
	"errors"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// Verifies: tenant-management AC-8; T-03
func TestCreateInputValidation(t *testing.T) {
	long := strings.Repeat("ä", maxNameRunes)
	cases := []struct {
		name string
		body string
		ok   bool
		want CreateRequest
	}{
		{"valid", `{"name":"Acme Corp","admin_email":"it@acme.example"}`, true, CreateRequest{"Acme Corp", "it@acme.example"}},
		{"name trimmed", `{"name":"  Acme  ","admin_email":"it@acme.example"}`, true, CreateRequest{"Acme", "it@acme.example"}},
		{"name at max", `{"name":"` + long + `","admin_email":"it@acme.example"}`, true, CreateRequest{long, "it@acme.example"}},
		{"name over max", `{"name":"` + long + `x","admin_email":"it@acme.example"}`, false, CreateRequest{}},
		{"empty name", `{"name":"","admin_email":"it@acme.example"}`, false, CreateRequest{}},
		{"blank name", `{"name":"   ","admin_email":"it@acme.example"}`, false, CreateRequest{}},
		{"control character", `{"name":"Ac\u0000me","admin_email":"it@acme.example"}`, false, CreateRequest{}},
		{"newline", `{"name":"Ac\nme","admin_email":"it@acme.example"}`, false, CreateRequest{}},
		{"no email", `{"name":"Acme"}`, false, CreateRequest{}},
		{"email without domain dot", `{"name":"Acme","admin_email":"it@acme"}`, false, CreateRequest{}},
		{"email with display name", `{"name":"Acme","admin_email":"IT <it@acme.example>"}`, false, CreateRequest{}},
		{"email with space", `{"name":"Acme","admin_email":"i t@acme.example"}`, false, CreateRequest{}},
		{"email too long", `{"name":"Acme","admin_email":"` + strings.Repeat("a", 250) + `@acme.example"}`, false, CreateRequest{}},
		{"unknown field", `{"name":"Acme","admin_email":"it@acme.example","plan":"gold"}`, false, CreateRequest{}},
		{"wrong type", `{"name":42,"admin_email":"it@acme.example"}`, false, CreateRequest{}},
		{"two objects", `{"name":"Acme","admin_email":"it@acme.example"}{}`, false, CreateRequest{}},
		{"not JSON", `name=Acme`, false, CreateRequest{}},
		{"empty", ``, false, CreateRequest{}},
		{"null", `null`, false, CreateRequest{}},
		{"too large", `{"name":"Acme","admin_email":"it@acme.example","x":"` + strings.Repeat("a", maxBody) + `"}`, false, CreateRequest{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := DecodeCreate(strings.NewReader(c.body))
			if c.ok {
				if err != nil || got != c.want {
					t.Fatalf("got %+v, %v; want %+v", got, err, c.want)
				}
				return
			}
			var ie *InputError
			if !errors.As(err, &ie) {
				t.Fatalf("err = %v, want an InputError", err)
			}
		})
	}
}

// Verifies: tenant-management AC-8; T-11
func FuzzDecodeCreate(f *testing.F) {
	for _, seed := range []string{
		`{"name":"Acme Corp","admin_email":"it@acme.example"}`,
		`{"name":"","admin_email":""}`,
		`{"name":"\u0000","admin_email":"a@b.c"}`,
		`[]`, `null`, `{"name":"a"}{}`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body string) {
		req, err := DecodeCreate(strings.NewReader(body))
		if err != nil {
			var ie *InputError
			if !errors.As(err, &ie) {
				t.Fatalf("error of the wrong kind: %v", err)
			}
			return
		}
		n := utf8.RuneCountInString(req.Name)
		if n == 0 || n > maxNameRunes || req.Name != strings.TrimSpace(req.Name) ||
			strings.ContainsFunc(req.Name, unicode.IsControl) || !validEmail(req.AdminEmail) {
			t.Fatalf("accepted an invalid request: %+v", req)
		}
	})
}
