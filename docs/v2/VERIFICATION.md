# Verification record — 2026-09-29

## Passed locally

- `make check`: gofmt check, `go vet`, full `go test -race ./...`.
- `make build`: `virfieldd`, `virfield`, `virfield-mcp`; Go 1.26.8, darwin/arm64.
- Staticcheck v0.7.0 (built with the selected toolchain).
- `govulncheck` v1.6.0: no vulnerabilities found after selecting Go 1.26.8 and
  updating `golang.org/x/sys` to v0.44.0.
- `node --check internal/web/app.js`: syntax check only, no Node runtime dependency.
- `plutil -lint deploy/ai.virfield.virfieldd.plist.example`.
- Installed Lume 0.5.3 read-only adapter check: four stopped macOS VMs, 0/2 slots.
- Built daemon + CLI smoke test on loopback port 17780, with a separate empty DB.
  During the smoke test Lume's port 7777 stopped accepting connections. The v2
  process remained responsive and reported `backend_unavailable`. The cause of
  Lume's disappearance was not established; v2 did not issue any Lume mutation.
  The temporary v2 daemon was terminated cleanly afterward.

The installed Go 1.26.2 had reachable standard-library advisories. The repository
now requires Go 1.26.8, downloaded as a project toolchain without changing the
Homebrew installation. `golang.org/x/sys` is pinned to v0.44.0 to remove the
additional module-only Windows advisory GO-2026-5024.

## Not run / not accepted

- `TestLiveLifecycle`: guarded and skipped, awaiting authorization for deletion
  of its two disposable test clones. Lume must first be available.
- No existing VM was started, stopped, deleted or provisioned during development.
- Browser visual/interactive QA: no browser surface was available. HTTP assets,
  security headers and JS syntax were checked; this is not browser acceptance.
- GitHub Actions workflow was added but has not run remotely.
- Image Manager, scoped SSH/tunnels, provisioning migration and full production
  fault/soak tests remain incomplete, as listed in README.md.

Do not interpret the passing mocked tests or read-only Lume check as live
lifecycle acceptance or a claim that the complete v2 scope is production-ready.
