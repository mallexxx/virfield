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
	Disks           []StorageDisk    `json:"disks,omitempty"`
	Limits          ResourceLimits   `json:"limits"`
	Reserved        Resources        `json:"reserved"`
	DiskAvailable   map[string]int64 `json:"disk_available_bytes"`
	Error           string           `json:"error,omitempty"`
	CacheBytes      int64            `json:"cache_bytes,omitempty"`
	CacheLimitBytes int64            `json:"cache_limit_bytes,omitempty"`
}

// StorageDisk identifies an independently observed filesystem. Node overlays and
// goldens may live on different devices; neither can be hidden by an aggregate.
type StorageDisk struct {
	Purpose        string `json:"purpose"`
	DeviceIdentity string `json:"device_identity"`
	AvailableBytes int64  `json:"available_bytes"`
	Observed       bool   `json:"observed"`
}
