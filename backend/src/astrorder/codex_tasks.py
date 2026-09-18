"""Project structured Codex runtime notifications into durable task records."""
import threading

from .schemas import TaskModel


def project_tasks(
    connection, method, params, now, *, history_before=None, protected=None, existing=None
):
    sid = params['threadId']
    if existing is None:
        existing = {
            task['id']: task
            for task in connection.store.list_tasks(connection.agent_id, sid)
        }

    def emit(task_id, kind, title, status, command=None, output=None):
        previous = existing.get(task_id, {})
        if history_before is not None:
            if task_id in protected or previous.get('updated_at', '') > history_before:
                return
            protected.add(task_id)
            # A recorded start is not proof that the process is still alive.
            if status in {'running', 'waiting_approval'} or (kind == 'subagent' and status == 'pending'):
                status = 'unknown'
        logs = previous.get('logs', [])
        if output is not None:
            logs = [{'id': task_id, 'text': output[-200_000:], 'level': 'info', 'created_at': now}]
        task = TaskModel(
            id=task_id, agent_id=connection.agent_id, session_id=sid,
            kind=kind, title=title[:512], status=status, command=command,
            logs=logs, created_at=previous.get('created_at', now), updated_at=now,
        ).model_dump()
        connection._event('task.upsert', sid, task)
        existing[task_id] = task

    if method == 'turn/plan/updated':
        plan = params.get('plan')
        statuses = {'pending': 'pending', 'inProgress': 'running', 'completed': 'completed'}
        if not isinstance(plan, list) or any(
            not isinstance(step, dict) or not isinstance(step.get('step'), str)
            or step.get('status') not in statuses for step in plan
        ):
            return
        plan_ids = {f'codex:todo:{index}' for index in range(len(plan))}
        for task in existing.values():
            if task['id'].startswith('codex:todo:') and task['id'] not in plan_ids:
                emit(task['id'], 'todo', task['title'], 'cancelled')
        for index, step in enumerate(plan):
            emit(f'codex:todo:{index}', 'todo', step['step'], statuses[step['status']])
    elif method in {'turn/goal/updated', 'goal/updated', 'goal/created'}:
        goal = params.get('goal') or params
        objective = goal.get('objective') or goal.get('title') or goal.get('text')
        if isinstance(objective, str) and objective.strip():
            goal_status = goal.get('status', 'running')
            status_map = {'pending': 'pending', 'running': 'running', 'active': 'running', 'in_progress': 'running', 'completed': 'completed', 'blocked': 'failed', 'failed': 'failed'}
            mapped_status = status_map.get(goal_status, 'running')
            emit('codex:goal:current', 'todo', f'目标: {objective.strip()}', mapped_status)
    elif method in {'item/started', 'item/completed'}:
        item = params.get('item')
        if not isinstance(item, dict) or not isinstance(item.get('id'), str):
            return
        if item.get('type') == 'plan' and history_before is not None:
            text = item.get('text')
            if isinstance(text, str) and text.strip():
                emit('codex:todo:0', 'todo', '执行计划', 'unknown', output=text)
        elif item.get('type') == 'collabAgentToolCall':
            states = item.get('agentsStates')
            if not isinstance(states, dict):
                return
            statuses = {'pendingInit': 'pending', 'running': 'running', 'interrupted': 'cancelled',
                        'completed': 'completed', 'errored': 'failed', 'shutdown': 'cancelled', 'notFound': 'unknown'}
            for child_id, state in states.items():
                if not isinstance(state, dict):
                    continue
                emit(f'codex:subagent:{child_id}', 'subagent', child_id,
                     statuses.get(state.get('status'), 'unknown'), output=state.get('message'))
        elif item.get('type') == 'subAgentActivity':
            child_id = item.get('agentThreadId')
            statuses = {'started': 'running', 'interacted': 'running', 'interrupted': 'cancelled', 'completed': 'completed'}
            if isinstance(child_id, str) and item.get('kind') in statuses:
                emit(f'codex:subagent:{child_id}', 'subagent', item.get('agentPath') or child_id,
                     statuses[item['kind']])
def project_task_history(connection, sid, entries, started, *, refresh_unknown=False):
    """Consume newest-first history without rolling back live or newer-page state."""
    with connection._lock:
        lock = connection._session_locks.setdefault(f'history:{sid}', threading.Lock())
    if not lock.acquire(blocking=False):
        return
    try:
        tasks = connection.store.list_tasks(connection.agent_id, sid)
        existing = {task['id']: task for task in tasks}
        protected = {
            task['id'] for task in tasks if not refresh_unknown or task['status'] != 'unknown'
        }
        # A plan is a snapshot: older snapshots must not restore removed steps.
        plan_seen = any(task['kind'] == 'todo' and task['id'] in protected for task in tasks)
        for entry in entries:
            item = entry.get('item')
            if not isinstance(item, dict):
                continue
            method = 'item/completed'
            params = {'threadId': sid, 'item': item}
            is_plan = item.get('type') == 'plan'
            if item.get('type') == 'dynamicToolCall' and item.get('tool') == 'update_plan':
                args = item.get('arguments')
                if item.get('success') is not True or not isinstance(args, dict) or not isinstance(args.get('plan'), list):
                    continue
                is_plan = True
                method = 'turn/plan/updated'
                params['plan'] = [
                    {**step, 'status': 'inProgress' if step.get('status') == 'in_progress' else step.get('status')}
                    if isinstance(step, dict) else step for step in args['plan']
                ]
            elif item.get('type') == 'dynamicToolCall' and item.get('tool') in {'create_goal', 'update_goal'}:
                args = item.get('arguments')
                if isinstance(args, dict):
                    obj = args.get('objective') or args.get('goal') or args.get('title')
                    if isinstance(obj, str) and obj.strip():
                        method = 'turn/goal/updated'
                        params['goal'] = {'objective': obj.strip(), 'status': args.get('status', 'running')}
                    elif item.get('tool') == 'update_goal' and args.get('status'):
                        method = 'turn/goal/updated'
                        params['goal'] = {'status': args['status']}
            if is_plan:
                if plan_seen:
                    continue
                plan_seen = True
            project_tasks(
                connection,
                method,
                params,
                started,
                history_before=started,
                protected=protected,
                existing=existing,
            )
    finally:
        lock.release()
