package page

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
)

const OrgOwnerRole = "ORG_OWNER"

// Memberships reads the user's Zitadel manager roles; they are not in the token.
type Memberships struct {
	ZitadelURL string
	HTTP       *http.Client
}

// IsOrgOwner reports whether the user holds ORG_OWNER of organization orgID.
func (m *Memberships) IsOrgOwner(ctx context.Context, accessToken, orgID string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.ZitadelURL+"/auth/v1/memberships/me/_search", strings.NewReader("{}"))
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.HTTP.Do(req)
	if err != nil {
		return false, fmt.Errorf("memberships: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return false, fmt.Errorf("memberships: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("memberships: status %d", resp.StatusCode)
	}
	var out struct {
		Result []struct {
			OrgID string   `json:"orgId"`
			Roles []string `json:"roles"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return false, fmt.Errorf("memberships: %w", err)
	}
	for _, m := range out.Result {
		if m.OrgID == orgID && slices.Contains(m.Roles, OrgOwnerRole) {
			return true, nil
		}
	}
	return false, nil
}
