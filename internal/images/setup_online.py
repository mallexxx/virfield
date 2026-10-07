"""State-aware native Setup Assistant for macOS with guest-bound APFS encryption.

Only recognized screens and visible OCR text anchors may drive input. Lifecycle,
SSH authentication, password rotation and final readiness remain in Go.
"""
import base64
import re
import time


def compact(text):
    return re.sub(r'[^a-z0-9]', '', text.lower())


def classify(text):
    t = compact(text)
    if 'searchtheweb' in t or 'spotlightsearch' in t:
        return 'dismiss_search'
    if any(compact(x) in t for x in ('account name is already', 'passwords do not match', 'password did not match')):
        return 'error'
    if 'creatingyouraccount' in t or 'creatingaccount' in t or 'settingupyourmac' in t:
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
    if 'chooseyourlook' in t or 'truetonedisplay' in t or 'keepyourmacuptodate' in t or 'selectyourtimezone' in t:
        return 'continue'
    if 'getstarted' in t or 'welcometomac' in t:
        return 'welcome'
    if 'lume' in t and 'enterpassword' in t:
        return 'login'
    if ('finder' in t and 'file' in t and 'edit' in t) or ('terminal' in t and 'shell' in t and 'edit' in t):
        return 'desktop'
    if ('language' in t and 'english' in t) or all(x in t for x in ('englishuk', 'englishaustralia', 'englishindia')):
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


def type_text(client, value):
    shifted = dict(zip('~!@#$%^&*()_+{}|:"<>?', '`1234567890-=[]\\;\',./'))
    for char in value:
        if not 32 <= ord(char) < 127:
            raise RuntimeError('Unsupported setup keyboard character')
        if char.isupper():
            client.keyPress('shift-' + char.lower())
        elif char in shifted:
            client.keyPress('shift-' + ('minus' if shifted[char] == '-' else shifted[char]))
        else:
            client.keyPress(char)
        time.sleep(0.05)


def blue_controls(frame):
    """Find filled native default buttons; text recognition still gates clicks."""
    x0, y0, x1, y1 = frame.width // 4, frame.height // 3, frame.width * 3 // 4, frame.height * 4 // 5
    pixels = frame.load()
    bands = []
    current = []
    for y in range(y0, y1):
        row = [x for x in range(x0, x1) if pixels[x, y][2] > 120 and pixels[x, y][2] > pixels[x, y][0] * 1.4 and pixels[x, y][2] > pixels[x, y][1] * 1.1]
        if len(row) >= 80:
            current.append((min(row), y, max(row)))
        elif current:
            bands.append(current)
            current = []
    if current:
        bands.append(current)
    result = []
    for band in bands:
        left, right = min(row[0] for row in band), max(row[2] for row in band)
        top, bottom = band[0][1], band[-1][1]
        if not 20 <= bottom - top <= 90:
            continue
        # Adjacent controls can share one horizontal band: Monterey draws a
        # blue outline around "Don't Skip" next to the filled "Skip" button.
        # An outline contributes only a few blue pixels per column, while a
        # filled button contributes most of its height. Split by that density
        # before OCR so the two labels cannot be merged into one crop.
        minimum = max(15, (bottom - top + 1) * 35 // 100)
        filled = []
        for x in range(left, right + 1):
            count = sum(1 for y in range(top, bottom + 1)
                        if pixels[x, y][2] > 120 and pixels[x, y][2] > pixels[x, y][0] * 1.4
                        and pixels[x, y][2] > pixels[x, y][1] * 1.1)
            if count >= minimum:
                filled.append(x)
        if not filled:
            continue
        start = last = filled[0]
        for x in filled[1:]:
            if x - last > 8:
                if 80 <= last - start <= 500:
                    result.append((start, top, last + 1, bottom + 1))
                start = x
            last = x
        if 80 <= last - start <= 500:
            result.append((start, top, last + 1, bottom + 1))
    return result


# Visible 40px circular-arrow signature from Monterey's native Hello screen.
# Match both foreground and background; a generic purple frame is insufficient.
_HELLO_ARROW = int('0000000000000000000000000000000001ff0000000fffe000001fc7f000007c007c0000f0001e0001e0000f0003c0000780038000038007000001c00e000000e00e000e00e00c000f00601c000780701c0003c0701c0001e0701c0ffff0301c1ffff0301c0fffe0301c0001c0701c000380701c000700700c000e00600e000c00e00e000000e007000001c0038000038003c000078001e0000f0000f0001e00007c007c00001ffff000000fffe0000001ff00000000000000000000000000000000000000000000', 16)


def hello_button(frame):
    if frame.size == (1920, 1080):
        # Ventura renders its language-independent Hello arrow in a fixed
        # circular button above the localized label. OCR cannot identify the
        # rotating greeting, so accept only the bounded white arrow geometry.
        patch = frame.crop((920, 807, 1000, 887)).convert('RGB')
        white = []
        for index, (r, g, b) in enumerate(patch.getdata()):
            # Ventura composites the nominally white arrow through the tinted
            # Hello background, producing pale pink as well as white pixels.
            if r > 235 and g > 100 and b > 75 and r - g < 170:
                white.append((index % 80, index // 80))
        if not 120 <= len(white) <= 400:
            return None
        left, right = min(x for x, _ in white), max(x for x, _ in white)
        top, bottom = min(y for _, y in white), max(y for _, y in white)
        if not (20 <= right - left <= 42 and 20 <= bottom - top <= 42):
            return None
        if not (32 <= (left + right) // 2 <= 48 and 32 <= (top + bottom) // 2 <= 48):
            return None
        return (960, 847)
    if frame.size != (1920, 1440):
        return None
    patch = frame.crop((940, 1185, 980, 1225)).convert('RGB')
    actual = 0
    for r, g, b in patch.getdata():
        actual = (actual << 1) | int(g > 65)
    expected = _HELLO_ARROW.bit_count()
    if (actual & _HELLO_ARROW).bit_count() < expected * 0.90:
        return None
    if (actual & ~_HELLO_ARROW).bit_count() > expected * 0.20:
        return None
    return (960, 1205)


def sleeping_display(frame):
    """Recognize only a fully dark framebuffer so a harmless key can wake it."""
    extrema = frame.getextrema()
    return all(high <= 2 for _low, high in extrema)


_LANGUAGE_ARROW = int('00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000700000000000000f80000000000000fc0000000000000fe0000000000000ff00000000000007f80000000000003fc0000000000001fe0000000000000ff00000000000007f80000000000003fc0000000000001fe0000000000000ff00000000000007f80000000000003fc0000000000001fe00007ffffffffff0000fffffffffff8000fffffffffff8000fffffffffff80007ffffffffff00001ffffffe1fe0000000000003fc0000000000007f8000000000000ff0000000000001fe0000000000003fc0000000000007f8000000000000ff0000000000001fe0000000000003fc0000000000007f8000000000000ff0000000000000fe0000000000000fc0000000000000f800000000000007000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000', 16)


def language_button(frame):
    if frame.size != (1920, 1440):
        return None
    actual = 0
    for r, g, b in frame.crop((1663, 1232, 1723, 1292)).convert('RGB').getdata():
        # Monterey renders the same arrow in light gray while its spinner is
        # visible; match its shape against the lighter panel background.
        actual = (actual << 1) | int(max(r, g, b) < 215 and max(r, g, b) - min(r, g, b) < 8)
    expected = _LANGUAGE_ARROW.bit_count()
    if (actual & _LANGUAGE_ARROW).bit_count() < expected * 0.90:
        return None
    if (actual & ~_LANGUAGE_ARROW).bit_count() > expected * 0.20:
        return None
    return (1693, 1262)


def login_password_field(frame):
    """Find Monterey's centered login password field when OCR misses it."""
    if frame.size != (1920, 1440):
        return None
    pixels = frame.load()
    x0, y0, x1, y1 = 600, 760, 1320, 1010
    bands = []
    current = []
    for y in range(y0, y1):
        xs = []
        for x in range(x0, x1):
            r, g, b = pixels[x, y]
            if r > 130 and g > 80 and b > 145 and b >= r * 0.9 and max(r, g, b) - min(r, g, b) < 120:
                xs.append(x)
        if len(xs) >= 180:
            current.append((min(xs), y, max(xs)))
        elif current:
            bands.append(current)
            current = []
    if current:
        bands.append(current)
    candidates = []
    for band in bands:
        left, right = min(row[0] for row in band), max(row[2] for row in band)
        top, bottom = band[0][1], band[-1][1]
        width, height = right - left + 1, bottom - top + 1
        cx, cy = (left + right) // 2, (top + bottom) // 2
        if 250 <= width <= 420 and 35 <= height <= 75 and 850 <= cx <= 1070 and 830 <= cy <= 960:
            candidates.append((cx, cy))
    return candidates[0] if len(candidates) == 1 else None


BOOTSTRAP_COMMAND = """printf 'lume\\n' | sudo -S -p '' /bin/sh -c '{ /usr/sbin/dseditgroup -o read com.apple.access_ssh >/dev/null 2>&1 || /usr/sbin/dseditgroup -o create com.apple.access_ssh; } && /usr/sbin/dseditgroup -o edit -a lume -t user com.apple.access_ssh && /bin/launchctl enable system/com.openssh.sshd && { /bin/launchctl print system/com.openssh.sshd >/dev/null 2>&1 || /bin/launchctl bootstrap system /System/Library/LaunchDaemons/ssh.plist; } && /bin/launchctl kickstart -k system/com.openssh.sshd && /usr/bin/pmset -a sleep 0 displaysleep 0' && echo VIRFIELD_BOOTSTRAP_DONE"""


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
    from vncdotool.client import VNCDoToolFactory
    from PIL import Image

    class SetupVNCFactory(VNCDoToolFactory):
        # Setup owns the guest console exclusively while this stage runs.
        shared = False

    config = json.load(sys.stdin)

    def read_ocr(arguments, payload=None):
        # OCR reads screenshots only. A loaded host can stall one process; retry
        # that read before treating the visible setup state as unavailable.
        for attempt in range(2):
            try:
                result = subprocess.run([config['tesseract'], *arguments], input=payload,
                                        capture_output=True, timeout=30, check=True)
                return result.stdout.decode('utf-8', errors='replace')
            except (subprocess.TimeoutExpired, subprocess.CalledProcessError) as exc:
                if attempt == 1:
                    raise RuntimeError('Setup OCR unavailable after two bounded attempts') from exc
                time.sleep(2)

    endpoint = urlparse(config['url'])
    if endpoint.scheme != 'vnc' or endpoint.hostname not in ('127.0.0.1', 'localhost', '::1'):
        raise RuntimeError('Setup VNC endpoint must be loopback')
    folder = pathlib.Path(config['directory'])
    folder.mkdir(mode=0o700, parents=True, exist_ok=True)
    previous = None
    filevault_toggle_sent = False
    unchanged = 0
    unknown = 0
    for step in range(160):
        client = api.connect(f'{endpoint.hostname}::{endpoint.port}', password=unquote(endpoint.password or ''), timeout=30, factory_class=SetupVNCFactory)
        try:
            picture = folder / f'setup-{step:03}.png'
            client.captureScreen(str(picture))
            client.disconnect()
            client = None
            frame = Image.open(picture).convert('RGB')
            if frame.width < 640 or frame.height < 480:
                raise RuntimeError('Unexpected guest resolution')
            output = read_ocr([str(picture), 'stdout', 'tsv'])
            words = []
            for row in csv.DictReader(io.StringIO(output), delimiter='\t'):
                if row.get('text', '').strip() and float(row['conf']) >= 20:
                    words.append({**{k: int(row[k]) for k in ('left', 'top', 'width', 'height')}, 'text': row['text'].strip()})
            # Native focus rings can hide footer labels from full-frame OCR.
            # Read the footer independently; coordinates still come from text.
            footer_top = frame.height * 4 // 5
            footer = frame.crop((frame.width // 2, footer_top, frame.width, frame.height)).convert('L')
            stream = io.BytesIO()
            footer.resize((footer.width * 2, footer.height * 2)).save(stream, format='PNG')
            output = read_ocr(['stdin', 'stdout', '--psm', '11', 'tsv'], stream.getvalue())
            for row in csv.DictReader(io.StringIO(output), delimiter='\t'):
                if row.get('text', '').strip() and float(row['conf']) >= 30:
                    item = {**{k: int(row[k]) // 2 for k in ('left', 'top', 'width', 'height')}, 'text': row['text'].strip()}
                    item['left'] += frame.width // 2
                    item['top'] += footer_top
                    words.append(item)
            text = ' '.join(w['text'] for w in words)
            action = classify(text)
            menu_stream = io.BytesIO()
            frame.crop((0, 0, frame.width, 48)).save(menu_stream, format='PNG')
            menu_text = read_ocr(['stdin', 'stdout', '--psm', '7'], menu_stream.getvalue())
            if action == 'unknown' and classify(menu_text) == 'desktop':
                action = 'desktop'
            login_field = login_password_field(frame)
            if action == 'unknown' and login_field is not None:
                action = 'login'
            hello = hello_button(frame)
            if hello is not None:
                action = 'hello'
            print(f'Observation {step}: {action}', flush=True)
            if action in ('boot', 'busy', 'unknown'):
                unknown += 1
                if action == 'boot' and sleeping_display(frame) and unknown % 6 == 1:
                    # Setup Assistant can turn off the virtual display while a
                    # recovery is waiting. Shift wakes it without activating a
                    # control or changing any setup choice.
                    client = api.connect(f'{endpoint.hostname}::{endpoint.port}', password=unquote(endpoint.password or ''), timeout=30, factory_class=SetupVNCFactory)
                    client.keyPress('shift')
                    client.disconnect()
                    client = None
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
            # Keep framebuffer reads separate from input, as in the Recovery
            # driver: a stalled Apple framebuffer response must not queue keys.
            client = api.connect(f'{endpoint.hostname}::{endpoint.port}', password=unquote(endpoint.password or ''), timeout=30, factory_class=SetupVNCFactory)

            def tap(phrase, optional=False, first=False):
                candidates = find(words, phrase)
                if not candidates:
                    if optional:
                        return False
                    raise RuntimeError(f'Expected visible control not found: {phrase}')
                x, y, *_ = (min if first else max)(candidates, key=lambda m: m[1])
                client.mouseMove(x, y)
                time.sleep(0.1)
                client.mouseDown(1)
                time.sleep(0.1)
                client.mouseUp(1)
                return True

            def uncheck(phrase, verify_only=False):
                matches = find(words, phrase)
                if not matches:
                    return False
                _, y, left, *_ = matches[0]
                # Native light-appearance checkboxes use a filled blue square.
                # Inspect only the visible control immediately before its label.
                patch = frame.crop((max(0, left - 28), max(0, y - 10), left - 2, y + 10))
                blue = sum(1 for r, g, b in patch.getdata() if b > 120 and b > r * 1.4 and b > g * 1.1)
                if blue > 8:
                    if verify_only:
                        raise RuntimeError('FileVault remained selected after disabling it')
                    client.mouseMove(left - 15, y)
                    client.mousePress(1)
                    return 'changed'
                return 'clear'

            def default_button(label):
                # Full-frame OCR can omit white text on a blue default button.
                # Read the actual filled control in isolation, not dialog prose.
                for box in blue_controls(frame):
                    crop = frame.crop(box).convert('L')
                    crop = crop.resize((crop.width * 3, crop.height * 3))
                    stream = io.BytesIO()
                    crop.save(stream, format='PNG')
                    result = read_ocr(['stdin', 'stdout', '--psm', '7'], stream.getvalue())
                    if compact(result) == compact(label):
                        left, top, right, bottom = box
                        client.mouseMove((left + right) // 2, (top + bottom) // 2)
                        time.sleep(0.1)
                        client.mouseDown(1)
                        time.sleep(0.1)
                        client.mouseUp(1)
                        return
                # Monterey's license confirmation has a gray Agree button and
                # a second, disabled Agree behind the modal. Restrict OCR to
                # the modal's controls so background text cannot be clicked.
                left, top = frame.width * 35 // 100, frame.height // 2
                crop = frame.crop((left, top, frame.width * 65 // 100, frame.height * 3 // 4)).convert('L')
                stream = io.BytesIO()
                crop.resize((crop.width * 2, crop.height * 2)).save(stream, format='PNG')
                result = read_ocr(['stdin', 'stdout', '--psm', '11', 'tsv'], stream.getvalue())
                modal_words = []
                for row in csv.DictReader(io.StringIO(result), delimiter='\t'):
                    if row.get('text', '').strip() and float(row['conf']) >= 30:
                        modal_words.append({**{k: int(row[k]) // 2 for k in ('left', 'top', 'width', 'height')}, 'text': row['text'].strip()})
                matches = find(modal_words, label)
                if matches:
                    x, y, *_ = max(matches, key=lambda m: m[1])
                    client.mouseMove(left + x, top + y)
                    time.sleep(0.1)
                    client.mouseDown(1)
                    time.sleep(0.1)
                    client.mouseUp(1)
                    return
                raise RuntimeError(f'Expected default button not recognized: {label}')

            if action == 'dismiss_search':
                for key in ('enter', 'shift', 'alt', 'ctrl', 'meta'):
                    client.keyUp(key)
                client.keyPress('esc')
            elif action == 'hello':
                client.mouseMove(*hello)
                time.sleep(0.1)
                client.mouseDown(1)
                time.sleep(0.1)
                client.mouseUp(1)
            elif action == 'language':
                tap('English', first=True)
                if frame.size == (1920, 1080):
                    # Ventura's native language picker places the continue
                    # button partly below this framebuffer. Enter activates the
                    # selected row's default action without a blind click.
                    time.sleep(0.5)
                    client.keyPress('enter')
                else:
                    arrow = language_button(frame)
                    if arrow is None:
                        raise RuntimeError('Language continue arrow not recognized')
                    client.mouseMove(*arrow)
                    time.sleep(0.1)
                    client.mouseDown(1)
                    time.sleep(0.1)
                    client.mouseUp(1)
            elif action == 'region':
                if tap('United States', optional=True):
                    tap('Continue')
                else:
                    countries = [w for w in words if 650 < w['left'] < 1200 and 620 < w['top'] < 1100]
                    if frame.size != (1920, 1440) or not countries:
                        raise RuntimeError('Country list is not visible')
                    tap(countries[0]['text'])
                    type_text(client, 'united states')
                    # The next observation must expose the selected country
                    # before Continue is allowed.
            elif action == 'not_now':
                tap('Not Now')
            elif action in ('apple_id', 'later'):
                tap('Set Up Later')
            elif action == 'confirm_skip':
                default_button('Skip')
            elif action == 'terms':
                tap('Agree')
            elif action == 'confirm_terms':
                default_button('Agree')
            elif action == 'create_account':
                fields = find(words, 'Full name')
                if fields:
                    _, y, _, _, right, _ = fields[0]
                else:
                    # Full-frame OCR sometimes misses the label beside the
                    # focused blue input. Confirm it in a narrow crop above
                    # the visible Account name row before choosing a field.
                    accounts = find(words, 'Account name')
                    if not accounts or not find(words, 'Password'):
                        raise RuntimeError('Account form did not expose its required labels')
                    _, account_y, left, top, right, _ = accounts[0]
                    crop = frame.crop((max(0, left - 80), max(0, top - 100),
                                       min(frame.width, right + 80), top - 15))
                    stream = io.BytesIO()
                    crop.resize((crop.width * 2, crop.height * 2)).save(stream, format='PNG')
                    label = read_ocr(['stdin', 'stdout', '--psm', '6'], stream.getvalue())
                    if 'fullname' not in compact(label):
                        raise RuntimeError('Account form did not expose Full name')
                    y = account_y - 60
                client.mouseMove(right + 140, y)
                client.mousePress(1)
                for index, value in enumerate(('lume', 'lume', 'lume', 'lume')):
                    if index:
                        client.keyPress('tab')
                    client.keyPress('alt-a')
                    type_text(client, value)
                tap('Continue')
            elif action == 'express':
                tap('Customize Settings')
            elif action == 'location':
                uncheck('Enable Location Services')
                tap('Continue')
            elif action == 'confirm_location':
                default_button("Don't Use")
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
                state = uncheck('Turn on FileVault disk encryption', filevault_toggle_sent) or uncheck('Turn on FileVault', filevault_toggle_sent)
                if not state:
                    raise RuntimeError('Cannot identify FileVault policy control')
                if state == 'changed':
                    filevault_toggle_sent = True
                    # Never continue based on the frame from before the click.
                    # Re-observe the checkbox, then verify APFS over SSH before
                    # declaring setup complete or publishing the golden.
                    previous = None
                    time.sleep(6)
                    continue
                tap('Continue')
            elif action == 'continue':
                tap('Continue')
            elif action == 'welcome':
                tap('Get Started')
            elif action == 'login':
                fields = find(words, 'Enter Password')
                if fields:
                    x, y, *_ = fields[0]
                elif login_field is not None:
                    x, y = login_field
                else:
                    raise RuntimeError('Login screen did not expose password field')
                client.mouseMove(x, y)
                client.mousePress(1)
                client.keyPress('alt-a')
                type_text(client, 'lume')
                client.keyPress('enter')
            elif action == 'desktop':
                # Bootstrap only the fresh guest's fixed account. The Go stage
                # validates SSH, rotates this public bootstrap password, sets
                # automatic login, and verifies the desktop after reboot.
                for key in ('enter', 'shift', 'alt', 'ctrl', 'meta'):
                    client.keyUp(key)
                client.keyPress('esc')
                time.sleep(0.2)
                client.keyPress('esc')
                time.sleep(0.5)
                if 'terminal' not in compact(menu_text):
                    client.keyPress('alt-space')
                    time.sleep(2)
                    type_text(client, 'Terminal')
                    client.keyPress('enter')
                    time.sleep(4)
                terminal = folder / 'terminal-ready.png'
                client.disconnect()
                client = api.connect(f'{endpoint.hostname}::{endpoint.port}', password=unquote(endpoint.password or ''), timeout=30, factory_class=SetupVNCFactory)
                client.captureScreen(str(terminal))
                client.disconnect()
                client = None
                terminal_frame = Image.open(terminal)
                top = terminal_frame.crop((0, 0, terminal_frame.width, 40))
                stream = io.BytesIO()
                top.save(stream, format='PNG')
                menu = read_ocr(['stdin', 'stdout', '--psm', '7'], stream.getvalue())
                body = read_ocr([str(terminal), 'stdout', '--psm', '11'])
                if 'terminal' not in compact(menu) or classify(body) == 'dismiss_search' or not any(x in body.lower() for x in ('zsh', 'bash')):
                    raise RuntimeError('Terminal is not the foreground guest app; refusing shell input')
                client = api.connect(f'{endpoint.hostname}::{endpoint.port}', password=unquote(endpoint.password or ''), timeout=30, factory_class=SetupVNCFactory)
                client.keyPress('ctrl-c')
                time.sleep(0.3)
                type_text(client, '/bin/echo ' + base64.b64encode(BOOTSTRAP_COMMAND.encode()).decode('ascii') + ' | /usr/bin/base64 -D | /bin/bash')
                client.keyPress('enter')
                time.sleep(5)
                return
            time.sleep(6)
        finally:
            if client is not None:
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
