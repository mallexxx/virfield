// Package domain defines the durable Virfield API contracts.
package domain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"time"
)

const MaxVMs = 2

// Error is a safe, machine-readable failure returned to API clients.
type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func (e *Error) Error() string        { return e.Code + ": " + e.Message }
func Err(code, message string) *Error { return &Error{Code: code, Message: message} }

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,79}$`)

func ValidName(s string) bool { return namePattern.MatchString(s) }
func NewID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b[:])
}

// Template is operator-configured or catalog-resolved into an approved storage
// location. A caller never supplies a host filesystem path.
type Template struct {
	Image    *ImageProfile `json:"image,omitempty"`
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Location string        `json:"location"`
}

// AcquireRequest reserves a slot immediately. Queueing belongs to Execution Broker.
type AcquireRequest struct {
	SSHPublicKey string `json:"ssh_public_key,omitempty"`
	Template     string `json:"template"`
	TTLSeconds   int    `json:"ttl_seconds"`
}

func (r AcquireRequest) Validate() error {
	if !ValidName(r.Template) {
		return Err("invalid_request", "template must be a configured image identifier")
	}
	if r.TTLSeconds < 60 || r.TTLSeconds > 86400 {
		return Err("invalid_request", "ttl_seconds must be between 60 and 86400")
	}
	return nil
}

// Lease holds capacity until cleanup is confirmed, including after expiry/failure.
type Lease struct {
	Resources      Resources      `json:"resources"`
	Source         *Template      `json:"source,omitempty"`
	ImageID        string         `json:"image_id,omitempty"`
	SSHPublicKey   string         `json:"ssh_public_key,omitempty"`
	SSH            *SSHConnection `json:"ssh,omitempty"`
	ImageManifest  string         `json:"image_manifest,omitempty"`
	Purpose        string         `json:"purpose,omitempty"`
	ID             string         `json:"id"`
	VMName         string         `json:"vm_name"`
	Location       string         `json:"location"`
	Template       string         `json:"template"`
	State          string         `json:"state"`
	CloneConfirmed bool           `json:"clone_confirmed"`
	StartPending   bool           `json:"start_pending"`
	IP             string         `json:"ip,omitempty"`
	ExpiresAt      time.Time      `json:"expires_at"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	Error          *Error         `json:"error,omitempty"`
}

// Job is a persisted state machine. Phase is written before each external effect.
type Job struct {
	Image     *ImageProfile `json:"image,omitempty"`
	Progress  string        `json:"progress,omitempty"`
	ID        string        `json:"id"`
	LeaseID   string        `json:"lease_id"`
	Kind      string        `json:"kind"`
	Phase     string        `json:"phase"`
	State     string        `json:"state"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	Deadline  time.Time     `json:"deadline"`
	Error     *Error        `json:"error,omitempty"`
}
type Event struct {
	ID      int64     `json:"event_id"`
	LeaseID string    `json:"lease_id"`
	JobID   string    `json:"job_id,omitempty"`
	Type    string    `json:"type"`
	Message string    `json:"message"`
	At      time.Time `json:"at"`
}
type Operation struct {
	Lease    Lease `json:"lease"`
	Job      Job   `json:"job"`
	Replayed bool  `json:"replayed"`
}

// VM is an observation, never an instruction or an ownership claim.
type VM struct {
	Resources    Resources `json:"resources"`
	Name         string    `json:"name"`
	Location     string    `json:"location"`
	OS           string    `json:"os"`
	State        string    `json:"state"`
	IP           string    `json:"ip,omitempty"`
	SSHAvailable bool      `json:"ssh_available"`
}

func (v VM) Key() string    { return v.Location + "/" + v.Name }
func (l Lease) Key() string { return l.Location + "/" + l.VMName }

type Observation struct {
	VMs      []VM      `json:"vms"`
	HostUsed int       `json:"host_used"`
	HostMax  int       `json:"host_max"`
	At       time.Time `json:"at"`
	Error    *Error    `json:"error,omitempty"`
}
type Capacity struct {
	Limit     int      `json:"limit"`
	Used      int      `json:"used"`
	Available int      `json:"available"`
	Blockers  []string `json:"blockers"`
}

func (c Capacity) FullError() *Error {
	return Err("capacity_exhausted", fmt.Sprintf("Cannot run more than %d macOS VMs: %d/%d slots occupied or reserved. Release a lease or stop an external VM; queued execution belongs to Broker.", c.Limit, c.Used, c.Limit))
}

type Status struct {
	Resources   *ResourceStatus `json:"resources,omitempty"`
	Capacity    Capacity        `json:"capacity"`
	Observation Observation     `json:"observation"`
	Leases      []Lease         `json:"leases"`
	Jobs        []Job           `json:"jobs"`
	Templates   []Template      `json:"templates"`
}
