package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxBody      = 16 << 10
	maxNameRunes = 100
	maxEmailLen  = 254
)

// CreateRequest is the body of POST /tenants.
type CreateRequest struct {
	Name       string `json:"name"`
	AdminEmail string `json:"admin_email"`
}

// InputError is a request the service refuses with 400.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &InputError{Msg: fmt.Sprintf(format, a...)} }

// DecodeCreate reads and checks a create request: one JSON object with known
// fields only, a name of 1–100 characters without control characters (spaces
// around it are trimmed) and a plain email address.
func DecodeCreate(r io.Reader) (CreateRequest, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxBody+1))
	if err != nil {
		return CreateRequest{}, invalid("reading the body: %v", err)
	}
	if len(data) > maxBody {
		return CreateRequest{}, invalid("the body is larger than %d bytes", maxBody)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var req CreateRequest
	if err := dec.Decode(&req); err != nil {
		return CreateRequest{}, invalid("the body is not a valid create request: %v", err)
	}
	if dec.More() {
		return CreateRequest{}, invalid("the body holds more than one JSON value")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return CreateRequest{}, invalid("the body holds more than one JSON value")
	}

	req.Name = strings.TrimSpace(req.Name)
	switch n := utf8.RuneCountInString(req.Name); {
	case n == 0:
		return CreateRequest{}, invalid("name is required")
	case n > maxNameRunes:
		return CreateRequest{}, invalid("name is longer than %d characters", maxNameRunes)
	}
	if !utf8.ValidString(req.Name) || strings.ContainsFunc(req.Name, unicode.IsControl) {
		return CreateRequest{}, invalid("name contains control characters")
	}
	if !validEmail(req.AdminEmail) {
		return CreateRequest{}, invalid("admin_email is not a valid email address")
	}
	return req, nil
}

// validEmail accepts a bare address (local@domain), as Zitadel does, and no
// display name, comments or brackets.
func validEmail(s string) bool {
	if s == "" || len(s) > maxEmailLen || strings.ContainsFunc(s, unicode.IsSpace) {
		return false
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Name != "" || addr.Address != s {
		return false
	}
	_, domain, ok := strings.Cut(s, "@")
	return ok && strings.Contains(domain, ".") && !strings.HasPrefix(domain, ".") && !strings.HasSuffix(domain, ".")
}
