/**
 * 4-phase virfield pipeline — shared by HTTP routes and MCP server.
 *
 * Scripts live in VMCONSOLE_SCRIPTS_DIR (~/Developer/virfield/scripts by default).
 * Each script writes progress to state.json in a timestamped log dir under
 * VMCONSOLE_LOG_BASE (~/Developer/virfield/logs by default), and creates a
 * "<vm>-latest" symlink for easy access.
 *
 * Phase args are the CLI flags passed to each script beyond the common set.
 * The caller supplies the actual VM names at runtime.
 */

export const STAGE_LABELS: Record<string, string> = {
  download_ipsw:   'Download macOS IPSW',
  create_vm:       'Create VM',
  setup_assistant: 'Setup Assistant',
  disable_sip:     'Disable SIP',
  provision_vm:    'Install Xcode & tools',
};

/** Script filename relative to SCRIPTS_DIR (download_ipsw is handled by build-golden-vm.sh itself) */
export const STAGE_SCRIPT_MAP: Record<string, string> = {
  create_vm:       '01-create-vm.sh',
  setup_assistant: '02-setup-assistant.sh',
  disable_sip:     '03-disable-sip.sh',
  provision_vm:    '04-provision-vm.sh',
};

/** Key used in state.json "stages" object → DB stage key */
export const STATE_KEY_TO_DB_STAGE: Record<string, string> = {
  '00-download-ipsw':   'download_ipsw',
  '01-create-vm':       'create_vm',
  '02-setup-assistant': 'setup_assistant',
  '03-disable-sip':     'disable_sip',
  '04-provision-vm':    'provision_vm',
};

/** DB stage key → state.json key (for building script args) */
export const STAGE_STATE_KEY: Record<string, string> = {
  download_ipsw:   '00-download-ipsw',
  create_vm:       '01-create-vm',
  setup_assistant: '02-setup-assistant',
  disable_sip:     '03-disable-sip',
  provision_vm:    '04-provision-vm',
};

/** Ordered stage keys */
export const STAGE_ORDER = ['download_ipsw', 'create_vm', 'setup_assistant', 'disable_sip', 'provision_vm'] as const;
export type StageKey = (typeof STAGE_ORDER)[number];

/**
 * Tool IDs accepted by vm-setup.sh via the TOOLS env var (passed as --tools to 04-provision-vm.sh).
 * These match the `want <id>` checks in vm-setup.sh.
 */
export const PROVISION_TOOLS: Array<{ id: string; label: string }> = [
  { id: 'system',          label: 'System config' },
  { id: 'autologin',       label: 'Auto-login' },
  { id: 'ssh_key',         label: 'SSH key' },
  { id: 'homebrew',        label: 'Homebrew' },
  { id: 'socat',           label: 'socat relay' },
  { id: 'peekaboo',        label: 'Peekaboo' },
  { id: 'peekaboo_agent',  label: 'Peekaboo agent' },
  { id: 'screenresolution',label: 'screenresolution' },
  { id: 'xcbeautify',      label: 'xcbeautify' },
  { id: 'jq',              label: 'jq' },
  { id: 'logging',         label: 'Logging plist' },
  { id: 'tcc',             label: 'TCC permissions' },
  { id: 'automation',      label: 'Automation mode' },
];

/**
 * Build the CLI args for a phase script.
 * Single-VM pipeline: all stages operate on goldenVm.
 */
export function buildStageArgs(
  stage: StageKey,
  vmNames: { goldenVm: string; baseVm?: string; nosipVm?: string },
  opts: { ipsw?: string; xcode?: string; tools?: string; record?: boolean; logDir?: string; vmshare?: string },
): string[] {
  const { goldenVm } = vmNames;
  const common: string[] = [];
  if (opts.logDir)   common.push('--log-dir', opts.logDir);
  if (opts.record)   common.push('--record');
  if (opts.vmshare)  common.push('--vmshare', opts.vmshare);

  switch (stage) {
    case 'create_vm':
      return ['--vm', goldenVm, '--ipsw', opts.ipsw ?? 'latest', ...common];
    case 'setup_assistant':
      return ['--vm', goldenVm, ...common];
    case 'disable_sip':
      return ['--vm', goldenVm, ...common];
    case 'provision_vm': {
      const args = ['--vm', goldenVm, ...common];
      if (opts.xcode) args.push('--xcode', opts.xcode);
      if (opts.tools) args.push('--tools', opts.tools);
      return args;
    }
    default:
      return common;
  }
}
