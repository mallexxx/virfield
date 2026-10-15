package httpapi

import (
	"github.com/mallexxx/virfield/internal/domain"
	"net/http"
)

func (s *Server) transferImage(w http.ResponseWriter, r *http.Request) {
	var request domain.ImageTransferRequest
	if err := decode(w, r, &request); err != nil {
		s.fail(w, err)
		return
	}
	op, err := s.c.TransferImage(r.Context(), request, r.Header.Get("Idempotency-Key"))
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Location", "/api/v1/jobs/"+op.Job.ID)
	status := http.StatusAccepted
	if op.Replayed {
		status = http.StatusOK
	}
	write(w, status, op)
}
