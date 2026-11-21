package build

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Amartuvshins0404/pr-preview/internal/config"
	"github.com/Amartuvshins0404/pr-preview/internal/run"
)

type Builder struct {
	cfg config.Config
}

func New(cfg config.Config) *Builder { return &Builder{cfg: cfg} }

type Result struct {
	SHA     string
	Kind    string
	Recipe  string
	Service string
	Port    int
}

func (b *Builder) Build(ctx context.Context, p Project, log io.Writer) (Result, error) {
	dir := b.Dir(p.Slug)
	sha, err := fetch(ctx, dir+"/src", p.Owner, p.Repo, p.Number, b.cfg.GitHubToken, log)
	if err != nil {
		return Result{}, err
	}
	p.SHA = sha
	r, err := b.pickRecipe(ctx, dir, dir+"/src", p.Repo, log)
	if err != nil {
		return Result{SHA: sha}, err
	}
	fmt.Fprintf(log, "recipe: %s (%s)\n", r.File, r.Kind)
	p.Recipe = r.File
	if err := os.Remove(b.override(p.Slug)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Result{SHA: sha}, err
	}
	s, err := b.inspect(ctx, p)
	if err != nil {
		return Result{SHA: sha}, err
	}
	fmt.Fprintf(log, "entry: service %s, port %d\n", s.Entry, s.Port)
	if err := b.writeOverride(p.Slug, s); err != nil {
		return Result{SHA: sha}, err
	}
	res := Result{SHA: sha, Kind: r.Kind, Recipe: r.File, Service: s.Entry, Port: s.Port}
	return res, b.Compose(ctx, p, log, "up", "-d", "--build", "--remove-orphans")
}

func (b *Builder) Probe(ctx context.Context, p Project, service string, port int, log io.Writer) error {
	url := fmt.Sprintf("http://%s:%d/", ProjectName(p.Slug), port)
	fmt.Fprintf(log, "waiting for %s\n", url)
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		req.Host = b.cfg.Host(p.Slug)
		if resp, err := client.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode < 502 {
				fmt.Fprintf(log, "entry answered HTTP %d\n", resp.StatusCode)
				return nil
			}
		}
		if exited, err := b.exited(ctx, p, service); err == nil && exited {
			_ = b.Compose(ctx, p, log, "logs", "--no-color", "--tail", "100", service)
			return fmt.Errorf("entry service %s exited before it answered HTTP", service)
		}
		select {
		case <-ctx.Done():
			_ = b.Compose(context.Background(), p, log, "logs", "--no-color", "--tail", "100", service)
			return fmt.Errorf("entry service %s did not answer HTTP: %w", service, ctx.Err())
		case <-tick.C:
		}
	}
}

func (b *Builder) exited(ctx context.Context, p Project, service string) (bool, error) {
	cmd, args := b.composeCmd(p, nil)
	out, err := cmd.Output(ctx, "docker", append(args, "ps", "-a", "--format", "json", service)...)
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		var c struct {
			State string `json:"State"`
		}
		if line == "" || json.Unmarshal([]byte(line), &c) != nil {
			continue
		}
		if c.State == "exited" || c.State == "dead" {
			return true, nil
		}
	}
	return false, nil
}

func (b *Builder) Down(ctx context.Context, p Project, log io.Writer) error {
	err := b.Compose(ctx, p, log, "down", "--volumes", "--rmi", "local", "--remove-orphans")
	out, lerr := run.Cmd{}.Output(ctx, "docker", "volume", "ls", "-q", "--filter", "label=com.docker.compose.project="+ProjectName(p.Slug))
	if lerr == nil {
		for _, v := range strings.Fields(string(out)) {
			_ = run.Cmd{Out: log}.Run(ctx, "docker", "volume", "rm", "-f", v)
		}
	}
	return err
}

type ComposeProject struct {
	Name   string `json:"Name"`
	Status string `json:"Status"`
}

func ListProjects(ctx context.Context) ([]ComposeProject, error) {
	out, err := run.Cmd{}.Output(ctx, "docker", "compose", "ls", "--all", "--format", "json")
	if err != nil {
		return nil, err
	}
	var all []ComposeProject
	if err := json.Unmarshal(out, &all); err != nil {
		return nil, err
	}
	var pv []ComposeProject
	for _, p := range all {
		if strings.HasPrefix(p.Name, "pv-") {
			pv = append(pv, p)
		}
	}
	return pv, nil
}

func (b *Builder) Prune(ctx context.Context) {
	for _, args := range [][]string{
		{"buildx", "prune", "--force", "--all", "--max-used-space", "15gb"},
		{"image", "prune", "--force"},
	} {
		out, err := run.Cmd{}.Output(ctx, "docker", args...)
		if err != nil {
			slog.Warn("prune failed", "cmd", strings.Join(args[:2], " "), "err", err)
			continue
		}
		slog.Info("pruned", "cmd", strings.Join(args[:2], " "), "out", lastLine(out))
	}
}

func (b *Builder) WarmTLS(slug string) {
	d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second}, Config: &tls.Config{ServerName: b.cfg.Host(slug), InsecureSkipVerify: true}}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	conn, err := d.DialContext(ctx, "tcp", b.cfg.CaddyTLSAddr)
	if err != nil {
		slog.Debug("tls warm failed", "slug", slug, "err", err)
		return
	}
	conn.Close()
	slog.Info("tls warmed", "slug", slug)
}

func lastLine(b []byte) string {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	return lines[len(lines)-1]
}
