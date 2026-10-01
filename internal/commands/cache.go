package commands

import (
	"fmt"

	"github.com/zgo-cli/zgo/internal/cache"
	"github.com/zgo-cli/zgo/internal/config"
)

func RunCache(cfg *config.Config, action string) error {
	store := cache.NewStore(cfg.CacheDir)

	switch action {
	case "clean":
		if err := store.Clean(); err != nil {
			return fmt.Errorf("failed to clean cache: %w", err)
		}
		fmt.Println("🧹 Cache cleaned successfully.")

	case "path":
		fmt.Println(cfg.CacheDir)

	case "size", "":
		size, err := store.TotalSize()
		if err != nil {
			return err
		}
		fmt.Printf("Cache directory: %s\nTotal size: %.2f MB\n", cfg.CacheDir, float64(size)/(1024*1024))

	default:
		return fmt.Errorf("unknown cache action %q. Valid actions: size, path, clean", action)
	}

	return nil
}
