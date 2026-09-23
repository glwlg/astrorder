"""One-way migration of obsolete Astrorder session aliases to native IDs.

No native runtime is contacted. Identity comes only from source_session_id;
never infer it from a title, path, timestamp, or a hash prefix.
"""
from sqlalchemy import select

from astrorder.models import CommandRow, EventRow, MessageRow, SessionRow, TaskRow


def migrate_native_identity(db):
    legacy = db.scalars(select(SessionRow).where(
        SessionRow.id.like('history-%'),
        SessionRow.source_session_id.is_not(None),
        SessionRow.source_session_id != SessionRow.id,
        SessionRow.source_session_id != '',
    )).all()
    for old in legacy:
        native_id = old.source_session_id
        target = db.scalar(select(SessionRow).where(
            SessionRow.agent_id == old.agent_id, SessionRow.id == native_id))
        for model in (MessageRow, CommandRow, TaskRow):
            for child in db.scalars(select(model).where(
                model.agent_id == old.agent_id, model.session_id == old.id)).all():
                duplicate = db.scalar(select(model).where(
                    model.agent_id == old.agent_id, model.session_id == native_id,
                    model.id == child.id))
                if duplicate is not None:
                    # Refuse destructive conflict resolution. The transaction rolls back.
                    fields = [c.name for c in model.__table__.columns
                              if c.name not in ('row_id', 'session_id')]
                    if any(getattr(child, name) != getattr(duplicate, name) for name in fields):
                        raise ValueError(f'Native identity migration conflict in {model.__tablename__}')
                    db.delete(child)
                else:
                    child.session_id = native_id
                    if model is CommandRow:
                        from astrorder.store import _hash_payload
                        child.payload_hash = _hash_payload(dict(
                            id=child.id, agent_id=child.agent_id, session_id=native_id,
                            action=child.action, text=child.text,
                            attachment_ids=[item['id'] for item in child.attachments],
                            target_id=child.target_id,
                        ))
        for event in db.scalars(select(EventRow).where(
            EventRow.agent_id == old.agent_id, EventRow.session_id == old.id)).all():
            event.session_id = native_id
            data = dict(event.data)
            if data.get('session_id') == old.id:
                data['session_id'] = native_id
            if event.type.startswith('session.') and data.get('id') == old.id:
                data['id'] = native_id
            event.data = data
        if target is None:
            old.id = native_id
            old.control_state = 'native'
        else:
            if not target.title or target.title == native_id:
                target.title = old.title
            for field in ('project_id', 'project_name', 'workspace'):
                if getattr(target, field) is None:
                    setattr(target, field, getattr(old, field))
            target.control_state = 'native'
            db.delete(old)
        db.flush()
