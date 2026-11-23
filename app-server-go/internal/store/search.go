package store

import (
	"context"
	"strings"
)

type MessageSearchHit struct {
	ID           string `json:"id"`
	AgentID      string `json:"agent_id"`
	SessionID    string `json:"session_id"`
	Role         string `json:"role"`
	Snippet      string `json:"snippet"`
	CreatedAt    string `json:"created_at"`
	SessionTitle string `json:"session_title"`
	ProjectName  string `json:"project_name,omitempty"`
	Workspace    string `json:"workspace,omitempty"`
	AgentKind    string `json:"agent_kind,omitempty"`
	AgentName    string `json:"agent_name,omitempty"`
}

func escapeLikeTerm(term string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(term) + "%"
}

func buildSearchSnippet(text string, terms []string) string {
	collapsed := strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	runes := []rune(collapsed)
	if len(runes) <= 140 {
		return collapsed
	}
	lower := strings.ToLower(collapsed)
	matchByteIdx := -1
	for _, term := range terms {
		idx := strings.Index(lower, strings.ToLower(term))
		if idx >= 0 && (matchByteIdx < 0 || idx < matchByteIdx) {
			matchByteIdx = idx
		}
	}
	if matchByteIdx < 0 {
		return string(runes[:140]) + "…"
	}
	matchRuneIdx := len([]rune(lower[:matchByteIdx]))
	start := matchRuneIdx - 36
	if start < 0 {
		start = 0
	}
	end := start + 140
	if end > len(runes) {
		end = len(runes)
		start = max(0, end-140)
	}
	snippet := string(runes[start:end])
	if start > 0 {
		snippet = "…" + snippet
	}
	if end < len(runes) {
		snippet += "…"
	}
	return snippet
}

// SearchMessages performs case-insensitive multi-term search across persisted
// session transcript messages, capping hits per session so results span
// multiple matching conversations.
func (s *Store) SearchMessages(ctx context.Context, rawQuery string, limit int) ([]MessageSearchHit, error) {
	fields := strings.Fields(strings.TrimSpace(rawQuery))
	if len(fields) == 0 {
		return []MessageSearchHit{}, nil
	}
	if len(fields) > 6 {
		fields = fields[:6]
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	var where strings.Builder
	where.WriteString(`m.kind = 'message' AND COALESCE(s.ephemeral, 0) = 0 AND COALESCE(s.native_kind, '') NOT IN ('subagent', 'smoke')`)
	args := make([]any, 0, len(fields)+1)
	for _, term := range fields {
		where.WriteString(` AND lower(m.text) LIKE lower(?) ESCAPE '\'`)
		args = append(args, escapeLikeTerm(term))
	}
	fetchLimit := limit * 4
	if fetchLimit < 60 {
		fetchLimit = 60
	}
	args = append(args, fetchLimit)

	query := `
		SELECT
			m.id,
			m.agent_id,
			m.session_id,
			m.role,
			m.text,
			m.created_at,
			COALESCE(s.title, ''),
			COALESCE(s.project_name, ''),
			COALESCE(s.workspace, ''),
			COALESCE(a.kind, ''),
			COALESCE(a.name, '')
		FROM messages m
		JOIN sessions s ON s.agent_id = m.agent_id AND s.id = m.session_id
		LEFT JOIN agents a ON a.id = m.agent_id
		WHERE ` + where.String() + `
		ORDER BY m.created_at DESC
		LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	hits := make([]MessageSearchHit, 0, limit)
	perSession := make(map[string]int)
	for rows.Next() {
		var hit MessageSearchHit
		var fullText string
		if err := rows.Scan(
			&hit.ID,
			&hit.AgentID,
			&hit.SessionID,
			&hit.Role,
			&fullText,
			&hit.CreatedAt,
			&hit.SessionTitle,
			&hit.ProjectName,
			&hit.Workspace,
			&hit.AgentKind,
			&hit.AgentName,
		); err != nil {
			return nil, err
		}
		key := hit.AgentID + "::" + hit.SessionID
		if perSession[key] >= 3 {
			continue
		}
		perSession[key]++
		hit.Snippet = buildSearchSnippet(fullText, fields)
		hits = append(hits, hit)
		if len(hits) >= limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return hits, nil
}
