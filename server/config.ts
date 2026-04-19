/**
 * Runtime configuration — all env vars in one place.
 *
 * VMCONSOLE_SCRIPTS_DIR  — path to the virfield scripts directory.
 *                          Default: ~/Developer/virfield/scripts
 *
 * VMCONSOLE_LOG_BASE     — root directory for script log dirs and state.json files.
 *                          Matches LOG_BASE in _lib.sh.
 *                          Default: ~/Developer/virfield/logs
 *
 * VMCONSOLE_REPO_DIR     — path to the git repo that contains build-for-testing.sh.
 *                          Default: ~/Developer/repo
 *
 * VMCONSOLE_VMSHARE      — host virtiofs share directory (mounted inside VMs).
 *                          Default: ~/VMShare
 *
 * PORT                   — HTTP server port. Default: 3000.
 */

import { homedir } from 'os';
import { join } from 'path';

export const SCRIPTS_DIR: string =
  process.env.VMCONSOLE_SCRIPTS_DIR
  ?? process.env.DDG_SCRIPTS_DIR  // legacy alias
  ?? join(homedir(), 'Developer', 'virfield', 'scripts');

export const LOG_BASE: string =
  process.env.VMCONSOLE_LOG_BASE
  ?? process.env.DDG_LOG_BASE
  ?? join(homedir(), 'Developer', 'virfield', 'logs');

/** ~/Developer/virfield/state/<vm-name>.json — persistent per-VM state file */
export const STATE_DIR: string =
  process.env.VMCONSOLE_STATE_DIR
  ?? process.env.DDG_STATE_DIR
  ?? join(homedir(), 'Developer', 'virfield', 'state');

/** VNC recording output directory */
export const RECORDINGS_DIR: string =
  process.env.VMCONSOLE_RECORDINGS_DIR
  ?? process.env.DDG_RECORDINGS_DIR
  ?? join(homedir(), 'Developer', 'virfield', 'recordings');

export const REPO_DIR: string =
  process.env.VMCONSOLE_REPO_DIR
  ?? process.env.DDG_REPO_DIR
  ?? join(homedir(), 'Developer', 'repo');

export const VMSHARE: string =
  process.env.VMCONSOLE_VMSHARE
  ?? process.env.DDG_VMSHARE
  ?? join(homedir(), 'VMShare');

export const PORT: number = Number(process.env.PORT ?? 3000);
