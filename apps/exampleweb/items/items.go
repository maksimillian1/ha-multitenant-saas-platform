// Package items reads a tenant's items from ExampleApp through the API Gateway
// with the signed-in user's token.
package items

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

var ErrUnauthorized = errors.New("gateway rejected the token")

type Item struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Client struct {
	GatewayURL string
	HTTP       *http.Client
}

func (c *Client) List(ctx context.Context, accessToken string) ([]Item, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.GatewayURL+"/items", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET /items: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("GET /items: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /items: status %d", resp.StatusCode)
	}
	var out struct {
		Items []Item `json:"items"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("GET /items: %w", err)
	}
	return out.Items, nil
}
