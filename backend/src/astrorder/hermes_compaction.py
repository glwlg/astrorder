"""Translate native Hermes compaction lifecycle events without inferring success."""
from uuid import uuid4

from .timeutil import utc_now


def compaction_update(params, active):
    payload = params.get('payload')
    if not isinstance(payload, dict):
        return None
    event, kind = params.get('type'), payload.get('kind')
    if event == 'status.update' and kind in {'compacting', 'compressing'}:
        state = 'running'
    elif event == 'status.update' and kind == 'compacted':
        state = 'completed'
    elif active and event == 'error':
        state = 'failed'
    elif active and event == 'message.complete':
        state = 'unknown'
    else:
        return None
    return {
        'id': active['id'] if active else f'hermes-compaction-{uuid4()}',
        'created_at': active['created_at'] if active else utc_now().isoformat(),
        'state': state,
    }
