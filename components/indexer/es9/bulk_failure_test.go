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
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/schema"
	"github.com/elastic/go-elasticsearch/v9"
	"github.com/elastic/go-elasticsearch/v9/esutil"
)

func TestStoreReportsActualBulkResponseFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"success", 200, `{"errors":false,"items":[{"index":{"status":201}}]}`, ""},
		{"item failure", 200, `{"errors":true,"items":[{"index":{"status":400,"error":{"type":"mapper_parsing_exception","reason":"invalid field"}}}]}`, "mapper_parsing_exception"},
		{"request failure", 400, `{"error":{"type":"bad_request"},"status":400}`, "bulk write failed"},
		{"decode failure", 200, `not json`, "bulk write failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Elastic-Product", "Elasticsearch")
				w.Header().Set("Content-Type", "application/json")
				if !strings.HasSuffix(r.URL.Path, "/_bulk") {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
					return
				}
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()

			client, err := elasticsearch.NewClient(elasticsearch.Config{
				Addresses: []string{server.URL},
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			idx, err := NewIndexer(ctx, &IndexerConfig{
				Client: client,
				Index:  "test",
				DocumentToFields: func(_ context.Context, d *schema.Document) (map[string]FieldValue, error) {
					return map[string]FieldValue{"content": {Value: d.Content}}, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			// Use the real SDK worker and HTTP client, with one worker to
			// avoid allocating the SDK's large default buffer per CPU.
			idx.newBulkIndexer = func(cfg esutil.BulkIndexerConfig) (esutil.BulkIndexer, error) {
				cfg.NumWorkers = 1
				return esutil.NewBulkIndexer(cfg)
			}
			ids, err := idx.Store(ctx, []*schema.Document{{ID: "1", Content: "test"}})
			if tc.want == "" {
				if err != nil || len(ids) != 1 || ids[0] != "1" {
					t.Fatalf("success: ids=%v err=%v", ids, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) || ids != nil {
				t.Fatalf("expected failure %q with nil IDs, got ids=%v err=%v", tc.want, ids, err)
			}
		})
	}
}

func TestBulkAddConcurrentFailureCallbacks(t *testing.T) {
	want := errors.New("async write failure")
	var cfg esutil.BulkIndexerConfig
	var item esutil.BulkIndexerItem
	bi := &MockBulkIndexer{
		AddFunc: func(_ context.Context, it esutil.BulkIndexerItem) error {
			item = it
			return nil
		},
		CloseFunc: func(ctx context.Context) error {
			var wg sync.WaitGroup
			for j := 0; j < 20; j++ {
				wg.Add(2)
				go func() {
					defer wg.Done()
					cfg.OnError(ctx, want)
				}()
				go func() {
					defer wg.Done()
					item.OnFailure(ctx, item, esutil.BulkIndexerResponseItem{}, want)
				}()
			}
			wg.Wait()
			return nil
		},
	}
	i := &Indexer{
		newBulkIndexer: func(c esutil.BulkIndexerConfig) (esutil.BulkIndexer, error) {
			cfg = c
			return bi, nil
		},
		config: &IndexerConfig{
			Index: "test",
			DocumentToFields: func(_ context.Context, d *schema.Document) (map[string]FieldValue, error) {
				return map[string]FieldValue{"content": {Value: d.Content}}, nil
			},
		},
	}
	err := i.bulkAdd(context.Background(), []*schema.Document{{ID: "1"}}, &indexer.Options{})
	if !errors.Is(err, want) {
		t.Fatalf("expected wrapped asynchronous error, got %v", err)
	}
}
