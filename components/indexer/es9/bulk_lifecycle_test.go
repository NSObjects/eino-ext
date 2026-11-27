/*
 * Copyright 2025 CloudWeGo Authors
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

package es9

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/schema"
	"github.com/elastic/go-elasticsearch/v9"
	"github.com/elastic/go-elasticsearch/v9/esutil"
)

func TestBulkAddAlwaysJoinsWorkers(t *testing.T) {
	primary := errors.New("primary failure")
	cleanup := errors.New("close failure")
	for _, mode := range []string{"success", "mapping", "embedding", "add", "close", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var cfg esutil.BulkIndexerConfig
			created, closed := 0, 0
			bi := &MockBulkIndexer{
				AddFunc: func(context.Context, esutil.BulkIndexerItem) error {
					if mode == "add" {
						return primary
					}
					return nil
				},
				CloseFunc: func(joinCtx context.Context) error {
					closed++
					if joinCtx.Err() != nil {
						t.Error("worker join must not use a canceled context")
					}
					flushErr := cfg.OnFlushStart(context.Background()).Err()
					if mode == "mapping" || mode == "embedding" || mode == "add" || mode == "canceled" {
						if !errors.Is(flushErr, context.Canceled) {
							t.Errorf("abort must cancel flush before joining: %v", flushErr)
						}
						return cleanup
					}
					if flushErr != nil {
						t.Errorf("normal close must allow final flush: %v", flushErr)
					}
					if mode == "close" {
						return cleanup
					}
					return nil
				},
			}
			i := &Indexer{
				newBulkIndexer: func(c esutil.BulkIndexerConfig) (esutil.BulkIndexer, error) {
					created++
					cfg = c
					return bi, nil
				},
				config: &IndexerConfig{
					Index:     "test",
					BatchSize: 1,
					DocumentToFields: func(context.Context, *schema.Document) (map[string]FieldValue, error) {
						switch mode {
						case "mapping":
							return nil, primary
						case "embedding":
							return map[string]FieldValue{"content": {Value: "text", EmbedKey: "vector"}}, nil
						case "canceled":
							cancel()
						}
						return map[string]FieldValue{"content": {Value: "text"}}, nil
					},
				},
			}
			err := i.bulkAdd(ctx, []*schema.Document{{ID: "1"}}, &indexer.Options{
				Embedding: &mockEmbedding{err: primary},
			})
			if created != 1 || closed != 1 {
				t.Fatalf("created=%d closed=%d", created, closed)
			}
			var want error
			switch mode {
			case "mapping", "embedding", "add":
				want = primary
			case "close":
				want = cleanup
			case "canceled":
				want = context.Canceled
			}
			if !errors.Is(err, want) {
				t.Fatalf("want %v, got %v", want, err)
			}
			if cfg.OnFlushStart(context.Background()).Err() == nil {
				t.Fatal("flush context still live after bulkAdd returned")
			}
		})
	}
}

func TestBulkAddCanceledBeforeCreation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	i := &Indexer{
		newBulkIndexer: func(esutil.BulkIndexerConfig) (esutil.BulkIndexer, error) {
			t.Fatal("must not create workers for canceled call")
			return nil, nil
		},
		config: &IndexerConfig{},
	}
	if err := i.bulkAdd(ctx, nil, &indexer.Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled, got %v", err)
	}
}

func TestStoreCancelsRealBulkRequestAndJoins(t *testing.T) {
	started := make(chan struct{})
	ended := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/_bulk") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		defer close(ended)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	client, err := elasticsearch.NewClient(elasticsearch.Config{
		Addresses:    []string{server.URL},
		DisableRetry: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	idx, err := NewIndexer(ctx, &IndexerConfig{
		Client: client,
		Index:  "test",
		DocumentToFields: func(context.Context, *schema.Document) (map[string]FieldValue, error) {
			return map[string]FieldValue{"content": {Value: "text"}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	idx.newBulkIndexer = func(cfg esutil.BulkIndexerConfig) (esutil.BulkIndexer, error) {
		cfg.NumWorkers = 1
		return esutil.NewBulkIndexer(cfg)
	}
	done := make(chan error, 1)
	go func() {
		ids, err := idx.Store(ctx, []*schema.Document{{ID: "1"}})
		if ids != nil {
			done <- errors.New("canceled Store returned IDs")
			return
		}
		done <- err
	}()
	select {
	case <-started:
		cancel()
	case <-ctx.Done():
		t.Fatal("bulk request did not start")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want canceled, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Store did not return after cancellation")
	}
	select {
	case <-ended:
	case <-time.After(3 * time.Second):
		t.Fatal("server did not observe canceled request")
	}
}
