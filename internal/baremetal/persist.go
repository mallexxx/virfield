package baremetal

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

// remembered is a node's last successful probe as written to disk.
type remembered struct {
	Golden domain.VM         `json:"golden"`
	Host   domain.Host       `json:"host"`
	Leases map[string]string `json:"leases"`
	IP     string            `json:"ip"`
	At     time.Time         `json:"at"`
}

// Persist remembers each node's last probe in dir, so a restart while a node
// sleeps keeps its golden admissible and its leases in inventory. Remembered
// probes are marked changed: the first Observe dials every node again.
func (r *Runtime) Persist(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.persistDir = dir
	for _, n := range r.nodes {
		b, err := os.ReadFile(r.probePath(n.ID))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		var m remembered
		if err := json.Unmarshal(b, &m); err != nil {
			return errors.New("node " + n.ID + " probe record is corrupt")
		}
		r.last[n.ID] = probe{golden: m.Golden, host: m.Host, leases: m.Leases, ip: m.IP, at: m.At}
		r.changed[n.ID] = true
	}
	return nil
}

func (r *Runtime) probePath(id string) string {
	return filepath.Join(r.persistDir, id+".probe.json")
}

// remember writes one probe atomically; r.mu is held.
func (r *Runtime) remember(id string, p probe) error {
	if r.persistDir == "" {
		return nil
	}
	m := remembered{Golden: p.golden, Host: p.host, Leases: p.leases, IP: p.ip, At: p.at}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := r.probePath(id) + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, r.probePath(id))
}
