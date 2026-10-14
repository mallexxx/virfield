// Package domain defines the durable Virfield API contracts.
package domain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// MaxVMs bounds configuration mistakes. Each provider reports the lower,
// actually usable capacity for its current hosts.
const MaxVMs = 1024

// Error is a safe, machine-readable failure returned to API clients.
type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func (e *Error) Error() string        { return e.Code + ": " + e.Message }
func Err(code, message string) *Error { return &Error{Code: code, Message: message} }

var (
	namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,79}$`)
	uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

func ValidName(s string) bool { return namePattern.MatchString(s) }
func ValidUUID(s string) bool { return uuidPattern.MatchString(strings.ToLower(s)) }
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
	Image     *ImageProfile `json:"image,omitempty"`
	Resources Resources     `json:"resources,omitempty"`
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Location  string        `json:"location"`
	Provider  string        `json:"provider,omitempty"`
	Source    string        `json:"source,omitempty"`
	OS        string        `json:"os,omitempty"`
	Arch      string        `json:"arch,omitempty"`
	GuestUser string        `json:"guest_user,omitempty"`
	GuestHome string        `json:"guest_home,omitempty"`
	// LegacyUUID marks an identity-preserving legacy macOS template. Clones of
	// the same VM UUID are mutually exclusive because the encrypted guest disk is
	// bound to that preserved identity.
	LegacyUUID string `json:"legacy_uuid,omitempty"`
}

// AcquireRequest reserves a slot immediately. Queueing belongs to Execution Broker.
type AcquireRequest struct {
	DestinationLocation string `json:"destination_location,omitempty"`
	SSHPublicKey        string `json:"ssh_public_key,omitempty"`
	Template            string `json:"template"`
	TTLSeconds          int    `json:"ttl_seconds"`
}

func (r AcquireRequest) Validate() error {
	if !ValidName(r.Template) {
		return Err("invalid_request", "template must be a configured image identifier")
	}
	if r.DestinationLocation != "" && !ValidName(r.DestinationLocation) {
		return Err("invalid_request", "destination_location must be an operator-configured storage name")
	}
	if r.TTLSeconds < 60 || r.TTLSeconds > 86400 {
		return Err("invalid_request", "ttl_seconds must be between 60 and 86400")
	}
	return nil
}

// Lease holds capacity until cleanup is confirmed, including after expiry/failure.
type Lease struct {
	Owner                   string         `json:"owner,omitempty"`
	Portable                bool           `json:"portable,omitempty"`
	Resources               Resources      `json:"resources"`
	Source                  *Template      `json:"source,omitempty"`
	ImageID                 string         `json:"image_id,omitempty"`
	SSHPublicKey            string         `json:"ssh_public_key,omitempty"`
	SSH                     *SSHConnection `json:"ssh,omitempty"`
	ImageManifest           string         `json:"image_manifest,omitempty"`
	LegacyUUID              string         `json:"legacy_uuid,omitempty"`
	Purpose                 string         `json:"purpose,omitempty"`
	ID                      string         `json:"id"`
	VMName                  string         `json:"vm_name"`
	Location                string         `json:"location"`
	Template                string         `json:"template"`
	Provider                string         `json:"provider,omitempty"`
	GuestUser               string         `json:"guest_user,omitempty"`
	GuestHome               string         `json:"guest_home,omitempty"`
	State                   string         `json:"state"`
	CloneConfirmed          bool           `json:"clone_confirmed"`
	StartPending            bool           `json:"start_pending"`
	StartRejected           bool           `json:"start_rejected,omitempty"`
	StartAbsentObservations int            `json:"start_absent_observations,omitempty"`
	IP                      string         `json:"ip,omitempty"`
	// SSHPort is the guest SSH port on IP; 0 means 22.
	SSHPort   int       `json:"ssh_port,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Error     *Error    `json:"error,omitempty"`
}

// Job is a persisted state machine. Phase is written before each external effect.
type Job struct {
	Export    *RegistryExport `json:"export,omitempty"`
	Image     *ImageProfile   `json:"image,omitempty"`
	Progress  string          `json:"progress,omitempty"`
	ID        string          `json:"id"`
	LeaseID   string          `json:"lease_id"`
	Kind      string          `json:"kind"`
	Phase     string          `json:"phase"`
	State     string          `json:"state"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
	Deadline  time.Time       `json:"deadline"`
	Error     *Error          `json:"error,omitempty"`
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
	Resources   Resources `json:"resources"`
	Name        string    `json:"name"`
	Location    string    `json:"location"`
	Provider    string    `json:"provider,omitempty"`
	HostID      string    `json:"host_id,omitempty"`
	ContainerID string    `json:"container_id,omitempty"`
	OS          string    `json:"os"`
	State       string    `json:"state"`
	IP          string    `json:"ip,omitempty"`
	// SSHPort is the guest SSH port on IP; 0 means 22.
	SSHPort           int       `json:"ssh_port,omitempty"`
	UnavailableReason string    `json:"unavailable_reason,omitempty"`
	LastSeenAt        time.Time `json:"last_seen_at,omitempty"`
	SSHAvailable      bool      `json:"ssh_available"`
	// UITesting is a physical macOS boot volume's XCUITest readiness: ready,
	// devtools_disabled, automation_mode_requires_auth or no_console_session.
	// Empty when not reported.
	UITesting string `json:"ui_testing,omitempty"`
}

func (v VM) Key() string    { return v.Location + "/" + v.Name }
func (l Lease) Key() string { return l.Location + "/" + l.VMName }

type Observation struct {
	VMs               []VM               `json:"vms"`
	Hosts             []Host             `json:"hosts,omitempty"`
	StorageContainers []StorageContainer `json:"storage_containers,omitempty"`
	Provider          string             `json:"provider,omitempty"`
	HostUsed          int                `json:"host_used"`
	HostMax           int                `json:"host_max"`
	At                time.Time          `json:"at"`
	Error             *Error             `json:"error,omitempty"`
}
type Capacity struct {
	Limit     int      `json:"limit"`
	Used      int      `json:"used"`
	Available int      `json:"available"`
	Blockers  []string `json:"blockers"`
}

func (c Capacity) FullError() *Error {
	return Err("capacity_exhausted", fmt.Sprintf("Cannot run more than %d targets: %d/%d slots occupied or reserved. Release a lease or stop an external target; queued execution belongs to Broker.", c.Limit, c.Used, c.Limit))
}

type Status struct {
	Resources            *ResourceStatus           `json:"resources,omitempty"`
	NodeResources        map[string]ResourceStatus `json:"node_resources,omitempty"`
	ImageReservations    []ImageReservation        `json:"image_reservations,omitempty"`
	Capacity             Capacity                  `json:"capacity"`
	Observation          Observation               `json:"observation"`
	Leases               []Lease                   `json:"leases"`
	Jobs                 []Job                     `json:"jobs"`
	Templates            []Template                `json:"templates"`
	TemplateAvailability []TemplateAvailability    `json:"template_availability,omitempty"`
}

type Host struct {
	ID                string       `json:"id"`
	Name              string       `json:"name"`
	Provider          string       `json:"provider"`
	OS                string       `json:"os"`
	Arch              string       `json:"arch"`
	Availability      string       `json:"availability"`
	UnavailableReason string       `json:"unavailable_reason,omitempty"`
	LastSeenAt        time.Time    `json:"last_seen_at,omitempty"`
	WakeOnLAN         bool         `json:"wake_on_lan,omitempty"`
	Capacity          HostCapacity `json:"capacity"`
}

type HostCapacity struct {
	Total              Resources `json:"total"`
	Allocatable        Resources `json:"allocatable"`
	Allocated          Resources `json:"allocated"`
	Available          Resources `json:"available"`
	MemoryReserveBytes int64     `json:"memory_reserve_bytes"`
	MaxVMs             int       `json:"max_vms"`
	UsedVMs            int       `json:"used_vms"`
}

type StorageContainer struct {
	ID                string    `json:"id"`
	PoolID            string    `json:"pool_id,omitempty"`
	HostID            string    `json:"host_id"`
	Provider          string    `json:"provider"`
	Kind              string    `json:"kind"`
	Path              string    `json:"path,omitempty"`
	DeviceIdentity    string    `json:"device_identity,omitempty"`
	Availability      string    `json:"availability"`
	UnavailableReason string    `json:"unavailable_reason,omitempty"`
	CapacityBytes     int64     `json:"capacity_bytes,omitempty"`
	AvailableBytes    int64     `json:"available_bytes,omitempty"`
	CacheBytes        int64     `json:"cache_bytes,omitempty"`
	LastSeenAt        time.Time `json:"last_seen_at,omitempty"`
}

type TemplateAvailability struct {
	TemplateID        string `json:"template_id"`
	ContainerID       string `json:"container_id"`
	Availability      string `json:"availability"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
}

// Deadline is the job's failure limit, not a predicted completion time.
type ImageReservation struct {
	ImageID  string    `json:"image_id"`
	JobID    string    `json:"job_id,omitempty"`
	State    string    `json:"state"`
	Since    time.Time `json:"since"`
	Deadline time.Time `json:"deadline,omitempty"`
}
