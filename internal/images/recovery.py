"""Paired Recovery driver for Apple silicon macOS guests.

Follows upstream Lume's csrutil workflow, with observed transitions rather than
blind key counts. Normal-boot canonical verification remains mandatory in Go.
"""
import csv
import io
import json
import pathlib
import subprocess
import sys
import time
from urllib.parse import urlparse, unquote


def word(words, expected):
    matches = [w for w in words if w.get('text', '').lower().strip('.,:') == expected.lower() and float(w.get('conf', '-1')) >= 30]
    return matches[0] if matches else None


def picker_options(words):
    found = word(words, 'Options')
    # The boot picker briefly shows Options centered before the macOS volume
    # appears. Clicking that stale coordinate selects Macintosh HD instead.
    return found if found and int(found.get('left', '0')) >= 960 else None


def click_control(client, found, pause=time.sleep):
    # Recovery coalesces rapid pointer events. Keep the pointer at the observed
    # control through mouse-down/up; moving it away immediately can switch the
    # newly opened menu to a different menu-bar item (observed on Sequoia).
    client.mouseMove(int(found['left']) + int(found['width']) // 2,
                     int(found['top']) + int(found['height']) // 2)
    pause(0.2)
    client.mouseDown(1)
    pause(0.1)
    client.mouseUp(1)
    pause(0.2)


def run_recovery(wait_for, click_word, keys, type_line, password):
    _, words = wait_for('boot-picker', lambda text, words: picker_options(words) is not None, 120)
    click_word(words, 'Options')
    _, words = wait_for('options-selected', lambda text, words: word(words, 'Continue') is not None)
    click_word(words, 'Continue')
    text, words = wait_for('recovery-entry', lambda text, words: word(words, 'English') is not None or word(words, 'Utilities') is not None or 'select a user' in text, 180)
    if word(words, 'English') is not None and word(words, 'Utilities') is None:
        click_word(words, 'English')
        keys('enter')
        text, words = wait_for('recovery-ready', lambda text, words: word(words, 'Utilities') is not None or 'select a user' in text, 120)
    if 'select a user' in text:
        click_word(words, 'lume')
        _, words = wait_for('recovery-user-selected', lambda text, words: word(words, 'Next') is not None)
        click_word(words, 'Next')
        wait_for('recovery-user-password', lambda text, words: 'password' in text and 'lume' in text)
        type_line(password)
    _, words = wait_for('recovery-menu', lambda text, words: word(words, 'Utilities') is not None, 120)
    click_word(words, 'Utilities')
    _, words = wait_for('utilities-menu', lambda text, words: word(words, 'Terminal') is not None, 30)
    click_word(words, 'Terminal')
    wait_for('terminal-shell', lambda text, words: 'bash' in text, 30)
    type_line('csrutil disable')
    text, _ = wait_for('csrutil-confirmation', lambda text, words: any(p in text for p in ('[y/n]', 'y/n', 'authorized user', 'password')), 30)
    if 'y/n' in text:
        type_line('y')
        text, _ = wait_for('administrator-prompt', lambda text, words: 'authorized user' in text or 'password' in text, 30)
    if 'authorized user' in text and 'password' not in text:
        type_line('lume')
        wait_for('administrator-password', lambda text, words: 'password' in text, 30)
    type_line(password)
    wait_for('sip-policy-committed', lambda text, words: 'successfully disabled system integrity protection' in text or ('system integrity protection is off' in text and 'restart the machine for the changes to take effect' in text), 90)
    # Keep the signed policy and shut down cleanly. Normal-boot canonical csrutil
    # verification in Go is mandatory before the image can be published.
    type_line('shutdown -h now')
    print('Recovery requested clean shutdown', flush=True)


def run():
    from vncdotool import api
    from PIL import Image

    config = json.load(sys.stdin)
    url = urlparse(config['url'])
    if url.scheme != 'vnc' or url.hostname != '127.0.0.1':
        raise RuntimeError('Recovery VNC must be the private loopback endpoint')
    folder = pathlib.Path(config['directory'])
    folder.mkdir(mode=0o700, parents=True, exist_ok=True)
    sequence = 0

    def connect():
        return api.connect(f'{url.hostname}::{url.port}', password=unquote(url.password or ''), timeout=15)

    def observe(label):
        nonlocal sequence
        picture = folder / f'{sequence:03}-{label}.png'
        sequence += 1
        client = connect()
        try:
            client.captureScreen(str(picture))
        finally:
            client.disconnect()
        with Image.open(picture) as frame:
            if frame.size not in ((1920, 1080), (1920, 1440)):
                # Recovery starts with a temporary 1280x720 framebuffer. Never
                # interact until the configured display mode has been negotiated.
                return '', []
        raw = subprocess.run([config['tesseract'], str(picture), 'stdout', '--psm', '11', 'tsv'], capture_output=True, text=True, timeout=20, check=True).stdout
        words = list(csv.DictReader(io.StringIO(raw), delimiter='\t'))
        # Sparse OCR omits the dark-on-gray Continue button beneath Options.
        # Read this versioned ROI only after Options reaches its stable right
        # position; exact OCR is still required before clicking.
        if label == 'options-selected' and picker_options(words) is not None:
            roi = folder / f'{sequence:03}-picker-button.png'
            with Image.open(picture) as frame:
                offset_y = (frame.height - 1080) // 2
                frame.crop((1088, 681 + offset_y, 1280, 725 + offset_y)).convert('L').resize((576, 132)).save(roi)
            raw_button = subprocess.run([config['tesseract'], str(roi), 'stdout', '--psm', '7', 'tsv'], capture_output=True, text=True, timeout=20, check=True).stdout
            for w in csv.DictReader(io.StringIO(raw_button), delimiter='\t'):
                if w.get('text', '').lower() == 'continue':
                    for field, offset in (('left', 1088), ('top', 681 + offset_y), ('width', 0), ('height', 0)):
                        w[field] = str(int(w[field]) // 3 + offset)
                    words.append(w)
        text = ' '.join(w['text'] for w in words if w.get('text')).lower()
        return text, words

    def wait_for(label, predicate, seconds=90):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            try:
                text, words = observe(label)
            except (TimeoutError, ConnectionError, OSError):
                time.sleep(2)
                continue
            if predicate(text, words):
                print(label + ': verified', flush=True)
                return text, words
            time.sleep(2)
        raise RuntimeError('Recovery did not reach verified ' + label)


    def click_word(words, expected):
        found = word(words, expected)
        if not found:
            raise RuntimeError('Required Recovery control not recognized: ' + expected)
        client = connect()
        try:
            click_control(client, found)
        finally:
            client.disconnect()
        time.sleep(2)

    def keys(*values):
        client = connect()
        try:
            for value in values:
                client.keyPress(value)
                time.sleep(0.1)
        finally:
            client.disconnect()

    def type_line(value):
        if not all(c in 'abcdefghijklmnopqrstuvwxyz0123456789 -' for c in value):
            raise RuntimeError('Unsupported Recovery keyboard characters')
        client = connect()
        try:
            for c in value:
                client.keyPress(c)
                time.sleep(0.05)
            client.keyPress('enter')
        finally:
            client.disconnect()
        time.sleep(2)

    run_recovery(wait_for, click_word, keys, type_line, config['admin_password'])


def main():
    from vncdotool import api
    try:
        run()
    finally:
        # Twisted's worker pool can keep Python alive after a successful GUI
        # run even though its reactor thread is daemonized. Close it explicitly.
        api.shutdown()


if __name__ == '__main__':
    main()
