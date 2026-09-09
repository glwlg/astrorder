import sqlite3
from astrorder.native_user_activity import read_user_activity


def test_reads_last_user_time_not_session_start_or_assistant_time(tmp_path):
    file = tmp_path / 'state.db'
    with sqlite3.connect(file) as db:
        db.execute('CREATE TABLE messages(id INTEGER PRIMARY KEY, session_id TEXT, role TEXT, timestamp REAL, content TEXT)')
        db.executemany('INSERT INTO messages VALUES (?,?,?,?,?)', [(1, 's', 'user', 10, 'private'), (2, 's', 'assistant', 20, 'private'), (3, 's', 'tool', 30, 'private'), (4, 'other', 'user', 50, 'private')])
    assert read_user_activity(file, ['s']) == [{'id': 's', 'last_user_at': '1970-01-01T00:00:10.000Z'}]
