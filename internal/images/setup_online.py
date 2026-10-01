"""State-aware native Setup Assistant for macOS with guest-bound APFS encryption.

Only recognized screens and visible OCR text anchors may drive input. Lifecycle,
SSH authentication, password rotation and final readiness remain in Go.
"""
import re


def compact(text):
    return re.sub(r'[^a-z0-9]', '', text.lower())


def classify(text):
    t = compact(text)
    if any(compact(x) in t for x in ('account name is already', 'passwords do not match', 'password did not match')):
        return 'error'
    if 'creatingyouraccount' in t or 'settingupyourmac' in t:
        return 'busy'
    if 'areyousureyouwanttoskip' in t:
        return 'confirm_skip'
    if 'ihavereadandagree' in t:
        return 'confirm_terms'
    if 'countryorregion' in t:
        return 'region'
    if 'writtenandspokenlanguages' in t:
        return 'continue'
    if 'accessibility' in t and ('vision' in t or 'motor' in t):
        return 'not_now'
    if 'dataprivacy' in t:
        return 'continue'
    if 'migrationassistant' in t or 'transferinformationtothismac' in t:
        return 'not_now'
    if 'signinwithyourappleid' in t or 'signintoyourappleaccount' in t:
        return 'apple_id'
    if 'termsandconditions' in t:
        return 'terms'
    if 'createacomputeraccount' in t or ('fullname' in t and 'accountname' in t and 'verify' in t):
        return 'create_account'
    if 'expresssetup' in t:
        return 'express'
    if 'dontwanttouselocation' in t or 'donotuselocation' in t:
        return 'confirm_location'
    if 'enablelocationservices' in t:
        return 'location'
    if 'analytics' in t:
        return 'analytics'
    if 'screentime' in t:
        return 'later'
    if 'improvesiri' in t:
        return 'improve_siri'
    if 'enableasksiri' in t or 'enablesiri' in t:
        return 'siri'
    if 'filevault' in t:
        return 'filevault'
    if 'chooseyourlook' in t or 'truetonedisplay' in t or 'keepyourmacuptodate' in t:
        return 'continue'
    if 'getstarted' in t or 'welcometomac' in t:
        return 'welcome'
    if 'finder' in t and 'file' in t and 'edit' in t:
        return 'desktop'
    if 'language' in t and 'english' in t:
        return 'language'
    return 'boot' if not t else 'unknown'


def find(words, phrase):
    """Match only complete adjacent OCR tokens; return their visible center."""
    target = [compact(x) for x in phrase.split()]
    keys = [compact(w['text']) for w in words]
    matches = []
    for start in range(len(keys) - len(target) + 1):
        if keys[start:start + len(target)] != target:
            continue
        group = words[start:start + len(target)]
        left = min(w['left'] for w in group)
        top = min(w['top'] for w in group)
        right = max(w['left'] + w['width'] for w in group)
        bottom = max(w['top'] + w['height'] for w in group)
        # Do not combine text from unrelated rows into a fictitious button.
        if bottom - top > 40:
            continue
        matches.append(((left + right) // 2, (top + bottom) // 2, left, top, right, bottom))
    return matches


BOOTSTRAP_COMMAND = """printf 'lume\\n' | sudo -S -p '' /bin/sh -c '{ /usr/sbin/dseditgroup -o read com.apple.access_ssh >/dev/null 2>&1 || /usr/sbin/dseditgroup -o create com.apple.access_ssh; } && /usr/sbin/dseditgroup -o edit -a lume -t user com.apple.access_ssh && /bin/launchctl enable system/com.openssh.sshd && { /bin/launchctl print system/com.openssh.sshd >/dev/null 2>&1 || /bin/launchctl bootstrap system /System/Library/LaunchDaemons/ssh.plist; } && /usr/bin/pmset -a sleep 0 displaysleep 0' && echo VIRFIELD_BOOTSTRAP_DONE"""


def run():
    import csv
    import io
    import json
    import pathlib
    import subprocess
    import sys
    import time
    from urllib.parse import urlparse, unquote
    from vncdotool import api
    from PIL import Image

    config = json.load(sys.stdin)
    endpoint = urlparse(config['url'])
    if endpoint.scheme != 'vnc' or endpoint.hostname not in ('127.0.0.1', 'localhost', '::1'):
        raise RuntimeError('Setup VNC endpoint must be loopback')
    folder = pathlib.Path(config['directory'])
    folder.mkdir(mode=0o700, parents=True, exist_ok=True)
    previous = None
    unchanged = 0
    unknown = 0
    for step in range(160):
        client = api.connect(f'{endpoint.hostname}::{endpoint.port}', password=unquote(endpoint.password or ''), timeout=30)
        try:
            picture = folder / f'setup-{step:03}.png'
            client.captureScreen(str(picture))
            frame = Image.open(picture).convert('RGB')
            if frame.width < 640 or frame.height < 480:
                raise RuntimeError('Unexpected guest resolution')
            output = subprocess.run([config['tesseract'], str(picture), 'stdout', 'tsv'], capture_output=True, text=True, timeout=30, check=True).stdout
            words = []
            for row in csv.DictReader(io.StringIO(output), delimiter='\t'):
                if row.get('text', '').strip() and float(row['conf']) >= 20:
                    words.append({**{k: int(row[k]) for k in ('left', 'top', 'width', 'height')}, 'text': row['text'].strip()})
            text = ' '.join(w['text'] for w in words)
            action = classify(text)
            print(f'Observation {step}: {action}', flush=True)
            if action in ('boot', 'busy', 'unknown'):
                unknown += 1
                if unknown >= (30 if action in ('boot', 'busy') else 3):
                    raise RuntimeError(f'Unrecognized setup state; inspect setup-{step:03}.png')
                time.sleep(5)
                continue
            if action == 'error':
                raise RuntimeError('Setup rejected account input; inspect the current screen')
            unknown = 0
            signature = (action, compact(text))
            if signature == previous:
                unchanged += 1
                if unchanged >= 12:
                    raise RuntimeError('Setup did not change after input; refusing to repeat a mutation')
                time.sleep(5)
                continue
            unchanged = 0
            previous = signature

            def tap(phrase, optional=False):
                candidates = find(words, phrase)
                if not candidates:
                    if optional:
                        return False
                    raise RuntimeError(f'Expected visible control not found: {phrase}')
                x, y, *_ = max(candidates, key=lambda m: m[1])
                client.mouseMove(x, y)
                client.mousePress(1)
                return True

            def uncheck(phrase):
                matches = find(words, phrase)
                if not matches:
                    return False
                _, y, left, *_ = matches[0]
                # Native light-appearance checkboxes use a filled blue square.
                # Inspect only the visible control immediately before its label.
                patch = frame.crop((max(0, left - 28), max(0, y - 10), left - 2, y + 10))
                blue = sum(1 for r, g, b in patch.getdata() if b > 120 and b > r * 1.4 and b > g * 1.1)
                if blue > 8:
                    client.mouseMove(left - 15, y)
                    client.mousePress(1)
                return True

            if action == 'language':
                tap('English')
                client.keyPress('enter')
            elif action == 'region':
                tap('United States')
                tap('Continue')
            elif action == 'not_now':
                tap('Not Now')
            elif action in ('apple_id', 'later'):
                tap('Set Up Later')
            elif action == 'confirm_skip':
                tap('Skip')
            elif action == 'terms':
                tap('Agree')
            elif action == 'confirm_terms':
                client.keyPress('enter')
            elif action == 'create_account':
                fields = find(words, 'Full name')
                if not fields:
                    raise RuntimeError('Account form did not expose Full name')
                _, y, _, _, right, _ = fields[0]
                client.mouseMove(right + 140, y)
                client.mousePress(1)
                for index, value in enumerate(('lume', 'lume', 'lume', 'lume')):
                    if index:
                        client.keyPress('tab')
                    client.keyPress('meta-a')
                    client.type(value)
                tap('Continue')
            elif action == 'express':
                tap('Customize Settings')
            elif action == 'location':
                uncheck('Enable Location Services')
                tap('Continue')
            elif action == 'confirm_location':
                tap("Don't Use")
            elif action == 'analytics':
                uncheck('Share Mac Analytics')
                uncheck('Share with app developers')
                tap('Continue')
            elif action == 'siri':
                if not uncheck('Enable Ask Siri'):
                    uncheck('Enable Siri')
                tap('Continue')
            elif action == 'improve_siri':
                tap('Not Now')
                tap('Continue')
            elif action == 'filevault':
                if not uncheck('Turn on FileVault disk encryption'):
                    if not uncheck('Turn on FileVault'):
                        raise RuntimeError('Cannot identify FileVault policy control')
                tap('Continue')
            elif action == 'continue':
                tap('Continue')
            elif action == 'welcome':
                tap('Get Started')
            elif action == 'desktop':
                # Bootstrap only the fresh guest's fixed account. The Go stage
                # validates SSH, rotates this public bootstrap password, sets
                # automatic login, and verifies the desktop after reboot.
                client.keyPress('meta-space')
                time.sleep(2)
                client.type('Terminal')
                client.keyPress('enter')
                time.sleep(4)
                terminal = folder / 'terminal-ready.png'
                client.captureScreen(str(terminal))
                terminal_frame = Image.open(terminal)
                top = terminal_frame.crop((0, 0, terminal_frame.width, 40))
                stream = io.BytesIO()
                top.save(stream, format='PNG')
                menu = subprocess.run([config['tesseract'], 'stdin', 'stdout', '--psm', '7'], input=stream.getvalue(), capture_output=True, timeout=30, check=True).stdout.decode('utf-8', errors='replace')
                if 'terminal' not in compact(menu):
                    raise RuntimeError('Terminal is not the foreground guest app; refusing shell input')
                client.type(BOOTSTRAP_COMMAND)
                client.keyPress('enter')
                time.sleep(5)
                return
            time.sleep(6)
        finally:
            client.disconnect()
    raise RuntimeError('Setup Assistant exceeded its observation budget')


def main():
    from vncdotool import api
    try:
        run()
    finally:
        api.shutdown()


if __name__ == '__main__':
    main()
