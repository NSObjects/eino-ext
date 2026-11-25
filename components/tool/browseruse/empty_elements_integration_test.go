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
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBrowserUseIntegrationPageWithoutInteractiveElements(t *testing.T) {
	chrome := os.Getenv("EINO_BROWSERUSE_CHROME")
	if chrome == "" {
		t.Skip("set EINO_BROWSERUSE_CHROME to a Chrome executable")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/button" {
			_, _ = fmt.Fprint(w, "<html><body><button>test</button></body></html>")
			return
		}
		_, _ = fmt.Fprint(w, "<html><body>plain text</body></html>")
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	b, err := NewBrowserUseTool(ctx, &Config{
		Headless:           true,
		ChromeInstancePath: chrome,
		ExtraChromiumArgs:  []string{"disable-dev-shm-usage"},
	})
	require.NoError(t, err)
	defer b.Cleanup()

	run := func(action Action, url string) {
		t.Helper()
		res, err := b.Execute(&Param{Action: action, URL: &url})
		require.NoError(t, err)
		require.NotNil(t, res)
		require.Empty(t, res.Error)
	}
	run(ActionGoToURL, server.URL+"/button")
	require.Len(t, b.elements, 1)
	run(ActionGoToURL, server.URL+"/empty")
	require.Empty(t, b.elements, "old elements must not survive empty-page navigation")
	state, err := b.GetCurrentState()
	require.NoError(t, err)
	require.Equal(t, server.URL+"/empty", state.URL)
	require.Empty(t, state.InteractiveElements)

	run(ActionOpenTab, server.URL+"/empty")
	require.Empty(t, b.elements)
	state, err = b.GetCurrentState()
	require.NoError(t, err)
	require.Empty(t, state.InteractiveElements)
}
