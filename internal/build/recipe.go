package build

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Amartuvshins0404/pr-preview/internal/run"
)

const (
	KindRepo       = "repo"
	KindServer     = "server"
	KindDockerfile = "dockerfile"
	KindRailpack   = "railpack"
)

type recipe struct {
	Kind string
	File string
}

func (b *Builder) pickRecipe(ctx context.Context, dir, src, repo, base string, log io.Writer) (recipe, error) {
	if base != "" {
		return recipe{KindRepo, base}, nil
	}
	for _, name := range []string{repo + ".yml", repo + ".yaml"} {
		if f := filepath.Join(b.cfg.RecipesDir, name); exists(f) {
			return recipe{KindServer, f}, nil
		}
	}
	gen := filepath.Join(dir, "compose.gen.yml")
	if df := filepath.Join(src, "Dockerfile"); exists(df) {
		port, err := exposedPort(df)
		if err != nil {
			return recipe{}, err
		}
		build := map[string]any{"context": src}
		return recipe{KindDockerfile, gen}, writeGenerated(gen, build, port)
	}
	plan := filepath.Join(dir, "railpack-plan.json")
	if err := b.railpackPrepare(ctx, src, plan, filepath.Join(dir, "railpack-info.json"), log); err != nil {
		return recipe{}, err
	}
	build := map[string]any{
		"context":    src,
		"dockerfile": plan,
		"args":       map[string]string{"BUILDKIT_SYNTAX": b.cfg.RailpackFrontend},
	}
	return recipe{KindRailpack, gen}, writeGenerated(gen, build, 3000)
}

func (b *Builder) railpackPrepare(ctx context.Context, src, plan, info string, log io.Writer) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		err = run.Cmd{Dir: src, Out: log}.Run(ctx, "railpack", "prepare", src, "--plan-out", plan, "--info-out", info)
		var exit *exec.ExitError
		if err == nil || !errors.As(err, &exit) || exit.ExitCode() != 75 {
			return err
		}
	}
	return err
}

func writeGenerated(path string, build map[string]any, port int) error {
	doc := map[string]any{
		"services": map[string]any{
			"app": map[string]any{
				"build":       build,
				"environment": map[string]string{"PORT": strconv.Itoa(port)},
			},
		},
		"x-preview": map[string]any{"service": "app", "port": port},
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func exposedPort(dockerfile string) (int, error) {
	f, err := os.Open(dockerfile)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	port := 3000
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 || !strings.EqualFold(fields[0], "EXPOSE") {
			continue
		}
		for _, tok := range fields[1:] {
			if n, err := strconv.Atoi(strings.SplitN(tok, "/", 2)[0]); err == nil && n > 0 {
				port = n
			}
		}
	}
	if err := sc.Err(); err != nil {
		return 0, fmt.Errorf("read Dockerfile: %w", err)
	}
	return port, nil
}

func exists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
