package build

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Amartuvshins0404/pr-preview/internal/run"
)

const gatewayNet = "pr-preview"

type Project struct {
	Slug    string
	Owner   string
	Repo    string
	Number  int
	SHA     string
	Recipe  string
	BaseRef string
}

func (b *Builder) Dir(slug string) string { return filepath.Join(b.cfg.DataDir, "previews", slug) }

func (b *Builder) LogPath(slug string) string { return filepath.Join(b.Dir(slug), "build.log") }

func ProjectName(slug string) string { return "pv-" + slug }

func gatewayName(slug string) string { return ProjectName(slug) + "_" + gatewayNet }

func (b *Builder) override(slug string) string { return filepath.Join(b.Dir(slug), "override.yml") }

func (b *Builder) composeCmd(p Project, log io.Writer) (run.Cmd, []string) {
	dir := b.Dir(p.Slug)
	args := []string{"compose", "-p", ProjectName(p.Slug), "--project-directory", filepath.Join(dir, "src")}
	if p.Recipe != "" && exists(p.Recipe) {
		args = append(args, "-f", p.Recipe)
		if exists(b.override(p.Slug)) {
			args = append(args, "-f", b.override(p.Slug))
		}
	}
	if envFile := filepath.Join(b.cfg.RecipesDir, p.Repo+".env"); exists(envFile) {
		args = append(args, "--env-file", envFile)
	}
	host := b.cfg.Host(p.Slug)
	env := []string{
		"PREVIEW_URL=https://" + host,
		"PREVIEW_HOST=" + host,
		"PREVIEW_SLUG=" + p.Slug,
		"PREVIEW_OWNER=" + p.Owner,
		"PREVIEW_REPO=" + p.Repo,
		"PREVIEW_PR=" + strconv.Itoa(p.Number),
		"PREVIEW_SHA=" + p.SHA,
	}
	return run.Cmd{Dir: dir, Env: env, Out: log}, args
}

func (b *Builder) Compose(ctx context.Context, p Project, log io.Writer, args ...string) error {
	cmd, base := b.composeCmd(p, log)
	return cmd.Run(ctx, "docker", append(base, args...)...)
}

type spec struct {
	Entry    string
	Port     int
	Note     string
	Public   bool
	Services []string
	Networks []string
}

func (b *Builder) inspect(ctx context.Context, p Project) (spec, error) {
	cmd, args := b.composeCmd(p, nil)
	out, err := cmd.Output(ctx, "docker", append(args, "config", "--format", "json")...)
	if err != nil {
		return spec{}, err
	}
	var cfg composeConfig
	if err := json.Unmarshal(out, &cfg); err != nil {
		return spec{}, fmt.Errorf("parse compose config: %w", err)
	}
	if cfg.Preview == nil || cfg.Preview.Port <= 0 {
		return spec{}, fmt.Errorf("recipe %s needs a top-level x-preview with a port", p.Recipe)
	}
	if err := checkConfig(b.Dir(p.Slug), cfg); err != nil {
		return spec{}, err
	}
	s := spec{Port: cfg.Preview.Port, Entry: cfg.Preview.Service, Note: strings.TrimSpace(cfg.Preview.Note), Public: cfg.Preview.Public}
	for name := range cfg.Services {
		s.Services = append(s.Services, name)
	}
	sort.Strings(s.Services)
	if s.Entry == "" {
		s.Entry = defaultEntry(s.Services)
	}
	svc, ok := cfg.Services[s.Entry]
	if s.Entry == "" || !ok {
		return spec{}, fmt.Errorf("x-preview.service %q is not a service of the recipe; set it to one of %s", s.Entry, strings.Join(s.Services, ", "))
	}
	for n := range svc.Networks {
		s.Networks = append(s.Networks, n)
	}
	if len(s.Networks) == 0 {
		s.Networks = []string{"default"}
	}
	sort.Strings(s.Networks)
	return s, nil
}

func defaultEntry(services []string) string {
	if len(services) == 1 {
		return services[0]
	}
	for _, want := range []string{"web", "app"} {
		for _, s := range services {
			if s == want {
				return s
			}
		}
	}
	return ""
}

func (b *Builder) writeOverride(slug string, s spec) error {
	var w strings.Builder
	w.WriteString("services:\n")
	for _, name := range s.Services {
		fmt.Fprintf(&w, "  %s:\n", strconv.Quote(name))
		w.WriteString("    ports: !reset []\n    container_name: !reset null\n")
		w.WriteString("    privileged: false\n    cap_add: !reset []\n    devices: !reset []\n")
		w.WriteString("    security_opt: [\"no-new-privileges:true\"]\n    pids_limit: 1024\n")
		fmt.Fprintf(&w, "    mem_limit: %s\n    cpus: %s\n", strconv.Quote(b.cfg.PreviewMemory), strconv.Quote(b.cfg.PreviewCPUs))
		if b.cfg.PreviewRuntime != "" {
			fmt.Fprintf(&w, "    runtime: %s\n", strconv.Quote(b.cfg.PreviewRuntime))
		}
		if name != s.Entry {
			continue
		}
		w.WriteString("    networks:\n")
		for _, n := range s.Networks {
			fmt.Fprintf(&w, "      %s: {}\n", strconv.Quote(n))
		}
		fmt.Fprintf(&w, "      %s:\n        aliases: [%s]\n", strconv.Quote(gatewayNet), strconv.Quote(ProjectName(slug)))
	}
	fmt.Fprintf(&w, "networks:\n  %s: {}\n", strconv.Quote(gatewayNet))
	return os.WriteFile(b.override(slug), []byte(w.String()), 0o644)
}

func (b *Builder) Join(ctx context.Context, slug string) error {
	_, err := run.Cmd{}.Output(ctx, "docker", "network", "connect", gatewayName(slug), b.cfg.Self)
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return err
	}
	return nil
}

func (b *Builder) leave(ctx context.Context, slug string) {
	_, _ = run.Cmd{}.Output(ctx, "docker", "network", "disconnect", "--force", gatewayName(slug), b.cfg.Self)
}
