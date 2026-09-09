"""Bounded, read-only native transcript reader. Stdlib-only for SSH stdin execution."""
import base64
import json
import sqlite3
from pathlib import Path


def _lineage(db, session_id):
    columns = {row[1] for row in db.execute('PRAGMA table_info(sessions)')}
    if not db.execute('SELECT 1 FROM sessions WHERE id = ?', (session_id,)).fetchone():
        raise ValueError('Native session does not exist')
    chain = [session_id]
    if not {'parent_session_id', 'end_reason', 'model_config', 'source', 'started_at', 'ended_at'}.issubset(columns):
        return chain
    # Follow only explicit native compression lineage, never sibling branches/delegates.
    fork = "json_extract(COALESCE(child.model_config, '{}'), '$._branched_from') IS NULL AND json_extract(COALESCE(child.model_config, '{}'), '$._delegate_from') IS NULL AND json_extract(COALESCE(child.model_config, '{}'), '$._reset_from') IS NULL AND COALESCE(child.source, '') != 'tool'"
    current = session_id
    for _ in range(64):
        row = db.execute(f"SELECT parent.id FROM sessions child JOIN sessions parent ON child.parent_session_id = parent.id WHERE child.id = ? AND parent.end_reason = 'compression' AND {fork}", (current,)).fetchone()
        if not row or row[0] in chain:
            break
        current = row[0]
        chain.insert(0, current)
    current = session_id
    for _ in range(64):
        row = db.execute(f"SELECT child.id FROM sessions parent JOIN sessions child ON child.parent_session_id = parent.id WHERE parent.id = ? AND parent.end_reason = 'compression' AND {fork} ORDER BY CASE WHEN child.end_reason = 'compression' THEN 0 WHEN child.ended_at IS NULL THEN 1 ELSE 2 END, child.started_at DESC, child.id DESC LIMIT 1", (current,)).fetchone()
        if not row or row[0] in chain:
            break
        current = row[0]
        chain.append(current)
    return chain


def read_native_page(database, session_id, source_id, before=None, limit=2):
    if not isinstance(limit, int) or not 1 <= limit <= 200:
        raise ValueError('Invalid page size')
    boundary = None
    if before is not None:
        try:
            if not before.startswith('native:'):
                raise ValueError('Invalid native cursor')
            token = before[7:]
            cursor = json.loads(base64.urlsafe_b64decode(token + '=' * (-len(token) % 4)))
            if cursor['session'] != session_id or cursor['source'] != source_id:
                raise ValueError('Foreign native cursor')
            boundary = cursor['before']
            if not isinstance(boundary, int) or boundary <= 0:
                raise ValueError('Invalid native boundary')
        except (KeyError, TypeError, json.JSONDecodeError) as exc:
            raise ValueError('Invalid native cursor') from exc
    path = Path(database).resolve()
    db = sqlite3.connect(path.as_uri() + '?mode=ro', uri=True, timeout=3)
    db.row_factory = sqlite3.Row
    try:
        db.execute('PRAGMA query_only=ON')
        ids = _lineage(db, session_id)
        columns = {row[1] for row in db.execute('PRAGMA table_info(messages)')}
        selected = [name for name in ('id', 'session_id', 'role', 'content', 'timestamp', 'tool_call_id', 'tool_calls', 'tool_name', 'reasoning', 'reasoning_content', 'reasoning_details', 'codex_reasoning_items', 'display_kind') if name in columns]
        if not {'id', 'session_id', 'role', 'content'}.issubset(columns):
            raise ValueError('Unsupported native message schema')
        where = 'session_id IN (' + ','.join('?' for _ in ids) + ')'
        params = list(ids)
        if boundary is not None:
            if not db.execute('SELECT id FROM messages WHERE ' + where + ' AND id=?', [*params, boundary]).fetchone():
                raise ValueError('Native cursor anchor is outside this session')
            where += ' AND id < ?'
            params.append(boundary)
        if 'active' in columns:
            where += ' AND (active=1 OR compacted=1)' if 'compacted' in columns else ' AND active=1'
        if 'display_kind' in columns:
            where += " AND COALESCE(display_kind, '') != 'hidden'"
        rows = db.execute('SELECT ' + ','.join(selected) + ' FROM messages WHERE ' + where + ' ORDER BY id DESC LIMIT ?', [*params, limit + 1]).fetchall()
        has_more = len(rows) > limit
        items = [dict(row) for row in reversed(rows[:limit])]
        cursor = None
        if has_more and items:
            value = {'session': session_id, 'source': source_id, 'before': items[0]['id']}
            cursor = 'native:' + base64.urlsafe_b64encode(json.dumps(value, separators=(',', ':')).encode()).decode().rstrip('=')
        return {'items': items, 'next_cursor': cursor}
    finally:
        db.close()


if __name__ == '__main__':
    import sys
    try:
        payload = json.loads(sys.stdin.readline())
        page = read_native_page(payload['database'], payload['session_id'], payload['source_id'], payload.get('before'), payload.get('limit', 2))
        print(json.dumps({'ok': True, **page}, ensure_ascii=False))
    except Exception:
        print(json.dumps({'ok': False, 'error': 'Native paged read failed'}))
        raise SystemExit(1)
