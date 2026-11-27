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
	"sync/atomic"
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
				AddFunc: func(ctx context.Context, item esutil.BulkIndexerItem) error {
					if mode == "add" {
						return primary
					}
					item.OnSuccess(ctx, item, esutil.BulkIndexerResponseItem{Status: 201})
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

func TestStoreAbortsQueuedBulkItemsOnLaterMappingFailure(t *testing.T) {
	primary := errors.New("later mapping failed")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"unexpected bulk request","status":400}`)
	}))
	defer server.Close()
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
		Client:    client,
		Index:     "test",
		BatchSize: 1,
		Embedding: &mockEmbedding{size: []int{1}, mockVector: []float64{1}},
		DocumentToFields: func(_ context.Context, doc *schema.Document) (map[string]FieldValue, error) {
			if doc.ID == "3" {
				return nil, primary
			}
			return map[string]FieldValue{"content": {Value: doc.Content, EmbedKey: "vector"}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var bulk esutil.BulkIndexer
	var flushes, canceledFlushes atomic.Int32
	idx.newBulkIndexer = func(cfg esutil.BulkIndexerConfig) (esutil.BulkIndexer, error) {
		// Force the first queued item to remain buffered until Close.
		cfg.NumWorkers = 1
		cfg.FlushInterval = time.Hour
		cfg.FlushBytes = 1 << 20
		cfg.OnFlushEnd = func(flushCtx context.Context) {
			flushes.Add(1)
			if errors.Is(flushCtx.Err(), context.Canceled) {
				canceledFlushes.Add(1)
			}
		}
		var err error
		bulk, err = esutil.NewBulkIndexer(cfg)
		return bulk, err
	}
	// Mapping doc 2 fills a new batch and enqueues doc 1. Doc 3 fails
	// before doc 2 is enqueued. A canceled final flush must still finish
	// the worker lifecycle without transmitting the previously queued item.
	ids, err := idx.Store(ctx, []*schema.Document{
		{ID: "1", Content: "first"},
		{ID: "2", Content: "second"},
		{ID: "3", Content: "third"},
	})
	if !errors.Is(err, primary) || ids != nil {
		t.Fatalf("expected original mapping failure, got ids=%v err=%v", ids, err)
	}
	if bulk == nil || bulk.Stats().NumAdded != 1 {
		t.Fatal("test must enqueue one real SDK item before the failure")
	}
	if flushes.Load() != 1 || canceledFlushes.Load() != 1 {
		t.Fatalf("expected joined canceled final flush, got total=%d canceled=%d", flushes.Load(), canceledFlushes.Load())
	}
	if requests.Load() != 0 {
		t.Fatalf("abort transmitted queued items: requests=%d", requests.Load())
	}
	if ctx.Err() != nil {
		t.Fatalf("aborting workers must not cancel caller's context: %v", ctx.Err())
	}
}
