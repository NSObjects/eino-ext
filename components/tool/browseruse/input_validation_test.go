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

package browseruse

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInvokableRunRejectsInvalidJSON(t *testing.T) {
	ctx := context.Background()
	b := &Tool{ctx: ctx}
	for _, input := range []string{
		"",
		"{",
		`{"action":"web_search","query":123}`,
		`{"action":"click_element","index":"invalid"}`,
		`{"action":"go_to_url","url":"https://example.com"} trailing`,
	} {
		t.Run(input, func(t *testing.T) {
			output, err := b.InvokableRun(ctx, input)
			require.ErrorContains(t, err, "invalid browser tool arguments")
			require.NotNil(t, err)
			require.Empty(t, output)
		})
	}
}

func TestExecuteRejectsNilParams(t *testing.T) {
	b := &Tool{}
	result, err := b.Execute(nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.Error)
	require.Empty(t, result.Output)
}

func TestExecuteClickElementInvalidIndex(t *testing.T) {
	b := &Tool{elements: make([]ElementInfo, 2)}
	for _, index := range []int{-1, -100, 2, 3} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			result, err := b.Execute(&Param{Action: ActionClickElement, Index: &index})
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Contains(t, result.Error, "out of range")
			require.Empty(t, result.Output)
		})
	}
}

func TestInvokableRunPreservesActionValidation(t *testing.T) {
	ctx := context.Background()
	b := &Tool{ctx: ctx}
	for _, input := range []string{
		`{"action":"click_element","index":-1}`,
		`{"action":"click_element"}`,
		`{"action":"unknown"}`,
	} {
		t.Run(input, func(t *testing.T) {
			output, err := b.InvokableRun(ctx, input)
			require.NoError(t, err)
			var result ToolResult
			require.NoError(t, json.Unmarshal([]byte(output), &result))
			require.NotEmpty(t, result.Error)
			require.Empty(t, result.Output)
		})
	}
}
