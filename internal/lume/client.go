// Package lume contains the only Lume transport. Mutations are never retried:
// a lost response is an ambiguous outcome, not proof that nothing happened.
package lume

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
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

type Client struct {
	base string
	http *http.Client
}

func New(base string) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("lume URL must be a loopback http://host:port origin")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, fmt.Errorf("lume must run on loopback")
	}
	tr := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, MaxIdleConns: 4, MaxIdleConnsPerHost: 4, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 5 * time.Minute}
	return &Client{base: strings.TrimRight(base, "/"), http: &http.Client{Transport: tr, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) request(ctx context.Context, method, path string, body any, result any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("lume %s: %w", method, err)
	}
	defer resp.Body.Close()
	// Do not copy upstream bodies into events: they may contain credentials/paths.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("lume %s returned HTTP %d", method, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024+1))
	if err != nil {
		return err
	}
	if len(b) > 4*1024*1024 {
		return fmt.Errorf("lume response exceeds 4 MiB")
	}
	if result != nil {
		if err := json.Unmarshal(b, result); err != nil {
			return fmt.Errorf("invalid Lume response: %w", err)
		}
	}
	return nil
}
func (c *Client) Observe(ctx context.Context) (domain.Observation, error) {
	// Host status includes VMs the VM list may not yet report as running.
	var host struct {
		Used      *int   `json:"vm_count"`
		Max       *int   `json:"max_vms"`
		Available *int   `json:"available_slots"`
		Status    string `json:"status"`
	}
	if err := c.request(ctx, "GET", "/lume/host/status", nil, &host); err != nil {
		return domain.Observation{}, err
	}
	if host.Used == nil || host.Max == nil || host.Available == nil || *host.Used < 0 || *host.Max < 1 || *host.Available < 0 || *host.Available > max(0, *host.Max-*host.Used) || host.Status != "healthy" {
		return domain.Observation{}, fmt.Errorf("invalid or unhealthy Lume capacity response")
	}
	var raw []struct {
		Name     string `json:"name"`
		Location string `json:"locationName"`
		OS       string `json:"os"`
		Status   string `json:"status"`
		IP       string `json:"ipAddress"`
		SSH      bool   `json:"sshAvailable"`
	}
	if err := c.request(ctx, "GET", "/lume/vms", nil, &raw); err != nil {
		return domain.Observation{}, err
	}
	if raw == nil {
		return domain.Observation{}, fmt.Errorf("lume VM inventory must be an array")
	}
	o := domain.Observation{VMs: []domain.VM{}, HostUsed: max(*host.Used, *host.Max-*host.Available), HostMax: *host.Max, At: time.Now().UTC()}
	seen := map[string]bool{}
	for _, v := range raw {
		if !domain.ValidName(v.Name) || !domain.ValidName(v.Location) || v.Status == "" || v.OS == "" {
			return o, fmt.Errorf("invalid Lume VM inventory")
		}
		vm := domain.VM{Name: v.Name, Location: v.Location, OS: v.OS, State: v.Status, IP: v.IP, SSHAvailable: v.SSH}
		if seen[vm.Key()] {
			return o, fmt.Errorf("duplicate Lume VM identity")
		}
		seen[vm.Key()] = true
		o.VMs = append(o.VMs, vm)
	}
	return o, nil
}
func (c *Client) Clone(ctx context.Context, t domain.Template, l domain.Lease) error {
	return c.request(ctx, "POST", "/lume/vms/clone", map[string]string{"name": t.Name, "newName": l.VMName, "sourceLocation": t.Location, "destLocation": l.Location}, nil)
}
func (c *Client) Start(ctx context.Context, l domain.Lease) error {
	return c.request(ctx, "POST", "/lume/vms/"+url.PathEscape(l.VMName)+"/run", map[string]any{"noDisplay": true, "storage": l.Location}, nil)
}
func (c *Client) Stop(ctx context.Context, l domain.Lease) error {
	return c.request(ctx, "POST", "/lume/vms/"+url.PathEscape(l.VMName)+"/stop", map[string]string{"storage": l.Location}, nil)
}
func (c *Client) Delete(ctx context.Context, l domain.Lease) error {
	return c.request(ctx, "DELETE", "/lume/vms/"+url.PathEscape(l.VMName)+"?storage="+url.QueryEscape(l.Location), nil, nil)
}
