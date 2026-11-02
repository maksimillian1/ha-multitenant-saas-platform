// Package contracttest gives tests the usage event contract of
// docs/contracts/usage-event (ADR-0010): the versions a service declares in
// participants.json and the examples in those versions' schemas. A service's
// tests run against every version it declares, so a declaration the code does
// not keep fails. Only tests import it.
package contracttest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Dir is docs/contracts/usage-event, found from this file so that tests in
// any package reach it.
func Dir(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate contracttest")
	}
	return filepath.Join(filepath.Dir(file), "../../../../docs/contracts/usage-event")
}

type channel struct {
	Schema       string `json:"schema"`
	Participants map[string]struct {
		Produces []string `json:"produces"`
		Consumes []string `json:"consumes"`
	} `json:"participants"`
}

func manifest(t testing.TB) map[string]channel {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(Dir(t), "participants.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]channel
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("participants.json: %v", err)
	}
	return m
}

// Produces returns the versions service writes on channel ("http", "kafka").
func Produces(t testing.TB, ch, service string) []string {
	t.Helper()
	return versions(t, ch, service, func(p []string, _ []string) []string { return p })
}

// Consumes returns the versions service reads on channel.
func Consumes(t testing.TB, ch, service string) []string {
	t.Helper()
	return versions(t, ch, service, func(_ []string, c []string) []string { return c })
}

func versions(t testing.TB, ch, service string, pick func(produces, consumes []string) []string) []string {
	t.Helper()
	c, ok := manifest(t)[ch]
	if !ok {
		t.Fatalf("participants.json has no channel %q", ch)
	}
	p, ok := c.Participants[service]
	if !ok {
		t.Fatalf("participants.json: %s is not a participant of %s", service, ch)
	}
	v := pick(p.Produces, p.Consumes)
	if len(v) == 0 {
		t.Fatalf("participants.json: %s declares no versions in this role on %s", service, ch)
	}
	return v
}

// Examples returns the examples of the channel's schema in version, e.g.
// schemas/kafka-record.v1.json for ("kafka", "v1"), as written, so large
// integers keep their digits.
func Examples(t testing.TB, ch, version string) []json.RawMessage {
	t.Helper()
	c, ok := manifest(t)[ch]
	if !ok {
		t.Fatalf("participants.json has no channel %q", ch)
	}
	file := filepath.Join(Dir(t), "schemas", fmt.Sprintf("%s.%s.json", c.Schema, version))
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Examples []json.RawMessage `json:"examples"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	if len(s.Examples) == 0 {
		t.Fatalf("%s has no examples", file)
	}
	return s.Examples
}
