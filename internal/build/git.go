package build

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Amartuvshins0404/pr-preview/internal/run"
)

func fetch(ctx context.Context, src, owner, repo string, number int, token string, log io.Writer) (string, error) {
	if err := os.MkdirAll(src, 0o755); err != nil {
		return "", err
	}
	auth := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+token))
	cmd := run.Cmd{Dir: src, Out: log, Env: []string{
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=http.https://github.com/.extraHeader",
		"GIT_CONFIG_VALUE_0=" + auth,
		"GIT_CONFIG_KEY_1=safe.directory",
		"GIT_CONFIG_VALUE_1=*",
	}}
	if _, err := os.Stat(filepath.Join(src, ".git")); err != nil {
		if err := cmd.Run(ctx, "git", "init", "--quiet"); err != nil {
			return "", err
		}
	}
	remote := fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)
	ref := fmt.Sprintf("refs/pull/%d/head", number)
	if err := cmd.Run(ctx, "git", "fetch", "--depth", "1", "--no-tags", "--quiet", remote, ref); err != nil {
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
