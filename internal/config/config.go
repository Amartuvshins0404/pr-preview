package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	BaseDomain       string
	GitHubToken      string
	AccessPassword   string
	ReportOwners     []string
	MaxRunning       int
	Idle             time.Duration
	Poll             time.Duration
	BuildConcurrency int
	BuildTimeout     time.Duration
	DataDir          string
	RecipesDir       string
	Listen           string
	Network          string
	RailpackFrontend string
	CaddyTLSAddr     string
}

func Load() (Config, error) {
	c := Config{
		BaseDomain:       strings.ToLower(strings.TrimSpace(os.Getenv("BASE_DOMAIN"))),
		GitHubToken:      strings.TrimSpace(os.Getenv("GITHUB_TOKEN")),
		AccessPassword:   os.Getenv("ACCESS_PASSWORD"),
		DataDir:          str("DATA_DIR", "/var/lib/pr-preview"),
		RecipesDir:       str("RECIPES_DIR", "/recipes"),
		Listen:           str("LISTEN_ADDR", ":8080"),
		Network:          "pr-preview",
		RailpackFrontend: str("RAILPACK_FRONTEND", "ghcr.io/railwayapp/railpack-frontend"),
		CaddyTLSAddr:     str("CADDY_TLS_ADDR", "caddy:443"),
	}
	for _, o := range strings.Split(os.Getenv("REPORT_OWNERS"), ",") {
		if o = strings.ToLower(strings.TrimSpace(o)); o != "" {
			c.ReportOwners = append(c.ReportOwners, o)
		}
	}
	var errs []error
	num := func(key string, def int) int {
		v := strings.TrimSpace(os.Getenv(key))
		if v == "" {
			return def
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			errs = append(errs, fmt.Errorf("%s must be a positive integer, got %q", key, v))
			return def
		}
		return n
	}
	c.MaxRunning = num("MAX_RUNNING", 4)
	c.Idle = time.Duration(num("IDLE_MINUTES", 240)) * time.Minute
	c.Poll = time.Duration(num("POLL_SECONDS", 60)) * time.Second
	c.BuildConcurrency = num("BUILD_CONCURRENCY", 1)
	c.BuildTimeout = time.Duration(num("BUILD_TIMEOUT_MINUTES", 30)) * time.Minute
	if c.BaseDomain == "" {
		errs = append(errs, errors.New("BASE_DOMAIN is required"))
	}
	if c.GitHubToken == "" {
		errs = append(errs, errors.New("GITHUB_TOKEN is required"))
	}
	return c, errors.Join(errs...)
}

func (c Config) Reports(owner string) bool {
	owner = strings.ToLower(owner)
	for _, o := range c.ReportOwners {
		if o == "*" || o == owner {
			return true
		}
	}
	return false
}

func (c Config) Origin() string { return "https://" + c.BaseDomain }

func (c Config) Host(slug string) string { return slug + "." + c.BaseDomain }

func str(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
