"""Generate private launchd/MCP files from an initialized deployment; never start services."""

import argparse
import grp
import ipaddress
import json
import os
from pathlib import Path
import plistlib
import pwd
import shutil
import stat
import tempfile
from urllib.parse import urlsplit


def executable(value):
    path = Path(value)
    if not path.is_absolute() or not path.is_file() or not os.access(path, os.X_OK):
        raise ValueError(f"Expected an absolute executable path: {path}")
    return str(path)


def private_file(path, uid):
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_uid != uid or info.st_mode & 0o077:
        raise ValueError(f"Expected an owner-only regular file: {path}")


def render(directory, account, group):
    """Validate first, returning artifacts without changing the deployment."""
    directory = directory.resolve(strict=True)
    info = directory.stat()
    if not directory.is_dir() or info.st_uid != account.pw_uid or info.st_mode & 0o077:
        raise ValueError("Deployment must be a private directory owned by the VM user")
    config_path = directory / "config.json"
    private_file(config_path, account.pw_uid)
    config = json.loads(config_path.read_text())
    if Path(config["state_dir"]).resolve() != directory:
        raise ValueError("config.state_dir must identify this deployment")
    token = Path(config["token_file"])
    if not token.is_absolute():
        raise ValueError("token_file must be absolute")
    private_file(token, account.pw_uid)
    listen = urlsplit("http://" + config["listen"])
    if not ipaddress.ip_address(listen.hostname).is_loopback or not listen.port:
        raise ValueError("listen must specify a loopback IP and a nonzero port")
    lume = urlsplit(config["lume_url"])
    if (lume.scheme != "http" or lume.hostname != "127.0.0.1" or not lume.port
            or lume.username or lume.password or lume.path or lume.query or lume.fragment):
        raise ValueError("Managed Lume requires http://127.0.0.1:PORT without credentials or path")
    tools = config["image_tools"]
    for name in ("lume", "python", "tesseract"):
        executable(tools[name])
    executable(str(Path(tools["vnc_bin"]) / "vncdotool"))
    for binary in ("virfield", "virfieldd", "virfield-mcp", "virfield-lume"):
        executable(str(directory / "bin" / binary))
    args = {
        "virfieldd": [str(directory / "bin/virfieldd"), "-config", str(config_path)],
        "lume": [str(directory / "bin/virfield-lume"), "-binary", tools["lume"],
                 "-log", str(directory / "lume.log"), "-port", str(lume.port)],
    }
    artifacts = {}
    for service, arguments in args.items():
        template = Path(__file__).parent / f"ai.virfield.{service}.plist.example"
        data = plistlib.loads(template.read_bytes())
        data.update(UserName=account.pw_name, GroupName=group,
                    WorkingDirectory=str(directory), ProgramArguments=arguments,
                    StandardErrorPath=str(directory / f"{service}.startup.log"))
        data["EnvironmentVariables"]["HOME"] = account.pw_dir
        artifacts[f"ai.virfield.{service}.plist"] = plistlib.dumps(data)
    mcp = {"mcpServers": {"virfield": {
        "command": str(directory / "bin/virfield-mcp"),
        "args": ["-url", "http://" + config["listen"], "-token-file", str(token)],
    }}}
    artifacts["mcp.json"] = (json.dumps(mcp, indent=2) + "\n").encode()
    return artifacts


def prepare(directory):
    if os.geteuid() == 0:
        raise ValueError("Run as the VM-owning user, without sudo")
    directory = directory.resolve(strict=True)
    account = pwd.getpwuid(os.getuid())
    artifacts = render(directory, account, grp.getgrgid(account.pw_gid).gr_name)
    destination = directory / "launchd"
    if destination.exists() or destination.is_symlink():
        raise ValueError("launchd already exists; inspect it before changing a deployment")
    staging = Path(tempfile.mkdtemp(prefix=".launchd-", dir=directory))
    try:
        for name, content in artifacts.items():
            with (staging / name).open("xb") as file:
                os.fchmod(file.fileno(), 0o600)
                file.write(content)
        staging.rename(destination)
    finally:
        if staging.exists():
            shutil.rmtree(staging)
    return destination


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("deployment", type=Path)
    args = parser.parse_args()
    try:
        result = prepare(args.deployment)
    except (OSError, ValueError, KeyError, TypeError) as error:
        parser.exit(1, f"Cannot prepare services: {error}\n")
    print(f"Prepared {result}; review the plists before system registration. No services started.")
