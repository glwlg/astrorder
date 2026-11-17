package daemonclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

// CodexHistoryPage contains native thread items, newest first. Reading this
// catalog endpoint neither attaches nor resumes the thread.
type CodexHistoryPage struct {
	Data       []map[string]any `json:"data"`
	NextCursor *string          `json:"nextCursor"`
}

func (c *Client) ReadCodexHistoryPage(ctx context.Context, target *pb.RuntimeTarget, threadID, cursor string, limit uint32) (CodexHistoryPage, error) {
	if c == nil || c.Runtime == nil || target == nil || threadID == "" || limit < 1 || limit > 200 {
		return CodexHistoryPage{}, errors.New("native Codex history request is invalid")
	}
	direction := "desc"
	query := &pb.CodexThreadItemsListQuery{ThreadId: threadID, Limit: limit, SortDirection: &direction}
	if cursor != "" {
		query.Cursor = &pb.NullableString{State: &pb.NullableString_Value{Value: cursor}}
	}
	response, err := c.Runtime.QueryNative(ctx, &pb.NativeQueryRequest{
		Target: target,
		Query:  &pb.NativeQueryRequest_CodexThreadItemsList{CodexThreadItemsList: query},
	})
	if err != nil {
		return CodexHistoryPage{}, err
	}
	if response == nil || response.Result == nil {
		return CodexHistoryPage{}, errors.New("native Codex history response is missing")
	}
	decoder := json.NewDecoder(bytes.NewReader(response.Result.Utf8Json))
	decoder.UseNumber()
	var page CodexHistoryPage
	if err := decoder.Decode(&page); err != nil {
		return CodexHistoryPage{}, err
	}
	if decoder.Decode(new(any)) != io.EOF || page.Data == nil || len(page.Data) > int(limit) {
		return CodexHistoryPage{}, errors.New("native Codex history page is invalid")
	}
	for _, item := range page.Data {
		if item == nil {
			return CodexHistoryPage{}, errors.New("native Codex history item is invalid")
		}
	}
	if page.NextCursor != nil && (*page.NextCursor == "" || *page.NextCursor == cursor) {
		return CodexHistoryPage{}, errors.New("native Codex history continuation is invalid")
	}
	return page, nil
}
