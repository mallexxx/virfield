package domain

// AdmissionConstraint records the exact arithmetic used for one quota. Byte
// quantities remain integers; Summary also labels decimal GB and binary GiB.
// A nil headroom means the inputs are unknown, not zero capacity.
type AdmissionConstraint struct {
	Resource      string `json:"resource"`
	Location      string `json:"location,omitempty"`
	Unit          string `json:"unit"`
	Limit         int64  `json:"limit"`
	Reserved      int64  `json:"reserved"`
	Requested     int64  `json:"requested"`
	SafetyReserve int64  `json:"safety_reserve"`
	ObservedUsed  int64  `json:"observed_used"`
	Headroom      *int64 `json:"headroom"`
	Allowed       bool   `json:"allowed"`
	Failure       string `json:"failure,omitempty"`
	UnknownReason string `json:"unknown_reason,omitempty"`
}

// AdmissionSnapshot is advisory in status. Admission and dispatch recheck use
// the same calculation under the controller lock before reserving resources.
type AdmissionSnapshot struct {
	Host             string                `json:"host"`
	Constraints      []AdmissionConstraint `json:"constraints"`
	SelectedLocation string                `json:"selected_location,omitempty"`
	Allowed          bool                  `json:"allowed"`
	ErrorCode        string                `json:"error_code,omitempty"`
	Summary          string                `json:"summary"`
}

type TemplateAdmission struct {
	InventoryVerified bool                         `json:"inventory_verified"`
	Acquire           AdmissionSnapshot            `json:"acquire"`
	Destinations      map[string]AdmissionSnapshot `json:"destinations,omitempty"`
	ImageBuild        *AdmissionSnapshot           `json:"image_build,omitempty"`
}
