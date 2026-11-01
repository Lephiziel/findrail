import io
import pathlib
import sys
import unittest
import urllib.error
import urllib.request
from unittest.mock import patch

sys.path.insert(0, str(pathlib.Path(__file__).parent))
import smoke


class RejectedRequestTests(unittest.TestCase):
    def setUp(self):
        self.request = urllib.request.Request('http://127.0.0.1:1/api/v1/sources',
                                             data=b'{}', method='POST')

    def test_windows_reset_still_requires_actual_http_error(self):
        rejection = urllib.error.HTTPError(self.request.full_url, 403, 'Forbidden', {}, io.BytesIO(b'{}'))
        try:
            with patch.object(smoke.sys, 'platform', 'win32'), \
                 patch.object(smoke.time, 'sleep'), \
                 patch.object(smoke.urllib.request, 'urlopen', side_effect=[ConnectionResetError(), rejection]) as open_request:
                with self.assertRaises(urllib.error.HTTPError) as caught:
                    smoke.rejected_request(self.request, timeout=4)
                self.assertIs(caught.exception, rejection)
                self.assertEqual(caught.exception.code, 403)
                self.assertEqual(open_request.call_count, 2)
                open_request.assert_called_with(self.request, timeout=4)
        finally:
            rejection.close()

    def test_persistent_windows_reset_fails_with_a_finite_budget(self):
        with patch.object(smoke.sys, 'platform', 'win32'), \
             patch.object(smoke.time, 'sleep'), \
             patch.object(smoke.urllib.request, 'urlopen', side_effect=ConnectionResetError()) as open_request:
            with self.assertRaises(ConnectionResetError):
                smoke.rejected_request(self.request, timeout=4)
            self.assertEqual(open_request.call_count, 5)

    def test_non_windows_reset_is_not_retried(self):
        with patch.object(smoke.sys, 'platform', 'linux'), \
             patch.object(smoke.urllib.request, 'urlopen', side_effect=ConnectionResetError()) as open_request:
            with self.assertRaises(ConnectionResetError):
                smoke.rejected_request(self.request, timeout=4)
            self.assertEqual(open_request.call_count, 1)

    def test_other_transport_failure_is_not_retried(self):
        with patch.object(smoke.sys, 'platform', 'win32'), \
             patch.object(smoke.urllib.request, 'urlopen', side_effect=TimeoutError()) as open_request:
            with self.assertRaises(TimeoutError):
                smoke.rejected_request(self.request, timeout=4)
            self.assertEqual(open_request.call_count, 1)

    def test_http_success_is_left_for_the_callers_rejection_assertion(self):
        response = object()
        with patch.object(smoke.sys, 'platform', 'win32'), \
             patch.object(smoke.urllib.request, 'urlopen', return_value=response) as open_request:
            self.assertIs(smoke.rejected_request(self.request, timeout=4), response)
            self.assertEqual(open_request.call_count, 1)


if __name__ == '__main__':
    unittest.main()
