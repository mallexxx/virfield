import unittest
import setup_online


class OnlineSetupTests(unittest.TestCase):
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
