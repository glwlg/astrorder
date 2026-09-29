from astrorder.core.system_environment import merge_system_environment, remote_codex_environment


def test_merge_system_environment_preserves_every_system_user_and_process_variable():
    environment = merge_system_environment(
        process_environment={'PROCESS_ONLY': 'process', 'SHARED': 'process'},
        machine_environment={'MACHINE_ONLY': 'machine', 'SHARED': 'machine'},
        user_environment={'USER_ONLY': 'user', 'SHARED': 'user'},
    )

    assert environment['MACHINE_ONLY'] == 'machine'
    assert environment['USER_ONLY'] == 'user'
    assert environment['PROCESS_ONLY'] == 'process'
    assert environment['SHARED'] == 'process'


def test_remote_codex_environment_excludes_windows_paths_and_cache_settings():
    assert remote_codex_environment({
        'TEMP': r'C:\Users\luwei\AppData\Local\Temp',
        'UV_CACHE_DIR': r'P:\workspace\env\uv\cache',
        'HERMES_HOME': r'C:\Users\luwei\AppData\Local\hermes',
        'opencodex_api_auth_token': 'gateway-secret',
    }) == {'OPENCODEX_API_AUTH_TOKEN': 'gateway-secret'}
