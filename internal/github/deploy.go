package github

import (
	"context"
	"fmt"
)

type DeployState string

const (
	InProgress DeployState = "in_progress"
	Success    DeployState = "success"
	Failure    DeployState = "failure"
	Inactive   DeployState = "inactive"
)

func (c *Client) CreateDeployment(ctx context.Context, owner, repo, sha, description string) (int64, error) {
	body := map[string]any{
		"ref":                    sha,
		"environment":            "pr-preview",
		"transient_environment":  true,
		"production_environment": false,
		"required_contexts":      []string{},
		"auto_merge":             false,
		"description":            description,
	}
	var out struct {
		ID int64 `json:"id"`
	}
	if err := c.do(ctx, "POST", fmt.Sprintf("/repos/%s/%s/deployments", owner, repo), body, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

func (c *Client) SetDeploymentStatus(ctx context.Context, owner, repo string, id int64, state DeployState, url string) error {
	body := map[string]any{"state": state, "auto_inactive": false}
	switch state {
	case Success:
		body["environment_url"] = url
		body["log_url"] = url
	case Failure, InProgress:
		body["log_url"] = url
	}
	return c.do(ctx, "POST", fmt.Sprintf("/repos/%s/%s/deployments/%d/statuses", owner, repo, id), body, nil)
}
