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
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/elastic/go-elasticsearch/v9"
	"github.com/elastic/go-elasticsearch/v9/esutil"
)

func TestStoreRequiresCompleteBulkAcknowledgment(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"both acknowledged", `{"errors":false,"items":[{"index":{"status":201}},{"index":{"status":200}}]}`, ""},
		{"one missing", `{"errors":false,"items":[{"index":{"status":201}}]}`, "incomplete bulk acknowledgment: expected 2, got 1"},
		{"both missing", `{"errors":false,"items":[]}`, "incomplete bulk acknowledgment: expected 2, got 0"},
		{"invalid status", `{"errors":false,"items":[{"index":{"status":201}},{"index":{}}]}`, "invalid bulk success status 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Elastic-Product", "Elasticsearch")
				w.Header().Set("Content-Type", "application/json")
				if !strings.HasSuffix(r.URL.Path, "/_bulk") {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				data, err := io.ReadAll(r.Body)
				if err != nil || strings.Count(string(data), "\n") != 4 {
					t.Errorf("expected two NDJSON index operations: err=%v", err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				_, _ = io.WriteString(w, tc.body)
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
				Client: client,
				Index:  "test",
				DocumentToFields: func(_ context.Context, doc *schema.Document) (map[string]FieldValue, error) {
					return map[string]FieldValue{"content": {Value: doc.Content}}, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			idx.newBulkIndexer = func(cfg esutil.BulkIndexerConfig) (esutil.BulkIndexer, error) {
				cfg.NumWorkers = 1
				cfg.FlushBytes = 1 << 20
				cfg.FlushInterval = time.Hour
				return esutil.NewBulkIndexer(cfg)
			}
			ids, err := idx.Store(ctx, []*schema.Document{
				{ID: "1", Content: "first"},
				{ID: "2", Content: "second"},
			})
			if tc.want == "" {
				if err != nil || len(ids) != 2 || ids[0] != "1" || ids[1] != "2" {
					t.Fatalf("expected both IDs, got %v, err=%v", ids, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) || ids != nil {
				t.Fatalf("expected %q and nil IDs, got %v, err=%v", tc.want, ids, err)
			}
		})
	}
}
