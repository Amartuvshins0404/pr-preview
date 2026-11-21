package github

import (
	"context"
	"errors"
	"fmt"
)

type PR struct {
	Owner   string
	Repo    string
	Number  int
	Title   string
	URL     string
	HeadSHA string
	BaseRef string
	Private bool
	Fork    bool
}

const openPRs = `query($after: String) {
  viewer {
    pullRequests(states: OPEN, first: 100, after: $after, orderBy: {field: CREATED_AT, direction: DESC}) {
      pageInfo { hasNextPage endCursor }
      nodes {
        number title url headRefOid baseRefName
        repository { name isPrivate isFork owner { login } }
      }
    }
  }
}`

type prPage struct {
	Data struct {
		Viewer struct {
			PullRequests struct {
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
				Nodes []struct {
					Number     int    `json:"number"`
					Title      string `json:"title"`
					URL        string `json:"url"`
					HeadRefOid string `json:"headRefOid"`
					BaseRef    string `json:"baseRefName"`
					Repository struct {
						Name      string `json:"name"`
						IsPrivate bool   `json:"isPrivate"`
						IsFork    bool   `json:"isFork"`
						Owner     struct {
							Login string `json:"login"`
						} `json:"owner"`
					} `json:"repository"`
				} `json:"nodes"`
			} `json:"pullRequests"`
		} `json:"viewer"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func (c *Client) OpenPRs(ctx context.Context) ([]PR, error) {
	var out []PR
	var after *string
	for page := 0; page < 50; page++ {
		var p prPage
		body := map[string]any{"query": openPRs, "variables": map[string]any{"after": after}}
		if err := c.do(ctx, "POST", "/graphql", body, &p); err != nil {
			return nil, err
		}
		if len(p.Errors) > 0 {
			return nil, fmt.Errorf("github graphql: %s", p.Errors[0].Message)
		}
		prs := p.Data.Viewer.PullRequests
		for _, n := range prs.Nodes {
			out = append(out, PR{
				Owner:   n.Repository.Owner.Login,
				Repo:    n.Repository.Name,
				Number:  n.Number,
				Title:   n.Title,
				URL:     n.URL,
				HeadSHA: n.HeadRefOid,
				BaseRef: n.BaseRef,
				Private: n.Repository.IsPrivate,
				Fork:    n.Repository.IsFork,
			})
		}
		if !prs.PageInfo.HasNextPage {
			return out, nil
		}
		cursor := prs.PageInfo.EndCursor
		after = &cursor
	}
	return nil, errors.New("github graphql: too many pages")
}
