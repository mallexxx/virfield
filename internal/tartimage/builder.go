package tartimage

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mallexxx/virfield/internal/domain"
	"github.com/mallexxx/virfield/internal/guestssh"
	"github.com/mallexxx/virfield/internal/legacyios"
	"github.com/mallexxx/virfield/internal/registry"
	"github.com/mallexxx/virfield/internal/tart"
	"github.com/mallexxx/virfield/internal/xcodearchive"
)

// Runtime is the subset of the Tart runtime client the builder drives.
type Runtime interface {
	Observe(context.Context) (domain.Observation, error)
	Start(context.Context, domain.Lease) error
	Stop(context.Context, domain.Lease) error
}

type runner interface {
	Run(ctx context.Context, dir string, env []string, log, name string, args ...string) error
}

type Config struct {
	StateDir string
	Packer   string
	// TartBinary is put first on Packer's PATH so both use one Tart.
	TartBinary    string
	XcodeArchives string
	// Homes maps a Tart container ID to its TART_HOME.
	Homes       map[string]string
	Credentials []tart.RegistryCredential
	Runtime     Runtime
	Archives    *xcodearchive.Store
	Registry    ImageRegistry
}

type Builder struct {
	config        Config
	run           runner
	guests        guests
	deviceSupport func(ctx context.Context, dir, ip, version, build string, progress func(string) error) (string, error)
	stateTimeout  time.Duration
	statePoll     time.Duration
	startRetry    time.Duration
	// pluginMu serializes packer init: concurrent installs into the shared
	// plugin directory can leave a starting build without its plugin.
	pluginMu sync.Mutex
}

func New(config Config) *Builder {
	manager := &guestssh.Manager{Dir: config.StateDir}
	return &Builder{config: config, run: execRunner{}, guests: sshGuests{manager}, deviceSupport: qualifyDeviceSupport, stateTimeout: 3 * time.Minute, statePoll: 2 * time.Second, startRetry: 3 * time.Second}
}

func (b *Builder) Step(ctx context.Context, l domain.Lease, p domain.ImageProfile, step string, progress func(string) error) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if p.Toolchain != "" && p.ToolchainSHA256 != legacyios.Digest() {
		return domain.Err("unsupported_toolchain", "Pinned toolchain digest differs from this builder; no image operation was performed")
	}
	if p.Tart == nil {
		return domain.Err("invalid_profile", "Tart builder requires a pinned Tart source")
	}
	home, ok := b.config.Homes[l.Location]
	if !ok {
		return domain.Err("invalid_profile", "Image location is not a configured Tart container")
	}
	if l.GuestUser != "admin" {
		return domain.Err("invalid_profile", "Tart images use the Cirrus admin account")
	}
	switch step {
	case "resolve":
		return b.resolve(ctx, l, p, progress)
	case "build":
		return b.build(ctx, l, p, home)
	case "qualify":
		return b.qualify(ctx, l, p, progress)
	case "stop":
		return b.stop(ctx, l)
	}
	return domain.Err("invalid_profile", "unsupported Tart image stage")
}

func (b *Builder) folder(l domain.Lease) string {
	return filepath.Join(b.config.StateDir, "images", l.ID)
}

func (b *Builder) archive(p domain.ImageProfile) string {
	return filepath.Join(b.config.XcodeArchives, p.Xcode.SHA1+".xip")
}

func (b *Builder) resolve(ctx context.Context, l domain.Lease, p domain.ImageProfile, progress func(string) error) error {
	if p.Xcode != nil {
		if b.config.Archives != nil {
			if _, err := b.config.Archives.Archive(ctx, *p.Xcode, progress); err != nil {
				return err
			}
		}
		if err := verifyArchive(ctx, b.archive(p), p.Xcode.SHA1); err != nil {
			return err
		}
	}
	folder := b.folder(l)
	if err := os.MkdirAll(folder, 0700); err != nil {
		return err
	}
	if err := writeRecipe(filepath.Join(folder, "recipe")); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(folder, "recipe.sha256"), []byte(RecipeDigest()), 0600); err != nil {
		return err
	}
	b.pluginMu.Lock()
	err := b.packer(ctx, l, nil, "packer-init.log", "init", "developer.pkr.hcl")
	b.pluginMu.Unlock()
	if err != nil {
		return domain.Err("image_command_failed", "Packer plugin initialization failed; inspect the private packer-init log")
	}
	// Keep remote transfer in the replayable resolve stage. Packer receives a
	// digest already present in this container and only performs the VM build.
	credentials, err := b.registryEnvFor(p)
	if err != nil {
		return err
	}
	if err := progress("Pulling the pinned Tart source into the configured cache"); err != nil {
		return err
	}
	env := append([]string{"TART_HOME=" + b.config.Homes[l.Location], "TART_NO_AUTO_PRUNE=1"}, credentials...)
	if err := b.run.Run(ctx, folder, env, filepath.Join(folder, "tart-pull.log"), b.config.TartBinary, "pull", p.Tart.Upstream); err != nil {
		return domain.Err("image_download_failed", "Pinned Tart source download failed; inspect private tart-pull.log and retry this resolve job")
	}
	return nil
}

func (b *Builder) build(ctx context.Context, l domain.Lease, p domain.ImageProfile, home string) error {
	if p.Provision == "uitest-27-v1" {
		return b.buildUITest(ctx, l, p, home)
	}
	if p.Xcode == nil {
		return b.buildBase(ctx, l, p, home)
	}
	sip := "enabled"
	if p.DisableSIP {
		sip = "disabled"
	}
	vars := []string{
		"base=" + p.Tart.Upstream,
		"vm_name=" + l.VMName,
		"macos_version=" + p.MacOS,
		"macos_build=" + p.Build,
		"sip=" + sip,
		"security=" + p.Security,
		"xip=" + b.archive(p),
		"xip_sha1=" + p.Xcode.SHA1,
		"xcode_version=" + p.Xcode.Version,
		"xcode_build=" + p.Xcode.Build,
	}
	args := []string{"build"}
	for _, v := range vars {
		args = append(args, "-var", v)
	}
	template := "developer.pkr.hcl"
	if strings.Split(p.MacOS, ".")[0] == "27" {
		template = "developer27.pkr.hcl"
	}
	args = append(args, template)
	credentials, err := b.registryEnvFor(p)
	if err != nil {
		return err
	}
	env := append([]string{"TART_HOME=" + home}, credentials...)
	if err := b.packer(ctx, l, env, "packer-build.log", args...); err != nil {
		return domain.Err("image_command_failed", "Packer build failed; inspect the private packer-build log")
	}
	return nil
}

func (b *Builder) buildBase(ctx context.Context, l domain.Lease, p domain.ImageProfile, home string) error {
	sip := "enabled"
	if p.DisableSIP {
		sip = "disabled"
	}
	args := []string{"build"}
	for _, v := range []string{"base=" + p.Tart.Upstream, "vm_name=" + l.VMName, "macos_version=" + p.MacOS, "macos_build=" + p.Build, "sip=" + sip, "security=" + p.Security} {
		args = append(args, "-var", v)
	}
	template := "base.pkr.hcl"
	if strings.Split(p.MacOS, ".")[0] == "27" {
		template = "base27.pkr.hcl"
	}
	args = append(args, template)
	credentials, err := b.registryEnvFor(p)
	if err != nil {
		return err
	}
	if err := b.packer(ctx, l, append([]string{"TART_HOME=" + home}, credentials...), "packer-build.log", args...); err != nil {
		return domain.Err("image_command_failed", "Packer base build failed; inspect the private build log")
	}
	return nil
}

func (b *Builder) CachedXcode(x domain.XcodeRelease) bool {
	return b.config.Archives != nil && b.config.Archives.CachedXcode(x)
}

// buildUITest adopts the upstream Xcode image and runs the shared
// uitest-27-v1 recipe; no Xcode archive is uploaded.
func (b *Builder) buildUITest(ctx context.Context, l domain.Lease, p domain.ImageProfile, home string) error {
	vars := []string{"base=" + p.Tart.Upstream, "vm_name=" + l.VMName, "macos_version=" + p.MacOS, "macos_build=" + p.Build}
	args := []string{"build"}
	for _, v := range vars {
		args = append(args, "-var", v)
	}
	args = append(args, "uitest.pkr.hcl")
	credentials, err := b.registryEnvFor(p)
	if err != nil {
		return err
	}
	env := append([]string{"TART_HOME=" + home}, credentials...)
	if err := b.packer(ctx, l, env, "packer-build.log", args...); err != nil {
		return domain.Err("image_command_failed", "Packer build failed; inspect the private packer-build log")
	}
	return nil
}

// registryEnv scopes a configured credential to its registry, as the Tart
// runtime does; the host Docker credential is never relied on.
func (b *Builder) registryEnv(ref string) []string {
	for _, c := range b.config.Credentials {
		if strings.HasPrefix(ref, c.Host+"/") {
			return []string{"TART_REGISTRY_HOSTNAME=" + c.Host, "TART_REGISTRY_USERNAME=" + c.Username, "TART_REGISTRY_PASSWORD=" + c.Token}
		}
	}
	return nil
}

func (b *Builder) packer(ctx context.Context, l domain.Lease, env []string, log string, args ...string) error {
	base := []string{"CHECKPOINT_DISABLE=1", "TART_NO_AUTO_PRUNE=1", "PACKER_PLUGIN_PATH=" + filepath.Join(b.config.StateDir, "packer-plugins")}
	if b.config.TartBinary != "" {
		base = append(base, "PATH="+filepath.Dir(b.config.TartBinary)+":"+os.Getenv("PATH"))
	}
	dir := filepath.Join(b.folder(l), "recipe")
	return b.run.Run(ctx, dir, append(base, env...), filepath.Join(b.folder(l), log), b.config.Packer, args...)
}

func (b *Builder) stop(ctx context.Context, l domain.Lease) error {
	vm, ok, err := b.vm(ctx, l)
	if err != nil {
		return err
	}
	if !ok {
		return domain.Err("vm_missing", "Image VM is missing from its Tart container")
	}
	if vm.State != "stopped" {
		if err := b.config.Runtime.Stop(ctx, l); err != nil {
			return err
		}
	}
	return b.waitState(ctx, l, "stopped")
}

func (b *Builder) vm(ctx context.Context, l domain.Lease) (domain.VM, bool, error) {
	o, err := b.config.Runtime.Observe(ctx)
	if err != nil {
		return domain.VM{}, false, err
	}
	for _, vm := range o.VMs {
		if vm.Name == l.VMName && vm.Location == l.Location {
			return vm, true, nil
		}
	}
	return domain.VM{}, false, nil
}

// waitState polls the runtime; "running" additionally requires an IP.
// Inventory errors are retried: tart list fails while another VM is cloned.
func (b *Builder) waitState(ctx context.Context, l domain.Lease, state string) error {
	wait, cancel := context.WithTimeout(ctx, b.stateTimeout)
	defer cancel()
	for {
		vm, ok, err := b.vm(wait, l)
		if err == nil && ok && vm.State == state && (state != "running" || vm.IP != "") {
			return nil
		}
		select {
		case <-wait.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if state == "running" {
				return domain.Err("guest_timeout", "Image VM did not start with an IP address")
			}
			return domain.Err("stop_timeout", "Image VM did not stop")
		case <-time.After(b.statePoll):
		}
	}
}

func verifyArchive(ctx context.Context, path, digest string) error {
	if _, err := os.Lstat(path); err != nil {
		return domain.Err("unsafe_cache", "Pinned Xcode archive is not in the configured cache")
	}
	return xcodearchive.Verify(ctx, path, domain.XcodeRelease{SHA1: digest})
}

type execRunner struct{}

// Run interrupts Packer on cancellation so it can stop and clean up its VM.
func (execRunner) Run(ctx context.Context, dir string, env []string, log, name string, args ...string) error {
	out, err := os.OpenFile(log, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer out.Close()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = out, out
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 2 * time.Minute
	return cmd.Run()
}

func (b *Builder) ValidateTemplate(t domain.Template) error {
	if t.Image == nil || t.Image.Tart == nil {
		return domain.Err("invalid_profile", "Tart builder requires a pinned Tart profile; IPSW and retired Lume recipes are unsupported")
	}
	if t.GuestUser != "admin" {
		return domain.Err("invalid_profile", "Tart images require the admin account")
	}
	if _, ok := b.config.Homes[t.Location]; !ok {
		return domain.Err("invalid_profile", "Image location is not a Tart container")
	}
	return t.Image.Validate()
}

func (b *Builder) registryEnvFor(p domain.ImageProfile) ([]string, error) {
	if p.Registry == nil {
		return b.registryEnv(p.Tart.Upstream), nil
	}
	if b.config.Registry == nil {
		return nil, domain.Err("registry_unconfigured", "Imported Tart images require the configured registry source")
	}
	source, err := b.config.Registry.Source(p.Registry.Source)
	if err != nil {
		return nil, err
	}
	if source.Organization != p.Registry.Organization {
		return nil, domain.Err("invalid_registry", "Registry provenance is outside the approved source")
	}
	token, err := registry.Credentials(source)
	if err != nil {
		return nil, err
	}
	if token == "" {
		return nil, nil
	}
	return []string{"TART_REGISTRY_HOSTNAME=ghcr.io", "TART_REGISTRY_USERNAME=" + source.Username, "TART_REGISTRY_PASSWORD=" + token}, nil
}
