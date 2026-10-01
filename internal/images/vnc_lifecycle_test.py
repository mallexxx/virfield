import types
import unittest
from unittest.mock import Mock, patch
import assistant
import recovery


class VNCShutdown(unittest.TestCase):
    def test_both_entrypoints_stop_runtime_on_success_and_failure(self):
        for script in (assistant, recovery):
            for error in (None, RuntimeError('unknown guest screen')):
                with self.subTest(script=script.__name__, failure=bool(error)):
                    shutdown = Mock()
                    vnc = types.SimpleNamespace(api=types.SimpleNamespace(shutdown=shutdown))
                    with patch.dict('sys.modules', {'vncdotool': vnc}), patch.object(script, 'run', side_effect=error):
                        if error:
                            with self.assertRaises(RuntimeError):
                                script.main()
                        else:
                            script.main()
                    shutdown.assert_called_once_with()
