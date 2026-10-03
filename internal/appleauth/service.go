// Package appleauth owns short-lived, local Apple sign-in sessions. No account,
// password, code, callback or cookie is written to jobs, events or daemon logs.
package appleauth

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
)

type JobControl interface {
	AppleAuthJob(context.Context, string) (domain.XcodeRelease, error)
	ResumeAppleDownload(context.Context, string) error
}

type Phone struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
}
type Reply struct {
	State    string  `json:"state"`
	Message  string  `json:"message,omitempty"`
	IDPURL   string  `json:"idpURL,omitempty"`
	AppleURL string  `json:"appleURL,omitempty"`
	Phones   []Phone `json:"phones,omitempty"`
	Cookies  string  `json:"cookies,omitempty"`
}
type Request struct {
	Op       string `json:"op"`
	Account  string `json:"account,omitempty"`
	Password string `json:"password,omitempty"`
	Code     string `json:"code,omitempty"`
	PhoneID  int    `json:"phoneID,omitempty"`
	Callback string `json:"callback,omitempty"`
}
type session struct {
	jobID   string
	expires time.Time
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	input   io.WriteCloser
	output  *bufio.Reader
	pipe    *os.File
	mu      sync.Mutex
	stopMu  sync.Mutex
	last    Reply
	revoked atomic.Bool
}
type Service struct {
	HelperPath string
	CookiePath string
	Jobs       JobControl
	mu         sync.Mutex
	sessions   map[string]*session
	now        func() time.Time
}

func New(helper, cookiePath string, jobs JobControl) (*Service, error) {
	if !filepath.IsAbs(cookiePath) || jobs == nil {
		return nil, errors.New("apple sign-in needs an absolute cookie path and job controller")
	}
	if helper != "" {
		if !filepath.IsAbs(helper) {
			return nil, errors.New("apple sign-in helper path must be absolute")
		}
		st, err := os.Lstat(helper)
		if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0111 == 0 {
			return nil, errors.New("apple sign-in helper is missing or not executable")
		}
	}
	return &Service{HelperPath: helper, CookiePath: cookiePath, Jobs: jobs, sessions: map[string]*session{}, now: time.Now}, nil
}

// Begin returns a secret link suffix. Reissuing a link revokes the prior one.
func (s *Service) Begin(ctx context.Context, jobID string) (string, error) {
	release, err := s.Jobs.AppleAuthJob(ctx, jobID)
	if err != nil {
		return "", err
	}
	download, err := url.Parse(release.URL)
	if err != nil || download.Scheme != "https" || download.Host != "download.developer.apple.com" ||
		!strings.HasPrefix(download.Path, "/Developer_Tools/") || !strings.HasSuffix(download.Path, ".xip") {
		return "", domain.Err("invalid_request", "Xcode release has no Apple Developer download URL")
	}
	appleURL := "https://developer.apple.com/services-account/download?path=" + url.QueryEscape(download.Path)
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	s.mu.Lock()
	var stopped []*session
	for key, old := range s.sessions {
		if old.jobID == jobID || s.now().After(old.expires) {
			delete(s.sessions, key)
			old.revoked.Store(true)
			stopped = append(stopped, old)
		}
	}
	s.sessions[token] = &session{jobID: jobID, expires: s.now().Add(15 * time.Minute), last: Reply{State: "browser", AppleURL: appleURL}}
	s.mu.Unlock()
	for _, old := range stopped {
		old.stop()
	}
	return token, nil
}

// ResumeBrowserCookies accepts only a one-time session from the local Apple
// browser. The browser shows Apple's own site; no password or 2FA enters Go.
func (s *Service) ResumeBrowserCookies(ctx context.Context, token, cookies string) error {
	s.mu.Lock()
	x := s.sessions[token]
	if x == nil || x.revoked.Load() || s.now().After(x.expires) {
		s.mu.Unlock()
		return domain.Err("not_found", "Apple sign-in link has expired; request a new link")
	}
	s.mu.Unlock()
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.revoked.Load() || s.now().After(x.expires) {
		return domain.Err("not_found", "Apple sign-in link has expired; request a new link")
	}
	if _, err := s.Jobs.AppleAuthJob(ctx, x.jobID); err != nil {
		return err
	}
	if err := writeCookies(s.CookiePath, cookies); err != nil {
		return err
	}
	if err := s.Jobs.ResumeAppleDownload(ctx, x.jobID); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
	x.revoked.Store(true)
	x.stop()
	return nil
}

func (s *Service) Session(token string) (Reply, error) {
	s.mu.Lock()
	x := s.sessions[token]
	if x == nil || x.revoked.Load() || s.now().After(x.expires) {
		s.mu.Unlock()
		return Reply{}, domain.Err("not_found", "Apple sign-in link has expired; request a new link")
	}
	s.mu.Unlock()
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.last, nil
}

func (x *session) stop() {
	x.stopMu.Lock()
	defer x.stopMu.Unlock()
	if x.cancel != nil {
		x.cancel()
	}
	if x.input != nil {
		_ = x.input.Close()
	}
	if x.pipe != nil {
		_ = x.pipe.Close()
	}
}

func (s *Service) start(x *session) error {
	ctx, cancel := context.WithDeadline(context.Background(), x.expires)
	cmd := exec.CommandContext(ctx, s.HelperPath)
	cmd.Stderr = io.Discard
	input, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return err
	}
	output, writer, err := os.Pipe()
	if err != nil {
		_ = input.Close()
		cancel()
		return err
	}
	cmd.Stdout = writer
	if err := cmd.Start(); err != nil {
		_ = input.Close()
		_ = output.Close()
		_ = writer.Close()
		cancel()
		return err
	}
	_ = writer.Close()
	x.stopMu.Lock()
	x.cmd, x.cancel, x.input, x.output = cmd, cancel, input, bufio.NewReaderSize(output, 65536)
	x.pipe = output
	if x.revoked.Load() {
		cancel()
		_ = input.Close()
		_ = output.Close()
	}
	x.stopMu.Unlock()
	go func() { _ = cmd.Wait() }()
	return nil
}

func (s *Service) Exchange(ctx context.Context, token string, req Request) (Reply, error) {
	s.mu.Lock()
	x := s.sessions[token]
	if x == nil || x.revoked.Load() || s.now().After(x.expires) {
		s.mu.Unlock()
		return Reply{}, domain.Err("not_found", "Apple sign-in link has expired; request a new link")
	}
	s.mu.Unlock()
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.revoked.Load() || s.now().After(x.expires) {
		return Reply{}, domain.Err("not_found", "Apple sign-in link has expired; request a new link")
	}
	if _, err := s.Jobs.AppleAuthJob(ctx, x.jobID); err != nil {
		return Reply{}, err
	}
	if x.cmd == nil {
		if err := s.start(x); err != nil {
			return Reply{}, domain.Err("apple_auth_unavailable", "Apple sign-in helper could not start")
		}
	}
	b, err := json.Marshal(req)
	if err != nil || len(b) > 8192 {
		return Reply{}, domain.Err("invalid_request", "Apple sign-in request is too large")
	}
	if _, err := x.input.Write(append(b, '\n')); err != nil {
		return Reply{}, domain.Err("apple_auth_unavailable", "Apple sign-in helper stopped; request a new link")
	}
	deadline := time.AfterFunc(60*time.Second, x.stop)
	defer deadline.Stop()
	line, err := x.output.ReadBytes('\n')
	if err != nil || len(line) > 65536 {
		return Reply{}, domain.Err("apple_auth_unavailable", "Apple sign-in helper did not respond; request a new link")
	}
	var reply Reply
	if json.Unmarshal(line, &reply) != nil {
		return Reply{}, domain.Err("apple_auth_unavailable", "Apple sign-in helper returned invalid state")
	}
	if x.revoked.Load() || s.now().After(x.expires) {
		return Reply{}, domain.Err("not_found", "Apple sign-in link has expired; request a new link")
	}
	if reply.State != "authenticated" {
		reply.Cookies = ""
		x.last = reply
		return reply, nil
	}
	if err := writeCookies(s.CookiePath, reply.Cookies); err != nil {
		return Reply{}, err
	}
	reply.Cookies = ""
	resumeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Jobs.ResumeAppleDownload(resumeCtx, x.jobID); err != nil {
		return Reply{}, err
	}
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
	x.stop()
	return reply, nil
}

func writeCookies(path, contents string) error {
	if len(contents) == 0 || len(contents) > 64<<10 || !strings.HasSuffix(contents, "\n") {
		return domain.Err("apple_auth_unavailable", "Apple returned no usable download cookies")
	}
	usable := 0
	for _, line := range strings.Split(strings.TrimSuffix(contents, "\n"), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 7 || strings.ContainsAny(line, "\r") {
			return domain.Err("apple_auth_unavailable", "Apple returned invalid download cookies")
		}
		host := strings.TrimPrefix(fields[0], ".")
		if host != "apple.com" && host != "developer.apple.com" && host != "download.developer.apple.com" {
			return domain.Err("apple_auth_unavailable", "Apple returned invalid download cookies")
		}
		expiry, expiryErr := strconv.ParseInt(fields[4], 10, 64)
		if (fields[1] != "TRUE" && fields[1] != "FALSE") || (fields[3] != "TRUE" && fields[3] != "FALSE") || expiryErr != nil ||
			(fields[2] != "/" && !strings.HasPrefix(fields[2], "/Developer_Tools/")) ||
			(expiry != 0 && expiry < time.Now().Unix()) ||
			(&http.Cookie{Name: fields[5], Value: fields[6]}).Valid() != nil {
			return domain.Err("apple_auth_unavailable", "Apple returned invalid download cookies")
		}
		if host == "download.developer.apple.com" || host == "developer.apple.com" || fields[1] == "TRUE" {
			usable++
		}
	}
	if usable == 0 {
		return domain.Err("apple_auth_unavailable", "Apple returned no usable download cookies")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".cookies-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err := tmp.Chmod(0600); err != nil {
		return err
	}
	if _, err := io.WriteString(tmp, contents); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("save Apple session: %w", err)
	}
	return nil
}
