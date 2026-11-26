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
	"errors"
	"testing"

	"github.com/bytedance/mockey"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

func TestUpdateTabsInfoPreservesAttachmentsOnTargetError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	b := &Tool{
		tabs:        []TabInfo{{ID: 0, TargetID: "child"}},
		tabContexts: map[target.ID]context.Context{"child": ctx},
		tabCancels:  map[target.ID]context.CancelFunc{"child": func() { calls++; cancel() }},
	}
	wantErr := errors.New("target discovery failed")
	defer mockey.Mock(chromedp.Targets).Return(nil, wantErr).Build().UnPatch()
	require.ErrorIs(t, b.updateTabsInfo(ctx), wantErr)
	require.Len(t, b.tabs, 1)
	require.Contains(t, b.tabContexts, target.ID("child"))
	require.Contains(t, b.tabCancels, target.ID("child"))
	require.NoError(t, ctx.Err())
	require.Zero(t, calls)
}

func TestReconcileInactiveTabsUsesCompleteTargetSnapshot(t *testing.T) {
	root, cancelRoot := context.WithCancel(context.Background())
	defer cancelRoot()
	child, cancelChild := context.WithCancel(root)
	defer cancelChild()
	live, cancelLive := context.WithCancel(root)
	defer cancelLive()
	calls := 0
	b := &Tool{
		browserCtx: root,
		tabContexts: map[target.ID]context.Context{
			"root": root, "gone": child, "live": live,
		},
		tabCancels: map[target.ID]context.CancelFunc{
			"gone": func() { calls++; cancelChild() },
			"live": cancelLive,
		},
	}
	// Even if a target changes type, a present ID is not evidence of closure.
	snapshot := []*target.Info{{Type: "other", TargetID: "live"}}
	b.reconcileInactiveTabs(snapshot)
	b.reconcileInactiveTabs(snapshot)
	require.Len(t, b.tabContexts, 1)
	require.Contains(t, b.tabContexts, target.ID("live"))
	require.Len(t, b.tabCancels, 1)
	require.ErrorIs(t, child.Err(), context.Canceled)
	require.Equal(t, 1, calls)
	require.NoError(t, live.Err())
	require.NoError(t, root.Err())
}
