"""Versioned macOS Setup Assistant finisher; guest VNC only, no lifecycle."""


def locate_phrase(tsv, phrase, min_y=0):
    """Locate a complete OCR label on one line; never infer a button from a title."""
    import csv
    from io import StringIO

    target = phrase.lower().split()
    lines = {}
    for word in csv.DictReader(StringIO(tsv), delimiter='\t'):
        label = word.get('text', '').strip().lower().strip('.,:!?')
        if not label:
            continue
        line = tuple(word[k] for k in ('page_num', 'block_num', 'par_num', 'line_num'))
        lines.setdefault(line, []).append((int(word['word_num']), label, int(word['left']), int(word['top']), int(word['width']), int(word['height'])))
    matches = []
    for line in lines.values():
        line.sort()
        for start in range(len(line) - len(target) + 1):
            part = line[start:start + len(target)]
            if [word[1] for word in part] != target or min(word[3] for word in part) < min_y:
                continue
            left = min(word[2] for word in part)
            right = max(word[2] + word[4] for word in part)
            top = min(word[3] for word in part)
            bottom = max(word[3] + word[5] for word in part)
            matches.append(((left + right) // 2, (top + bottom) // 2))
    return max(matches, key=lambda point: point[1]) if matches else None

def classify(screen, welcome_button=""):
    if ''.join(welcome_button.lower().split()) == "getstarted":
        return "welcome"
    text = ' '.join(screen.lower().split())
    if not text:
        return 'boot'
    if 'accessibility features adapt your mac' in text:
        return 'accessibility'
    if 'are you sure you want to skip signing in' in text:
        return 'confirm_account_skip'
    if 'sign in to your apple account' in text:
        return 'account'
    if 'age range' in text and ('adult' in text or 'person who will use this mac' in text):
        return 'adult'
    if 'choose your look' in text or 'analytics' in text or 'liquid glass' in text:
        return 'continue'
    if 'screen time' in text:
        return 'later'
    if 'mac data will not be' in text:
        return 'confirm_filevault_skip'
    if 'your mac is ready for filevault' in text:
        return 'later'
    if any(word in text for word in ('update mac automatically', 'siri', 'location services')):
        return 'continue'
    if any(word in text for word in ('welcome to mac', 'get started')):
        return 'welcome'
    if 'finder' in text or 'last login:' in text:
        return 'desktop'
    return 'unknown'


def run():
    import json
    from io import BytesIO
    import pathlib
    import subprocess
    import sys
    import time
    from urllib.parse import urlparse, unquote
    from vncdotool import api
    from PIL import Image

    config = json.load(sys.stdin)
    url = urlparse(config['url'])
    if url.scheme != 'vnc' or url.hostname not in ('127.0.0.1', 'localhost', '::1'):
        raise RuntimeError('VNC endpoint must be loopback')
    folder = pathlib.Path(config['directory'])
    folder.mkdir(mode=0o700, parents=True, exist_ok=True)
    unknown_frames = 0
    last_clicked_screen = None
    unchanged_frames = 0
    for step in range(60):
        # Apple's VNC server may withhold a second framebuffer update when the
        # screen is unchanged. A fresh connection requests a full initial frame;
        # reconnecting a read never repeats a mouse action.
        client = api.connect(f'{url.hostname}::{url.port}', password=unquote(url.password or ''), timeout=30)
        try:
            picture = folder / f'assistant-{step:02}.png'
            client.captureScreen(str(picture))
            with Image.open(picture) as frame:
                if frame.size not in ((1920, 1080), (1920, 1440)):
                    raise RuntimeError('Unexpected guest resolution; fixed coordinates are unsafe')
                tall = frame.height == 1440
            screen = subprocess.run([config['tesseract'], str(picture), 'stdout'], capture_output=True, text=True, timeout=30, check=True).stdout
            # Full-page segmentation can miss the isolated final button on a
            # photographic wallpaper. Recognize only its exact label in the
            # verified 1920x1080 button region, never infer it from an empty OCR.
            with Image.open(picture) as frame:
                button = frame.crop((860, 814, 1060, 875)).convert('L').resize((600, 183))
                png = BytesIO()
                button.save(png, format='PNG')
            button_text = subprocess.run([config['tesseract'], 'stdin', 'stdout', '--psm', '7'], input=png.getvalue(), capture_output=True, timeout=30, check=True).stdout.decode('utf-8', errors='replace')
            action = classify(screen, button_text if not tall else '')
            print(f'Observation {step}: {action}', flush=True)
            def tap(x, y, delay=8):
                client.mouseMove(x, y)
                client.mousePress(1)
                time.sleep(delay)
            def tap_label(picture, phrases, min_y, delay=8):
                result = subprocess.run([config['tesseract'], str(picture), 'stdout', 'tsv'], capture_output=True, text=True, timeout=30, check=True)
                for phrase in phrases:
                    point = locate_phrase(result.stdout, phrase, min_y)
                    if point is not None:
                        tap(*point, delay)
                        return
                raise RuntimeError(f'Expected Assistant button {phrases!r} is not visible in {picture.name}')
            if tall and action in ('boot', 'unknown'):
                result = subprocess.run([config['tesseract'], str(picture), 'stdout', 'tsv'], capture_output=True, text=True, timeout=30, check=True)
                if locate_phrase(result.stdout, 'Get Started', 500) is not None:
                    action = 'welcome'
            if action in ('boot', 'unknown'):
                unknown_frames += 1
                limit = 24 if action == 'boot' else 3
                if unknown_frames >= limit:
                    raise RuntimeError(f'No recognized assistant screen; inspect assistant-{step:02}.png')
                time.sleep(5)
                continue
            unknown_frames = 0
            normalized = ' '.join(screen.lower().split())
            if normalized == last_clicked_screen:
                unchanged_frames += 1
                if unchanged_frames >= 6:
                    raise RuntimeError('Assistant screen did not change after the last action; refusing repeated clicks')
                time.sleep(5)
                continue
            unchanged_frames = 0
            last_clicked_screen = normalized
            if tall and action == 'accessibility':
                tap_label(picture, ('Not Now',), 800, 12)
            elif tall and action == 'account':
                tap_label(picture, ('Other Sign-In Options',), 650, 2)
                menu = folder / f'assistant-{step:02}-menu.png'
                client.captureScreen(str(menu))
                tap_label(menu, ('Set Up Later', 'Not Now'), 650, 5)
            elif tall and action == 'adult':
                tap_label(picture, ('Adult',), 500)
            elif tall and action == 'continue':
                tap_label(picture, ('Continue',), 800)
            elif tall and action == 'later':
                tap_label(picture, ('Set Up Later', 'Not Now'), 800)
            elif tall and action == 'confirm_account_skip':
                tap_label(picture, ('Skip', 'Continue'), 400)
            elif tall and action == 'confirm_filevault_skip':
                tap_label(picture, ('Continue',), 400)
            elif tall and action == 'welcome':
                tap_label(picture, ('Get Started',), 500, 15)
                return
            elif action == 'accessibility':
                tap(1600, 1058, 12)
            elif action == 'confirm_account_skip':
                tap(1080, 746)
            elif action == 'account':
                tap(360, 1058, 2)
                tap(377, 1031, 5)
            elif action == 'adult':
                tap(960, 888)
            elif action == 'continue':
                tap(1600, 1058)
            elif action == 'later':
                tap(1300, 1058)
            elif action == 'confirm_filevault_skip':
                tap(1080, 667)
            elif action == 'welcome':
                tap(960, 845, 15)
                # The Go stage verifies .AppleSetupDone, Finder and absence of
                # Setup Assistant over pinned SSH. Desktop OCR is not authority.
                return
            elif action == 'desktop':
                return
        finally:
            client.disconnect()
    raise RuntimeError('Assistant did not finish in 60 bounded observations')


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
