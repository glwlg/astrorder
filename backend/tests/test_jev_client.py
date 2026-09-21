from __future__ import annotations
import pytest
from astrorder.config import Settings
from astrorder.jev_client import get_jev_key, set_jev_key, evaluate_blackboard_component

def test_jev_key_storage(tmp_path):
    from astrorder.store import Store
    settings = Settings(database_url=f'sqlite:///{tmp_path}/test_jev.sqlite3')
    store = Store(settings)
    assert get_jev_key(store) is None
    test_key = 'apikey_test_12345'
    set_jev_key(store, test_key)
    assert get_jev_key(store) == test_key
    set_jev_key(store, None)
    assert get_jev_key(store) is None
