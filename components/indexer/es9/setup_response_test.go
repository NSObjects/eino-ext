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
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/elastic/go-elasticsearch/v9"
)

type setupResponseBody struct {
	reads, closes int
}

func (b *setupResponseBody) Read([]byte) (int, error) {
	b.reads++
	if b.closes > 0 {
		return 0, fmt.Errorf("read after close")
	}
	return 0, io.EOF
}

func (b *setupResponseBody) Close() error {
	b.closes++
	return nil
}

func TestNewIndexerClosesSetupResponsesOnce(t *testing.T) {
	for _, tc := range []struct {
		name         string
		existsStatus int
		createStatus int
		want         string
	}{
		{"exists", 200, 0, ""},
		{"existence failure", 403, 0, "check index existence failed, HTTP status: 403"},
		{"create success", 404, 200, ""},
		{"create failure", 404, 400, "create index failed, HTTP status: 400"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			existsBody := &setupResponseBody{}
			createBody := &setupResponseBody{}
			transport := &mockTransportCreation{
				existsResponse: &http.Response{
					StatusCode: tc.existsStatus,
					Body:       existsBody,
					Header:     http.Header{"X-Elastic-Product": []string{"Elasticsearch"}},
				},
			}
			if tc.createStatus != 0 {
				transport.createResponse = &http.Response{
					StatusCode: tc.createStatus,
					Body:       createBody,
					Header:     http.Header{"X-Elastic-Product": []string{"Elasticsearch"}},
				}
			}
			client, err := elasticsearch.NewClient(elasticsearch.Config{
				Transport:    transport,
				DisableRetry: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			idx, err := NewIndexer(context.Background(), &IndexerConfig{
				Client:    client,
				Index:     "test",
				IndexSpec: &IndexSpec{},
				DocumentToFields: func(context.Context, *schema.Document) (map[string]FieldValue, error) {
					return nil, nil
				},
			})
			if tc.want == "" {
				if idx == nil || err != nil {
					t.Fatalf("expected success, got indexer=%v err=%v", idx, err)
				}
			} else if idx != nil || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q, got indexer=%v err=%v", tc.want, idx, err)
			}
			if existsBody.closes != 1 || existsBody.reads != 0 {
				t.Fatalf("existence body reads=%d closes=%d", existsBody.reads, existsBody.closes)
			}
			wantCreates := 0
			if tc.createStatus != 0 {
				wantCreates = 1
			}
			if createBody.closes != wantCreates || createBody.reads != 0 {
				t.Fatalf("create body reads=%d closes=%d", createBody.reads, createBody.closes)
			}
			if !transport.existsCalled || transport.createCalled != (wantCreates == 1) {
				t.Fatal("unexpected index setup request sequence")
			}
		})
	}
}
