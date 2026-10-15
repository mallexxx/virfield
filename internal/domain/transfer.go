package domain

// ImageTransferRequest copies a managed, verified local golden. Source deletion
// is deliberately absent: it is a separate operator operation.
type ImageTransferRequest struct {
	Template string `json:"template"`
	ID       string `json:"id"`
	Location string `json:"location"`
}

func (r ImageTransferRequest) Validate() error {
	if !ValidName(r.Template) || !ValidName(r.ID) || !ValidName(r.Location) {
		return Err("invalid_request", "transfer requires a source template, new ID/name and configured destination location")
	}
	return nil
}

// ImageTransfer pins both the source record and destination template before I/O.
// ContentSHA256 covers the copied runtime files; credentials never enter it.
type ImageTransfer struct {
	Source        Lease    `json:"source"`
	Destination   Template `json:"destination"`
	ContentSHA256 string   `json:"content_sha256,omitempty"`
}
