from astrorder.system_environment import merge_system_environment


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
