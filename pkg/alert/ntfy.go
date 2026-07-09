package alert

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// NtfyClient posts plain-text push notifications to an ntfy.sh (or
// self-hosted ntfy) topic URL. A zero-value topicURL makes every Send a
// no-op, so services can wire this unconditionally.
type NtfyClient struct {
	topicURL  string
	authToken string
	client    *http.Client
}

// NewNtfyClient builds a client for topicURL. authToken, if set, is sent as
// an ntfy access-token Bearer header (https://docs.ntfy.sh/publish/#access-tokens)
// so the topic name alone isn't sufficient for anyone to read the stream.
// Public ntfy.sh topics are world-readable by default — self-host ntfy or
// set authToken before pointing this at anything but a throwaway test topic.
func NewNtfyClient(topicURL, authToken string) *NtfyClient {
	return &NtfyClient{
		topicURL:  strings.TrimSpace(topicURL),
		authToken: strings.TrimSpace(authToken),
		client:    &http.Client{Timeout: 5 * time.Second},
	}
}

// Send fires a push notification. Errors are returned rather than logged so
// callers can decide whether a failed alert is itself worth logging.
func (c *NtfyClient) Send(ctx context.Context, title, message string, priority int, tags ...string) error {
	if c == nil || c.topicURL == "" {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.topicURL, strings.NewReader(message))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("Title", title)
	req.Header.Set("Priority", strconv.Itoa(priority))
	if len(tags) > 0 {
		req.Header.Set("Tags", strings.Join(tags, ","))
	}
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}
