import sys
sys.path.extend(['backend/src', 'connectors/codex'])
from astrorder.store import Store
from astrorder.config import Settings
from astrorder.service import ControlService
from astrorder.events import EventHub
from astrorder.native_codex import CodexConnection
import json

settings = Settings(database_url='sqlite:///astrorder.sqlite3')
store = Store(settings)
service = ControlService(store, EventHub(), settings)
conn = CodexConnection(settings, store, service)
sid = '01a0a844-996d-73a2-bcea-e8e0858923fa'
conn.connect()
items_res = conn._request('thread/items/list', {'threadId': sid, 'limit': 20, 'sortDirection': 'desc'})
data = items_res.get('data', [])
print("RAW DATA LENGTH:", len(data))
if data:
    print("FIRST ITEM KEYS:", list(data[0].keys()))
    print("FIRST ITEM:", json.dumps(data[0], indent=2))
conn.disconnect()
