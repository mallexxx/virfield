// virfield is the thin v2 API command-line client.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/mallexxx/virfield/internal/client"
	"github.com/mallexxx/virfield/internal/config"
	"github.com/mallexxx/virfield/internal/domain"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	base := flag.String("url", "http://127.0.0.1:7780", "daemon URL")
	tokenFile := flag.String("token-file", "", "owner-only API token file")
	key := flag.String("key", "", "stable idempotency key; required for acquire/release")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: virfield [flags] status | acquire TEMPLATE TTL_SECONDS | lease ID | job ID | events [AFTER] | release ID | renew ID RFC3339 | resolve ID VM CONFIRM-NO-OPERATION-IN-FLIGHT | init DIRECTORY TEMPLATE VM LOCATION")
		flag.PrintDefaults()
	}
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		return errors.New("command required")
	}
	if args[0] == "init" {
		if len(args) != 5 {
			return errors.New("usage: virfield init DIRECTORY TEMPLATE VM LOCATION")
		}
		return initialize(args[1], args[2], args[3], args[4])
	}
	token, err := config.Token(*tokenFile)
	if err != nil {
		return err
	}
	c, err := client.New(*base, token)
	if err != nil {
		return err
	}
	method, path := "GET", ""
	var body any
	switch args[0] {
	case "status":
		if len(args) != 1 {
			return errors.New("usage: status")
		}
		path = "status"
	case "acquire":
		if len(args) != 3 || *key == "" {
			return errors.New("usage: -key STABLE_KEY acquire TEMPLATE TTL_SECONDS")
		}
		ttl, err := strconv.Atoi(args[2])
		if err != nil {
			return err
		}
		method = "POST"
		path = "leases"
		body = domain.AcquireRequest{Template: args[1], TTLSeconds: ttl}
	case "lease", "job":
		if len(args) != 2 || !domain.ValidName(args[1]) {
			return errors.New("valid ID required")
		}
		if args[0] == "lease" {
			path = "leases/" + args[1]
		} else {
			path = "jobs/" + args[1]
		}
	case "events":
		if len(args) > 2 {
			return errors.New("usage: events [AFTER]")
		}
		path = "events"
		if len(args) == 2 {
			n, err := strconv.ParseInt(args[1], 10, 64)
			if err != nil || n < 0 {
				return errors.New("AFTER must be a nonnegative integer")
			}
			path += "?after=" + args[1]
		}
	case "release":
		if len(args) != 2 || !domain.ValidName(args[1]) || *key == "" {
			return errors.New("usage: -key STABLE_KEY release LEASE_ID (permanently deletes the leased VM)")
		}
		method = "POST"
		path = "leases/" + args[1] + "/release"
	case "resolve":
		if len(args) != 4 || !domain.ValidName(args[1]) || !domain.ValidName(args[2]) || args[3] != "CONFIRM-NO-OPERATION-IN-FLIGHT" || *key == "" {
			return errors.New("usage: -key STABLE_KEY resolve LEASE_ID VM_NAME CONFIRM-NO-OPERATION-IN-FLIGHT; inspect Lume first, cleanup permanently deletes the VM")
		}
		method = "POST"
		path = "leases/" + args[1] + "/resolve"
		body = map[string]any{"vm_name": args[2], "confirm_no_operation_in_flight": true}
	case "renew":
		if len(args) != 3 || !domain.ValidName(args[1]) {
			return errors.New("usage: renew LEASE_ID RFC3339")
		}
		method = "PUT"
		path = "leases/" + args[1] + "/expiry"
		body = map[string]string{"expires_at": args[2]}
	default:
		return errors.New("unknown command; run virfield -help")
	}
	b, err := c.Do(context.Background(), method, path, body, *key)
	if err != nil {
		return err
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
func initialize(dir, id, name, location string) error {
	if !domain.ValidName(id) || !domain.ValidName(name) || !domain.ValidName(location) {
		return errors.New("invalid template identity")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	// Refuse existing directories so init can never rotate a live token or database.
	if err := os.Mkdir(dir, 0700); err != nil {
		return err
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return err
	}
	tokenPath := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenPath, []byte(hex.EncodeToString(raw[:])+"\n"), 0600); err != nil {
		return err
	}
	cfg := config.Config{Listen: "127.0.0.1:7780", LumeURL: "http://127.0.0.1:7777", StateDir: dir, TokenFile: tokenPath, MaxVMs: 2, Templates: []domain.Template{{ID: id, Name: name, Location: location}}}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, append(b, '\n'), 0600); err != nil {
		return err
	}
	fmt.Println("Created", path)
	fmt.Println("Start: virfieldd -config", path)
	return nil
}
