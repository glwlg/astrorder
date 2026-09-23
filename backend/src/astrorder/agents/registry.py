"""Current connection registry, excluding empty retired Agent projections."""
def current_agents(store):
    connections={row['id'] for row in store.list_ssh_connections()}
    used={row['agent_id'] for row in store.list_sessions()}
    return [a for a in store.list_agents() if not (a.get('connection_id') and a['connection_id'] not in connections and a.get('status')=='disconnected' and a['id'] not in used)]
