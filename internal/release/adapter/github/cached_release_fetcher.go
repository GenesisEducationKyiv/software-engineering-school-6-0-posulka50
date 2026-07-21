package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

const releaseCacheTTL = 5 * time.Minute

// CachedReleaseFetcher wraps a ReleaseFetcher and caches latest release data in Redis.
type CachedReleaseFetcher struct {
	inner ReleaseFetcher
	redis *redis.Client
}

func NewCachedReleaseFetcher(inner ReleaseFetcher, redisClient *redis.Client) *CachedReleaseFetcher {
	return &CachedReleaseFetcher{inner: inner, redis: redisClient}
}

func (c *CachedReleaseFetcher) GetLatestRelease(ctx context.Context, owner, repo string) (*Release, error) {
	cacheKey := fmt.Sprintf("github:release:%s/%s", owner, repo)

	if cached, err := c.redis.Get(ctx, cacheKey).Result(); err == nil {
		slog.Debug("github: cache hit for release", "owner", owner, "repo", repo)
		if cached == "notfound" {
			return nil, ErrNotFound
		}
		var release Release
		if err := json.Unmarshal([]byte(cached), &release); err == nil {
			return &release, nil
		}
	}

	release, err := c.inner.GetLatestRelease(ctx, owner, repo)

	switch {
	case err == nil:
		if data, jsonErr := json.Marshal(release); jsonErr == nil {
			c.redis.Set(ctx, cacheKey, string(data), releaseCacheTTL)
		}
	case errors.Is(err, ErrNotFound):
		c.redis.Set(ctx, cacheKey, "notfound", releaseCacheTTL)
	}

	return release, err
}
