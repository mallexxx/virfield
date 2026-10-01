package httpapi

import (
	"github.com/mallexxx/virfield/internal/domain"
	"net/http"
)

func (s *Server) registrySources(w http.ResponseWriter, r *http.Request) {
	v, err := s.c.RegistrySources()
	if err != nil {
		s.fail(w, err)
		return
	}
	write(w, 200, v)
}
func (s *Server) registryResolve(w http.ResponseWriter, r *http.Request) {
	var req domain.RegistryResolveRequest
	if err := decode(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.c.ResolveRegistry(r.Context(), req)
	if err != nil {
		s.fail(w, err)
		return
	}
	write(w, 200, v)
}
func (s *Server) pullImage(w http.ResponseWriter, r *http.Request) {
	var req domain.ImagePullRequest
	if err := decode(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.c.PullImage(r.Context(), req, r.Header.Get("Idempotency-Key"))
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Location", "/api/v1/jobs/"+v.Job.ID)
	status := 202
	if v.Replayed {
		status = 200
	}
	write(w, status, v)
}

func (s *Server) publishImage(w http.ResponseWriter, r *http.Request) {
	var req domain.ImagePublishRequest
	if err := decode(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.c.PublishImage(r.Context(), req, r.Header.Get("Idempotency-Key"))
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Location", "/api/v1/jobs/"+v.Job.ID)
	status := 202
	if v.Replayed {
		status = 200
	}
	write(w, status, v)
}
