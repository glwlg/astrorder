import importlib.util
import tempfile
import unittest
from pathlib import Path

MODULE_PATH = Path(__file__).with_name('orchestrate.py')


class OrchestrationTests(unittest.TestCase):
    def load(self):
        spec = importlib.util.spec_from_file_location('orchestrate', MODULE_PATH)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        return module

    def test_model_effort_and_new_named_sessions(self):
        module = self.load()
        for role, expected in [('frontend', 'gpt-5.6-luna'), ('backend', 'gpt-5.6-luna'), ('integration', 'gpt-5.6-terra')]:
            command = module.build_command('hermes', Path('project'), role, 'unique-run')
            self.assertEqual(command[command.index('--model') + 1], expected)
            self.assertEqual(command[command.index('--reasoning') + 1], 'max')
            self.assertIn('--create-if-missing', command)
            self.assertNotIn('--resume', command)
            self.assertIn('unique-run', command[command.index('--continue') + 1])

    def test_integration_waits_for_both_successful_exits_and_reports(self):
        module = self.load()
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            reports = root / 'docs' / 'reports'
            reports.mkdir(parents=True)
            codes = {'frontend': 0}
            self.assertFalse(module.ready_for_integration(root, codes))
            codes['backend'] = 0
            self.assertFalse(module.ready_for_integration(root, codes))
            (reports / 'frontend.md').write_text('handoff', encoding='utf-8')
            (reports / 'backend.md').write_text('handoff', encoding='utf-8')
            self.assertTrue(module.ready_for_integration(root, codes))
            codes['backend'] = 1
            self.assertFalse(module.ready_for_integration(root, codes))

    def test_resume_keeps_exact_session_without_title_lookup(self):
        module = self.load()
        command = module.build_command('hermes', Path('project'), 'backend', 'run', 'original-session')
        self.assertEqual(command[command.index('--resume') + 1], 'original-session')
        self.assertNotIn('--continue', command)
        self.assertNotIn('--create-if-missing', command)

    def test_tool_directory_overrides_inherited_home_without_global_changes(self):
        module = self.load()
        inherited = {'TERMINAL_CWD': 'C:/Users/luwei', 'KEEP_ME': 'yes'}
        root = Path(__file__).resolve().parents[1]
        result = module.worker_environment(root, inherited)
        self.assertEqual(result['TERMINAL_CWD'], str(root))
        self.assertEqual(result['KEEP_ME'], 'yes')
        self.assertEqual(inherited['TERMINAL_CWD'], 'C:/Users/luwei')


if __name__ == '__main__':
    unittest.main()
