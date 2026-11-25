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

package openai

import (
	"slices"
	"sync"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	"github.com/stretchr/testify/require"
	orderedmap "github.com/wk8/go-ordered-map/v2"
)

// TestToToolsConcurrentSharedToolInfo is a regression test for the data race
// in toTools: schema.ParamsOneOf.ToJSONSchema() returns the shared internal
// schema, so sorting it in place corrupts Required under concurrency
// (intermittent 400 "non-unique elements", see issue #1005).
func TestToToolsConcurrentSharedToolInfo(t *testing.T) {
	newProps := func(kv ...any) *orderedmap.OrderedMap[string, *jsonschema.Schema] {
		m := orderedmap.New[string, *jsonschema.Schema]()
		for i := 0; i+1 < len(kv); i += 2 {
			m.Set(kv[i].(string), kv[i+1].(*jsonschema.Schema))
		}
		return m
	}

	shared := &jsonschema.Schema{
		Type:     "object",
		Required: []string{"limit", "file_path", "offset"}, // deliberately unsorted
		Properties: newProps(
			"options", &jsonschema.Schema{
				Type:     "object",
				Required: []string{"zulu", "alpha"}, // deliberately unsorted
			},
			"tags", &jsonschema.Schema{
				Type: "array",
				Items: &jsonschema.Schema{
					Type:     "object",
					Required: []string{"zeta", "eta"}, // deliberately unsorted
				},
			},
		),
	}

	ti := &schema.ToolInfo{
		Name:        "read_file",
		Desc:        "reads a file",
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(shared),
	}

	wantRoot := []string{"file_path", "limit", "offset"}
	wantNested := []string{"alpha", "zulu"}
	wantItems := []string{"eta", "zeta"}

	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				tools, err := toTools([]*schema.ToolInfo{ti})
				if err != nil {
					t.Errorf("toTools: %v", err)
					return
				}
				got := tools[0].Function.Parameters
				if !slices.Equal(got.Required, wantRoot) {
					t.Errorf("root Required corrupted: %v", got.Required)
					return
				}
				nested, _ := got.Properties.Get("options")
				if nested == nil || !slices.Equal(nested.Required, wantNested) {
					t.Errorf("nested Required corrupted: %v", nested)
					return
				}
				tags, _ := got.Properties.Get("tags")
				if tags == nil || tags.Items == nil || !slices.Equal(tags.Items.Required, wantItems) {
					t.Errorf("array items Required corrupted: %v", tags)
					return
				}
			}
		}()
	}
	wg.Wait()

	// The shared schema must be completely untouched.
	if !slices.Equal(shared.Required, []string{"limit", "file_path", "offset"}) {
		t.Errorf("shared schema Required was mutated: %v", shared.Required)
	}
	opts, _ := shared.Properties.Get("options")
	if opts == nil || !slices.Equal(opts.Required, []string{"zulu", "alpha"}) {
		t.Errorf("shared nested Required was mutated: %v", opts)
	}
	tags, _ := shared.Properties.Get("tags")
	if tags == nil || tags.Items == nil || !slices.Equal(tags.Items.Required, []string{"zeta", "eta"}) {
		t.Errorf("shared array items Required was mutated: %v", tags)
	}
}


func TestSortedToolSchemaPreservesOriginalTraversal(t *testing.T) {
	t.Run("nil and parameterless", func(t *testing.T) {
		require.Nil(t, sortedToolSchema(nil))
		tools, err := toTools([]*schema.ToolInfo{{Name: "ping"}})
		require.NoError(t, err)
		require.Nil(t, tools[0].Function.Parameters)
	})
	t.Run("object without properties", func(t *testing.T) {
		original := &jsonschema.Schema{Type: "object", Required: []string{"b", "a"}}
		got := sortedToolSchema(original)
		require.Equal(t, []string{"a", "b"}, got.Required)
		require.Equal(t, []string{"b", "a"}, original.Required)
		require.Nil(t, got.Properties)
	})
	t.Run("optional root retains existing sorting behavior", func(t *testing.T) {
		child := &jsonschema.Schema{Type: "object", Required: []string{"b", "a"}}
		root := &jsonschema.Schema{Type: "object", Properties: orderedmap.New[string, *jsonschema.Schema]()}
		root.Properties.Set("child", child)
		got := sortedToolSchema(root)
		nested, _ := got.Properties.Get("child")
		require.Equal(t, []string{"b", "a"}, nested.Required)
	})
	t.Run("shared nodes stay shared within the private copy", func(t *testing.T) {
		child := &jsonschema.Schema{Type: "object", Required: []string{"b", "a"}}
		root := &jsonschema.Schema{Type: "object", Required: []string{"z", "a"}, Properties: orderedmap.New[string, *jsonschema.Schema]()}
		root.Properties.Set("z", child)
		root.Properties.Set("a", child)
		got := sortedToolSchema(root)
		require.Equal(t, "z", got.Properties.Oldest().Key)
		first, _ := got.Properties.Get("z")
		second, _ := got.Properties.Get("a")
		require.Same(t, first, second)
		require.NotSame(t, child, first)
		require.Equal(t, []string{"a", "b"}, first.Required)
		require.Equal(t, []string{"b", "a"}, child.Required)
	})
}
