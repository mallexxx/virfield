package httpapi

import (
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

var appleAuthTemplate = template.Must(template.New("apple-auth").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Apple Developer sign-in</title><link rel="stylesheet" href="/style.css"></head>
<body><main><h1>Open Apple Developer</h1>
<p>Virfield opens Apple's website in a private browser window. Enter your Apple Account and verification code on Apple's page. Virfield receives only the download session and continues this Xcode job.</p>
<p><a href="{{.AppURL}}">Open Apple Developer sign-in</a></p>
</main></body></html>`))

type applePage struct {
	AppURL template.URL
}

func (s *Server) appleOriginOK(r *http.Request) bool {
	u, err := url.Parse(s.origin)
	return err == nil && u.Scheme == "http" && r.Host == u.Host
}

func (s *Server) appleAuthLink(w http.ResponseWriter, r *http.Request) {
	if s.origin == "" || s.apple == nil {
		s.fail(w, domain.Err("apple_auth_unavailable", "Apple browser sign-in is not configured"))
		return
	}
	token, err := s.apple.Begin(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	reply, err := s.apple.Session(token)
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	write(w, 200, map[string]any{
		"url":        s.origin + "/apple-auth/" + token,
		"deep_link":  s.appleAppURL(token, reply.AppleURL),
		"expires_at": time.Now().Add(15 * time.Minute).UTC(),
	})
}

func (s *Server) appleAppURL(token, downloadURL string) string {
	u := &url.URL{Scheme: "virfield-apple-auth", Host: "start"}
	q := u.Query()
	q.Set("token", token)
	q.Set("origin", s.origin)
	q.Set("download", downloadURL)
	u.RawQuery = q.Encode()
	return u.String()
}

func (s *Server) appleAuthPage(w http.ResponseWriter, r *http.Request) {
	if !s.appleOriginOK(r) {
		http.Error(w, "Local sign-in link is invalid", http.StatusForbidden)
		return
	}
	token := r.PathValue("token")
	reply, err := s.apple.Session(token)
	if err != nil {
		http.Error(w, "Sign-in link expired; ask the agent for a new link", http.StatusNotFound)
		return
	}
	appURL := s.appleAppURL(token, reply.AppleURL)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.URL.Query().Get("manual") != "1" {
		w.Header().Set("Location", appURL)
		w.WriteHeader(http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := appleAuthTemplate.Execute(w, applePage{AppURL: template.URL(appURL)}); err != nil {
		s.log.Error("render Apple browser launch page", "error", err)
	}
}

func (s *Server) appleBrowserSession(w http.ResponseWriter, r *http.Request) {
	if !s.appleOriginOK(r) || r.Header.Get("Origin") != "" {
		http.Error(w, "Apple browser request is invalid", http.StatusForbidden)
		return
	}
	if ct := strings.Split(r.Header.Get("Content-Type"), ";")[0]; ct != "application/json" {
		http.Error(w, "Invalid Apple browser request", http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var input struct {
		Cookies string `json:"cookies"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil {
		http.Error(w, "Invalid Apple browser request", http.StatusBadRequest)
		return
	}
	if err := s.apple.ResumeBrowserCookies(r.Context(), r.PathValue("token"), input.Cookies); err != nil {
		s.fail(w, err)
		return
	}
	write(w, http.StatusAccepted, map[string]string{"status": "download_queued"})
}
