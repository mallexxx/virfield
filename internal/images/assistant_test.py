import unittest
from assistant import classify


class CapturedScreenRegression(unittest.TestCase):
    def test_photographic_welcome_requires_exact_button_ocr(self):
        self.assertEqual(classify('', 'Get Started\n'), 'welcome')
        self.assertEqual(classify('', 'Started'), 'boot')

    def test_boot_frame_has_no_click_action(self):
        self.assertEqual(classify(''), 'boot')

    def test_macos27_age_title_missing_from_real_ocr(self):
        screen = '''i Select the age range of the person who will use this Mac.
This information will help Mac set up parental controls and safety features.
Child 12 or younger Teen 13 to 17 Adult 18 or older'''
        self.assertEqual(classify(screen), 'adult')

    def test_account_confirmation_precedes_background_title(self):
        self.assertEqual(classify('Sign In to Your Apple Account Are you sure you want to skip signing in with an Apple Account?'), 'confirm_account_skip')

    def test_unknown_screen_never_clicks(self):
        self.assertEqual(classify('The disk could not be mounted. Erase disk?'), 'unknown')

    def test_liquid_glass_uses_bottom_continue_not_center_welcome(self):
        screen = 'Liquid Glass Slide from clear to tinted to reveal content beneath or add more opacity. Continue Accessibility'
        self.assertEqual(classify(screen), 'continue')
        self.assertEqual(classify('Welcome to Mac Get Started'), 'welcome')

    def test_real_accessibility_screen(self):
        self.assertEqual(classify('Accessibility features adapt your Mac to your individual needs. Vision Motor Hearing Cognitive Not Now'), 'accessibility')


if __name__ == '__main__':
    unittest.main()
