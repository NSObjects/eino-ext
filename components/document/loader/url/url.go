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

	"github.com/cloudwego/eino-ext/components/document/parser/html"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/components/document/parser"
	"github.com/cloudwego/eino/schema"
)

var _ document.Loader = (*Loader)(nil)

// LoaderConfig is the config for url Loader.
type LoaderConfig struct {
	// optional, default: parser/html.
	Parser parser.Parser

	// optional.
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
		conf.Client = http.DefaultClient
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
	defer readerCloser.Close()

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

	return resp.Body, nil
}

func (l *Loader) GetType() string {
	return "URLLoader"
}

func (l *Loader) IsCallbacksEnabled() bool {
	return true
}
