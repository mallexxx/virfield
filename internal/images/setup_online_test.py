import unittest
import base64
import json
from pathlib import Path
import zlib
import setup_online


class OnlineSetupTests(unittest.TestCase):
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
                                          ('language', (1693, 1262), setup_online.language_button)]:
            with self.subTest(name=name):
                f = Frame()
                rgb = zlib.decompress(base64.b64decode(fixtures[name]['pixels']))
                f.pixels = list(zip(rgb[0::3], rgb[1::3], rgb[2::3]))
                self.assertEqual(recognize(f), expected)
                self.assertEqual(f.asserted_box, tuple(fixtures[name]['box']))
                for color in [(0, 0, 0), (255, 255, 255), (68, 20, 174), (235, 235, 235)]:
                    f.pixels = [color] * len(f.pixels)
                    self.assertIsNone(recognize(f))
                f.size = (1920, 1080)
                self.assertIsNone(recognize(f))

    def test_known_screens_and_safe_unknown(self):
        for text, expected in [
            ('Select Your Country or Region United States Continue', 'region'),
            ('Sign In with Your Apple ID Set Up Later', 'apple_id'),
            ('Create a Computer Account Full name Account name Password Verify', 'create_account'),
            ('The account name is already in use', 'error'),
            ('I have read and agree to the terms Agree', 'confirm_terms'),
            ('Finder File Edit View Go Window Help', 'desktop'),
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
