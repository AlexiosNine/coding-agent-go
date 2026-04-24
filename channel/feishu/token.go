package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

const (
	tokenURL      = "https://open.feishu.cn/open-apis/auth/v3/app_access_token/internal"
	tokenRefreshBefore = 5 * time.Minute
)

// TokenCache fetches and caches a Feishu app_access_token, refreshing it
// automatically before it expires.
type TokenCache struct {
	appID     string
	appSecret string
	client    *http.Client

	mu        sync.RWMutex
	token     string
	expiresAt time.Time
}

// newTokenCache creates a TokenCache for the given app credentials.
func newTokenCache(appID, appSecret string) *TokenCache {
	return &TokenCache{
		appID:     appID,
		appSecret: appSecret,
		client:    &http.Client{Timeout: 10 * time.Second},
	}
}

// Get returns a valid app_access_token, refreshing if necessary.
func (c *TokenCache) Get(ctx context.Context) (string, error) {
	c.mu.RLock()
	if c.token != "" && time.Until(c.expiresAt) > tokenRefreshBefore {
		tok := c.token
		c.mu.RUnlock()
		return tok, nil
	}
	c.mu.RUnlock()

	return c.refresh(ctx)
}

// refresh fetches a new token from the Feishu API with exponential back-off.
func (c *TokenCache) refresh(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock.
	if c.token != "" && time.Until(c.expiresAt) > tokenRefreshBefore {
		return c.token, nil
	}

	var lastErr error
	for attempt := range 3 {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(1<<attempt) * time.Second):
			}
		}

		tok, expire, err := c.fetchToken(ctx)
		if err != nil {
			lastErr = err
			continue
		}

		c.token = tok
		c.expiresAt = time.Now().Add(time.Duration(expire) * time.Second)
		return tok, nil
	}
	return "", fmt.Errorf("feishu: token refresh failed after 3 attempts: %w", lastErr)
}

// fetchToken makes a single HTTP call to obtain a new token.
func (c *TokenCache) fetchToken(ctx context.Context) (token string, expireSecs int, err error) {
	body, _ := json.Marshal(AppAccessTokenRequest{
		AppID:     c.appID,
		AppSecret: c.appSecret,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	var result AppAccessTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", 0, err
	}
	if result.Code != 0 {
		return "", 0, fmt.Errorf("feishu: token API error %d: %s", result.Code, result.Msg)
	}
	return result.AppAccessToken, result.Expire, nil
}
