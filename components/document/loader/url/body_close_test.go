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
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
)

type closeErrorBody struct {
	statusTrackingBody
	err error
}

func (b *closeErrorBody) Close() error {
	b.closes++
	return b.err
}

func TestLoadClosesBodyBeforeTerminalCallback(t *testing.T) {
	parseErr := errors.New("parse failure")
	closeErr := errors.New("close failure")
	for _, tc := range []struct {
		name       string
		parseErr   error
		closeErr   error
		wantErr    error
		bodyReader io.Reader
		limit      int64
	}{
		{name: "success"},
		{name: "close failure", closeErr: closeErr, wantErr: closeErr},
		{name: "parse failure", parseErr: parseErr, wantErr: parseErr},
		{name: "both fail", parseErr: parseErr, closeErr: closeErr, wantErr: parseErr},
		{name: "overflow and close fail", closeErr: closeErr, wantErr: ErrResponseTooLarge, limit: 3},
		{name: "read and close fail", closeErr: closeErr, wantErr: io.ErrUnexpectedEOF, bodyReader: limitReaderFunc(func([]byte) (int, error) { return 0, io.ErrUnexpectedEOF })},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &closeErrorBody{
				statusTrackingBody: statusTrackingBody{reader: strings.NewReader("content")},
				err:                tc.closeErr,
			}
			if tc.bodyReader != nil {
				body.reader = tc.bodyReader
			}
			var events []string
			var callbackErr error
			handler := callbacks.NewHandlerBuilder().
				OnStartFn(func(ctx context.Context, _ *callbacks.RunInfo, _ callbacks.CallbackInput) context.Context {
					events = append(events, "start")
					require.Zero(t, body.closes)
					return ctx
				}).
				OnEndFn(func(ctx context.Context, _ *callbacks.RunInfo, _ callbacks.CallbackOutput) context.Context {
					events = append(events, "end")
					require.Equal(t, 1, body.closes)
					return ctx
				}).
				OnErrorFn(func(ctx context.Context, _ *callbacks.RunInfo, err error) context.Context {
					events = append(events, "error")
					callbackErr = err
					require.Equal(t, 1, body.closes)
					return ctx
				}).Build()
			ctx := callbacks.InitCallbacks(context.Background(), &callbacks.RunInfo{}, handler)
			l, err := NewLoader(ctx, &LoaderConfig{
				MaxResponseBytes: tc.limit,
				Client: &http.Client{Transport: statusTransportFunc(func(req *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, Request: req}, nil
				})},
				Parser: &MockParser{mock: func(r io.Reader) ([]*schema.Document, error) {
					data, err := io.ReadAll(r)
					require.Zero(t, body.closes)
					if err != nil {
						return nil, err
					}
					if tc.parseErr != nil {
						return nil, tc.parseErr
					}
					return []*schema.Document{{Content: string(data)}}, nil
				}},
			})
			require.NoError(t, err)
			docs, err := l.Load(ctx, document.Source{URI: "https://example.invalid/document"})
			require.Equal(t, 1, body.closes)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Nil(t, docs)
				require.Equal(t, []string{"start", "error"}, events)
				require.Equal(t, err, callbackErr)
				if tc.wantErr != closeErr {
					require.NotErrorIs(t, err, closeErr)
				}
			} else {
				require.NoError(t, err)
				require.Len(t, docs, 1)
				require.Equal(t, "content", docs[0].Content)
				require.Equal(t, []string{"start", "end"}, events)
			}
		})
	}
}
