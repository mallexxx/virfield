import unittest
import base64
import json
from pathlib import Path
import zlib
import setup_online
from PIL import Image, ImageDraw


class OnlineSetupTests(unittest.TestCase):
    def test_filled_button_is_separate_from_adjacent_blue_outline(self):
        frame = Image.new('RGB', (1920, 1440), (230, 230, 230))
        draw = ImageDraw.Draw(frame)
        draw.rectangle((726, 874, 957, 941), outline=(41, 99, 220), width=7)
        draw.rectangle((968, 874, 1187, 941), fill=(41, 99, 220))
        boxes = setup_online.blue_controls(frame)
        self.assertEqual(len(boxes), 1)
        self.assertGreaterEqual(boxes[0][0], 960)
        self.assertLessEqual(boxes[0][2], 1190)

    def test_keyboard_uses_supported_vnc_methods_and_us_shift_mapping(self):
        class Keyboard:
            def __init__(self):
                self.keys = []
            def keyPress(self, value):
                self.keys.append(value)
        keyboard = Keyboard()
        setup_online.type_text(keyboard, 'A|"$_;')
        self.assertEqual(keyboard.keys, ['shift-a', 'shift-\\', "shift-'", 'shift-4', 'shift-minus', ';'])
        with self.assertRaises(RuntimeError):
            setup_online.type_text(keyboard, '\n')

    def test_bootstrap_starts_sshd_after_enabling_it(self):
        self.assertIn('launchctl enable system/com.openssh.sshd', setup_online.BOOTSTRAP_COMMAND)
        self.assertIn('launchctl kickstart -k system/com.openssh.sshd', setup_online.BOOTSTRAP_COMMAND)

    def test_visible_native_arrows_and_false_positives(self):
        # RGB crops recorded from the real Monterey console, decoded without
        # requiring host imaging packages for the controller test suite.
        fixtures = json.loads((Path(__file__).parent / 'testdata/native-buttons.json').read_text())
        class Frame:
            size = (1920, 1440)
            def crop(self, box):
                self.asserted_box = box
                return self
            def convert(self, mode):
                return self
            def getdata(self):
                return self.pixels
        for name, expected, recognize in [('hello', (960, 1205), setup_online.hello_button),
                                          ('language', (1693, 1262), setup_online.language_button),
                                          ('language_grey', (1693, 1262), setup_online.language_button)]:
            with self.subTest(name=name):
                f = Frame()
                rgb = zlib.decompress(base64.b64decode(fixtures[name]['pixels']))
                f.pixels = list(zip(rgb[0::3], rgb[1::3], rgb[2::3]))
                self.assertEqual(recognize(f), expected)
                self.assertEqual(f.asserted_box, tuple(fixtures[name]['box']))
                for color in [(0, 0, 0), (255, 255, 255), (68, 20, 174), (235, 235, 235)]:
                    f.pixels = [color] * len(f.pixels)
                    self.assertIsNone(recognize(f))

    def test_ventura_hello_arrow_at_1080p(self):
        class Patch:
            def __init__(self, pixels):
                self.pixels = pixels
            def convert(self, mode):
                return self
            def getdata(self):
                return self.pixels
        class Frame:
            size = (1920, 1080)
            def __init__(self, pixels):
                self.pixels = pixels
            def crop(self, box):
                self.asserted_box = box
                return Patch(self.pixels)
        pixels = [(215, 72, 8)] * (80 * 80)
        for y in range(26, 55):
            for x in range(48, 54):
                pixels[y * 80 + x] = (250, 250, 250)
        for x in range(25, 55):
            pixels[40 * 80 + x] = (250, 250, 250)
        frame = Frame(pixels)
        self.assertEqual(setup_online.hello_button(frame), (960, 847))
        self.assertEqual(frame.asserted_box, (920, 807, 1000, 887))
        for color in [(0, 0, 0), (255, 255, 255), (215, 72, 8)]:
            self.assertIsNone(setup_online.hello_button(Frame([color] * (80 * 80))))

    def test_sleeping_display_requires_a_fully_dark_frame(self):
        self.assertTrue(setup_online.sleeping_display(Image.new('RGB', (8, 8), (0, 0, 0))))
        self.assertFalse(setup_online.sleeping_display(Image.new('RGB', (8, 8), (0, 0, 3))))
        self.assertFalse(setup_online.sleeping_display(Image.new('RGB', (8, 8), (20, 20, 20))))

    def test_monterey_login_password_field_without_ocr(self):
        frame = Image.new('RGB', (1920, 1440), (70, 20, 140))
        draw = ImageDraw.Draw(frame)
        draw.rounded_rectangle((800, 874, 1120, 934), radius=30, fill=(188, 125, 205))
        self.assertEqual(setup_online.login_password_field(frame), (960, 904))
        frame = Image.new('RGB', (1920, 1440), (70, 20, 140))
        draw = ImageDraw.Draw(frame)
        draw.rounded_rectangle((800, 820, 1120, 870), radius=25, fill=(188, 125, 205))
        draw.rounded_rectangle((800, 900, 1120, 950), radius=25, fill=(188, 125, 205))
        self.assertIsNone(setup_online.login_password_field(frame))

    def test_known_screens_and_safe_unknown(self):
        for text, expected in [
            ('Select Your Country or Region United States Continue', 'region'),
            ('Sign In with Your Apple ID Set Up Later', 'apple_id'),
            ('Create a Computer Account Full name Account name Password Verify', 'create_account'),
            ('The account name is already in use', 'error'),
            ('I have read and agree to the terms Agree', 'confirm_terms'),
            ('Finder File Edit View Go Window Help', 'desktop'),
            ('Terminal Shell Edit View Window Help', 'desktop'),
            ('lume Enter Password', 'login'),
            ('Select Your Time Zone Closest City Cupertino Continue', 'continue'),
            ('A dialog never seen before Continue', 'unknown'),
            ('Creating your account', 'busy'),
            ('Create a Computer Account Full name Creating account...', 'busy'),
        ]:
            with self.subTest(text=text):
                self.assertEqual(setup_online.classify(text), expected)

    def test_anchors_do_not_span_unrelated_rows(self):
        words = [dict(text='Set', left=10, top=10, width=20, height=12),
                 dict(text='Up', left=34, top=10, width=18, height=12),
                 dict(text='Later', left=56, top=10, width=30, height=12)]
        self.assertEqual(len(setup_online.find(words, 'Set Up Later')), 1)
        words[-1]['top'] = 80
        self.assertEqual(setup_online.find(words, 'Set Up Later'), [])
        self.assertEqual(setup_online.find(words, 'Continue'), [])


if __name__ == '__main__':
    unittest.main()
