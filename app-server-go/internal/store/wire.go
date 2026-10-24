package store

import "fmt"

func agentWire(row map[string]any) (map[string]any, error) {
	if row == nil {
		return nil, nil
	}
	result := copyMap(row)
	delete(result, "updated_at")
	if result["source_id"] == nil || result["source_id"] == "" {
		result["source_id"] = result["id"]
	}
	if result["runtime_id"] == nil || result["runtime_id"] == "" {
		result["runtime_id"] = result["id"]
	}
	return result, nil
}

func sessionWire(row map[string]any) (map[string]any, error) {
	if row == nil {
		return nil, nil
	}
	result := copyMap(row)
	delete(result, "row_id")
	stamp, err := wireTime(result["updated_at"])
	if err != nil {
		return nil, err
	}
	result["updated_at"] = stamp
	result["ephemeral"], err = boolValue(result["ephemeral"], false)
	if err != nil {
		return nil, err
	}
	result["handoff_context_consumed"], err = boolValue(result["handoff_context_consumed"], false)
	if err != nil {
		return nil, err
	}
	if result["source_id"] == nil || result["source_id"] == "" {
		result["source_id"] = result["agent_id"]
	}
	if result["source_session_id"] == nil || result["source_session_id"] == "" {
		result["source_session_id"] = result["id"]
	}
	result["parent_session_id"] = result["handoff_from_session_id"]
	result["parent_agent_id"] = result["handoff_from_agent_id"]
	if result["delegation_parent_session_id"] == "" {
		result["delegation_parent_session_id"] = nil
	}
	if result["delegation_parent_agent_id"] == "" {
		result["delegation_parent_agent_id"] = nil
	}
	return result, nil
}

func projectWire(row map[string]any) (map[string]any, error) {
	return timestampsWire(row, "updated_at")
}

func attachmentWire(row map[string]any, summary bool) (map[string]any, error) {
	if row == nil {
		return nil, nil
	}
	if summary {
		id := fmt.Sprint(row["id"])
		return map[string]any{
			"id": id, "name": row["name"], "media_type": row["media_type"],
			"url": "/api/v1/attachments/" + id,
		}, nil
	}
	return timestampsWire(row, "created_at")
}

func messageWire(row map[string]any) (map[string]any, error) {
	if row == nil {
		return nil, nil
	}
	result, err := timestampsWire(row, "created_at")
	if result != nil {
		delete(result, "row_id")
	}
	return result, err
}

func commandWire(row map[string]any) (map[string]any, error) {
	if row == nil {
		return nil, nil
	}
	result, err := timestampsWire(row, "created_at")
	if result != nil {
		delete(result, "row_id")
		delete(result, "updated_at")
		delete(result, "payload_hash")
	}
	return result, err
}

func taskWire(row map[string]any) (map[string]any, error) {
	if row == nil {
		return nil, nil
	}
	result, err := timestampsWire(row, "created_at", "updated_at")
	if result != nil {
		delete(result, "row_id")
	}
	return result, err
}

func sshWire(row map[string]any) (map[string]any, error) {
	if row == nil {
		return nil, nil
	}
	display := row["display_name"]
	if display == nil || display == "" {
		for _, key := range []string{"ssh_config_alias", "host", "id"} {
			if row[key] != nil && row[key] != "" {
				display = row[key]
				break
			}
		}
	}
	profile := row["profile_name"]
	if profile == nil || profile == "" {
		profile = "default"
	}
	return map[string]any{
		"id": row["id"], "display_name": display, "profile_name": profile,
		"state": row["state"], "detail": row["detail"], "remote_os": row["remote_os"],
		"agent_id": row["agent_id"], "runtime_id": row["runtime_id"],
		"settings": map[string]any{
			"connection_id": row["id"], "display_name": display, "profile_name": profile,
			"host": row["host"], "port": row["port"], "user": row["user"],
			"ssh_config_alias": row["ssh_config_alias"], "identity_file": row["identity_file"],
			"hermes_path": row["hermes_path"], "workspace": row["workspace"],
		},
	}, nil
}

func historyWire(row map[string]any) (map[string]any, error) {
	if row == nil {
		return nil, nil
	}
	result, err := timestampsWire(row, "created_at")
	if result != nil {
		delete(result, "row_id")
		result["details"] = redactStructured(result["details"], 0)
	}
	return result, err
}

func reviewBindingWire(row map[string]any) (map[string]any, error) {
	if row == nil {
		return nil, nil
	}
	result, err := timestampsWire(row, "updated_at")
	if err != nil {
		return nil, err
	}
	result["enabled"], err = boolValue(result["enabled"], true)
	return result, err
}

func reviewRunWire(row map[string]any) (map[string]any, error) {
	return timestampsWire(row, "created_at", "updated_at")
}

func eventWire(row map[string]any) (map[string]any, error) {
	if row == nil {
		return nil, nil
	}
	return map[string]any{
		"id": row["event_id"], "cursor": row["cursor"], "type": row["type"],
		"agent_id": row["agent_id"], "session_id": row["session_id"], "data": row["data"],
	}, nil
}

func metricWire(row map[string]any) (map[string]any, error) {
	if row == nil {
		return nil, nil
	}
	result, err := timestampsWire(row, "updated_at")
	if result != nil {
		delete(result, "row_id")
	}
	return result, err
}

func timestampsWire(row map[string]any, columns ...string) (map[string]any, error) {
	if row == nil {
		return nil, nil
	}
	result := copyMap(row)
	for _, column := range columns {
		if result[column] == nil {
			continue
		}
		stamp, err := wireTime(result[column])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", column, err)
		}
		result[column] = stamp
	}
	return result, nil
}

func copyMap(row map[string]any) map[string]any {
	if row == nil {
		return nil
	}
	result := make(map[string]any, len(row))
	for key, value := range row {
		result[key] = value
	}
	return result
}

func wireRows(rows []map[string]any, convert func(map[string]any) (map[string]any, error)) ([]map[string]any, error) {
	result := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		converted, err := convert(row)
		if err != nil {
			return nil, err
		}
		result = append(result, converted)
	}
	return result, nil
}
