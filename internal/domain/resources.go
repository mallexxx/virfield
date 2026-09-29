package domain

// Resources are immutable reservations copied from the observed source VM.
type Resources struct {
	CPU         int   `json:"cpu"`
	MemoryBytes int64 `json:"memory_bytes"`
	DiskBytes   int64 `json:"disk_bytes"`
}
type ResourceLimits struct {
	CPU              int   `json:"cpu"`
	MemoryBytes      int64 `json:"memory_bytes"`
	DiskReserveBytes int64 `json:"disk_reserve_bytes"`
}
type ResourceStatus struct {
	Limits        ResourceLimits   `json:"limits"`
	Reserved      Resources        `json:"reserved"`
	DiskAvailable map[string]int64 `json:"disk_available_bytes"`
	Error         string           `json:"error,omitempty"`
}
