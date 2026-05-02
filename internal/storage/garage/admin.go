package garage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// AdminClient talks to the Garage admin HTTP API. It is the bridge between
// selfcloud's bucket / access-key resources and the underlying Garage state.
type AdminClient struct {
	base   string
	token  string
	client *http.Client
}

// NewAdminClient builds a client. base is e.g. "http://127.0.0.1:3903".
func NewAdminClient(base, token string) *AdminClient {
	return &AdminClient{
		base:   base,
		token:  token,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *AdminClient) do(ctx context.Context, method, path string, body any, out any) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("authorization", "Bearer "+c.token)
	req.Header.Set("content-type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("garage admin %s %s: %s: %s", method, path, resp.Status, string(b))
	}
	if out != nil && resp.StatusCode != http.StatusNoContent {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// Bucket represents a bucket as exposed by the admin API.
type Bucket struct {
	ID         string   `json:"id"`
	GlobalAlias []string `json:"globalAliases"`
	Objects    int64    `json:"objects"`
	Bytes      int64    `json:"bytes"`
}

// CreateBucket creates a bucket with the given alias.
func (c *AdminClient) CreateBucket(ctx context.Context, alias string) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	body := map[string]any{"globalAlias": alias}
	if err := c.do(ctx, http.MethodPost, "/v1/bucket", body, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// DeleteBucket removes a bucket by ID.
func (c *AdminClient) DeleteBucket(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/bucket?id="+id, nil, nil)
}

// ListBuckets returns all buckets.
func (c *AdminClient) ListBuckets(ctx context.Context) ([]Bucket, error) {
	var out []Bucket
	if err := c.do(ctx, http.MethodGet, "/v1/bucket?list", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateKey creates a fresh access key. Returns id, secret.
func (c *AdminClient) CreateKey(ctx context.Context, name string) (string, string, error) {
	var out struct {
		AccessKeyID     string `json:"accessKeyId"`
		SecretAccessKey string `json:"secretAccessKey"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/key?name="+name, nil, &out); err != nil {
		return "", "", err
	}
	return out.AccessKeyID, out.SecretAccessKey, nil
}

// AllowKey grants read/write access on a bucket for a key.
func (c *AdminClient) AllowKey(ctx context.Context, keyID, bucketID string, read, write, owner bool) error {
	body := map[string]any{
		"bucketId":    bucketID,
		"accessKeyId": keyID,
		"permissions": map[string]bool{"read": read, "write": write, "owner": owner},
	}
	return c.do(ctx, http.MethodPost, "/v1/bucket/allow", body, nil)
}

// DeleteKey removes an access key.
func (c *AdminClient) DeleteKey(ctx context.Context, keyID string) error {
	return c.do(ctx, http.MethodDelete, "/v1/key?id="+keyID, nil, nil)
}
