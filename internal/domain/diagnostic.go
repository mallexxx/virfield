package domain

import "time"

// RuntimeIdentity contains only inventory identity/endpoint fields. It never
// contains SSH credentials, host keys, desktop URLs or protocol payloads.
type RuntimeIdentity struct {
	RuntimeID   string    `json:"runtime_id,omitempty"`
	VMName      string    `json:"vm_name"`
	Location    string    `json:"location"`
	Provider    string    `json:"provider,omitempty"`
	HostID      string    `json:"host_id,omitempty"`
	ContainerID string    `json:"container_id,omitempty"`
	OS          string    `json:"os,omitempty"`
	Resources   Resources `json:"resources"`
	State       string    `json:"state"`
	IP          string    `json:"ip,omitempty"`
	SSHPort     int       `json:"ssh_port"`
	Exists      bool      `json:"exists"`
	ObservedAt  time.Time `json:"observed_at"`
	LastSeenAt  time.Time `json:"last_seen_at,omitempty"`
}

type DiagnosticError struct {
	Type              string `json:"type"`
	Code              string `json:"code,omitempty"`
	Message           string `json:"message"`
	FingerprintSHA256 string `json:"fingerprint_sha256,omitempty"`
}

// Diagnostic is an allowlisted technical record. Storage retains the latest 32
// per kind and lease until normal 30-day history retention, including after VM
// cleanup. Direction/byte counts describe transport, never its contents.
type Diagnostic struct {
	Version               int              `json:"version"`
	Kind                  string           `json:"kind"`
	Action                string           `json:"action"`
	Reason                string           `json:"reason"`
	Summary               string           `json:"summary"`
	At                    time.Time        `json:"at"`
	Service               string           `json:"service,omitempty"`
	Expected              *RuntimeIdentity `json:"expected,omitempty"`
	Observed              *RuntimeIdentity `json:"observed,omitempty"`
	ObservationAt         time.Time        `json:"observation_at,omitempty"`
	ObservationVerified   bool             `json:"observation_verified"`
	HostAvailability      string           `json:"host_availability,omitempty"`
	HostUnavailableReason string           `json:"host_unavailable_reason,omitempty"`
	BackendError          *DiagnosticError `json:"backend_error,omitempty"`
	Direction             string           `json:"direction,omitempty"`
	BytesToGuest          int64            `json:"bytes_to_guest,omitempty"`
	BytesToClient         int64            `json:"bytes_to_client,omitempty"`
}
