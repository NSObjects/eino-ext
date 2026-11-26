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
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
)

type limitReaderFunc func([]byte) (int, error)

func (f limitReaderFunc) Read(p []byte) (int, error) { return f(p) }

func TestLimitedResponseReaderBoundaries(t *testing.T) {
	for _, tc := range []struct {
		body  string
		limit int64
		want  string
		err   error
	}{
		{"", 3, "", nil},
		{"ab", 3, "ab", nil},
		{"abc", 3, "abc", nil},
		{"abcd", 3, "abc", ErrResponseTooLarge},
		{"hello", math.MaxInt64, "hello", nil},
	} {
		t.Run(fmt.Sprintf("%s/%d", tc.body, tc.limit), func(t *testing.T) {
			r := newLimitedResponseReader(strings.NewReader(tc.body), tc.limit)
			data, err := io.ReadAll(r)
			require.ErrorIs(t, err, tc.err)
			require.Equal(t, tc.want, string(data))
			_, err = r.Read(make([]byte, 1))
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
			} else {
				require.ErrorIs(t, err, io.EOF)
			}
		})
	}
	for _, limit := range []int64{0, -1} {
		require.Equal(t, defaultMaxResponseBytes, newLimitedResponseReader(strings.NewReader(""), limit).remaining)
	}
}

func TestLimitedResponseReaderTerminalReadAndNoProgress(t *testing.T) {
	t.Run("data with EOF at exact limit", func(t *testing.T) {
		calls := 0
		r := newLimitedResponseReader(limitReaderFunc(func(p []byte) (int, error) {
			calls++
			return copy(p, "abc"), io.EOF
		}), 3)
		data, err := io.ReadAll(r)
		require.NoError(t, err)
		require.Equal(t, "abc", string(data))
		require.Equal(t, 1, calls)
	})
	t.Run("temporary empty read during probe", func(t *testing.T) {
		calls := 0
		r := newLimitedResponseReader(limitReaderFunc(func(p []byte) (int, error) {
			calls++
			switch calls {
			case 1:
				return copy(p, "abc"), nil
			case 2:
				return 0, nil
			default:
				return 0, io.EOF
			}
		}), 3)
		data, err := io.ReadAll(r)
		require.NoError(t, err)
		require.Equal(t, "abc", string(data))
		require.Equal(t, 3, calls)
	})
	t.Run("no progress", func(t *testing.T) {
		r := newLimitedResponseReader(limitReaderFunc(func([]byte) (int, error) {
			return 0, nil
		}), 3)
		_, err := io.ReadAll(r)
		require.ErrorIs(t, err, io.ErrNoProgress)
	})
	t.Run("read error with data", func(t *testing.T) {
		wantErr := errors.New("broken response")
		r := newLimitedResponseReader(limitReaderFunc(func(p []byte) (int, error) {
			return copy(p, "ab"), wantErr
		}), 3)
		data, err := io.ReadAll(r)
		require.Equal(t, "ab", string(data))
		require.ErrorIs(t, err, wantErr)
		_, err = r.Read(make([]byte, 1))
		require.ErrorIs(t, err, wantErr)
	})
}

func TestLoadChecksEntireResponseDespiteParserBehavior(t *testing.T) {
	for _, size := range []int{3, 4} {
		for _, mode := range []string{"read_all", "ignore_error", "read_one", "no_read"} {
			t.Run(fmt.Sprintf("%d/%s", size, mode), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = io.WriteString(w, strings.Repeat("a", size))
				}))
				defer server.Close()
				l, err := NewLoader(context.Background(), &LoaderConfig{
					MaxResponseBytes: 3,
					Parser: &MockParser{mock: func(r io.Reader) ([]*schema.Document, error) {
						switch mode {
						case "read_all":
							_, err := io.ReadAll(r)
							if err != nil {
								return nil, err
							}
						case "ignore_error":
							_, _ = io.ReadAll(r)
						case "read_one":
							_, _ = r.Read(make([]byte, 1))
						}
						return []*schema.Document{{Content: "parsed"}}, nil
					}},
				})
				require.NoError(t, err)
				docs, err := l.Load(context.Background(), document.Source{URI: server.URL})
				if size > 3 {
					require.ErrorIs(t, err, ErrResponseTooLarge)
					require.Nil(t, docs)
				} else {
					require.NoError(t, err)
					require.Len(t, docs, 1)
				}
			})
		}
	}
}

func TestLoadLimitsDecompressedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		zw := gzip.NewWriter(w)
		_, _ = io.WriteString(zw, strings.Repeat("a", 4096))
		_ = zw.Close()
	}))
	defer server.Close()

	// The compressed payload fits within 128 bytes, but its decoded body
	// does not. Verify the real HTTP transport's automatic decompression.
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	l, err := NewLoader(context.Background(), &LoaderConfig{
		Client:           &http.Client{Transport: transport},
		MaxResponseBytes: 128,
		Parser: &MockParser{mock: func(r io.Reader) ([]*schema.Document, error) {
			_, _ = io.ReadAll(r)
			return []*schema.Document{{Content: "ignored read error"}}, nil
		}},
	})
	require.NoError(t, err)
	docs, err := l.Load(context.Background(), document.Source{URI: server.URL})
	require.ErrorIs(t, err, ErrResponseTooLarge)
	require.Nil(t, docs)
}

func TestLoadRejectsTruncatedHTTPBodyDespiteSuccessfulParser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, "short")
	}))
	defer server.Close()
	for _, mode := range []string{"ignore_error", "no_read"} {
		t.Run(mode, func(t *testing.T) {
			l, err := NewLoader(context.Background(), &LoaderConfig{
				MaxResponseBytes: 1024,
				Parser: &MockParser{mock: func(r io.Reader) ([]*schema.Document, error) {
					if mode == "ignore_error" {
						_, _ = io.ReadAll(r)
					}
					return []*schema.Document{{Content: "partial"}}, nil
				}},
			})
			require.NoError(t, err)
			docs, err := l.Load(context.Background(), document.Source{URI: server.URL})
			require.ErrorIs(t, err, io.ErrUnexpectedEOF)
			require.Nil(t, docs)
		})
	}
}

func TestLoadClosesBodyOnLimitAndReadFailure(t *testing.T) {
	readErr := errors.New("read failure")
	for _, tc := range []struct {
		name   string
		reader io.Reader
		want   error
	}{
		{"overflow", strings.NewReader("abcd"), ErrResponseTooLarge},
		{"read error", limitReaderFunc(func(p []byte) (int, error) { return copy(p, "ab"), readErr }), readErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &statusTrackingBody{reader: tc.reader}
			l, err := NewLoader(context.Background(), &LoaderConfig{
				MaxResponseBytes: 3,
				Client: &http.Client{Transport: statusTransportFunc(func(req *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, Request: req}, nil
				})},
				Parser: &MockParser{mock: func(r io.Reader) ([]*schema.Document, error) {
					_, _ = io.ReadAll(r)
					return []*schema.Document{{Content: "partial"}}, nil
				}},
			})
			require.NoError(t, err)
			docs, err := l.Load(context.Background(), document.Source{URI: "https://example.invalid/document"})
			require.ErrorIs(t, err, tc.want)
			require.Nil(t, docs)
			require.Equal(t, 1, body.closes)
		})
	}
}

func TestLoadRemainderHonorsClientTimeout(t *testing.T) {
	// Flush a valid response, then hold its body open until the client cancels.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "partial")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()

	// A short positive global timeout must survive the default-client copy.
	original := http.DefaultClient
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	http.DefaultClient = &http.Client{Transport: transport, Timeout: 2 * time.Second}
	defer func() { http.DefaultClient = original }()

	parsed := false
	l, err := NewLoader(context.Background(), &LoaderConfig{
		MaxResponseBytes: 1024,
		Parser: &MockParser{mock: func(io.Reader) ([]*schema.Document, error) {
			parsed = true
			return []*schema.Document{{Content: "partial"}}, nil
		}},
	})
	require.NoError(t, err)
	require.Equal(t, 2*time.Second, l.conf.Client.Timeout)
	// Keep a separate outer deadline so even a timeout regression cannot hang CI.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	docs, err := l.Load(ctx, document.Source{URI: server.URL})
	require.True(t, parsed, "parser must succeed before remainder validation")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Nil(t, docs)
	require.NoError(t, ctx.Err(), "the HTTP client timeout, not the outer deadline, must end the read")
}
