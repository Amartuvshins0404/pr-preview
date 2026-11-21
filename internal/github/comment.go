package github

import (
	"context"
	"fmt"
	"strings"
)

func (c *Client) UpsertComment(ctx context.Context, owner, repo string, number int, id int64, body string) (int64, error) {
	payload := map[string]string{"body": body}
	if id != 0 {
		err := c.do(ctx, "PATCH", fmt.Sprintf("/repos/%s/%s/issues/comments/%d", owner, repo, id), payload, nil)
		if err == nil {
			return id, nil
		}
		if !strings.Contains(err.Error(), "404") {
			return id, err
		}
	}
	var out struct {
		ID int64 `json:"id"`
	}
	if err := c.do(ctx, "POST", fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repo, number), payload, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}
