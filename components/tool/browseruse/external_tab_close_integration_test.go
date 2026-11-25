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

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A page may disappear independently of ActionCloseTab, for example when
// a user closes it or a page closes itself. Reconciliation must release
// the owned context without affecting the currently active sibling.
func TestBrowserUseIntegrationReconcilesExternallyClosedTab(t *testing.T) {
	chrome := os.Getenv("EINO_BROWSERUSE_CHROME")
	if chrome == "" {
		t.Skip("set EINO_BROWSERUSE_CHROME to a Chrome executable")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, "<html><body>test</body></html>")
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	b, err := NewBrowserUseTool(ctx, &Config{
		Headless:           true,
		ChromeInstancePath: chrome,
		ExtraChromiumArgs:  []string{"disable-dev-shm-usage"},
	})
	require.NoError(t, err)
	defer b.Cleanup()

	open := func(url string) {
		t.Helper()
		result, err := b.Execute(&Param{Action: ActionOpenTab, URL: &url})
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Empty(t, result.Error)
	}

	open(server.URL + "/external-close")
	closedCtx := b.ctx
	closedID := browserTargetID(closedCtx)
	require.NotEmpty(t, closedID)
	open(server.URL + "/survivor")
	active := b.ctx
	require.Len(t, b.tabContexts, 3)

	// Bypass the tool's CloseTab path to simulate a browser-side close.
	require.NoError(t, chromedp.Run(closedCtx, page.Close()))
	// Confirm the browser itself reports that the target is gone before
	// testing the Tool's reconciliation; this is not a timing assumption.
	require.Eventually(t, func() bool {
		targets, err := chromedp.Targets(b.browserCtx)
		if err != nil {
			return false
		}
		for _, info := range targets {
			if info.TargetID == closedID {
				return false
			}
		}
		return true
	}, 5*time.Second, 50*time.Millisecond)

	state, err := b.GetCurrentState()
	require.NoError(t, err)
	require.NotNil(t, state)
	require.Equal(t, server.URL+"/survivor", state.URL)
	for _, tab := range state.Tabs {
		require.NotEqual(t, closedID, tab.TargetID)
	}
	assert.NotContains(t, b.tabContexts, closedID, "closed target attachment must not remain cached")
	assert.ErrorIs(t, closedCtx.Err(), context.Canceled, "owned child context must be released")
	assert.Equal(t, active, b.ctx)
	assert.NoError(t, active.Err())
	assert.NoError(t, b.browserCtx.Err())
}
