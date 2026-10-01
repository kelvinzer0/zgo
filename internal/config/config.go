package config

import (
	"os"
	"time"

	"github.com/zgo-cli/zgo/internal/cache"
	"github.com/zgo-cli/zgo/internal/manifest"
)

type Config struct {
	BrokerURL   string
	BuilderRepo string
	GitHubToken string
	CacheDir    string
	StateDir    string
	DefaultTTL  time.Duration
}

func LoadConfig() *Config {
	brokerURL := os.Getenv("ZGO_BROKER_URL")
	if brokerURL == "" {
		brokerURL = "https://broker.zgo.dev"
	}

	builderRepo := os.Getenv("ZGO_BUILDER_REPO")
	if builderRepo == "" {
		builderRepo = "zgo-cli/builder"
	}

	token := os.Getenv("ZGO_GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}

	cacheDir := os.Getenv("ZGO_CACHE_DIR")
	if cacheDir == "" {
		cacheDir = cache.DefaultCacheDir()
	}

	stateDir := os.Getenv("ZGO_STATE_DIR")
	if stateDir == "" {
		stateDir, _ = manifest.UserStateDir()
	}

	return &Config{
		BrokerURL:   brokerURL,
		BuilderRepo: builderRepo,
		GitHubToken: token,
		CacheDir:    cacheDir,
		StateDir:    stateDir,
		DefaultTTL:  10 * time.Minute,
	}
}
