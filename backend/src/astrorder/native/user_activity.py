"""Read native user activity metadata only; never load message bodies."""
import json
import sqlite3
import sys
from datetime import UTC, datetime
from pathlib import Path

LIVE_WINDOW_SECONDS = 180


def _stamp(value):
    if isinstance(value, (int, float)) and value > 0:
        return datetime.fromtimestamp(value, UTC).isoformat(timespec='milliseconds').replace('+00:00', 'Z')
    if isinstance(value, str) and value.strip():
        return value.strip()
    return None


def _chunk(values, size=400):
    values = [item for item in values if isinstance(item, str) and item]
    for index in range(0, len(values), size):
        yield values[index:index + size]


def read_user_activity(database, session_ids):
    result = []
    path = Path(database)
    if not path.is_file():
        return result
    with sqlite3.connect(path.resolve().as_uri() + '?mode=ro', uri=True, timeout=3) as db:
        db.execute('PRAGMA query_only=ON')
        columns = {row[1] for row in db.execute('PRAGMA table_info(messages)')}
        if not {'session_id', 'role', 'timestamp'}.issubset(columns):
            return result
        for batch in _chunk(session_ids):
            marks = ','.join('?' for _ in batch)
            rows = db.execute(
                f"SELECT session_id, MAX(timestamp) FROM messages WHERE role='user' AND session_id IN ({marks}) GROUP BY session_id",
                batch,
            )
            for sid, timestamp in rows:
                stamp = _stamp(timestamp)
                if isinstance(sid, str) and stamp:
                    result.append({'id': sid, 'last_user_at': stamp})
    return result


def read_open_ids(database, now=None):
    path = Path(database)
    if not path.is_file():
        return []
    now = datetime.now(UTC).timestamp() if now is None else now
    recent = now - 1800
    with sqlite3.connect(path.resolve().as_uri() + '?mode=ro', uri=True, timeout=3) as db:
        db.execute('PRAGMA query_only=ON')
        columns = {row[1] for row in db.execute('PRAGMA table_info(sessions)')}
        if 'id' not in columns:
            return []
        where = []
        params: list = []
        if 'archived' in columns:
            where.append('COALESCE(archived, 0) = 0')
        if 'hidden' in columns:
            where.append('COALESCE(hidden, 0) = 0')
        activity = []
        if 'last_activity_at' in columns:
            activity.append('last_activity_at >= ?')
            params.append(recent)
        elif 'ended_at' in columns:
            activity.append('ended_at IS NULL')
        if activity:
            where.append('(' + ' OR '.join(activity) + ')')
        sql = 'SELECT id FROM sessions' + ((' WHERE ' + ' AND '.join(where)) if where else '')
        return [row[0] for row in db.execute(sql, params) if isinstance(row[0], str) and row[0]]


def read_live_ids(database, now=None):
    path = Path(database)
    if not path.is_file():
        return []
    now = datetime.now(UTC).timestamp() if now is None else now
    floor = now - LIVE_WINDOW_SECONDS
    with sqlite3.connect(path.resolve().as_uri() + '?mode=ro', uri=True, timeout=3) as db:
        db.execute('PRAGMA query_only=ON')
        columns = {row[1] for row in db.execute('PRAGMA table_info(sessions)')}
        if 'id' not in columns or 'last_activity_at' not in columns:
            return []
        where = ['last_activity_at >= ?']
        params = [floor]
        if 'ended_at' in columns:
            where.append('ended_at IS NULL')
        if 'archived' in columns:
            where.append('COALESCE(archived, 0) = 0')
        if 'hidden' in columns:
            where.append('COALESCE(hidden, 0) = 0')
        return [row[0] for row in db.execute('SELECT id FROM sessions WHERE ' + ' AND '.join(where), params) if isinstance(row[0], str) and row[0]]


def presence(database, session_ids):
    return {
        'items': read_user_activity(database, session_ids),
        'open_ids': read_open_ids(database),
        'live_ids': read_live_ids(database),
    }


if __name__ == '__main__':
    payload = json.load(sys.stdin)
    print(json.dumps(presence(payload['database'], payload.get('session_ids') or [])))
