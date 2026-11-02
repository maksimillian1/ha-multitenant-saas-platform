package kafka

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/contracttest"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/core"
)

// jsonValue decodes b keeping numbers exact, so 2^53 + 1 stays itself.
func jsonValue(t *testing.T, b []byte) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// UsageWriteAPI writes exactly the Kafka version it declares, and the record
// of the HTTP example's first event is the Kafka example. A new SchemaVersion
// without a declaration and a schema with examples fails here.
func TestEncodeWritesTheProducedVersion(t *testing.T) {
	want := fmt.Sprintf("v%d", core.SchemaVersion)
	produces := contracttest.Produces(t, "kafka", "writeapi")
	if !slices.Equal(produces, []string{want}) {
		t.Fatalf("participants.json: writeapi produces %v on kafka, the code writes %s", produces, want)
	}
	var batch struct{ Events []core.Event }
	httpVersion := contracttest.Consumes(t, "http", "writeapi")[0]
	if err := json.Unmarshal(contracttest.Examples(t, "http", httpVersion)[0], &batch); err != nil {
		t.Fatal(err)
	}
	got, err := Encode(batch.Events[0])
	if err != nil {
		t.Fatal(err)
	}
	example := contracttest.Examples(t, "kafka", want)[0]
	if !reflect.DeepEqual(jsonValue(t, got), jsonValue(t, example)) {
		t.Fatalf("Encode = %s\nwant %s", got, example)
	}
}
