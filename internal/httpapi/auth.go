package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
)

type Principal struct {
	Name   string
	Token  string
	Scopes []string
}

type authPrincipal struct {
	name   string
	hash   [32]byte
	scopes map[string]bool
}

type AuthSet struct{ entries atomic.Value }

func NewAuthSet(ownerToken string, principals []Principal) (*AuthSet, error) {
	a := &AuthSet{}
	if err := a.Replace(ownerToken, principals); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *AuthSet) Replace(ownerToken string, principals []Principal) error {
	if len(ownerToken) < 32 {
		return errors.New("owner token is too short")
	}
	entries := []authPrincipal{{name: "operator", hash: sha256.Sum256([]byte(ownerToken)), scopes: map[string]bool{"operator": true}}}
	seen := map[string]bool{"operator": true}
	for _, p := range principals {
		if p.Name == "" || seen[p.Name] || len(p.Token) < 32 || len(p.Scopes) == 0 {
			return errors.New("invalid principal token or name")
		}
		seen[p.Name] = true
		hash := sha256.Sum256([]byte(p.Token))
		for _, entry := range entries {
			if subtle.ConstantTimeCompare(hash[:], entry.hash[:]) == 1 {
				return errors.New("principal tokens must be distinct")
			}
		}
		scopes := map[string]bool{}
		for _, scope := range p.Scopes {
			if scope != "lease:own" && scope != "image:build" {
				return errors.New("invalid principal scope")
			}
			scopes[scope] = true
		}
		entries = append(entries, authPrincipal{name: p.Name, hash: hash, scopes: scopes})
	}
	a.entries.Store(entries)
	return nil
}

func (a *AuthSet) authenticate(header string) (authPrincipal, bool) {
	if !strings.HasPrefix(header, "Bearer ") {
		return authPrincipal{}, false
	}
	hash := sha256.Sum256([]byte(strings.TrimPrefix(header, "Bearer ")))
	entries, ok := a.entries.Load().([]authPrincipal)
	if !ok {
		return authPrincipal{}, false
	}
	var found authPrincipal
	matched := 0
	for _, entry := range entries {
		equal := subtle.ConstantTimeCompare(hash[:], entry.hash[:])
		if equal == 1 {
			found = entry
		}
		matched |= equal
	}
	return found, matched == 1
}

type principalContextKey struct{}

func withPrincipal(r *http.Request, p authPrincipal) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), principalContextKey{}, p))
}
func principalFrom(r *http.Request) authPrincipal {
	p, _ := r.Context().Value(principalContextKey{}).(authPrincipal)
	return p
}
func (p authPrincipal) operator() bool { return p.scopes["operator"] }
func (p authPrincipal) permits(r *http.Request) bool {
	if p.operator() {
		return true
	}
	if r.URL.Path == "/mcp" {
		return false
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != "api" || parts[1] != "v1" {
		return false
	}
	if p.scopes["lease:own"] && parts[2] == "leases" {
		if len(parts) == 3 && (r.Method == "POST" || r.Method == "GET") {
			return true
		}
		if len(parts) == 4 && r.Method == "GET" {
			return true
		}
		if len(parts) == 5 {
			switch parts[4] {
			case "tunnel":
				return r.Method == "POST" || r.Method == "DELETE"
			case "release":
				return r.Method == "POST"
			case "expiry":
				return r.Method == "PUT"
			}
		}
	}
	if p.scopes["image:build"] {
		if len(parts) == 4 && parts[2] == "images" && parts[3] == "catalog" && r.Method == "GET" {
			return true
		}
		if len(parts) == 3 && parts[2] == "images" && r.Method == "POST" {
			return true
		}
		if len(parts) == 5 && parts[2] == "images" && parts[4] == "build" && r.Method == "POST" {
			return true
		}
		if len(parts) == 4 && parts[2] == "jobs" && r.Method == "GET" {
			return true
		}
		if len(parts) == 5 && parts[2] == "jobs" && parts[4] == "apple-auth" && r.Method == "POST" {
			return true
		}
	}
	return false
}
