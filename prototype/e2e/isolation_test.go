package e2e

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// AC-1: each tenant creates items and reads back only its own.
func TestTenantsCreateAndReadOwnItems(t *testing.T) {
	tokA, tokB := token(t, tenA), token(t, tenB)
	run := randomID()
	a := createItem(t, tokA, "ac1-a-"+run)
	b := createItem(t, tokB, "ac1-b-"+run)
	if a.TenantID != tenA.OrgID || b.TenantID != tenB.OrgID {
		t.Fatalf("tenant_id: a=%q b=%q, want %q and %q", a.TenantID, b.TenantID, tenA.OrgID, tenB.OrgID)
	}

	for _, c := range []struct {
		tok        string
		ten        tenant
		own, other item
	}{{tokA, tenA, a, b}, {tokB, tenB, b, a}} {
		items := listItems(t, c.tok)
		if !containsID(items, c.own.ID) || containsID(items, c.other.ID) {
			t.Errorf("%s: GET /items must contain %s and not %s", c.ten.Name, c.own.ID, c.other.ID)
		}
		for _, it := range items {
			if it.TenantID != c.ten.OrgID {
				t.Errorf("%s: GET /items returned an item of tenant %q", c.ten.Name, it.TenantID)
			}
		}
		mustStatus(t, call(t, http.MethodGet, "/items/"+c.own.ID, c.tok, nil), http.StatusOK, c.ten.Name+" GET own item")
	}
}

// AC-2 / QS-01: tenant A gets 404 for B's item ID on GET, PUT and DELETE, and B's row is unchanged.
func TestCrossTenantAccessByIDReturns404(t *testing.T) {
	tokA, tokB := token(t, tenA), token(t, tenB)
	b := createItem(t, tokB, "ac2-b-"+randomID())

	mustStatus(t, call(t, http.MethodGet, "/items/"+b.ID, tokA, nil), http.StatusNotFound, "A GET B's item")
	mustStatus(t, call(t, http.MethodPut, "/items/"+b.ID, tokA, map[string]string{"name": "hijacked"}),
		http.StatusNotFound, "A PUT B's item")
	mustStatus(t, call(t, http.MethodDelete, "/items/"+b.ID, tokA, nil), http.StatusNotFound, "A DELETE B's item")

	r := call(t, http.MethodGet, "/items/"+b.ID, tokB, nil)
	mustStatus(t, r, http.StatusOK, "B GET own item after A's attempts")
	var got item
	r.decode(t, &got)
	if got.Name != b.Name {
		t.Fatalf("B's item name = %q, want %q", got.Name, b.Name)
	}
	row, err := psql(t, fmt.Sprintf("SELECT tenant_id || '|' || name FROM items WHERE id = '%s'", b.ID))
	if err != nil {
		t.Fatal(err)
	}
	if row != tenB.OrgID+"|"+b.Name {
		t.Fatalf("database row = %q, want %q", row, tenB.OrgID+"|"+b.Name)
	}
}

// AC-3: a tenant_id in the request body is ignored.
func TestBodyTenantIDIsIgnored(t *testing.T) {
	tokA, tokB := token(t, tenA), token(t, tenB)
	r := call(t, http.MethodPost, "/items", tokA, map[string]string{"name": "ac3-" + randomID(), "tenant_id": tenB.OrgID})
	mustStatus(t, r, http.StatusCreated, "A POST with B's tenant_id in the body")
	var it item
	r.decode(t, &it)
	if it.TenantID != tenA.OrgID {
		t.Fatalf("item stored under %q, want A's %q", it.TenantID, tenA.OrgID)
	}
	if containsID(listItems(t, tokB), it.ID) {
		t.Fatal("B's GET /items shows the item A created")
	}
}

// AC-4: an x-tenant-id header sent by the client is replaced by the gateway.
func TestClientTenantHeaderIsOverwritten(t *testing.T) {
	tokA, tokB := token(t, tenA), token(t, tenB)
	r := call(t, http.MethodPost, "/items", tokA, map[string]string{"name": "ac4-" + randomID()},
		tenantHeader, tenB.OrgID)
	mustStatus(t, r, http.StatusCreated, "A POST with x-tenant-id of B")
	var it item
	r.decode(t, &it)
	if it.TenantID != tenA.OrgID {
		t.Fatalf("request served as %q, want A's %q", it.TenantID, tenA.OrgID)
	}
	if containsID(listItems(t, tokB), it.ID) {
		t.Fatal("B's GET /items shows the item A created")
	}

	b := createItem(t, tokB, "ac4-b-"+randomID())
	mustStatus(t, call(t, http.MethodGet, "/items/"+b.ID, tokA, nil, tenantHeader, tenB.OrgID),
		http.StatusNotFound, "A GET B's item with x-tenant-id of B")
}

// AC-9: interleaved parallel requests of both tenants through PgBouncer in
// transaction mode (pool smaller than the concurrency) never leak rows.
func TestParallelRequestsDoNotLeakAcrossTenants(t *testing.T) {
	const iterations, workers = 200, 40
	toks := map[string]string{tenA.OrgID: token(t, tenA), tenB.OrgID: token(t, tenB)}
	run := randomID()

	var wg sync.WaitGroup
	errs := make(chan error, iterations)
	jobs := make(chan int)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				ten := tenA
				if i%2 == 1 {
					ten = tenB
				}
				if err := createAndCheck(toks[ten.OrgID], ten, fmt.Sprintf("ac9-%s-%s-%d", run, ten.Name, i)); err != nil {
					errs <- err
				}
			}
		}()
	}
	for i := 0; i < iterations; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	close(errs)

	n := 0
	for err := range errs {
		if n < 10 {
			t.Error(err)
		}
		n++
	}
	if n > 0 {
		t.Fatalf("%d of %d iterations failed", n, iterations)
	}
}

// createAndCheck creates one item and then lists the tenant's items, checking
// that nothing belongs to the other tenant.
func createAndCheck(tok string, ten tenant, name string) error {
	r, err := doCall(http.MethodPost, "/items", tok, map[string]string{"name": name})
	if err != nil {
		return err
	}
	if r.Status != http.StatusCreated || !strings.Contains(string(r.Body), ten.OrgID) {
		return fmt.Errorf("%s POST: %d %s", ten.Name, r.Status, r.Body)
	}
	if r, err = doCall(http.MethodGet, "/items", tok, nil); err != nil {
		return err
	}
	if r.Status != http.StatusOK {
		return fmt.Errorf("%s GET: %d %s", ten.Name, r.Status, r.Body)
	}
	other := tenB
	if ten.OrgID == tenB.OrgID {
		other = tenA
	}
	if strings.Contains(string(r.Body), other.OrgID) || strings.Contains(string(r.Body), "-"+other.Name+"-") {
		return fmt.Errorf("%s GET /items contains data of %s", ten.Name, other.Name)
	}
	return nil
}
