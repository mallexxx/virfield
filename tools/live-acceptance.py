"""Accept an installed candidate with two disposable Tart leases per selected template.

Requires explicit approval of deletion of only the leases accepted by this run.
Never resolves ambiguous operations, deletes goldens, or starts a second daemon.
Retains the private journal and keys for inspection if cleanup fails.
"""
import argparse
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import socket
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


class RefuseRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, msg, headers, newurl):
        return None


class Acceptance:
    def __init__(self, config, binary, destination):
        self.config = json.loads(config.read_text())
        self.binary = binary.resolve(strict=True)
        self.destination = destination.resolve()
        self.destination.mkdir(mode=0o700, parents=True, exist_ok=False)
        token = Path(self.config['token_file'])
        if token.is_symlink() or token.stat().st_mode & 0o077:
            raise ValueError('Token must be an owner-only regular file')
        self.token = token.read_text().strip()
        self.base = 'http://' + self.config['listen'] + '/api/v1/'
        url = urllib.parse.urlsplit(self.base)
        if not ipaddress.ip_address(url.hostname).is_loopback or not url.port:
            raise ValueError('Run locally against the installed loopback daemon')
        self.http = urllib.request.build_opener(RefuseRedirect())
        self.accepted = []
        self.results = []
        self.run_id = uuid.uuid4().hex
        self.requests = {}
        self.save()

    def api(self, path, body=None, key=None):
        request = urllib.request.Request(self.base + path,
            data=None if body is None else json.dumps(body).encode(),
            headers={'Authorization': 'Bearer ' + self.token, 'Content-Type': 'application/json'})
        if key:
            request.add_header('Idempotency-Key', self.run_id + '-' + key)
            self.requests[key] = dict(path=path, body=body)
            self.save()  # Persist the replay key/body BEFORE dispatch.
        for attempt in range(3):
            try:
                with self.http.open(request, timeout=30) as response:
                    return json.load(response)
            except (TimeoutError, urllib.error.URLError):
                if body is not None or attempt == 2:
                    raise
                time.sleep(5)

    def save(self):
        path = self.destination / 'journal.json'
        # Contains only this run's IDs/results, never API tokens or desktop URLs.
        content = dict(run_id=self.run_id, requests=self.requests, accepted=self.accepted, results=self.results)
        with path.open('w') as file:
            os.fchmod(file.fileno(), 0o600)
            json.dump(content, file, indent=2)

    def command(self, *arguments, success=True):
        result = subprocess.run(arguments, capture_output=True, text=True, timeout=90)
        if success and result.returncode:
            raise RuntimeError('Acceptance command failed; inspect the retained private identity/journal')
        return result

    def wait(self, lease_id, state, timeout=900):
        deadline = time.monotonic() + timeout
        previous = None
        while time.monotonic() < deadline:
            lease = self.api('leases/' + lease_id)
            if lease['state'] == state:
                return lease
            if lease['state'] in ('quarantined', 'needs_attention'):
                raise RuntimeError('Lease needs inspection: ' + lease_id + ' ' + lease['state'])
            if lease['state'] != previous:
                print(lease_id + ': ' + lease['state'], flush=True)
                previous = lease['state']
            time.sleep(3)
        raise RuntimeError('Acceptance deadline: ' + lease_id)

    def pair(self, template, sequential=False):
        profiles = {t['id']: t for t in self.api('status')['templates']}
        selected = profiles[template]
        profile = selected['image']
        if not ('tart' in profile or 'node' in profile) or selected.get('provider') not in (None, '', 'tart', 'baremetal'):
            raise ValueError('This acceptance runner requires a configured Tart or node VM image')
        pair = []
        identities = []
        host_keys = []
        for i in range(2):
            identity = self.destination / (template + '-' + str(i))
            self.command(str(self.binary), 'keygen', str(identity))
            identities.append(identity)

        def acquire(i):
            identity = identities[i]
            request = dict(template=template, ttl_seconds=3600,
                           ssh_public_key=(identity / 'id_ed25519.pub').read_text(), destination_location=selected['location'])
            result = self.api('leases', request, template + '-acquire-' + str(i))
            lease = result['lease']
            if not lease['vm_name'].startswith('vf-') or lease.get('purpose') == 'image':
                raise RuntimeError('Unexpected disposable lease identity')
            self.accepted.append(dict(id=lease['id'], vm_name=lease['vm_name'], template=template))
            pair.append((lease, identity))
            self.save()
            replay = self.api('leases', request, template + '-acquire-' + str(i))
            if replay['lease']['id'] != lease['id']:
                raise RuntimeError('Acquire replay created a new lease')

        def release(lease):
            current = self.api('leases/' + lease['id'])
            if current['vm_name'] != lease['vm_name'] or current.get('purpose') == 'image':
                raise RuntimeError('Cleanup identity changed; retaining journal')
            if current['state'] != 'released':
                self.api('leases/' + lease['id'] + '/release', {}, lease['id'] + '-release')
                self.wait(lease['id'], 'released', timeout=600)
                print(lease['id'] + ': released', flush=True)
                self.save()

        try:
            if not sequential:
                for i in range(2):
                    acquire(i)
            for i in range(2):
                if sequential:
                    acquire(i)
                initial, identity = pair[i]
                lease = self.wait(initial['id'], 'ready')
                host_keys.append(lease['ssh']['host_key'])
                self.command(str(self.binary), '-url', self.base.removesuffix('/api/v1/'),
                             '-token-file', self.config['token_file'], 'ssh-config', lease['id'], str(identity))
                ssh_config = str(identity / 'config')
                out = self.command('ssh', '-F', ssh_config, 'virfield',
                    '/usr/bin/sw_vers -productVersion; /usr/bin/sw_vers -buildVersion; /usr/bin/csrutil status').stdout.splitlines()
                sip = 'disabled' if profile.get('disable_sip') else 'enabled'
                if out != [profile['macos'], profile['build'], 'System Integrity Protection status: ' + sip + '.']:
                    raise RuntimeError('Guest OS/build/SIP mismatch')
                # Cross-lease key must be rejected against the same host pin.
                other = identities[1 - i]
                bad_config = identity / 'wrong-key-config'
                text = (identity / 'config').read_text().replace(str(identity / 'id_ed25519'), str(other / 'id_ed25519'))
                with bad_config.open('w') as file:
                    os.fchmod(file.fileno(), 0o600)
                    file.write(text)
                bad = self.command('ssh', '-F', str(bad_config), 'virfield', 'true', success=False)
                if bad.returncode == 0 or 'Permission denied' not in bad.stderr:
                    raise RuntimeError('Cross-lease rejection was not verified')
                self.command('ssh', '-F', ssh_config, 'virfield',
                    "mkdir -p ~/workspace; printf 'virfield-acceptance\\n' > ~/workspace/acceptance.txt")
                artifact = identity / 'acceptance.txt'
                self.command('scp', '-F', ssh_config, 'virfield:workspace/acceptance.txt', str(artifact))
                if artifact.read_bytes() != b'virfield-acceptance\n':
                    raise RuntimeError('Artifact export mismatch')
                tunnel = self.api('leases/' + lease['id'] + '/tunnel', dict(service='desktop'))
                address = tunnel['address'].rsplit(':', 1)
                with socket.create_connection((address[0], int(address[1])), timeout=10) as connection:
                    banner = connection.recv(12)
                if not banner.startswith(b'RFB '):
                    raise RuntimeError('Desktop protocol is not reachable')
                self.results.append(dict(lease_id=lease['id'], template=template,
                    mode='sequential' if sequential else 'concurrent',
                    guest_checked=True, cross_key_rejected=True, desktop_banner=banner.decode().strip(),
                    artifact_sha256=hashlib.sha256(artifact.read_bytes()).hexdigest()))
                self.save()
                if sequential:
                    release(lease)
            if host_keys[0] == host_keys[1]:
                raise RuntimeError('Lease host keys are shared')
        finally:
            # Delete only IDs this script received from its own accepted requests.
            # Unknown/quarantined outcomes are retained for operator inspection.
            for lease, _ in pair:
                release(lease)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', type=Path, required=True)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--destination', type=Path, required=True)
    parser.add_argument('--approve-disposable-cleanup', action='store_true')
    parser.add_argument('--sequential', action='store_true', help='Accept two successive clones when disk reserve prevents concurrent clones')
    parser.add_argument('templates', nargs='+')
    args = parser.parse_args()
    if not args.approve_disposable_cleanup:
        parser.error('Explicit approval of this run\'s disposable VM deletion is required')
    run = Acceptance(args.config, args.binary, args.destination)
    status = run.api('status')
    if status.get('jobs') or status['capacity']['used'] or any(l.get('purpose') != 'image' for l in status.get('leases', [])):
        raise RuntimeError('Acceptance requires an idle installed manager')
    for template in args.templates:
        run.pair(template, sequential=args.sequential)
    print('Acceptance passed; private journal: ' + str(run.destination / 'journal.json'))


if __name__ == '__main__':
    main()
