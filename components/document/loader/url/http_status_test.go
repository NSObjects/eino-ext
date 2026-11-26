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

package url

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
)

type statusTransportFunc func(*http.Request) (*http.Response, error)

func (f statusTransportFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type statusTrackingBody struct {
	reader io.Reader
	reads  int
	closes int
}

func (b *statusTrackingBody) Read(p []byte) (int, error) {
	b.reads++
	return b.reader.Read(p)
}

func (b *statusTrackingBody) Close() error {
	b.closes++
	return nil
}

func TestLoadHTTPStatusAndBodyOwnership(t *testing.T) {
	for _, status := range []int{200, 201, 204, 206, 302, 304, 401, 404, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			success := status >= 200 && status < 300
			body := &statusTrackingBody{reader: strings.NewReader("private-response-body")}
			if status == http.StatusNoContent {
				body.reader = strings.NewReader("")
			}
			var parsed int
			var events []string
			var callbackErr error
			handler := callbacks.NewHandlerBuilder().
				OnStartFn(func(ctx context.Context, _ *callbacks.RunInfo, _ callbacks.CallbackInput) context.Context {
					events = append(events, "start")
					return ctx
				}).
				OnEndFn(func(ctx context.Context, _ *callbacks.RunInfo, _ callbacks.CallbackOutput) context.Context {
					events = append(events, "end")
					return ctx
				}).
				OnErrorFn(func(ctx context.Context, _ *callbacks.RunInfo, err error) context.Context {
					events = append(events, "error")
					callbackErr = err
					return ctx
				}).Build()
			ctx := callbacks.InitCallbacks(context.Background(), &callbacks.RunInfo{}, handler)
			l, err := NewLoader(ctx, &LoaderConfig{
				Client: &http.Client{
					Transport: statusTransportFunc(func(req *http.Request) (*http.Response, error) {
						return &http.Response{StatusCode: status, Header: make(http.Header), Body: body, Request: req}, nil
					}),
				},
				Parser: &MockParser{mock: func(r io.Reader) ([]*schema.Document, error) {
					parsed++
					data, err := io.ReadAll(r)
					if err != nil {
						return nil, err
					}
					return []*schema.Document{{Content: string(data)}}, nil
				}},
			})
			require.NoError(t, err)
			docs, err := l.Load(ctx, document.Source{URI: "https://example.invalid/document"})
			require.Equal(t, 1, body.closes)
			if success {
				require.NoError(t, err)
				require.Len(t, docs, 1)
				require.Equal(t, 1, parsed)
				require.Equal(t, []string{"start", "end"}, events)
			} else {
				require.ErrorContains(t, err, fmt.Sprintf("unexpected HTTP status code %d", status))
				require.NotContains(t, err.Error(), "private-response-body")
				require.Nil(t, docs)
				require.Zero(t, parsed)
				require.Zero(t, body.reads)
				require.Equal(t, []string{"start", "error"}, events)
				require.Equal(t, err, callbackErr)
			}
		})
	}
}

func TestLoadFollowsRedirectAccordingToClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "final content")
	}))
	defer server.Close()
	for _, follow := range []bool{true, false} {
		t.Run(fmt.Sprint(follow), func(t *testing.T) {
			client := &http.Client{}
			if !follow {
				client.CheckRedirect = func(*http.Request, []*http.Request) error {
					return http.ErrUseLastResponse
				}
			}
			l, err := NewLoader(context.Background(), &LoaderConfig{
				Client: client,
				Parser: &MockParser{mock: func(r io.Reader) ([]*schema.Document, error) {
					data, err := io.ReadAll(r)
					if err != nil {
						return nil, err
					}
					return []*schema.Document{{Content: string(data)}}, nil
				}},
			})
			require.NoError(t, err)
			docs, err := l.Load(context.Background(), document.Source{URI: server.URL + "/redirect"})
			if follow {
				require.NoError(t, err)
				require.Len(t, docs, 1)
				require.Equal(t, "final content", docs[0].Content)
			} else {
				require.ErrorContains(t, err, "unexpected HTTP status code 302")
				require.Nil(t, docs)
			}
		})
	}
}
