import sqlite3
import time
from astrorder.native.user_activity import presence, read_user_activity


def test_reads_last_user_time_not_session_start_or_assistant_time(tmp_path):
    file = tmp_path / 'state.db'
    with sqlite3.connect(file) as db:
        db.execute('CREATE TABLE messages(id INTEGER PRIMARY KEY, session_id TEXT, role TEXT, timestamp REAL, content TEXT)')
        db.executemany('INSERT INTO messages VALUES (?,?,?,?,?)', [(1, 's', 'user', 10, 'private'), (2, 's', 'assistant', 20, 'private'), (3, 's', 'tool', 30, 'private'), (4, 'other', 'user', 50, 'private')])
    assert read_user_activity(file, ['s']) == [{'id': 's', 'last_user_at': '1970-01-01T00:00:10.000Z'}]


def test_presence_marks_open_and_recently_active_sessions(tmp_path):
    file = tmp_path / 'state.db'
    now = time.time()
    with sqlite3.connect(file) as db:
        db.execute('CREATE TABLE sessions(id TEXT, ended_at REAL, archived INTEGER, hidden INTEGER, last_activity_at REAL)')
        db.execute('CREATE TABLE messages(id INTEGER PRIMARY KEY, session_id TEXT, role TEXT, timestamp REAL)')
        db.executemany('INSERT INTO sessions VALUES (?,?,?,?,?)', [
            ('live', None, 0, 0, now),
            ('idle-open', None, 0, 0, now - 400),
            ('closed', 20, 0, 0, 10),
            ('continued', now - 400, 0, 0, now - 200),
            ('hidden', None, 0, 1, now),
        ])
        db.execute("INSERT INTO messages VALUES (1,'live','user',?)", (now,))
    data = presence(str(file), ['live', 'idle-open', 'closed', 'continued', 'hidden'])
    assert [row['id'] for row in data['items']] == ['live']
    assert set(data['open_ids']) == {'live', 'idle-open', 'continued'}
    assert data['live_ids'] == ['live']
