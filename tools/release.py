"""Build a clean macOS/arm64 release with source, dependency notices and checksums."""

import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def run(*args, **kwargs):
    return subprocess.check_output(args, cwd=ROOT, **kwargs)


def json_stream(text):
    decoder = json.JSONDecoder()
    while text.strip():
        item, end = decoder.raw_decode(text.lstrip())
        yield item
        text = text.lstrip()[end:]


def build(version, destination):
    if not re.fullmatch(r'v\d+\.\d+\.\d+(?:-[a-zA-Z0-9.-]+)?', version):
        raise ValueError('Use a release version such as v2.0.0')
    if run('git', 'status', '--porcelain').strip():
        raise ValueError('Commit all changes before packaging')
    revision = run('git', 'rev-parse', 'HEAD', text=True).strip()
    tagged = subprocess.run(['git', 'rev-parse', '--verify', f'refs/tags/{version}^{{commit}}'], cwd=ROOT, capture_output=True, text=True)
    if tagged.returncode == 0 and tagged.stdout.strip() != revision:
        raise ValueError('Existing release tag identifies a different commit')
    destination = destination.resolve()
    destination.mkdir(parents=True, exist_ok=True)
    name = f'virfield-{version}-darwin-arm64'
    archive_path = destination / f'{name}.tar.gz'
    checksum_path = destination / 'SHA256SUMS'
    if archive_path.exists() or checksum_path.exists():
        raise ValueError('Release outputs already exist; choose a new output directory')
    with tempfile.TemporaryDirectory(prefix='virfield-package-') as temporary:
        package = Path(temporary) / name
        package.mkdir()
        # Only committed source is packaged; ignored runtime state is never traversed.
        with tarfile.open(fileobj=io.BytesIO(run('git', 'archive', 'HEAD'))) as source:
            source.extractall(package, filter='data')
        environment = dict(os.environ, GOOS='darwin', GOARCH='arm64', CGO_ENABLED='0')
        subprocess.run(['go', 'build', '-trimpath', '-mod=readonly', '-o', str(package / 'bin') + '/', './cmd/...'],
                       cwd=ROOT, env=environment, check=True)
        notices = package / 'THIRD_PARTY_NOTICES'
        notices.mkdir()
        modules = {}
        packages = run('go', 'list', '-deps', '-json', './cmd/...', env=environment, text=True)
        for dependency in json_stream(packages):
            module = dependency.get('Module', {})
            if module and not module.get('Main'):
                modules[module['Path']] = module
        for path, module in sorted(modules.items()):
            directory = Path(module['Dir'])
            target = notices / (path + '@' + module['Version'])
            found = []
            for file in directory.rglob('*'):
                if file.is_file() and file.name.lower().startswith(('license', 'copying', 'notice', 'copyright')):
                    dest = target / file.relative_to(directory)
                    dest.parent.mkdir(parents=True, exist_ok=True)
                    shutil.copyfile(file, dest)
                    found.append(file)
            if not found:
                raise ValueError(f'Missing third-party license notice: {path}')
        goroot = Path(run('go', 'env', 'GOROOT', text=True).strip())
        shutil.copyfile(goroot / 'LICENSE', notices / 'Go-LICENSE')
        manifest = {'version': version, 'commit': revision, 'target': 'darwin/arm64',
                    'go': run('go', 'version', text=True).strip(),
                    'binaries': {file.name: hashlib.sha256(file.read_bytes()).hexdigest()
                                 for file in sorted((package / 'bin').iterdir())},
                    'modules': {p: m['Version'] for p, m in sorted(modules.items())}}
        (package / 'release.json').write_text(json.dumps(manifest, indent=2) + '\n')
        with tarfile.open(archive_path, 'w:gz') as archive:
            archive.add(package, arcname=name)
    checksum_path.write_text(f'{hashlib.sha256(archive_path.read_bytes()).hexdigest()}  {archive_path.name}\n')
    print(f'Created {archive_path} from {revision}; checksums: {checksum_path}')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('version')
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    try:
        build(args.version, args.output)
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        parser.exit(1, f'Release packaging failed: {error}\n')
