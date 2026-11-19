package antigravity

import (
	"encoding/json"
	"path"
	"regexp"
	"strconv"
	"strings"
)

var (
	showingLinesRe = regexp.MustCompile(`(?s)^Showing lines (\d+)-(\d+) of (\d+)\r?\n(.*)$`)
	exitCodeRe     = regexp.MustCompile(`(?m)^Command exited with code (-?\d+)\.?\s*$`)
)

func firstNonEmptyStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if raw, ok := m[k]; ok && raw != nil {
			if s, ok := raw.(string); ok {
				if strings.TrimSpace(s) != "" {
					return s
				}
			}
		}
	}
	return ""
}

func firstInt(m map[string]any, keys ...string) (int, bool) {
	for _, k := range keys {
		if raw, ok := m[k]; ok && raw != nil {
			if n, ok := intVal(raw); ok {
				return n, true
			}
		}
	}
	return 0, false
}

func stripTimestampHeader(raw string) string {
	s := strings.ReplaceAll(raw, "\r\n", "\n")
	for {
		trimmed := strings.TrimLeft(s, "\n")
		if strings.HasPrefix(trimmed, "Created At:") || strings.HasPrefix(trimmed, "Completed At:") {
			if idx := strings.IndexByte(trimmed, '\n'); idx >= 0 {
				s = trimmed[idx+1:]
				continue
			}
			return ""
		}
		return trimmed
	}
}

func baseFileName(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	b := path.Base(p)
	if b == "." || b == "/" || b == "" {
		return "file"
	}
	return b
}

func buildUnifiedDiffLines(prefix string, body string) string {
	if body == "" {
		return ""
	}
	const maxPreviewBytes = 65536
	truncated := false
	if len(body) > maxPreviewBytes {
		body = body[:maxPreviewBytes]
		truncated = true
	}
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	var b strings.Builder
	for i, ln := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(prefix)
		b.WriteString(ln)
	}
	if truncated {
		b.WriteString("\n... (truncated)")
	}
	return b.String()
}

func buildMultiReplaceDiff(filePath string, rawChunks any) string {
	var chunks []map[string]any
	switch v := rawChunks.(type) {
	case []any:
		for _, el := range v {
			if m, ok := el.(map[string]any); ok {
				chunks = append(chunks, m)
			}
		}
	case []map[string]any:
		chunks = v
	case string:
		var parsed []map[string]any
		if json.Unmarshal([]byte(v), &parsed) == nil {
			chunks = parsed
		}
	}
	if len(chunks) == 0 {
		return ""
	}
	name := baseFileName(filePath)
	var b strings.Builder
	b.WriteString("--- a/")
	b.WriteString(name)
	b.WriteString("\n+++ b/")
	b.WriteString(name)
	for _, ch := range chunks {
		oldStr := firstNonEmptyStr(ch, "TargetContent", "old_string", "Target")
		newStr := firstNonEmptyStr(ch, "ReplacementContent", "new_string", "Replacement")
		if oldStr == "" && newStr == "" {
			continue
		}
		b.WriteString("\n@@")
		if sl, ok := firstInt(ch, "StartLine", "start_line"); ok && sl > 0 {
			b.WriteString(" -")
			b.WriteString(strconv.Itoa(sl))
		}
		b.WriteString(" @@\n")
		if oldStr != "" {
			b.WriteString(buildUnifiedDiffLines("-", oldStr))
			b.WriteByte('\n')
		}
		if newStr != "" {
			b.WriteString(buildUnifiedDiffLines("+", newStr))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func normalizeAntigravityTool(
	id, name, status string,
	rawInput any,
	rawOutput, rawError string,
	extraArgs map[string]any,
	bgTask map[string]any,
) (map[string]any, string) {
	args := map[string]any{}
	switch v := rawInput.(type) {
	case map[string]any:
		for k, val := range v {
			args[k] = val
		}
	case string:
		trimmed := strings.TrimSpace(v)
		if strings.HasPrefix(trimmed, "{") {
			var parsed map[string]any
			dec := json.NewDecoder(strings.NewReader(trimmed))
			dec.UseNumber()
			if dec.Decode(&parsed) == nil && parsed != nil {
				args = parsed
			}
		}
	}
	for k, val := range extraArgs {
		if existing, exists := args[k]; !exists || existing == nil || existing == "" {
			args[k] = val
		}
	}

	tool := map[string]any{
		"id":        id,
		"name":      name,
		"status":    status,
		"arguments": args,
	}
	if rawInput != nil {
		if s, ok := rawInput.(string); !ok || s != "" {
			tool["input"] = rawInput
		}
	}
	if rawError != "" {
		tool["error"] = rawError
	}

	cleanOut := stripTimestampHeader(rawOutput)
	messageText := cleanOut

	isCommandTool := name == "run_command" || name == "command_status" || name == "send_command_input" ||
		firstNonEmptyStr(args, "command", "cmd", "CommandLine", "Command") != ""

	switch {
	case isCommandTool:
		cmd := firstNonEmptyStr(args, "command", "cmd", "CommandLine", "Command")
		if cmd == "" && bgTask != nil {
			cmd = strings.TrimSpace(str(bgTask, "command"))
		}
		if cmd != "" {
			args["command"] = cmd
		}
		if cwd := firstNonEmptyStr(args, "cwd", "Cwd"); cwd != "" {
			args["cwd"] = cwd
		}
		summary := firstNonEmptyStr(args, "summary", "toolSummary", "Summary")
		if summary == "" && bgTask != nil {
			summary = strings.TrimSpace(str(bgTask, "summary"))
		}
		if summary != "" {
			args["summary"] = summary
		}
		if bgTask != nil {
			args["background"] = true
			if tid := str(bgTask, "task_id"); tid != "" {
				args["task_id"] = tid
			}
		}

		if m := exitCodeRe.FindStringSubmatch(cleanOut); len(m) == 2 {
			if code, err := strconv.Atoi(m[1]); err == nil {
				tool["exit_code"] = code
			}
		}

		body := cleanOut
		if idx := strings.Index(body, "\nCommand output:\n"); idx >= 0 {
			body = body[idx+len("\nCommand output:\n"):]
		} else if strings.HasPrefix(body, "Command output:\n") {
			body = body[len("Command output:\n"):]
		}
		if bgTask != nil {
			if bgOut := strings.ReplaceAll(str(bgTask, "output"), "\r\n", "\n"); bgOut != "" {
				if strings.TrimSpace(body) == "" || strings.Contains(body, "running in background") || strings.Contains(body, "WaitMsBeforeAsync") {
					body = bgOut
				}
			}
		}
		messageText = strings.TrimSpace(body)
		if messageText == "" && cleanOut != "" {
			messageText = strings.TrimSpace(cleanOut)
		}

	case name == "read_file" || name == "view_file" || name == "view_file_outline" || name == "view_code_item" || name == "list_dir" || name == "read_resource" || name == "read_url_content":
		if p := firstNonEmptyStr(args, "path", "Path", "AbsolutePath", "File", "TargetFile", "DirectoryPath", "Uri", "Url"); p != "" {
			args["path"] = p
		}
		if sl, ok := firstInt(args, "start_line", "StartLine"); ok {
			args["start_line"] = sl
		}
		if el, ok := firstInt(args, "end_line", "EndLine"); ok {
			args["end_line"] = el
		}
		if m := showingLinesRe.FindStringSubmatch(cleanOut); len(m) == 5 && (name == "read_file" || name == "view_file") {
			endLine, _ := strconv.Atoi(m[2])
			totalLines, _ := strconv.Atoi(m[3])
			body := m[4]
			encoded, err := json.Marshal(map[string]any{
				"content":     body,
				"total_lines": totalLines,
				"truncated":   totalLines > 0 && endLine < totalLines,
			})
			if err == nil {
				messageText = string(encoded)
			}
		} else {
			messageText = strings.TrimSpace(cleanOut)
		}

	case name == "edit_file" || name == "replace_file_content" || name == "multi_replace_file_content" || name == "write_file" || name == "write_to_file" || name == "sed_file" || name == "notebook_edit":
		p := firstNonEmptyStr(args, "path", "Path", "TargetFile", "AbsolutePath")
		if p != "" {
			args["path"] = p
		}
		oldStr := firstNonEmptyStr(args, "old_string", "Target", "TargetContent")
		newStr := firstNonEmptyStr(args, "new_string", "Replacement", "ReplacementContent")
		if oldStr != "" || newStr != "" {
			args["old_string"] = oldStr
			args["new_string"] = newStr
		} else if chunks, hasChunks := args["ReplacementChunks"]; hasChunks && chunks != nil {
			if diff := buildMultiReplaceDiff(p, chunks); diff != "" {
				args["changes"] = []map[string]any{{"path": p, "diff": diff}}
			}
		} else if content := firstNonEmptyStr(args, "Content", "CodeContent", "content"); content != "" {
			diff := "--- /dev/null\n+++ b/" + baseFileName(p) + "\n@@\n" + buildUnifiedDiffLines("+", content)
			args["changes"] = []map[string]any{{"path": p, "diff": diff}}
		}
		messageText = strings.TrimSpace(cleanOut)

	case name == "grep_search" || name == "find_by_name" || name == "search_web" || name == "SearchContext":
		if q := firstNonEmptyStr(args, "query", "pattern", "Query", "Pattern"); q != "" {
			args["query"] = q
			args["pattern"] = q
		}
		if sp := firstNonEmptyStr(args, "path", "SearchPath", "SearchDirectory", "Directory"); sp != "" {
			args["path"] = sp
		}
		messageText = strings.TrimSpace(cleanOut)

	default:
		if messageText == "" && bgTask != nil {
			messageText = strings.TrimSpace(strings.ReplaceAll(str(bgTask, "output"), "\r\n", "\n"))
		}
	}

	if messageText == "" && rawError != "" {
		messageText = rawError
	}
	if rawOutput != "" {
		tool["output"] = rawOutput
	} else if messageText != "" {
		tool["output"] = messageText
	}

	return tool, messageText
}

func extractAntigravityQuestions(args map[string]any) ([]map[string]any, string) {
	if len(args) == 0 {
		return nil, ""
	}
	raw, exists := args["questions"]
	if !exists || raw == nil {
		return nil, ""
	}
	var items []any
	switch typed := raw.(type) {
	case []any:
		items = typed
	case []map[string]any:
		items = make([]any, len(typed))
		for i, m := range typed {
			items[i] = m
		}
	case string:
		trimmed := strings.TrimSpace(typed)
		if strings.HasPrefix(trimmed, "[") {
			_ = json.Unmarshal([]byte(trimmed), &items)
		}
	}
	if len(items) == 0 {
		return nil, ""
	}
	questions := make([]map[string]any, 0, len(items))
	titles := make([]string, 0, len(items))
	for _, elem := range items {
		m, ok := elem.(map[string]any)
		if !ok {
			continue
		}
		title := strings.TrimSpace(firstNonEmptyStr(m, "question", "title", "header"))
		if title == "" {
			continue
		}
		var opts []string
		if rawOpts, hasOpts := m["options"]; hasOpts && rawOpts != nil {
			switch optSlice := rawOpts.(type) {
			case []any:
				for _, optElem := range optSlice {
					switch ov := optElem.(type) {
					case string:
						if s := strings.TrimSpace(ov); s != "" {
							opts = append(opts, s)
						}
					case map[string]any:
						if s := strings.TrimSpace(firstNonEmptyStr(ov, "label", "title", "value")); s != "" {
							opts = append(opts, s)
						}
					}
				}
			case []string:
				for _, ov := range optSlice {
					if s := strings.TrimSpace(ov); s != "" {
						opts = append(opts, s)
					}
				}
			}
		}
		q := map[string]any{"title": title}
		if len(opts) > 0 {
			q["options"] = opts
		}
		if m["is_multi_select"] == true || m["multi_select"] == true {
			q["multi_select"] = true
		}
		questions = append(questions, q)
		titles = append(titles, title)
	}
	return questions, strings.Join(titles, "\n")
}
