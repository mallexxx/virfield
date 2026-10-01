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
	key := flag.String("key", "", "stable idempotency key; required for acquire, release, resolve and image mutations")
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, `Usage: virfield [flags] COMMAND [arguments]

Inspect: status | lease ID | job ID | events [AFTER]
Leases:  acquire TEMPLATE TTL_SECONDS PUBLIC_KEY_FILE | renew ID RFC3339 | release ID
SSH:     keygen DIRECTORY | ssh-config LEASE_ID IDENTITY_DIRECTORY | tunnel LEASE_ID | tunnel-close LEASE_ID
Images:  image-catalog | image-create ID MACOS [XCODE]
         image-build TEMPLATE_ID | image-provision TEMPLATE_ID EXACT_VM_NAME | image-delete TEMPLATE_ID EXACT_VM_NAME
Recover: resolve LEASE_ID VM_NAME CONFIRM-NO-OPERATION-IN-FLIGHT
         image-recover IMAGE_RECORD_ID EXACT_VM_NAME retry|reprovision|setup-online|delete CONFIRM-NO-OPERATION-IN-FLIGHT
Host:    backup | init DIRECTORY TEMPLATE VM LOCATION

release, image-delete and recovery cleanup permanently delete their VM.
Recovery requires prior inspection that no operation remains in flight.
`)
		flag.PrintDefaults()
	}
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		return errors.New("command required")
	}
	if args[0] == "keygen" {
		if len(args) != 2 {
			return errors.New("usage: keygen NEW_DIRECTORY")
		}
		if err := client.GenerateIdentity(args[1]); err != nil {
			return err
		}
		fmt.Println(filepath.Join(args[1], "id_ed25519.pub"))
		return nil
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
	case "image-catalog":
		if len(args) != 1 {
			return errors.New("usage: image-catalog")
		}
		path = "images/catalog"
	case "image-create":
		if (len(args) != 3 && len(args) != 4) || *key == "" {
			return errors.New("usage: -key STABLE_KEY image-create NEW_ID MACOS_VERSION_OR_CODENAME [XCODE_VERSION]")
		}
		req := domain.ImageCreateRequest{ID: args[1], MacOS: args[2]}
		if len(args) == 4 {
			req.Xcode = args[3]
		}
		if err := req.Validate(); err != nil {
			return err
		}
		method = "POST"
		path = "images"
		body = req
	case "image-recover":
		if len(args) != 5 || !domain.ValidName(args[1]) || !domain.ValidName(args[2]) || args[4] != "CONFIRM-NO-OPERATION-IN-FLIGHT" || *key == "" {
			return errors.New("usage: -key STABLE_KEY image-recover IMAGE_RECORD_ID EXACT_VM_NAME retry|reprovision|setup-online|delete CONFIRM-NO-OPERATION-IN-FLIGHT")
		}
		method = "POST"
		path = "images/" + args[1] + "/recover"
		body = map[string]any{"vm_name": args[2], "action": args[3], "confirm_no_operation_in_flight": true}
	case "image-build":
		if len(args) != 2 || !domain.ValidName(args[1]) || *key == "" {
			return errors.New("usage: -key STABLE_KEY image-build IMAGE_ID")
		}
		method = "POST"
		path = "images/" + args[1] + "/build"
		body = struct{}{}
	case "image-provision":
		if len(args) != 3 || !domain.ValidName(args[1]) || *key == "" {
			return errors.New("usage: -key STABLE_KEY image-provision TEMPLATE_ID EXACT_VM_NAME")
		}
		method = "POST"
		path = "images/" + args[1] + "/provision"
		body = map[string]string{"confirm_name": args[2]}
	case "image-delete":
		if len(args) != 3 || !domain.ValidName(args[1]) || *key == "" {
			return errors.New("usage: -key STABLE_KEY image-delete IMAGE_ID EXACT_VM_NAME (permanently deletes the image)")
		}
		method = "POST"
		path = "images/" + args[1] + "/delete"
		body = map[string]string{"confirm_name": args[2]}
	case "backup":
		if len(args) != 1 {
			return errors.New("usage: backup")
		}
		method = "POST"
		path = "maintenance/backup"
	case "status":
		if len(args) != 1 {
			return errors.New("usage: status")
		}
		path = "status"
	case "acquire":
		if len(args) != 4 || *key == "" {
			return errors.New("usage: -key STABLE_KEY acquire TEMPLATE TTL_SECONDS PUBLIC_KEY_FILE")
		}
		ttl, err := strconv.Atoi(args[2])
		if err != nil {
			return err
		}
		method = "POST"
		path = "leases"
		pub, err := os.ReadFile(args[3])
		if err != nil {
			return err
		}
		canonical, err := domain.CanonicalPublicKey(string(pub))
		if err != nil {
			return err
		}
		body = domain.AcquireRequest{Template: args[1], TTLSeconds: ttl, SSHPublicKey: canonical}
	case "ssh-config":
		if len(args) != 3 || !domain.ValidName(args[1]) {
			return errors.New("usage: ssh-config LEASE_ID IDENTITY_DIRECTORY")
		}
		b, err := c.Do(context.Background(), "GET", "leases/"+args[1], nil, "")
		if err != nil {
			return err
		}
		var l domain.Lease
		if err := json.Unmarshal(b, &l); err != nil {
			return err
		}
		if err := client.WriteSSHConfig(args[2], l); err != nil {
			return err
		}
		fmt.Println(filepath.Join(args[2], "config"))
		return nil
	case "tunnel", "tunnel-close":
		if len(args) != 2 || !domain.ValidName(args[1]) {
			return errors.New("usage: tunnel|tunnel-close LEASE_ID")
		}
		method = "POST"
		if args[0] == "tunnel-close" {
			method = "DELETE"
		}
		path = "leases/" + args[1] + "/tunnel"
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
	fmt.Println("Configure a pinned image profile and image tools before acquiring leases; see docs/IMAGE-PIPELINE.md")
	fmt.Println("Start: virfieldd -config", path)
	return nil
}
