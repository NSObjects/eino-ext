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
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/cloudwego/eino-ext/components/document/parser/html"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/components/document/parser"
	"github.com/cloudwego/eino/schema"
)

var _ document.Loader = (*Loader)(nil)

const defaultHTTPTimeout = 30 * time.Second

// LoaderConfig is the config for url Loader.
type LoaderConfig struct {
	// optional, default: parser/html.
	Parser parser.Parser

	// Optional. If nil, a copy of http.DefaultClient is used with a 30-second
	// timeout when the default client has no positive timeout. A supplied
	// client is used as-is, including its timeout and redirect policy.
	Client *http.Client

	// optional, default GET uri.
	RequestBuilder func(ctx context.Context, source document.Source, opts ...document.LoaderOption) (*http.Request, error)

	// MaxResponseBytes limits the complete response body read from the client.
	// Non-positive values use the default of 10 MiB. Oversized responses fail
	// with ErrResponseTooLarge; partial documents are not returned.
	MaxResponseBytes int64
}

func defaultRequestBuilder(ctx context.Context, source document.Source, opts ...document.LoaderOption) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, "GET", source.URI, nil)
}

// NewLoader creates a new loader for url.
func NewLoader(ctx context.Context, conf *LoaderConfig) (*Loader, error) {
	if conf == nil {
		conf = &LoaderConfig{}
	}

	if conf.Parser == nil {
		p, err := html.NewParser(context.Background(), &html.Config{
			Selector: &html.BodySelector,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create default HTML parser: %w", err)
		}

		conf.Parser = p
	}
	if conf.Client == nil {
		// Copy client settings, not the transport: retain proxy, connection
		// pooling, cookie jar and redirect policy without changing the global
		// default client or a caller-provided client.
		client := *http.DefaultClient
		if client.Timeout <= 0 {
			client.Timeout = defaultHTTPTimeout
		}
		conf.Client = &client
	}
	if conf.RequestBuilder == nil {
		conf.RequestBuilder = defaultRequestBuilder
	}

	return &Loader{
		conf: conf,
	}, nil
}

// Loader is a loader for url.
type Loader struct {
	conf *LoaderConfig
}

func (l *Loader) Load(ctx context.Context, src document.Source, opts ...document.LoaderOption) (docs []*schema.Document, err error) {
	ctx = callbacks.EnsureRunInfo(ctx, l.GetType(), components.ComponentOfLoader)
	ctx = callbacks.OnStart(ctx, &document.LoaderCallbackInput{
		Source: src,
	})
	defer func() {
		if err != nil {
			_ = callbacks.OnError(ctx, err)
		}
	}()

	if l.conf == nil || l.conf.Parser == nil {
		return nil, errors.New("parser is nil")
	}

	var readerCloser io.ReadCloser
	readerCloser, err = l.load(ctx, src, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load content from uri [%s]: %w", src.URI, err)
	}
	closed := false
	defer func() {
		if !closed {
			// Preserve the original load or parse error on failure paths.
			_ = readerCloser.Close()
		}
	}()

	o := document.GetLoaderCommonOptions(&document.LoaderOptions{}, opts...)

	body := newLimitedResponseReader(readerCloser, l.conf.MaxResponseBytes)
	docs, err = l.conf.Parser.Parse(ctx, body, append([]parser.Option{parser.WithURI(src.URI)}, o.ParserOptions...)...)
	// A parser can replace or swallow an underlying read error. Preserve
	// the response failure even if parsing itself appears successful.
	if body.err != nil && body.err != io.EOF {
		return nil, fmt.Errorf("read response body of uri [%s]: %w", src.URI, body.err)
	}
	if err != nil {
		return nil, fmt.Errorf("parse content of uri [%s] err: %w", src.URI, err)
	}

	// A successful parser need not consume the whole response. Validate the
	// remainder before reporting success, using the same bounded reader.
	if _, err = io.Copy(io.Discard, body); err != nil {
		return nil, fmt.Errorf("read response body of uri [%s]: %w", src.URI, err)
	}

	// A successful Load must release its response before reporting success.
	// Mark it closed before calling Close so even a failure is never retried.
	closed = true
	if closeErr := readerCloser.Close(); closeErr != nil {
		return nil, fmt.Errorf("failed to close response body of uri [%s]: %w", src.URI, closeErr)
	}

	_ = callbacks.OnEnd(ctx, &document.LoaderCallbackOutput{
		Source: src,
		Docs:   docs,
	})

	return docs, nil
}

func (l *Loader) load(ctx context.Context, src document.Source, opts ...document.LoaderOption) (io.ReadCloser, error) {
	req, err := l.conf.RequestBuilder(ctx, src, opts...)
	if err != nil {
		return nil, err
	}

	if req == nil {
		return nil, errors.New("request builder returned a nil request")
	}

	resp, err := l.conf.Client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		// Do not parse error responses or expose their potentially sensitive
		// contents in errors. The loader owns the body after a successful Do.
		_ = resp.Body.Close()
		return nil, fmt.Errorf("unexpected HTTP status code %d", resp.StatusCode)
	}

	return resp.Body, nil
}

func (l *Loader) GetType() string {
	return "URLLoader"
}

func (l *Loader) IsCallbacksEnabled() bool {
	return true
}
