"""Persist compaction progress through the existing replayable message stream."""
from astrorder.daemon.bridge import DaemonBridgeError


class HermesCompactionFrameRouter:
    def __init__(self, bridge, store, service):
        self.store, self.service = store, service
        self.close = bridge.register_native_frame_handler('hermes.compaction', self.project)

    def project(self, session_id, payload):
        labels = {
            'running': '正在压缩上下文', 'completed': '上下文压缩完成',
            'failed': '上下文压缩失败', 'unknown': '上下文压缩状态未确认',
        }
        agent_id = payload.get('agent_id')
        if (payload.get('session_id') != session_id or payload.get('state') not in labels
                or not isinstance(payload.get('id'), str)
                or not isinstance(agent_id, str)
                or self.store.get_session(agent_id, session_id) is None):
            raise DaemonBridgeError('Invalid Hermes compaction identity or state')
        message = self.store.upsert_message({
            'id': payload['id'], 'agent_id': agent_id, 'session_id': session_id,
            'role': 'system', 'kind': 'message', 'text': labels[payload['state']],
            'created_at': payload['created_at'], 'attachments': [],
        })
        self.service._server_event('message.upsert', agent_id=agent_id,
                                   session_id=session_id, data=message)
