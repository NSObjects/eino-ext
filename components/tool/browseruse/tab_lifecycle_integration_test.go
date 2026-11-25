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

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

// This integration regression is opt-in for local developers. CI explicitly
// provides a Chrome executable. Only locally served test pages are visited.
func TestBrowserUseIntegrationReusesManagedTabContexts(t *testing.T) {
	chrome := os.Getenv("EINO_BROWSERUSE_CHROME")
	if chrome == "" {
		t.Skip("set EINO_BROWSERUSE_CHROME to a Chrome executable")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, "<html><body><button>test</button></body></html>")
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	created := 0
	var root context.Context
	var parents []context.Context
	var children []context.Context
	b, err := newBrowserUseTool(ctx, &Config{
		Headless:           true,
		ChromeInstancePath: chrome,
		ExtraChromiumArgs:  []string{"disable-dev-shm-usage"},
	}, func(parent context.Context, opts ...chromedp.ContextOption) (context.Context, context.CancelFunc) {
		created++
		parents = append(parents, parent)
		child, cancelChild := chromedp.NewContext(parent, opts...)
		children = append(children, child)
		if created == 1 {
			root = child
		}
		return child, cancelChild
	})
	require.NoError(t, err)
	t.Cleanup(b.Cleanup)

	run := func(param *Param) {
		t.Helper()
		result, err := b.Execute(param)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Empty(t, result.Error)
	}
	firstURL, secondURL := server.URL+"/first", server.URL+"/second"
	run(&Param{Action: ActionGoToURL, URL: &firstURL})
	run(&Param{Action: ActionOpenTab, URL: &secondURL})
	require.Equal(t, 2, created)
	afterOpen := created

	// Both targets already have a live context. Switching should reuse
	// their existing contexts instead of creating another CDP session.
	for i := 0; i < 3; i++ {
		for _, url := range []string{firstURL, secondURL} {
			require.NoError(t, b.updateTabsInfo(b.ctx))
			found := false
			for _, tab := range b.tabs {
				if tab.URL != url {
					continue
				}
				id := tab.ID
				run(&Param{Action: ActionSwitchTab, TabID: &id})
				var currentURL string
				require.NoError(t, chromedp.Run(b.ctx, chromedp.Location(&currentURL)))
				require.Equal(t, url, currentURL)
				found = true
				break
			}
			require.True(t, found, "missing tab: %s", url)
		}
	}
	require.Equal(t, afterOpen, created, "switching managed tabs should not allocate additional contexts")
	require.Equal(t, root, b.browserCtx)
	require.Len(t, b.tabContexts, 2)

	thirdURL := server.URL + "/third"
	run(&Param{Action: ActionOpenTab, URL: &thirdURL})
	require.Equal(t, 3, created)
	require.Len(t, b.tabContexts, 3)
	require.Equal(t, root, parents[1])
	require.Equal(t, root, parents[2], "new tabs must share the stable browser parent")

	// A navigation failure must not change the active tab or retain a
	// newly created context. The two existing sibling tabs must survive.
	active := b.ctx
	invalidURL := "http://["
	failed, err := b.Execute(&Param{Action: ActionOpenTab, URL: &invalidURL})
	require.NoError(t, err)
	require.NotNil(t, failed)
	require.NotEmpty(t, failed.Error)
	require.Equal(t, active, b.ctx)
	require.Equal(t, 4, created)
	require.Len(t, b.tabContexts, 3)
	require.ErrorIs(t, children[3].Err(), context.Canceled)

	// Closing a managed tab should select an existing attachment, not
	// allocate another context or accidentally close a sibling.
	closedID := browserTargetID(b.ctx)
	require.NotEmpty(t, closedID)
	run(&Param{Action: ActionCloseTab})
	require.NotContains(t, b.tabContexts, closedID)
	require.Len(t, b.tabContexts, 2)
	require.Equal(t, 4, created)
	require.NotEqual(t, closedID, browserTargetID(b.ctx))
	require.NoError(t, chromedp.Run(b.ctx, chromedp.Location(new(string))))

	// The first tab is also the browser root. Closing its page must not
	// invalidate the remaining tab's attachment or prevent new tabs.
	rootID := browserTargetID(root)
	require.NoError(t, b.updateTabsInfo(b.ctx))
	foundRoot := false
	for _, tab := range b.tabs {
		if tab.TargetID == rootID {
			id := tab.ID
			run(&Param{Action: ActionSwitchTab, TabID: &id})
			foundRoot = true
			break
		}
	}
	require.True(t, foundRoot)
	run(&Param{Action: ActionCloseTab})
	require.NotContains(t, b.tabContexts, rootID)
	require.Len(t, b.tabContexts, 1)
	require.Equal(t, 4, created)
	var remainingURL string
	require.NoError(t, chromedp.Run(b.ctx, chromedp.Location(&remainingURL)))
	require.Equal(t, secondURL, remainingURL)

	fourthURL := server.URL + "/fourth"
	run(&Param{Action: ActionOpenTab, URL: &fourthURL})
	require.Equal(t, 5, created)
	require.Equal(t, root, parents[4])
	require.Len(t, b.tabContexts, 2)
}
