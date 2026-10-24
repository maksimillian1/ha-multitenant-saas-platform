package e2e

import (
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if err := loadConfig(); err != nil {
		fmt.Fprintln(os.Stderr, "e2e:", err)
		os.Exit(1)
	}
	if err := waitForGateway(3 * time.Minute); err != nil {
		fmt.Fprintln(os.Stderr, "e2e:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// waitForGateway waits until a valid token passes the gateway, which can take
// a moment right after the cluster and the tenants were created.
func waitForGateway(max time.Duration) error {
	deadline := time.Now().Add(max)
	var last string
	for time.Now().Before(deadline) {
		tok, err := fetchToken(tenA)
		if err == nil {
			var r response
			if r, err = doCall(http.MethodGet, "/items", tok, nil); err == nil {
				if r.Status == http.StatusOK {
					return nil
				}
				err = fmt.Errorf("%d %s", r.Status, r.Body)
			}
		}
		last = err.Error()
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("gateway did not accept a valid token within %s; last response: %s", max, last)
}
