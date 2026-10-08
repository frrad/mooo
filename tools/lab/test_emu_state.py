import unittest
from emu_state import classify, permission_target


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

    def test_expired_session_beats_old_navigation(self):
        self.assertEqual(classify('<hierarchy><node resource-id="com.kakao.talk:id/example"/><node text="Friends"/><node text="Chats"/><node text="Your verification information has expired. Please log in with your Kakao Account."/></hierarchy>'), 'session-expired')

    def test_phone_entry_is_not_home(self):
        self.assertEqual(classify('<hierarchy><node text="Enter Your Phone Number"/></hierarchy>'), 'phone-verification-required')

    def test_password_manager_overlay(self):
        self.assertEqual(classify('<hierarchy><node text="Google Password Manager"/><node text="Enter Your Phone Number"/></hierarchy>'), 'password-manager')

    def test_photo_modal_beats_underlying_profile(self):
        self.assertEqual(classify('<hierarchy><node text="Sync Contacts"/><node text="Get Started"/><node text="Please add a profile photo."/></hierarchy>'), 'default-photo')

    def test_repeated_contact_denial(self):
        for suffix in ('permission_deny_button', 'permission_deny_and_dont_ask_again_button'):
            xml = '<hierarchy><node resource-id="com.android.permissioncontroller:id/permission_message" text="Allow KakaoTalk to access your contacts?"/><node resource-id="com.android.permissioncontroller:id/' + suffix + '" bounds="[0,0][100,100]"/></hierarchy>'
            self.assertEqual(permission_target(xml), (50, 50))

    def test_unknown_permission_stops(self):
        self.assertIsNone(permission_target('<hierarchy><node resource-id="com.android.permissioncontroller:id/permission_message" text="Allow location?"/><node resource-id="com.android.permissioncontroller:id/permission_allow_button" bounds="[0,0][100,100]"/></hierarchy>'))

    def test_chat_requires_navigation(self):
        self.assertEqual(classify('<hierarchy><node resource-id="com.kakao.talk:id/chat_room_root"/></hierarchy>'), 'chat-visible')

    def test_unknown_dialog_beats_home(self):
        self.assertEqual(classify('<hierarchy><node resource-id="com.kakao.talk:id/sliding_tabs"/><node text="Friends"/><node resource-id="com.kakao.talk:id/txt_message" text="Unrecognized alert"/></hierarchy>'), 'unknown')

    def test_verification_modal_beats_underlying_phone_form(self):
        self.assertEqual(classify('<hierarchy><node text="Enter Your Phone Number"/><node text="You will receive a verification code"/></hierarchy>'), 'verification-required')
        self.assertEqual(classify('<hierarchy><node text="Enter Your Phone Number"/><node text="A verification code cannot be sent to this number"/></hierarchy>'), 'verification-unavailable')

    def test_chat_text_does_not_trigger_verification(self):
        self.assertEqual(classify('<hierarchy><node resource-id="com.kakao.talk:id/chat_room_root"/><node text="Here is a verification code"/></hierarchy>'), 'chat-visible')
