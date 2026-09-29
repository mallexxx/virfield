package client

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRedirectCannotLeakToken(t *testing.T) {
	targetCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls++; fmt.Fprint(w, `{}`) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	c, err := New(source.URL, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Do(context.Background(), "GET", "status", nil, ""); err == nil {
		t.Fatal("redirect accepted")
	}
	if targetCalls != 0 {
		t.Fatal("token sent to redirect destination")
	}
}
func TestRemoteHTTPRejected(t *testing.T) {
	if _, err := New("http://example.com", "secret"); err == nil {
		t.Fatal("cleartext remote API accepted")
	}
}
