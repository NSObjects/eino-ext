/*
 * Copyright 2024 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package ddgsearch

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type rateLimitTestTransport func(*http.Request) (*http.Response, error)

func (f rateLimitTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type rateLimitTestBody struct {
	io.Reader
	closes int
}

func (b *rateLimitTestBody) Close() error {
	b.closes++
	return nil
}

func TestRateLimitResponseBodiesClosed(t *testing.T) {
	for _, tc := range []struct {
		name       string
		maxRetries int
		statuses   []int
		success    bool
	}{
		{name: "no retry", statuses: []int{429}},
		{name: "exhausted retries", maxRetries: 1, statuses: []int{429, 429}},
		{name: "retry succeeds", maxRetries: 1, statuses: []int{429, 200}, success: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			var bodies []*rateLimitTestBody
			transport := rateLimitTestTransport(func(req *http.Request) (*http.Response, error) {
				for i, body := range bodies {
					if body.closes != 1 {
						t.Errorf("body %d closed %d times before next attempt", i, body.closes)
					}
				}
				i := len(bodies)
				if i >= len(tc.statuses) {
					t.Fatalf("unexpected retry %d", i)
				}
				payload := "rate limited"
				if tc.statuses[i] == http.StatusOK {
					payload = `{"results":[{"t":"result","u":"https://example.com","a":"description"}],"noResults":false}`
				}
				b := &rateLimitTestBody{Reader: strings.NewReader(payload)}
				bodies = append(bodies, b)
				return &http.Response{StatusCode: tc.statuses[i], Body: b, Header: make(http.Header)}, nil
			})
			client := &DDGS{
				client: &http.Client{Transport: transport},
				config: &Config{MaxRetries: tc.maxRetries},
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com", nil)
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.sendRequestWithRetry(ctx, req, &SearchParams{MaxResults: 1})
			if tc.success {
				if err != nil || result == nil || len(result.Results) != 1 || result.Results[0].Title != "result" {
					t.Fatalf("expected parsed result after retry: %v, %v", result, err)
				}
			} else if !errors.Is(err, ErrRateLimit) {
				t.Fatalf("expected rate limit error, got %v", err)
			}
			if len(bodies) != len(tc.statuses) {
				t.Fatalf("got %d attempts, want %d", len(bodies), len(tc.statuses))
			}
			for i, body := range bodies {
				if body.closes != 1 {
					t.Errorf("body %d closed %d times, want exactly once", i, body.closes)
				}
			}
		})
	}
}
