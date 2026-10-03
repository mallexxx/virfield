// Package httpapi serves the authenticated versioned API and embedded console.
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mallexxx/virfield/internal/appleauth"
	"github.com/mallexxx/virfield/internal/control"
	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/web"
)

type Server struct {
	c            *control.Controller
	authSet      *AuthSet
	log          *slog.Logger
	apple        *appleauth.Service
	origin       string
	allowedHosts map[string]bool
}

type Options struct {
	Auth         *AuthSet
	AllowedHosts []string
	MCP          http.Handler
	Backup       http.Handler
	AppleAuth    *appleauth.Service
	Origin       string
}

func New(c *control.Controller, token string, log *slog.Logger, options ...Options) http.Handler {
	var extra Options
	if len(options) > 0 {
		extra = options[0]
	}
	authSet := extra.Auth
	if authSet == nil {
		authSet = &AuthSet{}
		_ = authSet.Replace(token, nil)
	}
	s := &Server{c: c, authSet: authSet, log: log, apple: extra.AppleAuth, origin: extra.Origin}
	if len(extra.AllowedHosts) > 0 {
		s.allowedHosts = map[string]bool{}
		for _, host := range extra.AllowedHosts {
			s.allowedHosts[host] = true
		}
	}
	mux := http.NewServeMux()
	static := web.Handler()
	mux.Handle("GET /{$}", static)
	mux.Handle("GET /app.js", static)
	mux.Handle("GET /style.css", static)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { write(w, 200, map[string]string{"status": "alive"}) })
	api := http.NewServeMux()
	if extra.Backup != nil {
		api.Handle("POST /api/v1/maintenance/backup", extra.Backup)
	}
	api.HandleFunc("GET /api/v1/status", s.status)
	api.HandleFunc("GET /api/v1/images/catalog", s.catalog)
	api.HandleFunc("POST /api/v1/jobs/{id}/apple-auth", s.appleAuthLink)
	if s.apple != nil {
		mux.HandleFunc("GET /apple-auth/{token}", s.appleAuthPage)
		mux.HandleFunc("POST /apple-auth/{token}/session", s.appleBrowserSession)
	}
	api.HandleFunc("GET /api/v1/registry/sources", s.registrySources)
	api.HandleFunc("POST /api/v1/registry/resolve", s.registryResolve)
	api.HandleFunc("POST /api/v1/images/pull", s.pullImage)
	api.HandleFunc("POST /api/v1/images/publish", s.publishImage)
	api.HandleFunc("POST /api/v1/images", s.createImage)
	api.HandleFunc("POST /api/v1/images/{id}/delete", s.deleteImage)
	api.HandleFunc("POST /api/v1/images/{id}/build", s.buildImage)
	api.HandleFunc("POST /api/v1/images/{id}/provision", s.provisionImage)
	api.HandleFunc("POST /api/v1/images/{id}/recover", s.recoverImage)
	api.HandleFunc("POST /api/v1/leases", s.acquire)
	api.HandleFunc("GET /api/v1/leases", s.leases)
	api.HandleFunc("GET /api/v1/leases/{id}", s.lease)
	api.HandleFunc("POST /api/v1/leases/{id}/tunnel", s.openTunnel)
	api.HandleFunc("DELETE /api/v1/leases/{id}/tunnel", s.closeTunnel)
	api.HandleFunc("POST /api/v1/leases/{id}/release", s.release)
	api.HandleFunc("PUT /api/v1/leases/{id}/expiry", s.renew)
	api.HandleFunc("POST /api/v1/leases/{id}/resolve", s.resolve)
	api.HandleFunc("GET /api/v1/jobs/{id}", s.job)
	api.HandleFunc("GET /api/v1/events", s.events)
	mux.Handle("/api/", s.auth(api))
	if extra.MCP != nil {
		mux.Handle("/mcp", s.auth(http.MaxBytesHandler(extra.MCP, 64<<10)))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.allowedHosts != nil && !s.allowedHosts[r.Host] {
			http.Error(w, "Host is not allowed", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		mux.ServeHTTP(w, r)
	})
}
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No cookies, query-string tokens or cross-origin access. An explicit bearer
		// header is required even on loopback; browser mutations must be same-origin.
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host && origin != "https://"+r.Host {
			s.fail(w, domain.Err("forbidden", "cross-origin access is not allowed"))
			return
		}
		principal, ok := s.authSet.authenticate(r.Header.Get("Authorization"))
		if !ok {
			w.Header().Set("WWW-Authenticate", "Bearer")
			s.fail(w, domain.Err("unauthorized", "a valid bearer token is required"))
			return
		}
		if !principal.permits(r) {
			s.fail(w, domain.Err("forbidden", "principal lacks scope for this operation"))
			return
		}
		next.ServeHTTP(w, withPrincipal(r, principal))
	})
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (s *Server) fail(w http.ResponseWriter, err error) {
	var e *domain.Error
	if !errors.As(err, &e) {
		s.log.Error("API failure", "error", err)
		e = domain.Err("internal_error", "Operation could not be persisted; inspect daemon logs")
	}
	status := statusForCode(e.Code)
	if e.Retryable {
		w.Header().Set("Retry-After", "2")
	}
	write(w, status, map[string]any{"error": e})
}

func statusForCode(code string) int {
	switch code {
	case "invalid_request", "invalid_profile", "invalid_registry", "registry_invalid", "registry_format_unsupported", "ssh_profile_missing", "unsafe_retry":
		return http.StatusBadRequest
	case "unauthorized", "registry_auth_required":
		return http.StatusUnauthorized
	case "forbidden":
		return http.StatusForbidden
	case "not_found", "registry_not_found", "unknown_template", "unknown_registry", "version_not_found":
		return http.StatusNotFound
	case "registry_tag_exists", "resource_exhausted", "resource_unknown", "resource_unavailable", "ssh_key_in_use", "legacy_uuid_in_use", "image_in_use", "image_exists", "capacity_exhausted", "idempotency_conflict", "operation_in_progress", "lease_expired", "lease_released", "lease_unavailable", "outcome_unknown", "template_unavailable", "apple_auth_required", "backup_failed", "vm_drift", "vm_missing", "start_rejected", "catalog_changed", "disk_encrypted", "disk_locked", "disk_policy_unknown", "download_integrity", "image_interrupted", "ownership_unconfirmed", "registry_digest_mismatch", "registry_source_changed", "registry_unsanitized", "security_policy_failed", "sip_verification_failed", "unexpected_state", "unsafe_cache", "xcode_signature_failed", "xcode_version_mismatch":
		return http.StatusConflict
	case "backend_unavailable", "catalog_unavailable", "registry_unavailable", "apple_auth_unavailable", "tunnel_unavailable", "recovery_unavailable", "image_dependency", "setup_unavailable", "unsupported_lume", "registry_push_disabled", "registry_unconfigured", "image_credentials_missing":
		return http.StatusServiceUnavailable
	case "disk_space":
		return http.StatusInsufficientStorage
	case "guest_authentication_failed", "guest_build_mismatch", "guest_command_failed", "guest_connect_failed", "guest_identity_failed", "guest_shutdown_failed", "guest_ssh_failed", "guest_timeout", "guest_version_mismatch", "assistant_incomplete", "credential_autologin_failed", "credential_cleanup_failed", "credential_verification_failed", "desktop_timeout", "download_failed", "image_command_failed", "lease_ssh_failed", "provision_verification_failed", "recovery_stop_unconfirmed", "registry_bootstrap_failed", "registry_publish_unknown", "stop_timeout", "tool_provision_failed", "xcode_install_failed", "xcode_transfer_failed", "xcode_verification_failed":
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}
func decode(w http.ResponseWriter, r *http.Request, out any) error {
	if ct := strings.Split(r.Header.Get("Content-Type"), ";")[0]; ct != "application/json" {
		return domain.Err("invalid_request", "Content-Type must be application/json")
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return domain.Err("invalid_request", "body must be valid JSON with only documented fields (maximum 16 KiB)")
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return domain.Err("invalid_request", "body must contain exactly one JSON object")
	}
	return nil
}
func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	v, err := s.c.Status(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	write(w, 200, v)
}
func (s *Server) acquire(w http.ResponseWriter, r *http.Request) {
	var req domain.AcquireRequest
	if err := decode(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}
	owner := ""
	if p := principalFrom(r); !p.operator() {
		owner = p.name
	}
	v, err := s.c.AcquireAs(r.Context(), r.Header.Get("Idempotency-Key"), req, owner)
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
func (s *Server) lease(w http.ResponseWriter, r *http.Request) {
	v, err := s.c.Lease(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if p := principalFrom(r); !p.operator() && v.Owner != p.name {
		s.fail(w, domain.Err("forbidden", "lease belongs to another principal"))
		return
	}
	write(w, 200, v)
}
func (s *Server) leases(w http.ResponseWriter, r *http.Request) {
	all, err := s.c.Leases(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	p := principalFrom(r)
	want := r.URL.Query().Get("owner")
	if !p.operator() && want != "" && want != p.name {
		s.fail(w, domain.Err("forbidden", "cannot list another principal's leases"))
		return
	}
	visible := make([]domain.Lease, 0, len(all))
	for _, l := range all {
		if (!p.operator() && l.Owner != p.name) || (p.operator() && want != "" && l.Owner != want) {
			continue
		}
		visible = append(visible, l)
	}
	write(w, 200, map[string]any{"leases": visible})
}
func (s *Server) job(w http.ResponseWriter, r *http.Request) {
	v, err := s.c.Job(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if p := principalFrom(r); !p.operator() && v.Kind != "image_build" {
		s.fail(w, domain.Err("forbidden", "job is outside image build scope"))
		return
	}
	write(w, 200, v)
}
func (s *Server) ensureLeaseOwned(w http.ResponseWriter, r *http.Request) bool {
	p := principalFrom(r)
	if p.operator() {
		return true
	}
	l, err := s.c.Lease(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return false
	}
	if l.Owner != p.name {
		s.fail(w, domain.Err("forbidden", "lease belongs to another principal"))
		return false
	}
	return true
}
func (s *Server) release(w http.ResponseWriter, r *http.Request) {
	if !s.ensureLeaseOwned(w, r) {
		return
	}
	v, err := s.c.Release(r.Context(), r.PathValue("id"), r.Header.Get("Idempotency-Key"))
	if err != nil {
		s.fail(w, err)
		return
	}
	write(w, 202, v)
}
func (s *Server) renew(w http.ResponseWriter, r *http.Request) {
	if !s.ensureLeaseOwned(w, r) {
		return
	}
	var req struct {
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := decode(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.c.Renew(r.Context(), r.PathValue("id"), req.ExpiresAt)
	if err != nil {
		s.fail(w, err)
		return
	}
	write(w, 200, v)
}
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	after := int64(0)
	limit := 100
	var err error
	if v := r.URL.Query().Get("after"); v != "" {
		after, err = strconv.ParseInt(v, 10, 64)
		if err != nil || after < 0 {
			s.fail(w, domain.Err("invalid_request", "after must be a nonnegative event ID"))
			return
		}
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, err = strconv.Atoi(v)
		if err != nil || limit < 1 || limit > 500 {
			s.fail(w, domain.Err("invalid_request", "limit must be 1–500"))
			return
		}
	}
	if r.URL.Query().Get("tail") == "true" {
		if after != 0 {
			s.fail(w, domain.Err("invalid_request", "tail and after cannot be combined"))
			return
		}
		after = -1
	}
	es, err := s.c.Events(r.Context(), after, r.URL.Query().Get("lease_id"), limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	cursor := max(after, 0)
	if len(es) > 0 {
		cursor = es[len(es)-1].ID
	}
	write(w, 200, map[string]any{"events": es, "next_cursor": cursor})
}

func (s *Server) resolve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		VMName  string `json:"vm_name"`
		Confirm bool   `json:"confirm_no_operation_in_flight"`
	}
	if err := decode(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}
	op, err := s.c.Resolve(r.Context(), r.PathValue("id"), r.Header.Get("Idempotency-Key"), req.VMName, req.Confirm)
	if err != nil {
		s.fail(w, err)
		return
	}
	write(w, 202, op)
}

func (s *Server) deleteImage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ConfirmName string `json:"confirm_name"`
	}
	if err := decode(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}
	op, err := s.c.DeleteImage(r.Context(), r.PathValue("id"), r.Header.Get("Idempotency-Key"), req.ConfirmName)
	if err != nil {
		s.fail(w, err)
		return
	}
	write(w, 202, op)
}

func (s *Server) buildImage(w http.ResponseWriter, r *http.Request) {
	var req struct{}
	if err := decode(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}
	op, err := s.c.BuildImage(r.Context(), r.PathValue("id"), r.Header.Get("Idempotency-Key"))
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Location", "/api/v1/jobs/"+op.Job.ID)
	write(w, http.StatusAccepted, op)
}

func (s *Server) recoverImage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"vm_name"`
		Action  string `json:"action"`
		Confirm bool   `json:"confirm_no_operation_in_flight"`
	}
	if err := decode(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}
	op, err := s.c.RecoverImage(r.Context(), r.PathValue("id"), r.Header.Get("Idempotency-Key"), req.Name, req.Action, req.Confirm)
	if err != nil {
		s.fail(w, err)
		return
	}
	write(w, 202, op)
}

func (s *Server) provisionImage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ConfirmName string `json:"confirm_name"`
	}
	if err := decode(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}
	op, err := s.c.ProvisionImage(r.Context(), r.PathValue("id"), r.Header.Get("Idempotency-Key"), req.ConfirmName)
	if err != nil {
		s.fail(w, err)
		return
	}
	write(w, 202, op)
}

func (s *Server) openTunnel(w http.ResponseWriter, r *http.Request) {
	if !s.ensureLeaseOwned(w, r) {
		return
	}
	t, err := s.c.OpenTunnel(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	write(w, 200, t)
}
func (s *Server) closeTunnel(w http.ResponseWriter, r *http.Request) {
	if !s.ensureLeaseOwned(w, r) {
		return
	}
	if err := s.c.CloseTunnel(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	write(w, 200, map[string]bool{"closed": true})
}

func (s *Server) catalog(w http.ResponseWriter, r *http.Request) {
	v, err := s.c.ImageCatalog(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	write(w, 200, v)
}
func (s *Server) createImage(w http.ResponseWriter, r *http.Request) {
	var req domain.ImageCreateRequest
	if err := decode(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.c.CreateImage(r.Context(), req, r.Header.Get("Idempotency-Key"))
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
