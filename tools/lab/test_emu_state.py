import unittest
from emu_state import classify


class StateTests(unittest.TestCase):
    def test_login_is_not_home(self):
        self.assertEqual(classify('<hierarchy><node resource-id="com.kakao.talk:id/login_kakao_account"/><node text="Friends"/><node text="Chats"/></hierarchy>'), 'login-form')

    def test_unknown_is_not_authenticated(self):
        self.assertEqual(classify('<hierarchy><node text="Friends"/></hierarchy>'), 'unknown')

    def test_navigation_hint_requires_app_nodes(self):
        self.assertEqual(classify('<hierarchy><node text="Friends"/><node text="Chats"/></hierarchy>'), 'unknown')
        self.assertEqual(classify('<hierarchy><node resource-id="com.kakao.talk:id/example" text="Friends"/><node text="Chats"/></hierarchy>'), 'home-visible')

    def test_refused_sms(self):
        self.assertEqual(classify('<hierarchy><node text="A verification code cannot be sent to this number"/></hierarchy>'), 'verification-unavailable')

    def test_field_values_do_not_drive_state(self):
        self.assertEqual(classify('<hierarchy><node class="android.widget.EditText" text="verification code"/></hierarchy>'), 'unknown')
