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
	"net/http/httptest"
	"testing"

	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/components/document/parser"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
)

func TestLoadForwardsOptionsToRequestBuilder(t *testing.T) {
	const token = "test-option"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test-Option") != token {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, "wrong option")
			return
		}
		_, _ = io.WriteString(w, "expected")
	}))
	defer server.Close()

	ctx := context.Background()
	loader, err := NewLoader(ctx, &LoaderConfig{
		RequestBuilder: func(ctx context.Context, src document.Source, opts ...document.LoaderOption) (*http.Request, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URI, nil)
			if err != nil {
				return nil, err
			}
			// Common options must also reach the builder, not only Parse.
			common := document.GetLoaderCommonOptions(nil, opts...)
			require.Len(t, common.ParserOptions, 2)
			req.Header.Set("X-Test-Option", token)
			return req, nil
		},
		Parser: &MockParser{mock: func(r io.Reader) ([]*schema.Document, error) {
			data, err := io.ReadAll(r)
			if err != nil {
				return nil, err
			}
			return []*schema.Document{{Content: string(data)}}, nil
		}},
	})
	require.NoError(t, err)
	docs, err := loader.Load(ctx, document.Source{URI: server.URL},
		document.WithParserOptions(parser.WithURI("first"), parser.WithURI("second")))
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.Equal(t, "expected", docs[0].Content)
}

func TestLoadRejectsInvalidBuilderResult(t *testing.T) {
	builderErr := errors.New("builder failed")
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "nil request"},
		{name: "builder error", err: builderErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loader, err := NewLoader(context.Background(), &LoaderConfig{
				RequestBuilder: func(context.Context, document.Source, ...document.LoaderOption) (*http.Request, error) {
					return nil, tc.err
				},
			})
			require.NoError(t, err)
			docs, err := loader.Load(context.Background(), document.Source{URI: "http://example.invalid"})
			require.Nil(t, docs)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
			} else {
				require.ErrorContains(t, err, "nil request")
			}
		})
	}
}

func TestLoadValidatesParserBeforeRequest(t *testing.T) {
	called := false
	l := &Loader{conf: &LoaderConfig{
		RequestBuilder: func(context.Context, document.Source, ...document.LoaderOption) (*http.Request, error) {
			called = true
			return nil, errors.New("unexpected builder call")
		},
	}}
	docs, err := l.Load(context.Background(), document.Source{URI: "http://example.invalid"})
	require.ErrorContains(t, err, "parser is nil")
	require.Nil(t, docs)
	require.False(t, called)
}
