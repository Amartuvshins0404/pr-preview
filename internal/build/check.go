package build

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type composeConfig struct {
	Services map[string]service `json:"services"`
	Secrets  map[string]fileRef `json:"secrets"`
	Configs  map[string]fileRef `json:"configs"`
	Preview  *struct {
		Service string `json:"service"`
		Port    int    `json:"port"`
		Note    string `json:"note"`
		Public  bool   `json:"public"`
	} `json:"x-preview"`
}

type service struct {
	Networks    map[string]json.RawMessage `json:"networks"`
	NetworkMode string                     `json:"network_mode"`
	Pid         string                     `json:"pid"`
	Ipc         string                     `json:"ipc"`
	Userns      string                     `json:"userns_mode"`
	Volumes     []struct {
		Type   string `json:"type"`
		Source string `json:"source"`
	} `json:"volumes"`
	EnvFile []json.RawMessage `json:"env_file"`
	Build   *struct {
		Context    string            `json:"context"`
		Dockerfile string            `json:"dockerfile"`
		Contexts   map[string]string `json:"additional_contexts"`
	} `json:"build"`
}

type fileRef struct {
	File string `json:"file"`
}

func checkConfig(dir string, cfg composeConfig) error {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	for name, svc := range cfg.Services {
		for _, ns := range []string{svc.NetworkMode, svc.Pid, svc.Ipc, svc.Userns} {
			if ns == "host" || strings.HasPrefix(ns, "container:") || strings.HasPrefix(ns, "service:") {
				return fmt.Errorf("service %s shares a namespace (%s); a preview cannot", name, ns)
			}
		}
		var paths []string
		for _, v := range svc.Volumes {
			if v.Type == "bind" {
				paths = append(paths, v.Source)
			}
		}
		for _, raw := range svc.EnvFile {
			var s string
			var o struct {
				Path string `json:"path"`
			}
			if json.Unmarshal(raw, &s) == nil {
				paths = append(paths, s)
			} else if json.Unmarshal(raw, &o) == nil {
				paths = append(paths, o.Path)
			}
		}
		if b := svc.Build; b != nil && b.Context != "" && !strings.Contains(b.Context, "://") {
			paths = append(paths, b.Context)
			if b.Dockerfile != "" {
				df := b.Dockerfile
				if !filepath.IsAbs(df) {
					df = filepath.Join(b.Context, df)
				}
				paths = append(paths, df)
			}
			for _, c := range b.Contexts {
				if !strings.Contains(c, "://") && !strings.HasPrefix(c, "docker-image:") {
					paths = append(paths, c)
				}
			}
		}
		for _, p := range paths {
			if err := inside(root, p); err != nil {
				return fmt.Errorf("service %s: %w", name, err)
			}
		}
	}
	for name, f := range cfg.Secrets {
		if f.File != "" {
			if err := inside(root, f.File); err != nil {
				return fmt.Errorf("secret %s: %w", name, err)
			}
		}
	}
	for name, f := range cfg.Configs {
		if f.File != "" {
			if err := inside(root, f.File); err != nil {
				return fmt.Errorf("config %s: %w", name, err)
			}
		}
	}
	return nil
}

func inside(root, path string) error {
	escape := fmt.Errorf("path %s points outside the preview's own files", path)
	if !filepath.IsAbs(path) {
		return escape
	}
	p, rest := filepath.Clean(path), ""
	for {
		real, err := filepath.EvalSymlinks(p)
		if err == nil {
			p = filepath.Join(real, rest)
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if _, lerr := os.Lstat(p); lerr == nil {
			return escape
		}
		parent := filepath.Dir(p)
		if parent == p {
			return escape
		}
		p, rest = parent, filepath.Join(filepath.Base(p), rest)
	}
	if p != root && !strings.HasPrefix(p, root+string(filepath.Separator)) {
		return escape
	}
	return nil
}
