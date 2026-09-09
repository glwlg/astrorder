"""Read native user activity metadata only; never load message bodies."""
import json
import sqlite3
import sys
from datetime import UTC, datetime
from pathlib import Path


def read_user_activity(database, session_ids):
    result = []
    with sqlite3.connect(Path(database).resolve().as_uri() + '?mode=ro', uri=True, timeout=3) as db:
        db.execute('PRAGMA query_only=ON')
        for sid in session_ids:
            row = db.execute("SELECT timestamp FROM messages WHERE session_id=? AND role='user' ORDER BY id DESC LIMIT 1", (sid,)).fetchone()
            if row and isinstance(row[0], (int, float)):
                result.append({'id': sid, 'last_user_at': datetime.fromtimestamp(row[0], UTC).isoformat(timespec='milliseconds').replace('+00:00', 'Z')})
    return result


if __name__ == '__main__':
    payload = json.load(sys.stdin)
    print(json.dumps({'items': read_user_activity(payload['database'], payload['session_ids'])}))
