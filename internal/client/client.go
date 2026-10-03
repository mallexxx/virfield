// Package client is shared by CLI and MCP. It contains no host or VM operations.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

type Client struct {
	base  string
	token atomic.Value
	http  *http.Client
}

func New(base, token string) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("API URL must be an HTTP(S) origin")
	}
	ip := net.ParseIP(u.Hostname())
	local := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && (u.Scheme != "http" || !local) {
		return nil, fmt.Errorf("non-loopback API connections require HTTPS")
	}
	if token == "" {
		return nil, fmt.Errorf("API token is required")
	}
	c := &Client{base: strings.TrimRight(base, "/"), http: &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
	c.token.Store(token)
	return c, nil
}
func (c *Client) SetToken(token string) { c.token.Store(token) }
func (c *Client) Do(ctx context.Context, method, path string, body any, key string) (json.RawMessage, error) {
	budget := 10 * time.Second
	if path == "registry/resolve" {
		budget = 70 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+"/api/v1/"+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token.Load().(string))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 4*1024*1024 {
		return nil, fmt.Errorf("API response exceeds 4 MiB")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var envelope struct {
			Error *domain.Error `json:"error"`
		}
		if json.Unmarshal(b, &envelope) == nil && envelope.Error != nil {
			return nil, envelope.Error
		}
		return nil, fmt.Errorf("API returned HTTP %d", resp.StatusCode)
	}
	if !json.Valid(b) {
		return nil, fmt.Errorf("API returned invalid JSON")
	}
	return b, nil
}
