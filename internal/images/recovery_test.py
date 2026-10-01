import unittest
from recovery import click_control, run_recovery, word


def screen(text):
    return text.lower(), [{'text': w, 'conf': '95'} for w in text.split()]


class RecoveryTransitions(unittest.TestCase):
    def test_menu_click_keeps_pointer_at_observed_control_until_release(self):
        events = []

        class Client:
            def mouseMove(self, x, y): events.append(('move', x, y))
            def mouseDown(self, button): events.append(('down', button))
            def mouseUp(self, button): events.append(('up', button))

        click_control(Client(), {'left': '442', 'top': '12', 'width': '87', 'height': '27'},
                      lambda duration: events.append(('pause', duration)))
        self.assertEqual(events, [('move', 485, 25), ('pause', 0.2), ('down', 1),
                                  ('pause', 0.1), ('up', 1), ('pause', 0.2)])

    def drive(self, overrides=None):
        states = {
            'boot-picker': 'Macintosh HD Options',
            'options-selected': 'Options Continue',
            'recovery-entry': 'English Deutsch',
            'recovery-ready': 'macOS Recovery Utilities',
            'recovery-user-selected': 'lume Next',
            'recovery-user-password': 'Enter password for lume',
            'recovery-menu': 'macOS Recovery Utilities',
            'utilities-menu': 'Terminal',
            'terminal-shell': '-bash-3.2#',
            'csrutil-confirmation': 'Do you want to continue? [y/n]',
            'administrator-prompt': 'Enter name of an authorized user:',
            'administrator-password': 'Password:',
            'sip-policy-committed': 'System Integrity Protection is off. Restart the machine for the changes to take effect.',
        }
        states.update(overrides or {})
        actions = []

        def wait(label, predicate, seconds=90):
            text, words = screen(states[label])
            if not predicate(text, words):
                raise RuntimeError(label)
            return text, words

        def click(words, label):
            self.assertIsNotNone(word(words, label))
            actions.append(('click', label))

        try:
            run_recovery(wait, click, lambda key: actions.append(('key', key)),
                         lambda text: actions.append(('type', text)), 'fixturepassword')
        except RuntimeError as e:
            return actions, str(e)
        return actions, None

    def test_success_requires_language_terminal_and_signed_commit(self):
        actions, failure = self.drive()
        self.assertIsNone(failure)
        self.assertEqual([v for k, v in actions if k == 'type'],
                         ['csrutil disable', 'y', 'lume', 'fixturepassword', 'shutdown -h now'])
        self.assertLess(actions.index(('click', 'English')), actions.index(('click', 'Terminal')))

    def test_older_recovery_can_skip_language(self):
        actions, failure = self.drive({'recovery-entry': 'macOS Recovery Utilities'})
        self.assertIsNone(failure)
        self.assertNotIn(('click', 'English'), actions)

    def test_older_recovery_volume_owner_authentication(self):
        actions, failure = self.drive({'recovery-ready': 'Select a user you know the password for lume'})
        self.assertIsNone(failure)
        self.assertIn(('click', 'lume'), actions)
        self.assertLess(actions.index(('type', 'fixturepassword')), actions.index(('type', 'csrutil disable')))

    def test_macos27_single_user_password_prompt(self):
        actions, failure = self.drive({'administrator-prompt': 'Enter password for user lume:'})
        self.assertIsNone(failure)
        self.assertEqual([v for k, v in actions if k == 'type'],
                         ['csrutil disable', 'y', 'fixturepassword', 'shutdown -h now'])

    def test_time_machine_screen_never_receives_terminal_commands(self):
        actions, failure = self.drive({'terminal-shell': 'Time Machine System Restore Erase destination disk'})
        self.assertEqual(failure, 'terminal-shell')
        self.assertFalse(any(k == 'type' for k, _ in actions))

    def test_no_password_without_explicit_auth_prompt(self):
        actions, failure = self.drive({'administrator-password': 'Authentication service unavailable'})
        self.assertEqual(failure, 'administrator-password')
        self.assertNotIn(('type', 'fixturepassword'), actions)

    def test_failed_policy_is_not_shut_down_as_success(self):
        actions, failure = self.drive({'sip-policy-committed': 'Failed to update security configuration'})
        self.assertEqual(failure, 'sip-policy-committed')
        self.assertNotIn(('type', 'shutdown -h now'), actions)

    def test_ocr_must_match_exact_control_with_confidence(self):
        self.assertIsNone(word([{'text': 'Terminally', 'conf': '99'}], 'Terminal'))
        self.assertIsNone(word([{'text': 'Terminal', 'conf': '12'}], 'Terminal'))


if __name__ == '__main__':
    unittest.main()
