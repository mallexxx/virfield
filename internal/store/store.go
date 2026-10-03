// Package store owns the SQLite journal. All state transitions and their events
// commit atomically; callers must never infer success before Commit returns.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	_ "modernc.org/sqlite"
)

type Store struct {
	db   *sql.DB
	path string
}

func Open(path string) (*Store, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	path = absolute
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: path}
	db, err := sql.Open("sqlite", u.String()+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version > 7 {
		return fmt.Errorf("database schema %d is newer than this binary", version)
	}
	if version == 7 {
		return nil
	}
	if version > 0 {
		if err := s.snapshotBeforeMigration(version); err != nil {
			return fmt.Errorf("pre-migration backup failed: %w", err)
		}
	}
	if version == 5 || version == 6 {
		_, err := s.db.Exec(`BEGIN IMMEDIATE; CREATE INDEX IF NOT EXISTS jobs_lease ON jobs(lease_id); CREATE INDEX IF NOT EXISTS events_at ON events(at); PRAGMA user_version=7; COMMIT;`)
		return err
	}
	if version >= 1 && version <= 4 {
		// Version 5 persists catalog templates and Xcode manifests. Older
		// executors must not silently ignore the new provisioning requirements.
		_, err := s.db.Exec(`BEGIN IMMEDIATE; CREATE TABLE templates (id TEXT PRIMARY KEY, body TEXT NOT NULL); CREATE INDEX IF NOT EXISTS jobs_lease ON jobs(lease_id); CREATE INDEX IF NOT EXISTS events_at ON events(at); PRAGMA user_version=7; COMMIT;`)
		return err
	}
	_, err := s.db.Exec(`BEGIN IMMEDIATE;
 CREATE TABLE leases (id TEXT PRIMARY KEY, state TEXT NOT NULL, body TEXT NOT NULL);
 CREATE TABLE jobs (id TEXT PRIMARY KEY, lease_id TEXT NOT NULL REFERENCES leases(id), state TEXT NOT NULL, body TEXT NOT NULL);
 CREATE TABLE requests (key TEXT PRIMARY KEY, fingerprint TEXT NOT NULL, lease_id TEXT NOT NULL REFERENCES leases(id), job_id TEXT NOT NULL REFERENCES jobs(id));
 CREATE TABLE events (id INTEGER PRIMARY KEY AUTOINCREMENT, lease_id TEXT NOT NULL REFERENCES leases(id), job_id TEXT NOT NULL, type TEXT NOT NULL, message TEXT NOT NULL, at TEXT NOT NULL);
 CREATE INDEX events_lease ON events(lease_id,id);
 CREATE INDEX events_at ON events(at);
 CREATE INDEX jobs_state ON jobs(state);
 CREATE INDEX jobs_lease ON jobs(lease_id);
 CREATE TABLE templates (id TEXT PRIMARY KEY, body TEXT NOT NULL);
 PRAGMA user_version=7;
 COMMIT;`)
	return err
}
func (s *Store) Leases(ctx context.Context) ([]domain.Lease, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT body FROM leases WHERE state != 'released' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Lease{}
	for rows.Next() {
		var b string
		var l domain.Lease
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(b), &l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
func (s *Store) Lease(ctx context.Context, id string) (domain.Lease, error) {
	var b string
	var l domain.Lease
	err := s.db.QueryRowContext(ctx, `SELECT body FROM leases WHERE id=?`, id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return l, domain.Err("not_found", "lease does not exist")
	}
	if err != nil {
		return l, err
	}
	err = json.Unmarshal([]byte(b), &l)
	return l, err
}
func (s *Store) Jobs(ctx context.Context) ([]domain.Job, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT body FROM jobs WHERE state IN ('queued','running','needs_attention') ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Job{}
	for rows.Next() {
		var b string
		var j domain.Job
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(b), &j); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
func (s *Store) Job(ctx context.Context, id string) (domain.Job, error) {
	var b string
	var j domain.Job
	err := s.db.QueryRowContext(ctx, `SELECT body FROM jobs WHERE id=?`, id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return j, domain.Err("not_found", "job does not exist")
	}
	if err != nil {
		return j, err
	}
	err = json.Unmarshal([]byte(b), &j)
	return j, err
}
func (s *Store) CleanupJob(ctx context.Context, leaseID string) (*domain.Job, error) {
	var body string
	err := s.db.QueryRowContext(ctx, `SELECT body FROM jobs WHERE lease_id=? AND json_extract(body,'$.kind')='cleanup' ORDER BY rowid DESC LIMIT 1`, leaseID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var j domain.Job
	if err := json.Unmarshal([]byte(body), &j); err != nil {
		return nil, err
	}
	return &j, nil
}
func (s *Store) Replay(ctx context.Context, key, fp string) (*domain.Operation, error) {
	var actual, lid, jid string
	err := s.db.QueryRowContext(ctx, `SELECT fingerprint,lease_id,job_id FROM requests WHERE key=?`, key).Scan(&actual, &lid, &jid)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if actual != fp {
		return nil, domain.Err("idempotency_conflict", "idempotency key was already used with a different request")
	}
	l, err := s.Lease(ctx, lid)
	if err != nil {
		return nil, err
	}
	j, err := s.Job(ctx, jid)
	if err != nil {
		return nil, err
	}
	return &domain.Operation{Lease: l, Job: j, Replayed: true}, nil
}

// Save persists the complete transition. key is only set for a newly accepted request.
func (s *Store) Save(ctx context.Context, l domain.Lease, j *domain.Job, key, fp, typ, message string) error {
	return s.save(ctx, l, j, key, fp, typ, message, nil)
}

// SaveImage atomically registers the template with admission, request and job.
func (s *Store) SaveImage(ctx context.Context, l domain.Lease, j *domain.Job, key, fp, typ, message string, t domain.Template) error {
	return s.save(ctx, l, j, key, fp, typ, message, &t)
}
func (s *Store) save(ctx context.Context, l domain.Lease, j *domain.Job, key, fp, typ, message string, t *domain.Template) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if t != nil {
		b, err := json.Marshal(t)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO templates(id,body) VALUES(?,?)`, t.ID, string(b)); err != nil {
			return err
		}
	}
	lb, err := json.Marshal(l)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO leases(id,state,body) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET state=excluded.state,body=excluded.body`, l.ID, l.State, string(lb)); err != nil {
		return err
	}
	if j != nil && (j.Kind == "cleanup" || j.Kind == "image_delete") && j.Phase == "queued" {
		// Cancellation and cleanup acceptance are one transaction, including on
		// expiry. A crash cannot strand a canceled preparation without cleanup.
		if _, err = tx.ExecContext(ctx, `UPDATE jobs SET state='canceled',body=json_set(body,'$.state','canceled','$.updated_at',?) WHERE lease_id=? AND state IN ('queued','running','needs_attention')`, time.Now().UTC().Format(time.RFC3339Nano), l.ID); err != nil {
			return err
		}
	}
	jid := ""
	if j != nil {
		jid = j.ID
		jb, err := json.Marshal(j)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO jobs(id,lease_id,state,body) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET state=excluded.state,body=excluded.body`, j.ID, j.LeaseID, j.State, string(jb)); err != nil {
			return err
		}
	}
	if key != "" {
		if _, err = tx.ExecContext(ctx, `INSERT INTO requests(key,fingerprint,lease_id,job_id) VALUES(?,?,?,?)`, key, fp, l.ID, jid); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO events(lease_id,job_id,type,message,at) VALUES(?,?,?,?,?)`, l.ID, jid, typ, message, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Events(ctx context.Context, after int64, leaseID string, limit int) ([]domain.Event, error) {
	query := `SELECT id,lease_id,job_id,type,message,at FROM events WHERE id>? AND (?='' OR lease_id=?) ORDER BY id LIMIT ?`
	if after == -1 {
		query = `SELECT * FROM (SELECT id,lease_id,job_id,type,message,at FROM events WHERE id>? AND (?='' OR lease_id=?) ORDER BY id DESC LIMIT ?) ORDER BY id`
	}
	rows, err := s.db.QueryContext(ctx, query, after, leaseID, leaseID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Event{}
	for rows.Next() {
		var e domain.Event
		var at string
		if err := rows.Scan(&e.ID, &e.LeaseID, &e.JobID, &e.Type, &e.Message, &at); err != nil {
			return nil, err
		}
		e.At, err = time.Parse(time.RFC3339Nano, at)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) Templates(ctx context.Context) ([]domain.Template, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT body FROM templates ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Template{}
	for rows.Next() {
		var b string
		var t domain.Template
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(b), &t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
