package build

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Amartuvshins0404/pr-preview/internal/run"
)

func gitCmd(src, token string, log io.Writer) run.Cmd {
	auth := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+token))
	return run.Cmd{Dir: src, Out: log, Env: []string{
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=http.https://github.com/.extraHeader",
		"GIT_CONFIG_VALUE_0=" + auth,
		"GIT_CONFIG_KEY_1=safe.directory",
		"GIT_CONFIG_VALUE_1=*",
	}}
}

func remoteURL(owner, repo string) string {
	return fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)
}

func fetch(ctx context.Context, src, owner, repo string, number int, token string, log io.Writer) (string, error) {
	if err := os.MkdirAll(src, 0o755); err != nil {
		return "", err
	}
	cmd := gitCmd(src, token, log)
	if _, err := os.Stat(filepath.Join(src, ".git")); err != nil {
		if err := cmd.Run(ctx, "git", "init", "--quiet"); err != nil {
			return "", err
		}
	}
	ref := fmt.Sprintf("refs/pull/%d/head", number)
	if err := cmd.Run(ctx, "git", "fetch", "--depth", "1", "--no-tags", "--quiet", remoteURL(owner, repo), ref); err != nil {
		return "", err
	}
	if err := cmd.Run(ctx, "git", "checkout", "--quiet", "--force", "--detach", "FETCH_HEAD"); err != nil {
		return "", err
	}
	if err := cmd.Run(ctx, "git", "clean", "-ffdxq"); err != nil {
		return "", err
	}
	out, err := run.Cmd{Dir: src, Env: cmd.Env}.Output(ctx, "git", "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(out))
	fmt.Fprintf(log, "checked out %s\n", sha)
	return sha, nil
}

func baseRecipe(ctx context.Context, src, out string, p Project, token string, log io.Writer) (bool, error) {
	if err := os.Remove(out); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if p.BaseRef == "" {
		return false, nil
	}
	cmd := gitCmd(src, token, log)
	if err := cmd.Run(ctx, "git", "fetch", "--depth", "1", "--no-tags", "--quiet", remoteURL(p.Owner, p.Repo), "refs/heads/"+p.BaseRef); err != nil {
		fmt.Fprintf(log, "base branch %s not fetched, no repo recipe: %v\n", p.BaseRef, err)
		return false, nil
	}
	for _, name := range []string{"compose.yml", "compose.yaml"} {
		b, err := run.Cmd{Dir: src, Env: cmd.Env}.Output(ctx, "git", "show", "FETCH_HEAD:.preview/"+name)
		if err == nil {
			return true, os.WriteFile(out, b, 0o644)
		}
	}
	return false, nil
}
